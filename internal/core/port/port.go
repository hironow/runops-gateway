// Package port defines the primary and secondary port interfaces for runops-gateway.
// Ports are the boundary between the core domain and external infrastructure.
// Only the "context" standard library package is imported here.
package port

import (
	"context"
	"fmt"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// RunOpsUseCase is the primary port driven by external actors (Slack, CLI).
// Implementations live in the usecase layer and must not import adapter code.
type RunOpsUseCase interface {
	ApproveAction(ctx context.Context, req domain.ApprovalRequest, target NotifyTarget) error
	DenyAction(ctx context.Context, req domain.ApprovalRequest, target NotifyTarget) error
}

// GCPController is a secondary port for interacting with GCP resources.
type GCPController interface {
	// ShiftTraffic adjusts traffic on a Cloud Run service revision to the given percent.
	ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error
	// ExecuteJob triggers a Cloud Run job with extra arguments **appended** to
	// the job's existing Args (not replaced). Implementation must merge
	// `existingArgs + extraArgs` so entry script paths set in terraform survive
	// the override (avoids `node --mode=apply` misinterpretation bug).
	// Passing an empty slice runs the job with its default Args only.
	ExecuteJob(ctx context.Context, project, location, jobName string, extraArgs []string) error
	// TriggerBackup initiates a database backup for the specified Cloud SQL instance.
	TriggerBackup(ctx context.Context, project, instanceName string) error
	// UpdateWorkerPool shifts instance allocation for a Cloud Run worker pool revision to the given percent.
	UpdateWorkerPool(ctx context.Context, project, location, poolName, revision string, percent int32) error
}

// NotifyMode identifies the delivery channel for notifications.
type NotifyMode string

const (
	ModeSlack  NotifyMode = "slack"
	ModeStdout NotifyMode = "stdout"
)

// NotifyTarget describes where and how a notification should be delivered.
//
// CallbackURL (Slack response_url) is the primary transport. ChannelID and
// ThreadTS are populated when the call originates from /slack/interactive so
// FallbackNotifier (ADR 0017) can fall back to chat.postMessage when the
// response_url hits its 30-min / 5-call limits.
type NotifyTarget struct {
	CallbackURL string
	Mode        NotifyMode
	ChannelID   string
	ThreadTS    string
}

// Notifier is a secondary port for sending user-facing notifications.
type Notifier interface {
	UpdateMessage(ctx context.Context, target NotifyTarget, text string) error
	// ReplaceMessage replaces an existing message with a mrkdwn section block.
	ReplaceMessage(ctx context.Context, target NotifyTarget, text string) error
	// SendEphemeral sends a message visible only to userID.
	SendEphemeral(ctx context.Context, target NotifyTarget, userID, text string) error
	// OfferContinuation replaces the message with a completion summary and,
	// if nextReq is non-nil, buttons to advance or stop the rollout.
	// stopReq may be nil (no second button shown).
	OfferContinuation(ctx context.Context, target NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error
	// RebuildInitialApproval replaces the message with the initial approval prompt
	// (the same 3 buttons that cloudbuild's notify-slack.sh emits on first deploy).
	// Used after a recoverable error so the operator returns to a known-good state
	// rather than retrying with stale context. jobReq may be nil when the deployment
	// has no migration job (button "1. DB Migration → Canary" is suppressed).
	// errMsg is rendered above the buttons to explain why the prompt was rebuilt.
	RebuildInitialApproval(ctx context.Context, target NotifyTarget, errMsg string, jobReq, svcReq, denyReq *domain.ApprovalRequest) error
}

// AuthChecker is a secondary port for authorization and expiry validation.
type AuthChecker interface {
	// IsAuthorized reports whether approverID has permission to approve operations.
	IsAuthorized(approverID string) bool
	// IsExpired reports whether the request identified by issuedAt has timed out.
	IsExpired(issuedAt int64) bool
}

// StateStore tracks in-flight operations to prevent double execution.
type StateStore interface {
	// TryLock attempts to claim the operation key.
	// Returns true if successfully claimed (first caller), false if already claimed.
	TryLock(key string) bool
	// Release removes the lock for the given key (call after operation completes).
	Release(key string)
}

// OperationKey returns a canonical deduplication key for an ApprovalRequest.
func OperationKey(req domain.ApprovalRequest) string {
	return fmt.Sprintf("%s/%s/%s/%s/%d",
		req.Project, req.ResourceType, req.ResourceNames, req.Action, req.IssuedAt)
}

// Dispatcher is a secondary port that delivers a DispatchRequest to its target
// agent. Phase 1 implementation (StubDispatcher) only logs the request; Phase 2
// will swap in a Pub/Sub publisher that bridges to phonewave outbox.
type Dispatcher interface {
	// Dispatch hands off req to the underlying transport. Returns an error if
	// the dispatch could not be initiated; the actual agent execution is
	// asynchronous and reported back through a separate channel.
	Dispatch(ctx context.Context, req domain.DispatchRequest) error
}

// ConsumedTokenStore tracks single-use tokens (e.g. dispatch_approve clicks)
// to defeat button replay. Distinct from StateStore because the lifecycle is
// "consume forever within TTL" rather than "lock then release".
//
// Phase 1 backing implementation is in-memory (per Cloud Run instance). Phase 2
// will replace it with a Pub/Sub message ID-based dedup that survives autoscale.
type ConsumedTokenStore interface {
	// MarkConsumed records token as used. Returns true if this is the first
	// time the token was seen (caller may proceed), false if already
	// consumed (caller must reject as a replay).
	MarkConsumed(token string) bool
}

// DMailPublisher hands a DMail to the cross-process transport that delivers
// it to the destination tool's inbox via phonewave.
//
// Phase 2a implementation: Cloud Pub/Sub publisher (publishes to the
// dmail-inbound topic; an exe-coder VM subscriber receives and atomic-writes
// the .md file into a phonewave-watched outbox — see ADR 0013).
//
// Phase 1 / development uses a stub publisher (StubDispatcher) until the
// Pub/Sub infrastructure is provisioned.
type DMailPublisher interface {
	// PublishDMail returns the publisher-assigned message ID on success and
	// an error otherwise. Implementations should set Pub/Sub message
	// attributes per ADR 0013 (kind, target_tool, source,
	// dmail_schema_version, idempotency_key, traceparent).
	PublishDMail(ctx context.Context, m domain.DMail) (string, error)
}
