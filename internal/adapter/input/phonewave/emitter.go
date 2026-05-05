// Package phonewave (input side) bridges the 5-pillar archive directories to
// the gateway's DMail publisher.
//
// Each pillar's phonewave routing leaves a copy of every delivered D-Mail in
// its tool-local archive/ directory (sightjack/.siren/archive,
// paintress/.expedition/archive, amadeus/.gate/archive, dominator/.pass/archive).
// The dmail-emitter daemon fsnotifies those dirs and publishes anything new
// to the dmail-outbound Pub/Sub topic so the gateway can fan results back
// into Slack threads.
//
// Emitter is the bit you can unit test: it parses one .md file and publishes
// it. Watcher is the I/O loop that wires Emitter to fsnotify; it lives in
// watcher.go.
package phonewave

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Emitter publishes a single archive .md file as a D-Mail.
type Emitter struct {
	publisher port.DMailPublisher

	mu   sync.Mutex
	seen map[string]bool // dedup by absolute path
}

// NewEmitter constructs an Emitter around the given publisher.
func NewEmitter(p port.DMailPublisher) *Emitter {
	return &Emitter{publisher: p, seen: map[string]bool{}}
}

// PublishFile parses path as a D-Mail and publishes it. Silently no-ops for
// non-.md files, directories, dotfiles, and previously-seen paths so the
// fsnotify caller can hand it every Create event without filtering.
func (e *Emitter) PublishFile(ctx context.Context, path string) error {
	base := filepath.Base(path)
	if !strings.HasSuffix(strings.ToLower(base), ".md") {
		return nil
	}
	if strings.HasPrefix(base, ".") {
		return nil
	}

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("emitter: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	e.mu.Lock()
	if e.seen[abs] {
		e.mu.Unlock()
		return nil
	}
	e.seen[abs] = true
	e.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("emitter: read %s: %w", path, err)
	}
	mail, err := domain.ParseDMail(data)
	if err != nil {
		return fmt.Errorf("emitter: parse %s: %w", path, err)
	}

	id, err := e.publisher.PublishDMail(ctx, mail)
	if err != nil {
		// Publish failure means the dedup record is unreliable — clear it so
		// the fsnotify caller can retry on the next event.
		e.mu.Lock()
		delete(e.seen, abs)
		e.mu.Unlock()
		return fmt.Errorf("emitter: publish %s: %w", path, err)
	}
	slog.InfoContext(ctx, "dmail emitter: published",
		"path", path, "kind", mail.Kind, "target", mail.Target,
		"pubsub_message_id", id)
	return nil
}
