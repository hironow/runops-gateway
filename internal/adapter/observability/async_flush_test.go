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

	"github.com/hironow/runops-gateway/internal/adapter/observability"
)

// Note on test plumbing:
//
// `tracetest.InMemoryExporter.Shutdown` calls Reset() under the hood — once
// `tp.Shutdown` runs, the exporter forgets every span it ever held. We
// therefore use `tp.ForceFlush` (which spec'd to flush every End()-ed span
// without tearing the exporter down) to drive the SUT, then assert against
// `exp.GetSpans()`, then call `tp.Shutdown` for cleanup. This mirrors what
// the production sequence does in effect: Shutdown ultimately needs to
// flush, and ForceFlush is what tells us whether End()-ing happened in
// time.

// TestAsyncSpan_GoroutineFlush exercises the PendingTracker fix for the
// lost-span bug observed in production (dispatch idem
// d06bb726c5b21ee1b48f9d6aaa9023cb).
//
// Two sub-tests:
//
//  1. WithoutPendingTracker — bug-as-spec. ForceFlush is called right
//     after ServeHTTP returns. The goroutine has not yet End()-ed its
//     span, BSP cannot flush a span that was never enqueued, span lost.
//     This is the regression guard so a refactor that removes the
//     pending.Wait can't silently degrade observability.
//
//  2. WithPendingTracker — fix verified. The same sequence wraps the
//     goroutine in PendingTracker.Go and runs pending.Wait before
//     ForceFlush. The goroutine finishes End()-ing its span before the
//     SDK flushes; the span lands in the exporter.
//
// Background: experiments/2026-05-06_otel-goroutine-flush-cloudrun.md.
func TestAsyncSpan_GoroutineFlush(t *testing.T) {
	t.Run("WithoutPendingTracker_LostBecauseGoroutineHasNotEndedYet", func(t *testing.T) {
		exp := tracetest.NewInMemoryExporter()
		bsp := sdktrace.NewBatchSpanProcessor(exp,
			sdktrace.WithBatchTimeout(2*time.Second))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(bsp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		tracer := tp.Tracer("test")

		var (
			handlerDone    = make(chan struct{})
			goroutineEnded = make(chan struct{})
			wg             sync.WaitGroup
		)

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceCtx := context.WithoutCancel(r.Context())
			w.WriteHeader(http.StatusOK)
			wg.Add(1)
			go func() {
				defer wg.Done()
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
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("client GET: %v", err)
		}
		_ = resp.Body.Close()
		<-handlerDone

		// Cloud Run idle-kill simulation: ForceFlush immediately. There is
		// nothing to flush yet because the goroutine has not End()-ed its
		// span (it's in the middle of its 150ms sleep).
		flushCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if err := tp.ForceFlush(flushCtx); err != nil {
			t.Logf("ForceFlush returned %v (acceptable: nothing to flush)", err)
		}

		got := exp.GetSpans()
		if len(got) != 0 {
			t.Errorf("without PendingTracker, expected span to be lost (0 exported); got %d", len(got))
		}

		// cleanup: let the goroutine finish so subsequent tests start
		// from a clean state
		wg.Wait()
		<-goroutineEnded
	})

	t.Run("WithPendingTracker_FlushedBeforeShutdown", func(t *testing.T) {
		exp := tracetest.NewInMemoryExporter()
		bsp := sdktrace.NewBatchSpanProcessor(exp,
			sdktrace.WithBatchTimeout(2*time.Second))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(bsp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		tracer := tp.Tracer("test")
		var pending observability.PendingTracker

		handlerDone := make(chan struct{})
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			traceCtx := context.WithoutCancel(r.Context())
			w.WriteHeader(http.StatusOK)
			pending.Go(func() {
				time.Sleep(150 * time.Millisecond)
				_, span := tracer.Start(traceCtx, "usecase.dispatch_agent_task")
				time.Sleep(50 * time.Millisecond)
				span.End()
			})
			close(handlerDone)
		})

		srv := httptest.NewServer(handler)
		defer srv.Close()
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatalf("client GET: %v", err)
		}
		_ = resp.Body.Close()
		<-handlerDone

		// Mirror cmd/server/main.go shutdown ordering:
		//   (1) HTTP graceful — already done via srv.Close above
		//   (2) wait pending goroutines so spans are End()-ed
		//   (3) ForceFlush (the production code calls tp.Shutdown which
		//       internally flushes; we use ForceFlush so the in-memory
		//       exporter doesn't reset)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		pending.WaitWithDeadline(shutdownCtx, 4*time.Second)
		if err := tp.ForceFlush(shutdownCtx); err != nil {
			t.Errorf("ForceFlush: %v", err)
		}

		got := exp.GetSpans()
		if len(got) != 1 {
			t.Fatalf("with PendingTracker, expected 1 span exported, got %d", len(got))
		}
		if got[0].Name != "usecase.dispatch_agent_task" {
			t.Errorf("span name = %q, want %q", got[0].Name, "usecase.dispatch_agent_task")
		}
	})
}

// TestShutdownOrder_PendingMustWaitBeforeFlush documents the ordering
// contract: pending.Wait MUST run before flush. Reversing the order
// reproduces the bug even with PendingTracker in place.
func TestShutdownOrder_PendingMustWaitBeforeFlush(t *testing.T) {
	t.Run("ReversedOrder_FlushFirst_LosesSpan", func(t *testing.T) {
		exp := tracetest.NewInMemoryExporter()
		bsp := sdktrace.NewBatchSpanProcessor(exp,
			sdktrace.WithBatchTimeout(2*time.Second))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(bsp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		tracer := tp.Tracer("test")
		var pending observability.PendingTracker

		started := make(chan struct{})
		pending.Go(func() {
			time.Sleep(150 * time.Millisecond)
			_, span := tracer.Start(context.Background(), "usecase.dispatch_agent_task")
			time.Sleep(50 * time.Millisecond)
			span.End()
			close(started)
		})

		// WRONG ordering: flush before goroutine has End()-ed.
		flushCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := tp.ForceFlush(flushCtx); err != nil {
			t.Logf("ForceFlush: %v", err)
		}
		// Now wait for the goroutine and re-check — the span End() that
		// happens AFTER ForceFlush is silently late.
		pending.WaitWithDeadline(context.Background(), 1*time.Second)
		<-started

		got := exp.GetSpans()
		if len(got) != 0 {
			t.Errorf("reversed ordering: expected 0 spans seen by ForceFlush, got %d", len(got))
		}
	})

	t.Run("CorrectOrder_PendingThenFlush_DeliversSpan", func(t *testing.T) {
		exp := tracetest.NewInMemoryExporter()
		bsp := sdktrace.NewBatchSpanProcessor(exp,
			sdktrace.WithBatchTimeout(2*time.Second))
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(bsp))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		tracer := tp.Tracer("test")
		var pending observability.PendingTracker

		pending.Go(func() {
			time.Sleep(150 * time.Millisecond)
			_, span := tracer.Start(context.Background(), "usecase.dispatch_agent_task")
			time.Sleep(50 * time.Millisecond)
			span.End()
		})

		flushCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		pending.WaitWithDeadline(flushCtx, 4*time.Second)
		if err := tp.ForceFlush(flushCtx); err != nil {
			t.Errorf("ForceFlush: %v", err)
		}

		got := exp.GetSpans()
		if len(got) != 1 {
			t.Fatalf("correct ordering: expected 1 span, got %d", len(got))
		}
	})
}

// TestServer_GracefulHTTPShutdown_WaitsForInFlight is the regression guard
// for the **HTTP layer**: srv.Shutdown(ctx) must let already-running
// ServeHTTP calls finish (independent of any goroutine they spawn). This
// is the standard net/http guarantee and works without OTel; we test it
// here so a future refactor that swaps the server stack doesn't silently
// regress.
func TestServer_GracefulHTTPShutdown_WaitsForInFlight(t *testing.T) {
	handlerDone := make(chan struct{})
	handlerStarted := make(chan struct{})

	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(handlerStarted)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		close(handlerDone)
	})

	srv := httptest.NewUnstartedServer(mux)
	srv.Start()
	defer srv.Close()

	clientErr := make(chan error, 1)
	go func() {
		resp, err := http.Get(srv.URL + "/slow")
		if err == nil {
			_ = resp.Body.Close()
		}
		clientErr <- err
	}()

	// wait for the handler to actually start before shutting down
	<-handlerStarted

	srv.Config.SetKeepAlivesEnabled(false)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := srv.Config.Shutdown(shutdownCtx); err != nil {
		t.Errorf("Shutdown returned %v", err)
	}

	select {
	case <-handlerDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Shutdown returned before handler finished — graceful guarantee broken")
	}

	if err := <-clientErr; err != nil {
		t.Logf("client returned %v (acceptable; the handler finished server-side)", err)
	}
}
