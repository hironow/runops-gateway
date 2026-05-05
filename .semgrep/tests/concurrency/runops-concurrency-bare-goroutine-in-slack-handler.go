// This file is for semgrep rule testing only — it is intentionally placed
// outside any Go package directory so 'go build ./...' won't compile it.
//
//go:build never

package fake_slack_handler

func bad() {
	// ruleid: runops-concurrency-bare-goroutine-in-slack-handler
	go func() {
		_ = "violates rule: should use h.goAsync(func() {...})"
	}()
}

func good(h *Handler) {
	// ok: rule does not trigger when using goAsync
	h.goAsync(func() {
		_ = "fine"
	})
}

type Handler struct{}

func (h *Handler) goAsync(fn func()) { fn() }
