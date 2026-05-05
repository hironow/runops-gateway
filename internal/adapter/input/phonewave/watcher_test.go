package phonewave

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type recordingHandler struct {
	mu    sync.Mutex
	paths []string
}

func (r *recordingHandler) PublishFile(_ context.Context, path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.paths = append(r.paths, path)
	return nil
}

func (r *recordingHandler) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.paths))
	copy(out, r.paths)
	return out
}

func TestWatcher_StartupScan_DeliversExistingFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.md", "b.md", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h := &recordingHandler{}
	w := NewWatcher(h, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	got := h.snapshot()
	if len(got) < 3 {
		t.Errorf("startup scan should hand every entry to handler (it filters non-md itself); got %v", got)
	}
}

func TestWatcher_FsnotifyCreate_DeliversNewFile(t *testing.T) {
	dir := t.TempDir()
	h := &recordingHandler{}
	w := NewWatcher(h, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- w.Run(ctx)
	}()
	// Give the watcher a moment to install the fsnotify subscription.
	time.Sleep(150 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range h.snapshot() {
			if filepath.Base(p) == "new.md" {
				cancel()
				<-runDone
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-runDone
	t.Fatalf("fsnotify Create event for new.md was not delivered; got=%v", h.snapshot())
}
