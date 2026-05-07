package phonewave

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ArchiveRouter resolves the project_id for an fsnotify event path.
// Mirrors input/pubsub.OutboxRouter (#0006) but in the reverse
// direction: receiver maps project_id → outbox path, emitter maps
// archive path → project_id (#0007).
//
// Implementations also expose Mode() so the composition root can
// fail-fast when the operator-declared peer mode does not match the
// emitter's own mode (codex review v1 #3, ADR 0029).
type ArchiveRouter interface {
	ResolveProjectID(ctx context.Context, eventPath string) (string, error)
	Mode() string
}

// ErrPathNotMapped means the multi-mode router has no archive dir
// covering the given event path. The caller should skip-and-warn rather
// than nack: the dmail-emitter is a read-only watcher, so file content
// stays on disk for operator triage even when no message ships.
var ErrPathNotMapped = errors.New("event path has no project_id mapping")

// SingleArchiveRouter never resolves project_id from the path. Used in
// the legacy single-mode deployment (PHONEWAVE_ARCHIVE_DIRS only) so the
// pre-#0007 behaviour is preserved byte-for-byte: whatever frontmatter
// carries is what reaches Pub/Sub, and an empty project_id is fine.
type SingleArchiveRouter struct{}

// NewSingleArchiveRouter builds a single-mode router.
func NewSingleArchiveRouter() *SingleArchiveRouter { return &SingleArchiveRouter{} }

// ResolveProjectID returns ("", nil) — the caller relies on whatever
// already lives in DMail.Metadata["project_id"] for routing.
func (r *SingleArchiveRouter) ResolveProjectID(_ context.Context, _ string) (string, error) {
	return "", nil
}

// Mode reports the router's operating mode for peer-handshake checks.
func (r *SingleArchiveRouter) Mode() string { return "single" }

// MultiArchiveRouter routes events to a project_id by archive-dir
// prefix. Built from the parsed PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT map.
//
// Nested archive dirs are forbidden: if any registered cleaned dir is a
// path prefix of another (in either direction), the constructor fails.
// This keeps ResolveProjectID order-independent and removes the
// longer-first / shorter-first ambiguity entirely (codex review v1 #1).
type MultiArchiveRouter struct {
	entries []archiveEntry // unordered; nesting/duplicates are pre-empted at init
}

type archiveEntry struct {
	projectID  string
	cleanedDir string // filepath.Clean
}

// NewMultiArchiveRouter validates the supplied dirsByProject map and
// returns a router. Returns an error when two dirs match (duplicate) or
// when one dir is a path prefix of another (nested).
func NewMultiArchiveRouter(dirsByProject map[string]string) (*MultiArchiveRouter, error) {
	entries := make([]archiveEntry, 0, len(dirsByProject))
	for id, dir := range dirsByProject {
		entries = append(entries, archiveEntry{projectID: id, cleanedDir: filepath.Clean(dir)})
	}
	sep := string(filepath.Separator)
	for i := range entries {
		for j := range entries {
			if i == j {
				continue
			}
			if entries[i].cleanedDir == entries[j].cleanedDir {
				return nil, fmt.Errorf("archive dir %q registered for both %q and %q",
					entries[i].cleanedDir, entries[i].projectID, entries[j].projectID)
			}
			if strings.HasPrefix(entries[j].cleanedDir, entries[i].cleanedDir+sep) {
				return nil, fmt.Errorf("nested archive dirs forbidden: %q (project %q) is inside %q (project %q)",
					entries[j].cleanedDir, entries[j].projectID, entries[i].cleanedDir, entries[i].projectID)
			}
		}
	}
	return &MultiArchiveRouter{entries: entries}, nil
}

// ResolveProjectID returns the project_id whose archive dir contains
// eventPath, or wraps ErrPathNotMapped when nothing matches.
func (r *MultiArchiveRouter) ResolveProjectID(_ context.Context, eventPath string) (string, error) {
	cleaned := filepath.Clean(eventPath)
	sep := string(filepath.Separator)
	for _, e := range r.entries {
		if cleaned == e.cleanedDir {
			return e.projectID, nil
		}
		if strings.HasPrefix(cleaned, e.cleanedDir+sep) {
			return e.projectID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrPathNotMapped, eventPath)
}

// Mode reports the router's operating mode for peer-handshake checks.
func (r *MultiArchiveRouter) Mode() string { return "multi" }
