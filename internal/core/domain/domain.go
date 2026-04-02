// Package domain defines the core domain types for runops-gateway.
// It has no external dependencies; only the standard library is used.
package domain

import (
	"fmt"
	"strconv"
	"strings"
)

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

// Action represents a parsed operation to perform on a resource.
type Action struct {
	// Name is the operation type, e.g. "canary", "migrate_apply", "rollback".
	Name string
	// Percent is the traffic/instance percentage (0 when not applicable).
	Percent int32
}

// ParseAction parses an action string such as "canary_10" or "migrate_apply".
// For actions with a percent suffix (e.g. "canary_10"), Percent is extracted.
// Returns an error if the string is empty or the percent is invalid (not 0-100).
func ParseAction(s string) (Action, error) {
	if s == "" {
		return Action{}, fmt.Errorf("action string must not be empty")
	}
	parts := strings.SplitN(s, "_", 2)
	// Actions without a numeric suffix (e.g. "rollback")
	if len(parts) == 1 {
		return Action{Name: s}, nil
	}
	// Try to parse the suffix as a percent value
	n, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		// Suffix is not numeric — treat whole string as the name (e.g. "migrate_apply")
		return Action{Name: s}, nil
	}
	if n < 0 || n > 100 {
		return Action{}, fmt.Errorf("action percent must be between 0 and 100, got %d", n)
	}
	return Action{Name: parts[0], Percent: int32(n)}, nil
}

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
