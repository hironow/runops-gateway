package gcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	run "cloud.google.com/go/run/apiv2"
	runpb "cloud.google.com/go/run/apiv2/runpb"
	"google.golang.org/api/sqladmin/v1"
)

// Config holds GCP project and location settings.
type Config struct {
	ProjectID string // GOOGLE_CLOUD_PROJECT
	Location  string // CLOUD_RUN_LOCATION (e.g. "asia-northeast1")
}

// Controller implements port.GCPController using GCP client libraries.
type Controller struct {
	cfg Config
}

// NewController creates a new GCP Controller. Returns error if config is invalid.
func NewController(cfg Config) (*Controller, error) {
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gcp: ProjectID is required")
	}
	if cfg.Location == "" {
		cfg.Location = "asia-northeast1"
	}
	return &Controller{cfg: cfg}, nil
}

// Location returns the configured GCP location.
func (c *Controller) Location() string {
	return c.cfg.Location
}

// ShiftTraffic updates traffic allocation for a Cloud Run Service revision.
func (c *Controller) ShiftTraffic(ctx context.Context, serviceName, revision string, percent int32) error {
	slog.InfoContext(ctx, "gcp: shifting traffic",
		"service", serviceName, "revision", revision, "percent", percent)

	client, err := run.NewServicesClient(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create services client: %w", err)
	}
	defer client.Close()

	servicePath := fmt.Sprintf("projects/%s/locations/%s/services/%s",
		c.cfg.ProjectID, c.cfg.Location, serviceName)

	req := &runpb.UpdateServiceRequest{
		Service: &runpb.Service{
			Name: servicePath,
			Traffic: []*runpb.TrafficTarget{
				{
					Type:     runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION,
					Revision: revision,
					Percent:  percent,
				},
				{
					Type:    runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION,
					Tag:     "previous",
					Percent: 100 - percent,
				},
			},
		},
	}

	op, err := client.UpdateService(ctx, req)
	if err != nil {
		return fmt.Errorf("gcp: update service: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("gcp: wait update service LRO: %w", err)
	}

	slog.InfoContext(ctx, "gcp: traffic shift complete",
		"service", serviceName, "revision", revision, "percent", percent)
	return nil
}

// ExecuteJob runs a Cloud Run Job with optional argument overrides.
func (c *Controller) ExecuteJob(ctx context.Context, jobName string, args []string) error {
	slog.InfoContext(ctx, "gcp: executing job", "job", jobName, "args", args)

	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create jobs client: %w", err)
	}
	defer client.Close()

	jobPath := fmt.Sprintf("projects/%s/locations/%s/jobs/%s",
		c.cfg.ProjectID, c.cfg.Location, jobName)

	req := &runpb.RunJobRequest{
		Name: jobPath,
		Overrides: &runpb.RunJobRequest_Overrides{
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{
				{Args: args},
			},
		},
	}

	op, err := client.RunJob(ctx, req)
	if err != nil {
		return fmt.Errorf("gcp: run job: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("gcp: wait run job LRO: %w", err)
	}

	slog.InfoContext(ctx, "gcp: job execution complete", "job", jobName)
	return nil
}

// TriggerBackup initiates an on-demand Cloud SQL backup and waits for completion.
func (c *Controller) TriggerBackup(ctx context.Context, instanceName string) error {
	slog.InfoContext(ctx, "gcp: triggering cloud sql backup", "instance", instanceName)

	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create sqladmin service: %w", err)
	}

	backupRun := &sqladmin.BackupRun{
		Description: "Triggered by runops-gateway before migration",
	}

	op, err := svc.BackupRuns.Insert(c.cfg.ProjectID, instanceName, backupRun).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gcp: insert backup run: %w", err)
	}

	// Poll until done (Cloud SQL uses its own Operation type, not LRO)
	for {
		status, err := svc.Operations.Get(c.cfg.ProjectID, op.Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("gcp: get backup operation: %w", err)
		}
		if status.Status == "DONE" {
			if status.Error != nil && len(status.Error.Errors) > 0 {
				return fmt.Errorf("gcp: backup failed: %s", status.Error.Errors[0].Message)
			}
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
	}

	slog.InfoContext(ctx, "gcp: backup complete", "instance", instanceName)
	return nil
}
