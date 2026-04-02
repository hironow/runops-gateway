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
	// ApproveAction executes the approved operation described by req.
	ApproveAction(ctx context.Context, req domain.ApprovalRequest) error
	// DenyAction notifies the relevant parties that the operation was denied.
	DenyAction(ctx context.Context, req domain.ApprovalRequest) error
}

// GCPController is a secondary port for interacting with GCP resources.
type GCPController interface {
	// ShiftTraffic adjusts traffic on a Cloud Run service revision to the given percent.
	ShiftTraffic(ctx context.Context, serviceName, revision string, percent int32) error
	// ExecuteJob triggers a Cloud Run job with the provided arguments.
	ExecuteJob(ctx context.Context, jobName string, args []string) error
	// TriggerBackup initiates a database backup for the specified Cloud SQL instance.
	TriggerBackup(ctx context.Context, instanceName string) error
	// UpdateWorkerPool shifts instance allocation for a Cloud Run worker pool revision to the given percent.
	UpdateWorkerPool(ctx context.Context, poolName, revision string, percent int32) error
}

// NotifyTarget describes where and how a notification should be delivered.
// It supports both Slack and stdout (CLI) modes without importing Slack-specific types.
type NotifyTarget struct {
	// ResponseURL is the Slack response_url used for async message updates.
	// Empty when Mode is "stdout".
	ResponseURL string
	// Mode is the delivery channel: "slack" or "stdout".
	Mode string
}

// Notifier is a secondary port for sending user-facing notifications.
type Notifier interface {
	// UpdateMessage replaces an existing Slack message (or prints to stdout) with text.
	UpdateMessage(ctx context.Context, target NotifyTarget, text string) error
	// ReplaceMessage replaces an existing message with rich block content.
	ReplaceMessage(ctx context.Context, target NotifyTarget, blocks any) error
	// SendEphemeral sends a message visible only to userID.
	SendEphemeral(ctx context.Context, target NotifyTarget, userID, text string) error
	// OfferContinuation replaces the message with a completion summary and,
	// if nextReq is non-nil, buttons to advance or stop the rollout.
	// stopReq may be nil (no second button shown).
	OfferContinuation(ctx context.Context, target NotifyTarget, summary string, nextReq *domain.ApprovalRequest, stopReq *domain.ApprovalRequest) error
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
	return fmt.Sprintf("%s/%s/%s/%d",
		req.ResourceType, req.ResourceName, req.Action, req.IssuedAt)
}
