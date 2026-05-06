package deps_test

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestFsnotifyPinnedToHironowFork enforces the supply-chain pin: both the root
// module and the tools module MUST replace github.com/fsnotify/fsnotify with
// the self-managed fork github.com/hironow/fsnotify. This guards against a
// future `go mod tidy -e` (or a careless edit) silently dropping the replace
// directive and re-pulling upstream sources into the build.
func TestFsnotifyPinnedToHironowFork(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	re := regexp.MustCompile(`(?m)^replace\s+github\.com/fsnotify/fsnotify\s+=>\s+github\.com/hironow/fsnotify\s+`)

	for _, rel := range []string{"go.mod", "tools/go.mod"} {
		t.Run(rel, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join(repoRoot, rel))
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			if !re.Match(b) {
				t.Errorf("%s must contain `replace github.com/fsnotify/fsnotify => github.com/hironow/fsnotify ...`", rel)
			}
		})
	}
}
