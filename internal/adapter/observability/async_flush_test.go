package observability_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestAsyncSpan_LostOnShutdownBeforeGoroutineEnds is the RED test that
// reproduces the production observation: an HTTP handler returns 200
// immediately and spawns a goroutine that creates and ends a span later.
// If TracerProvider.Shutdown is invoked before the goroutine ends its
// span, the span never reaches the exporter — Shutdown can only flush
// what is already enqueued in the BatchSpanProcessor's queue, and a
// span that hasn't been End()-ed isn't yet enqueued.
//
// In Cloud Run min_instance_count=0 deploys this races with idle
// shutdown after the HTTP request returns, which is why the dispatch
// trace (`/slack/interactive` Approve -> goroutine -> usecase span)
// is sometimes missing from Cloud Trace while the synchronous root
// span (`/slack/command` -> Block Kit return) makes it through.
//
// Until this test goes GREEN we have a documented bug; the fix lives
// behind a follow-up commit (PendingTracker / WaitGroup pattern).
func TestAsyncSpan_LostOnShutdownBeforeGoroutineEnds(t *testing.T) {
	t.Skip(
		"RED test pinned for follow-up GREEN PR. Reproduces lost-span bug " +
			"observed in prod (see docs/issues/0004 + handover.md hammer-spot 7). " +
			"Remove this Skip line once PendingTracker / wg-based flush is in place.",
	)

	// given: in-memory exporter wired through a BSP whose flush schedule
	//        matches the production OTEL_BSP_SCHEDULE_DELAY (2s)
	exp := tracetest.NewInMemoryExporter()
	bsp := sdktrace.NewBatchSpanProcessor(exp,
		sdktrace.WithBatchTimeout(2*time.Second))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(bsp))
	tracer := tp.Tracer("test")

	var (
		handlerDone    = make(chan struct{})
		goroutineEnded = make(chan struct{})
		wg             sync.WaitGroup
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mirror PR #8 pattern: detach cancellation, span continues
		// after ServeHTTP returns.
		traceCtx := context.WithoutCancel(r.Context())
		w.WriteHeader(http.StatusOK)
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Simulate a slow Pub/Sub publish round-trip (~150ms).
			time.Sleep(150 * time.Millisecond)
			_, span := tracer.Start(traceCtx, "usecase.dispatch_agent_task")
			time.Sleep(50 * time.Millisecond)
			span.End()
			close(goroutineEnded)
		}()
		close(handlerDone)
	})

	srv := httptest.NewServer(handler)
	defer srv.Close()

	// when: client gets 200 and we immediately Shutdown — simulating
	//       Cloud Run idle-kill the moment the HTTP request finishes.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("client GET: %v", err)
	}
	_ = resp.Body.Close()
	<-handlerDone

	// 500ms is more than enough for the goroutine to finish (200ms total)
	// IF something waited for it. Without that wait, Shutdown returns
	// while the goroutine is still mid-flight and the span never lands.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := tp.Shutdown(shutdownCtx); err != nil {
		// Shutdown error is fine here — we are diagnosing exporter
		// content, not Shutdown's return value.
		t.Logf("tp.Shutdown returned %v", err)
	}

	// Sanity: clean up the goroutine before asserting (so we know the
	// span really did get End()-ed before the test's t.Fatalf).
	wg.Wait()
	select {
	case <-goroutineEnded:
	default:
		t.Fatal("goroutine never closed goroutineEnded channel")
	}

	// then (RED expectation):
	// the dispatch span IS missing from the exporter, despite the
	// goroutine having End()-ed it before this assert ran.
	got := exp.GetSpans()
	if len(got) != 1 {
		t.Fatalf(
			"expected 1 span exported, got %d (RED reproduction of the lost-span bug); "+
				"the goroutine ended the span AFTER tp.Shutdown returned, so BSP could not flush it",
			len(got),
		)
	}
}
