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
	req := domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeService,
		ResourceNames: "frontend-service",
		Targets:       "frontend-service-v2",
		Action:        "canary_10",
		ApproverID:    "U12345678",
		IssuedAt:      1700000000,
	}

	if req.ResourceType != domain.ResourceTypeService {
		t.Errorf("ResourceType = %q, want %q", req.ResourceType, domain.ResourceTypeService)
	}
	if req.ResourceNames != "frontend-service" {
		t.Errorf("ResourceNames = %q, want %q", req.ResourceNames, "frontend-service")
	}
	if req.IssuedAt != 1700000000 {
		t.Errorf("IssuedAt = %d, want %d", req.IssuedAt, 1700000000)
	}
}

func TestApprovalRequestCLIMode(t *testing.T) {
	// CLI mode uses IssuedAt == 0 (no expiry)
	req := domain.ApprovalRequest{
		Project:       "test-project",
		Location:      "asia-northeast1",
		ResourceType:  domain.ResourceTypeJob,
		ResourceNames: "migrate-job",
		Action:        "migrate_apply",
		ApproverID:    "admin@example.com",
		IssuedAt:      0,
	}

	if req.IssuedAt != 0 {
		t.Errorf("CLI mode IssuedAt should be 0, got %d", req.IssuedAt)
	}
}
