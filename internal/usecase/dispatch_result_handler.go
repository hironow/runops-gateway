package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// DispatchResultHandler routes a D-Mail received from the dmail-outbound topic
// (i.e. a 5-pillar tool's response to a previously-dispatched specification)
// back into the originating Slack thread.
//
// Phase 3 (ADR 0018) lives in the use case layer rather than directly in the
// subscriber adapter so the kind→Slack-text policy can be unit-tested without
// the Pub/Sub SDK; the OutboundSubscriber only owns the receive loop and ack
// semantics. See FallbackNotifier (ADR 0017) for the actual Bot Token /
// chat.postMessage transport.
type DispatchResultHandler struct {
	notifier port.Notifier
}

// NewDispatchResultHandler wires a result handler around any port.Notifier
// (typically a FallbackNotifier whose underlying Bot Token can post to a
// thread without a live response_url).
func NewDispatchResultHandler(n port.Notifier) *DispatchResultHandler {
	return &DispatchResultHandler{notifier: n}
}

// Handle renders mail into a Slack message and posts it back to the originating
// thread. Returns nil for messages that cannot be routed (missing slack_*
// metadata, unknown kind) so the caller acks-and-drops; returns an error only
// when the notifier itself fails (caller nacks for retry).
func (h *DispatchResultHandler) Handle(ctx context.Context, mail domain.DMail) error {
	channel := mail.Metadata["slack_channel_id"]
	thread := mail.Metadata["slack_thread_ts"]
	parent := mail.Metadata["parent_idempotency_key"]
	if channel == "" || thread == "" || parent == "" {
		slog.WarnContext(ctx, "dispatch result: missing slack metadata, dropping",
			"kind", mail.Kind, "target", mail.Target,
			"has_channel", channel != "", "has_thread", thread != "", "has_parent", parent != "")
		return nil
	}

	text, ok := renderResultMessage(mail)
	if !ok {
		slog.WarnContext(ctx, "dispatch result: unknown kind, dropping",
			"kind", mail.Kind, "target", mail.Target)
		return nil
	}

	target := port.NotifyTarget{
		Mode:      port.ModeSlack,
		ChannelID: channel,
		ThreadTS:  thread,
		// CallbackURL intentionally empty — the original /agent's response_url
		// is long expired by the time results come back through Pub/Sub. The
		// FallbackNotifier handles this by going straight to chat.postMessage
		// with channel + thread_ts.
	}

	if err := h.notifier.UpdateMessage(ctx, target, text); err != nil {
		return fmt.Errorf("dispatch result: notify thread: %w", err)
	}
	return nil
}

// renderResultMessage formats mail's body for Slack. Returns (text, true) for
// known kinds; (_, false) for unknown ones so the caller knows to drop.
//
// The leading emoji + "{source} → {target}" header makes channel scrollback
// scannable: operators can tell at a glance whether a thread reply came from
// paintress finishing a fix, amadeus flagging a divergence, etc. Body is
// appended verbatim so links and PR numbers in the original D-Mail survive.
func renderResultMessage(mail domain.DMail) (string, bool) {
	emoji, ok := kindEmoji(mail.Kind)
	if !ok {
		return "", false
	}
	header := fmt.Sprintf("%s *%s* → *%s*", emoji, mail.Source, mail.Target)
	switch mail.Kind {
	case domain.DMailKindReport:
		return fmt.Sprintf("%s 完了報告\n%s", header, mail.Body), true
	case domain.DMailKindDesignFeedback:
		return fmt.Sprintf("%s 設計フィードバック\n%s", header, mail.Body), true
	case domain.DMailKindImplementationFeedback:
		return fmt.Sprintf("%s 実装フィードバック\n%s", header, mail.Body), true
	case domain.DMailKindConvergence:
		return fmt.Sprintf("%s 世界線収束アラート\n%s", header, mail.Body), true
	case domain.DMailKindCIResult:
		return fmt.Sprintf("%s CI 結果\n%s", header, mail.Body), true
	default:
		return "", false
	}
}

func kindEmoji(k domain.DMailKind) (string, bool) {
	switch k {
	case domain.DMailKindReport:
		return "✅", true
	case domain.DMailKindDesignFeedback:
		return "🎨", true
	case domain.DMailKindImplementationFeedback:
		return "🔧", true
	case domain.DMailKindConvergence:
		return "🌐", true
	case domain.DMailKindCIResult:
		return "🚦", true
	default:
		return "", false
	}
}
