// Package domain defines the core domain types for runops-gateway.
// It has no external dependencies; only the standard library is used.
package domain

// ResourceType represents the kind of GCP resource to operate on.
type ResourceType string

const (
	// ResourceTypeService targets a Cloud Run service.
	ResourceTypeService ResourceType = "service"
	// ResourceTypeJob targets a Cloud Run job.
	ResourceTypeJob ResourceType = "job"
	// ResourceTypeWorkerPool targets a Cloud Run worker pool.
	ResourceTypeWorkerPool ResourceType = "worker-pool"
)

// ApprovalRequest carries all information needed to approve or deny an operation.
// It is the primary input to RunOpsUseCase and must remain free of
// infrastructure-specific types.
type ApprovalRequest struct {
	// ResourceType is the kind of GCP resource to operate on.
	ResourceType ResourceType
	// ResourceName is the name of the resource, e.g. "frontend-service".
	ResourceName string
	// Target is the revision name or equivalent identifier (optional for jobs).
	Target string
	// Action describes the operation, e.g. "canary_10" or "migrate_apply".
	Action string
	// ApproverID is the Slack user ID or email address of the approver.
	ApproverID string
	// Source identifies the request origin: "slack" or "cli".
	Source string
	// IssuedAt is a Unix timestamp used for expiry checks; 0 means no expiry (CLI mode).
	IssuedAt int64
	// ResponseURL is the Slack response_url for async message updates; empty in CLI mode.
	ResponseURL string
}
