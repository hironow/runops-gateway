package observability_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/observability"
)

// TestPendingTracker_Wait_BlocksUntilGoroutinesReturn covers the happy path:
// Wait(ctx) returns only after every goroutine started via Go(fn) has exited.
func TestPendingTracker_Wait_BlocksUntilGoroutinesReturn(t *testing.T) {
	// given
	var p observability.PendingTracker
	const n = 5
	var done atomic.Int32

	for range n {
		p.Go(func() {
			time.Sleep(80 * time.Millisecond)
			done.Add(1)
		})
	}

	// when
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	p.Wait(ctx)

	// then
	if got := done.Load(); got != int32(n) {
		t.Fatalf("Wait returned with done=%d, want %d (Wait did not block long enough)", got, n)
	}
	if ctx.Err() != nil {
		t.Errorf("ctx was cancelled (%v); Wait should have returned because goroutines finished, not on deadline", ctx.Err())
	}
}

// TestPendingTracker_Wait_RespectsCtxDeadline covers the deadline path:
// when goroutines outlive the ctx deadline, Wait still returns at the
// deadline (caller decides what to do with the leak).
func TestPendingTracker_Wait_RespectsCtxDeadline(t *testing.T) {
	// given — a goroutine that takes far longer than the deadline
	var p observability.PendingTracker
	finished := make(chan struct{})

	p.Go(func() {
		time.Sleep(500 * time.Millisecond)
		close(finished)
	})

	// when
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	p.Wait(ctx)
	elapsed := time.Since(start)

	// then — Wait must have returned around the deadline, not 500ms later
	if elapsed > 250*time.Millisecond {
		t.Errorf("Wait took %v; expected ~100ms (the ctx deadline)", elapsed)
	}
	// the goroutine itself may still be running; let cleanup finish
	select {
	case <-finished:
	case <-time.After(1 * time.Second):
		t.Fatal("background goroutine never finished; this is a test setup bug")
	}
}

// TestPendingTracker_WaitWithDeadline_ConvenienceWrapper just confirms the
// WaitWithDeadline sugar matches the manual ctx.WithTimeout + Wait flow.
func TestPendingTracker_WaitWithDeadline_ConvenienceWrapper(t *testing.T) {
	// given
	var p observability.PendingTracker
	finished := atomic.Bool{}

	p.Go(func() {
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})

	// when
	start := time.Now()
	p.WaitWithDeadline(context.Background(), 1*time.Second)
	elapsed := time.Since(start)

	// then
	if !finished.Load() {
		t.Errorf("goroutine had time but never set finished")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("WaitWithDeadline took %v; expected ~50ms", elapsed)
	}
}
