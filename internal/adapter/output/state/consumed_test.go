package state

import (
	"sync"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/core/port"
)

// compile-time interface assertion
var _ port.ConsumedTokenStore = (*MemoryConsumedStore)(nil)

func TestMemoryConsumedStore_FirstCallReturnsTrue(t *testing.T) {
	s := NewMemoryConsumedStore(time.Hour)
	if !s.MarkConsumed("k") {
		t.Error("first MarkConsumed should return true")
	}
}

func TestMemoryConsumedStore_SecondCallReturnsFalse(t *testing.T) {
	s := NewMemoryConsumedStore(time.Hour)
	s.MarkConsumed("k")
	if s.MarkConsumed("k") {
		t.Error("second MarkConsumed within TTL should return false (replay)")
	}
}

func TestMemoryConsumedStore_DistinctTokensIndependent(t *testing.T) {
	s := NewMemoryConsumedStore(time.Hour)
	if !s.MarkConsumed("a") {
		t.Fatal("a first call should be true")
	}
	if !s.MarkConsumed("b") {
		t.Error("b first call should be true (independent of a)")
	}
}

func TestMemoryConsumedStore_TTLExpiryAllowsReuse(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock := func() time.Time { return now }
	s := newMemoryConsumedStoreWithClock(5*time.Minute, clock)

	if !s.MarkConsumed("k") {
		t.Fatal("first call should succeed")
	}
	if s.MarkConsumed("k") {
		t.Error("immediate replay should be rejected")
	}

	// Advance clock past TTL — same token may be reused.
	now = now.Add(6 * time.Minute)
	if !s.MarkConsumed("k") {
		t.Error("after TTL expiry, the token should be reusable")
	}
}

func TestMemoryConsumedStore_ConcurrentSafe(t *testing.T) {
	s := NewMemoryConsumedStore(time.Hour)
	var wg sync.WaitGroup
	var trueCount int
	var mu sync.Mutex
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok := s.MarkConsumed("k")
			mu.Lock()
			if ok {
				trueCount++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if trueCount != 1 {
		t.Errorf("exactly one of 100 concurrent MarkConsumed should win; got %d", trueCount)
	}
}
