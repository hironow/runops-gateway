package phonewave

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

type recordingPublisher struct {
	mu    sync.Mutex
	mails []domain.DMail
	err   error
}

func (r *recordingPublisher) PublishDMail(_ context.Context, m domain.DMail) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mails = append(r.mails, m)
	if r.err != nil {
		return "", r.err
	}
	return "id-" + m.IdempotencyKey, nil
}

func (r *recordingPublisher) snapshot() []domain.DMail {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.DMail, len(r.mails))
	copy(out, r.mails)
	return out
}

func writeArchive(t *testing.T, dir string, name string, mail domain.DMail) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(mail.RenderMarkdown()), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestEmitter_PublishFile_ParsesAndPublishes(t *testing.T) {
	dir := t.TempDir()
	mail := domain.DMail{
		ID:             "01HZW",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k1",
		Body:           "PR #42 merged.",
	}
	path := writeArchive(t, dir, "msg.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())

	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("PublishFile: %v", err)
	}
	got := pub.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	if got[0].Kind != domain.DMailKindReport || got[0].Target != "amadeus" {
		t.Errorf("publish content drifted: %+v", got[0])
	}
}

func TestEmitter_PublishFile_SkipsNonMarkdown(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ignore.txt")
	_ = os.WriteFile(path, []byte("not a dmail"), 0o644)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Errorf("non-md should be skipped silently, got: %v", err)
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Errorf("publisher must not be called for non-md files; got %d", len(got))
	}
}

func TestEmitter_PublishFile_PropagatesPublishError(t *testing.T) {
	dir := t.TempDir()
	mail := domain.DMail{
		ID: "x", Kind: domain.DMailKindReport, Target: "amadeus",
		IdempotencyKey: "k", Body: "x",
	}
	path := writeArchive(t, dir, "x.md", mail)

	pub := &recordingPublisher{err: errors.New("boom")}
	e := NewEmitter(pub, NewSingleArchiveRouter())

	err := e.PublishFile(context.Background(), path)
	if err == nil {
		t.Fatal("expected publisher error to propagate")
	}
}

func TestEmitter_PublishFile_RetriesAfterParseError(t *testing.T) {
	// Reproduces the smoke-test bug: fsnotify can fire Create before the
	// writer has actually written the data, so PublishFile sees an empty/
	// truncated file and parse fails. Without a retry path, even the next
	// Write event would be deduped away.
	//
	// Phase 2c contract: parse failures must NOT poison the dedup record;
	// a follow-up event for the same path with valid content must succeed.
	dir := t.TempDir()
	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())

	path := filepath.Join(dir, "race.md")
	// Step 1: empty file (simulates fsnotify Create firing before Write).
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.PublishFile(context.Background(), path); err == nil {
		t.Fatal("expected parse error for empty file")
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Fatalf("publisher must not be called when parse fails; got %d", len(got))
	}

	// Step 2: full content arrives — emitter must retry, not skip.
	mail := domain.DMail{
		ID:             "x",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		IdempotencyKey: "k",
		Body:           "now valid",
	}
	if err := os.WriteFile(path, []byte(mail.RenderMarkdown()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("retry after parse error should succeed; got: %v", err)
	}
	if got := pub.snapshot(); len(got) != 1 {
		t.Errorf("expected one publish on retry; got %d", len(got))
	}
}

func TestEmitter_PublishFile_DedupsByPath(t *testing.T) {
	// Calling PublishFile twice for the same path must publish at most once.
	// fsnotify can deliver the same Create event twice on some filesystems
	// (or a Create + Write pair); the emitter should defend.
	dir := t.TempDir()
	mail := domain.DMail{
		ID: "01HZW", Kind: domain.DMailKindReport, Target: "amadeus",
		IdempotencyKey: "dup", Body: "x",
	}
	path := writeArchive(t, dir, "dup.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if got := pub.snapshot(); len(got) != 1 {
		t.Errorf("expected 1 publish (deduped), got %d", len(got))
	}
}

func TestEmitter_PublishFile_SkipsDirectoriesAndDotFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	dot := filepath.Join(dir, ".hidden.md")
	_ = os.WriteFile(dot, []byte("---\nkind: report\n---\nx"), 0o644)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())
	if err := e.PublishFile(context.Background(), filepath.Join(dir, "subdir")); err != nil {
		t.Errorf("dir should be skipped silently: %v", err)
	}
	if err := e.PublishFile(context.Background(), dot); err != nil {
		t.Errorf("dotfile should be skipped silently: %v", err)
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Errorf("publisher must not be called; got %d", len(got))
	}
}

func TestEmitter_PublishFile_MultiMode_AttachesProjectIDToMetadata(t *testing.T) {
	root := t.TempDir()
	dirFoo := filepath.Join(root, "foo")
	dirBar := filepath.Join(root, "bar")

	router, err := NewMultiArchiveRouter(map[string]string{
		"foo": dirFoo,
		"bar": dirBar,
	})
	if err != nil {
		t.Fatalf("router init: %v", err)
	}

	mail := domain.DMail{
		ID:             "01MULTI",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-multi",
		Body:           "multi-mode body",
	}
	path := writeArchive(t, dirFoo, "multi.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, router)
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("PublishFile: %v", err)
	}
	got := pub.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	if got[0].Metadata["project_id"] != "foo" {
		t.Errorf("metadata.project_id = %q, want foo", got[0].Metadata["project_id"])
	}
}

func TestEmitter_PublishFile_MultiMode_SkipsUnmappedPath(t *testing.T) {
	root := t.TempDir()
	dirFoo := filepath.Join(root, "foo")
	dirGhost := filepath.Join(root, "ghost") // intentionally not registered

	router, err := NewMultiArchiveRouter(map[string]string{
		"foo": dirFoo,
	})
	if err != nil {
		t.Fatalf("router init: %v", err)
	}

	mail := domain.DMail{
		ID:             "01GHOST",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-ghost",
		Body:           "should be skipped",
	}
	path := writeArchive(t, dirGhost, "ghost.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, router)
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("PublishFile should skip silently for unmapped path, got: %v", err)
	}
	if got := pub.snapshot(); len(got) != 0 {
		t.Errorf("publisher must not be called for unmapped path; got %d", len(got))
	}
}

func TestEmitter_PublishFile_MultiMode_OverridesFrontmatterMismatch(t *testing.T) {
	root := t.TempDir()
	dirFoo := filepath.Join(root, "foo")

	router, err := NewMultiArchiveRouter(map[string]string{
		"foo": dirFoo,
	})
	if err != nil {
		t.Fatalf("router init: %v", err)
	}

	// Frontmatter says project=stale (e.g. older tooling), but the
	// archive path is foo's. Path-derived must win and a warn must fire
	// (assertion is on the published metadata; the warn surfaces in
	// logs / OTel which we don't introspect here).
	mail := domain.DMail{
		ID:             "01MISMATCH",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-mismatch",
		Body:           "mismatch body",
		Metadata:       map[string]string{"project_id": "stale"},
	}
	path := writeArchive(t, dirFoo, "mismatch.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, router)
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("PublishFile: %v", err)
	}
	got := pub.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	if got[0].Metadata["project_id"] != "foo" {
		t.Errorf("path-derived must win on mismatch: got=%q want=foo", got[0].Metadata["project_id"])
	}
}

func TestEmitter_PublishFile_SingleMode_PassesFrontmatterMetadata(t *testing.T) {
	dir := t.TempDir()
	mail := domain.DMail{
		ID:             "01SINGLE",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "k-single",
		Body:           "single-mode body",
		Metadata:       map[string]string{"project_id": "from-frontmatter"},
	}
	path := writeArchive(t, dir, "single.md", mail)

	pub := &recordingPublisher{}
	e := NewEmitter(pub, NewSingleArchiveRouter())
	if err := e.PublishFile(context.Background(), path); err != nil {
		t.Fatalf("PublishFile: %v", err)
	}
	got := pub.snapshot()
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	// Single-mode router does NOT touch metadata; whatever the
	// frontmatter said reaches Pub/Sub unchanged.
	if got[0].Metadata["project_id"] != "from-frontmatter" {
		t.Errorf("single-mode should preserve frontmatter project_id; got=%q",
			got[0].Metadata["project_id"])
	}
}

// silence unused import sometimes kicks in across edits
var _ = errors.Is
