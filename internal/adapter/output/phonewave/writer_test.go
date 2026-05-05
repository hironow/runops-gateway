package phonewave

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOutboxWriter_WriteFile_CreatesAtomicallyAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	w := NewOutboxWriter(dir)

	if err := w.WriteFile("first.md", []byte("---\nkind: report\n---\n\nbody\n")); err != nil {
		t.Fatalf("first WriteFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "first.md"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "kind: report") {
		t.Errorf("on-disk content missing kind: %q", got)
	}

	// Second call with the same name + same content must be a no-op (idempotent).
	if err := w.WriteFile("first.md", []byte("---\nkind: report\n---\n\nbody\n")); err != nil {
		t.Errorf("idempotent re-write should succeed, got: %v", err)
	}
}

func TestOutboxWriter_WriteFile_RejectsExistingDifferentContent(t *testing.T) {
	dir := t.TempDir()
	w := NewOutboxWriter(dir)

	if err := w.WriteFile("dup.md", []byte("first")); err != nil {
		t.Fatalf("first WriteFile: %v", err)
	}
	err := w.WriteFile("dup.md", []byte("second"))
	if err == nil {
		t.Fatal("expected error when overwriting with different content")
	}
}

func TestOutboxWriter_WriteFile_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	w := NewOutboxWriter(dir)
	for _, bad := range []string{"../escape.md", "sub/dir.md", "/abs.md", ""} {
		if err := w.WriteFile(bad, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) should error", bad)
		}
	}
}

func TestOutboxWriter_WriteFile_LeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	w := NewOutboxWriter(dir)

	if err := w.WriteFile("ok.md", []byte("hello")); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestOutboxWriter_ConcurrentSameNameSameContent(t *testing.T) {
	dir := t.TempDir()
	w := NewOutboxWriter(dir)
	payload := []byte("same body")
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- w.WriteFile("race.md", payload)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent same-content write should not error: %v", err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(dir, "race.md"))
	if string(got) != "same body" {
		t.Errorf("final content mismatch: %q", got)
	}
}
