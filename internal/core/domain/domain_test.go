package domain_test

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestResourceTypeConstants(t *testing.T) {
	// given / when / then
	if domain.ResourceTypeService != "service" {
		t.Errorf("ResourceTypeService = %q, want %q", domain.ResourceTypeService, "service")
	}
	if domain.ResourceTypeJob != "job" {
		t.Errorf("ResourceTypeJob = %q, want %q", domain.ResourceTypeJob, "job")
	}
	if domain.ResourceTypeWorkerPool != "worker-pool" {
		t.Errorf("ResourceTypeWorkerPool = %q, want %q", domain.ResourceTypeWorkerPool, "worker-pool")
	}
}

func TestApprovalRequestZeroValue(t *testing.T) {
	// given
	var req domain.ApprovalRequest

	// when / then — zero value must be constructible without panics
	if req.ResourceType != "" {
		t.Errorf("expected empty ResourceType, got %q", req.ResourceType)
	}
	if req.IssuedAt != 0 {
		t.Errorf("expected IssuedAt == 0, got %d", req.IssuedAt)
	}
}

func TestApprovalRequestFields(t *testing.T) {
	// given
	req := domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeService,
		ResourceName: "frontend-service",
		Target:       "frontend-service-v2",
		Action:       "canary_10",
		ApproverID:   "U12345678",
		Source:       "slack",
		IssuedAt:     1700000000,
		ResponseURL:  "https://hooks.slack.com/actions/xxx",
	}

	// when / then
	if req.ResourceType != domain.ResourceTypeService {
		t.Errorf("ResourceType = %q, want %q", req.ResourceType, domain.ResourceTypeService)
	}
	if req.ResourceName != "frontend-service" {
		t.Errorf("ResourceName = %q, want %q", req.ResourceName, "frontend-service")
	}
	if req.Source != "slack" {
		t.Errorf("Source = %q, want %q", req.Source, "slack")
	}
	if req.IssuedAt != 1700000000 {
		t.Errorf("IssuedAt = %d, want %d", req.IssuedAt, 1700000000)
	}
}

func TestApprovalRequestCLIMode(t *testing.T) {
	// given — CLI mode has IssuedAt == 0 and empty ResponseURL
	req := domain.ApprovalRequest{
		ResourceType: domain.ResourceTypeJob,
		ResourceName: "migrate-job",
		Action:       "migrate_apply",
		ApproverID:   "admin@example.com",
		Source:       "cli",
		IssuedAt:     0,
		ResponseURL:  "",
	}

	// when / then
	if req.IssuedAt != 0 {
		t.Errorf("CLI mode IssuedAt should be 0, got %d", req.IssuedAt)
	}
	if req.ResponseURL != "" {
		t.Errorf("CLI mode ResponseURL should be empty, got %q", req.ResponseURL)
	}
}
