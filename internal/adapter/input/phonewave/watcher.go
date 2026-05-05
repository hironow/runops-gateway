package phonewave

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// FileHandler is invoked for every .md file the watcher observes (existing
// files at startup and new ones via fsnotify Create / Rename events).
type FileHandler interface {
	PublishFile(ctx context.Context, path string) error
}

// Watcher observes a set of phonewave archive directories and hands every
// .md file to the FileHandler.
type Watcher struct {
	dirs    []string
	handler FileHandler
}

// NewWatcher returns a Watcher rooted at the given archive dirs. They must
// already exist (the watcher does not create them — that's a 5-pillar runtime
// concern).
func NewWatcher(handler FileHandler, dirs ...string) *Watcher {
	return &Watcher{handler: handler, dirs: dirs}
}

// Run scans each watched directory once at startup (so messages that arrived
// while the daemon was offline are not lost) and then blocks on fsnotify
// events until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) error {
	notifier, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("watcher: fsnotify.NewWatcher: %w", err)
	}
	defer notifier.Close()

	for _, dir := range w.dirs {
		if err := notifier.Add(dir); err != nil {
			return fmt.Errorf("watcher: add %s: %w", dir, err)
		}
		if err := w.scanOnce(ctx, dir); err != nil {
			return err
		}
		slog.InfoContext(ctx, "dmail watcher: watching", "dir", dir)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-notifier.Events:
			if !ok {
				return nil
			}
			if !shouldHandle(ev) {
				continue
			}
			if err := w.handler.PublishFile(ctx, ev.Name); err != nil {
				slog.ErrorContext(ctx, "dmail watcher: publish failed", "path", ev.Name, "error", err)
			}
		case err, ok := <-notifier.Errors:
			if !ok {
				return nil
			}
			slog.WarnContext(ctx, "dmail watcher: fsnotify error", "error", err)
		}
	}
}

// scanOnce delivers every existing .md file in dir to the handler. Used at
// startup so the daemon catches up on archives written while it was down.
func (w *Watcher) scanOnce(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("watcher: scan %s: %w", dir, err)
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		if err := w.handler.PublishFile(ctx, path); err != nil {
			slog.WarnContext(ctx, "dmail watcher: startup scan publish failed",
				"path", path, "error", err)
		}
	}
	return nil
}

// shouldHandle returns true for events that may correspond to a newly
// available .md file. Includes Create, Write, and Rename so we catch:
//   - atomic temp+rename writers (Rename)
//   - shell heredoc / editor writers that fire Create then Write
//     (the first event may see an empty file and fail to parse;
//     the Write event lets the emitter retry once data has landed)
//   - truncate-then-write editors
// Emitter dedups by path, so re-firing on Write is harmless when the
// Create-side already succeeded.
func shouldHandle(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Rename) == 0 {
		return false
	}
	return strings.HasSuffix(strings.ToLower(ev.Name), ".md")
}
