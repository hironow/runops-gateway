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

// Controller implements port.GCPController using GCP client libraries.
type Controller struct{}

// NewController creates a new GCP Controller.
func NewController() *Controller {
	return &Controller{}
}

// ShiftTraffic updates traffic allocation for a Cloud Run Service revision.
func (c *Controller) ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error {
	slog.InfoContext(ctx, "gcp: shifting traffic",
		"project", project, "location", location, "service", serviceName, "revision", revision, "percent", percent)

	client, err := run.NewServicesClient(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create services client: %w", err)
	}
	defer client.Close()

	servicePath := fmt.Sprintf("projects/%s/locations/%s/services/%s",
		project, location, serviceName)

	// GET the current service to preserve template and other fields.
	// Cloud Run API v2 requires template in UpdateServiceRequest.
	svc, err := client.GetService(ctx, &runpb.GetServiceRequest{Name: servicePath})
	if err != nil {
		return fmt.Errorf("gcp: get service: %w", err)
	}

	svc.Traffic = []*runpb.TrafficTarget{
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
	}

	op, err := client.UpdateService(ctx, &runpb.UpdateServiceRequest{Service: svc})
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
func (c *Controller) ExecuteJob(ctx context.Context, project, location, jobName string, args []string) error {
	slog.InfoContext(ctx, "gcp: executing job", "project", project, "location", location, "job", jobName, "args", args)

	client, err := run.NewJobsClient(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create jobs client: %w", err)
	}
	defer client.Close()

	jobPath := fmt.Sprintf("projects/%s/locations/%s/jobs/%s",
		project, location, jobName)

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

// UpdateWorkerPool shifts instance allocation for a Cloud Run Worker Pool revision to the given percent.
func (c *Controller) UpdateWorkerPool(ctx context.Context, project, location, poolName, revision string, percent int32) error {
	slog.InfoContext(ctx, "gcp: updating worker pool",
		"project", project, "location", location, "pool", poolName, "revision", revision, "percent", percent)

	client, err := run.NewWorkerPoolsClient(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create worker pools client: %w", err)
	}
	defer client.Close()

	poolPath := fmt.Sprintf("projects/%s/locations/%s/workerPools/%s",
		project, location, poolName)

	// GET the current worker pool to preserve template and other fields.
	// Cloud Run API v2 requires template in UpdateWorkerPoolRequest.
	pool, err := client.GetWorkerPool(ctx, &runpb.GetWorkerPoolRequest{Name: poolPath})
	if err != nil {
		return fmt.Errorf("gcp: get worker pool: %w", err)
	}

	pool.InstanceSplits = []*runpb.InstanceSplit{
		{
			Type:     runpb.InstanceSplitAllocationType_INSTANCE_SPLIT_ALLOCATION_TYPE_REVISION,
			Revision: revision,
			Percent:  percent,
		},
		{
			Type:    runpb.InstanceSplitAllocationType_INSTANCE_SPLIT_ALLOCATION_TYPE_LATEST,
			Percent: 100 - percent,
		},
	}

	op, err := client.UpdateWorkerPool(ctx, &runpb.UpdateWorkerPoolRequest{WorkerPool: pool})
	if err != nil {
		return fmt.Errorf("gcp: update worker pool: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("gcp: wait update worker pool LRO: %w", err)
	}

	slog.InfoContext(ctx, "gcp: worker pool update complete",
		"pool", poolName, "revision", revision, "percent", percent)
	return nil
}

// TriggerBackup initiates an on-demand Cloud SQL backup and waits for completion.
func (c *Controller) TriggerBackup(ctx context.Context, project, instanceName string) error {
	slog.InfoContext(ctx, "gcp: triggering cloud sql backup", "project", project, "instance", instanceName)

	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		return fmt.Errorf("gcp: create sqladmin service: %w", err)
	}

	backupRun := &sqladmin.BackupRun{
		Description: "Triggered by runops-gateway before migration",
	}

	op, err := svc.BackupRuns.Insert(project, instanceName, backupRun).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gcp: insert backup run: %w", err)
	}

	// Poll until done (Cloud SQL uses its own Operation type, not LRO)
	for {
		status, err := svc.Operations.Get(project, op.Name).Context(ctx).Do()
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
