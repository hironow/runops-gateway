package observability

import (
	"context"
	"sync"
	"time"
)

// PendingTracker tracks goroutines spawned outside the HTTP request lifecycle
// (the Slack 3-second async pattern from ADR 0002). Per the OTel SDK spec,
// BatchSpanProcessor.Shutdown only flushes spans that have already been
// End()-ed; an in-flight span whose goroutine has not yet finished is
// silently dropped. PendingTracker lets main wait on these goroutines before
// tp.Shutdown so every span gets End()-ed (and thus enqueued) before the SDK
// shuts down.
//
// The shutdown ordering this enables is:
//
//	srv.Shutdown(ctx)             // (1) HTTP graceful, drains live requests
//	pending.WaitWithDeadline(ctx) // (2) wait for goroutines spawned by handlers
//	tp.Shutdown(ctx)              // (3) BSP final OTLP export
//
// Cloud Run's 10s SIGTERM grace must absorb all three; a 4s/4s/2s budget is
// the prod default. See experiments/2026-05-06_otel-goroutine-flush-cloudrun.md.
//
// PendingTracker is safe for concurrent use; the zero value is ready.
type PendingTracker struct {
	wg sync.WaitGroup
}

// Go runs fn in a tracked goroutine. fn MUST contain the entire goroutine
// body (including any tracer.Start/span.End pair). Returns immediately.
//
// Calling Go after Wait has returned is allowed but produces a goroutine
// the next Wait will track; do not call Go after tp.Shutdown.
func (p *PendingTracker) Go(fn func()) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		fn()
	}()
}

// Wait blocks until all tracked goroutines exit or until ctx is cancelled,
// whichever comes first. Caller is responsible for sizing ctx's deadline
// within Cloud Run's 10s SIGTERM grace.
func (p *PendingTracker) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// WaitWithDeadline is sugar for Wait with a fresh deadline derived from now.
// Use this from main's shutdown path; the parent context typically inherits
// the overall shutdown budget.
func (p *PendingTracker) WaitWithDeadline(parent context.Context, d time.Duration) {
	ctx, cancel := context.WithTimeout(parent, d)
	defer cancel()
	p.Wait(ctx)
}
