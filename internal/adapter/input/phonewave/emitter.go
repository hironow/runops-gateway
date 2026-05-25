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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// emitterTracerName identifies this package as the OTel instrumentation
// library. fsnotify-driven publish spans live here.
const emitterTracerName = "github.com/hironow/runops-gateway/internal/adapter/input/phonewave"

// Emitter publishes a single archive .md file as a D-Mail. The
// ArchiveRouter resolves the project_id from the file's archive path
// (multi-mode) or returns "" so the frontmatter value passes through
// unchanged (single-mode).
type Emitter struct {
	publisher port.DMailPublisher
	router    ArchiveRouter

	mu   sync.Mutex
	seen map[string]bool // dedup by absolute path
}

// NewEmitter constructs an Emitter around the given publisher and
// router. Pass NewSingleArchiveRouter() to preserve pre-#0007
// single-mode behaviour.
func NewEmitter(p port.DMailPublisher, r ArchiveRouter) *Emitter {
	return &Emitter{publisher: p, router: r, seen: map[string]bool{}}
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

	// Each fsnotify-detected file is its own root trigger — start a new span
	// (will become a root span on this binary unless something upstream
	// already established trace context, which fsnotify never does).
	ctx, span := otel.Tracer(emitterTracerName).Start(ctx, "dmail.emitter.publish_file")
	defer span.End()
	span.SetAttributes(attribute.String("file.path", path))

	info, err := os.Stat(path)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "stat failed")
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

	// Any failure below means we cannot trust the dedup record: a follow-up
	// fsnotify event (or the watcher's startup scan re-running) must be
	// allowed to retry the same path. fsnotify often delivers Create before
	// the writer has finished writing, so empty / partial reads are routine.
	clearOnFailure := func() {
		e.mu.Lock()
		delete(e.seen, abs)
		e.mu.Unlock()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		clearOnFailure()
		span.RecordError(err)
		span.SetStatus(codes.Error, "read failed")
		return fmt.Errorf("emitter: read %s: %w", path, err)
	}
	mail, err := domain.ParseDMail(data)
	if err != nil {
		clearOnFailure()
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse failed")
		return fmt.Errorf("emitter: parse %s: %w", path, err)
	}
	span.SetAttributes(
		attribute.String("dmail.kind", string(mail.Kind)),
		attribute.String("dmail.target", mail.Target),
	)

	// #0007: resolve project_id from the archive path (multi-mode)
	// or accept the frontmatter value unchanged (single-mode). When
	// path resolution and frontmatter disagree, the path-derived value
	// wins and a warn is logged so the operator can spot stale tooling
	// metadata. ErrPathNotMapped means "operator did not register this
	// archive dir"; the emitter skips publishing rather than nack — the
	// file stays on disk for triage.
	routedID, routerErr := e.router.ResolveProjectID(ctx, path)
	if errors.Is(routerErr, ErrPathNotMapped) {
		clearOnFailure()
		slog.WarnContext(ctx, "emitter: archive path not mapped to project_id; skipping publish",
			"path", path, "error", routerErr)
		span.AddEvent("skip", trace.WithAttributes(attribute.String("reason", "path_not_mapped")))
		return nil
	}
	if routerErr != nil {
		clearOnFailure()
		span.RecordError(routerErr)
		span.SetStatus(codes.Error, "router resolve failed")
		return fmt.Errorf("emitter: router resolve %s: %w", path, routerErr)
	}
	if routedID != "" {
		if mail.Metadata == nil {
			mail.Metadata = map[string]string{}
		}
		if existing := mail.Metadata["project_id"]; existing != "" && existing != routedID {
			slog.WarnContext(ctx, "emitter: frontmatter project_id != path-derived; using path-derived",
				"path", path,
				"frontmatter_project_id", existing,
				"routed_project_id", routedID)
		}
		mail.Metadata["project_id"] = routedID
	}
	if pid := mail.Metadata["project_id"]; pid != "" {
		span.SetAttributes(attribute.String("project_id", pid))
	}

	id, err := e.publisher.PublishDMail(ctx, mail)
	if err != nil {
		clearOnFailure()
		span.RecordError(err)
		span.SetStatus(codes.Error, "publish failed")
		return fmt.Errorf("emitter: publish %s: %w", path, err)
	}
	span.SetAttributes(attribute.String("pubsub.message_id", id))
	slog.InfoContext(ctx, "dmail emitter: published",
		"path", path, "kind", mail.Kind, "target", mail.Target,
		"pubsub_message_id", id)
	return nil
}
