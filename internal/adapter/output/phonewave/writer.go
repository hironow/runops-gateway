// Package phonewave bridges runops-gateway to the phonewave courier daemon by
// writing D-Mail .md files into a watched outbox directory.
//
// Phonewave fsnotifies the outbox and routes whatever appears there. Two
// invariants must hold to play nicely with that contract:
//  1. Atomic write — phonewave must never observe a half-written file. We
//     write to a sibling .tmp-<name> first then rename onto the target.
//  2. Idempotent — Pub/Sub may redeliver the same message, so writing the
//     same name + same bytes again is a no-op rather than an error.
//
// The same name with different bytes is a programmer mistake (the receiver
// derives the filename from the D-Mail ID, which is crypto-random); we
// surface it as an error rather than overwrite.
package phonewave

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// OutboxWriter writes D-Mail files into a single phonewave-watched directory.
type OutboxWriter struct {
	dir string
}

// NewOutboxWriter returns a writer rooted at dir. The directory is created on
// first WriteFile call if it does not already exist.
func NewOutboxWriter(dir string) *OutboxWriter {
	return &OutboxWriter{dir: dir}
}

// WriteFile atomically writes data to <dir>/<name>. Returns nil on success
// (including the idempotent same-content re-write case), an error if name is
// not a safe single-segment filename, the file already exists with different
// bytes, or the underlying I/O fails.
func (w *OutboxWriter) WriteFile(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("phonewave writer: mkdir %s: %w", w.dir, err)
	}

	final := filepath.Join(w.dir, name)
	if existing, err := os.ReadFile(final); err == nil {
		if bytes.Equal(existing, data) {
			return nil // idempotent re-delivery
		}
		return fmt.Errorf("phonewave writer: %s already exists with different content", name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("phonewave writer: stat %s: %w", final, err)
	}

	tmp, err := os.CreateTemp(w.dir, ".tmp-"+name+"-*")
	if err != nil {
		return fmt.Errorf("phonewave writer: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("phonewave writer: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("phonewave writer: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("phonewave writer: close temp: %w", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		// Race: another goroutine landed the same file first. If the existing
		// content matches, treat as idempotent success; otherwise surface.
		if existing, rErr := os.ReadFile(final); rErr == nil && bytes.Equal(existing, data) {
			cleanup()
			return nil
		}
		cleanup()
		return fmt.Errorf("phonewave writer: rename: %w", err)
	}
	return nil
}

// validateName rejects directory-traversing or otherwise unsafe names so the
// receiver can never escape the configured outbox directory regardless of
// what attribute values it pulls off Pub/Sub.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("phonewave writer: filename is required")
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("phonewave writer: filename must be a single path segment, got %q", name)
	}
	if strings.Contains(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("phonewave writer: filename must not contain path separators: %q", name)
	}
	if name == "." || name == ".." || strings.HasPrefix(name, "..") {
		return fmt.Errorf("phonewave writer: filename %q is not allowed", name)
	}
	return nil
}

// ParseDirsByProject decodes a `id1:/abs/path/1,id2:/abs/path/2` env var
// shared by both the receiver (#0006 — outbox dir) and the emitter
// (#0007 — archive dir). Each id is validated against
// domain.ValidateProjectID, each path must be absolute (relative paths
// are inherently ambiguous on workspace VMs / multi-instance Cloud Run),
// and duplicate ids are rejected so a typo never silently overwrites
// another project's mapping.
//
// Returns the parsed map, or an error that the binary init can surface
// at process boot (fail-loud > fail-late on the first message).
func ParseDirsByProject(s string) (map[string]string, error) {
	out := map[string]string{}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return out, nil
	}
	for _, raw := range strings.Split(trimmed, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return nil, fmt.Errorf("phonewave: empty entry in PHONEWAVE_OUTBOX_DIRS_BY_PROJECT")
		}
		idx := strings.Index(entry, ":")
		if idx <= 0 || idx == len(entry)-1 {
			return nil, fmt.Errorf("phonewave: entry must be id:path (got %q)", entry)
		}
		id := strings.TrimSpace(entry[:idx])
		path := strings.TrimSpace(entry[idx+1:])
		if err := domain.ValidateProjectID(id); err != nil {
			return nil, fmt.Errorf("phonewave: invalid project id %q: %w", id, err)
		}
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("phonewave: path for project %q must be absolute (got %q)", id, path)
		}
		if _, dup := out[id]; dup {
			return nil, fmt.Errorf("phonewave: duplicate project id %q", id)
		}
		out[id] = path
	}
	return out, nil
}

// ParseOutboxDirsByProject is a deprecated alias for ParseDirsByProject.
// Kept for one PR cycle so #0006 / receiver call sites keep compiling
// during the #0007 transition; remove in a follow-up PR.
//
// Deprecated: use ParseDirsByProject instead.
func ParseOutboxDirsByProject(s string) (map[string]string, error) {
	return ParseDirsByProject(s)
}
