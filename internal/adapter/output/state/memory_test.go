package state_test

import (
	"sync"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
	"github.com/hironow/runops-gateway/internal/core/port"
)

func TestMemoryStore_TryLock_FirstCall(t *testing.T) {
	// given
	s := state.NewMemoryStore()

	// when
	got := s.TryLock("key1")

	// then
	if !got {
		t.Error("expected TryLock to return true on first call")
	}
}

func TestMemoryStore_TryLock_SecondCall(t *testing.T) {
	// given
	s := state.NewMemoryStore()
	s.TryLock("key1")

	// when
	got := s.TryLock("key1")

	// then
	if got {
		t.Error("expected TryLock to return false on second call with same key")
	}
}

func TestMemoryStore_Release_AllowsRelock(t *testing.T) {
	// given
	s := state.NewMemoryStore()
	s.TryLock("key1")

	// when
	s.Release("key1")
	got := s.TryLock("key1")

	// then
	if !got {
		t.Error("expected TryLock to return true after Release")
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	// given
	s := state.NewMemoryStore()
	const goroutines = 10
	results := make([]bool, goroutines)
	var wg sync.WaitGroup

	// when — 10 goroutines TryLock same key concurrently
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx] = s.TryLock("shared-key")
		}(i)
	}
	wg.Wait()

	// then — exactly 1 goroutine should have succeeded
	trueCount := 0
	for _, r := range results {
		if r {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Errorf("expected exactly 1 successful TryLock, got %d", trueCount)
	}
}

func TestMemoryStore_Release_UnlockedKey_NoPanic(t *testing.T) {
	// given — Release a key that was never locked; must not panic
	s := state.NewMemoryStore()

	// when / then
	s.Release("never-locked-key") // must not panic
}

func TestMemoryStore_DoubleRelease_NoPanic(t *testing.T) {
	// given
	s := state.NewMemoryStore()
	s.TryLock("key1")
	s.Release("key1")

	// when / then — second Release must not panic or corrupt state
	s.Release("key1")

	// and the key can be re-locked after double release
	if !s.TryLock("key1") {
		t.Error("expected TryLock to succeed after double Release")
	}
}

func TestMemoryStore_IndependentKeys(t *testing.T) {
	// given — locking one key must not affect another
	s := state.NewMemoryStore()
	s.TryLock("key-a")

	// when
	gotB := s.TryLock("key-b")

	// then
	if !gotB {
		t.Error("expected TryLock on key-b to succeed independently of key-a")
	}
}

func TestMemoryStore_LockReleaseReLock(t *testing.T) {
	// given — verifies the full lock→release→lock cycle multiple times
	s := state.NewMemoryStore()

	for i := range 3 {
		if !s.TryLock("cycled-key") {
			t.Fatalf("iteration %d: expected TryLock to return true", i)
		}
		s.Release("cycled-key")
	}
}

func TestMemoryStore_ImplementsPort(t *testing.T) {
	// compile-time assertion that *MemoryStore satisfies port.StateStore
	var _ port.StateStore = (*state.MemoryStore)(nil)
}
