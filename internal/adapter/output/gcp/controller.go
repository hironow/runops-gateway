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
// Clients are created once and reused across calls.
type Controller struct {
	services    *run.ServicesClient
	jobs        *run.JobsClient
	workerPools *run.WorkerPoolsClient
}

// NewController creates a new GCP Controller with persistent gRPC clients.
func NewController(ctx context.Context) (*Controller, error) {
	svc, err := run.NewServicesClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp: create services client: %w", err)
	}
	jobs, err := run.NewJobsClient(ctx)
	if err != nil {
		svc.Close()
		return nil, fmt.Errorf("gcp: create jobs client: %w", err)
	}
	wp, err := run.NewWorkerPoolsClient(ctx)
	if err != nil {
		svc.Close()
		jobs.Close()
		return nil, fmt.Errorf("gcp: create worker pools client: %w", err)
	}
	return &Controller{services: svc, jobs: jobs, workerPools: wp}, nil
}

// Close releases all underlying gRPC connections.
func (c *Controller) Close() {
	if c.services != nil {
		c.services.Close()
	}
	if c.jobs != nil {
		c.jobs.Close()
	}
	if c.workerPools != nil {
		c.workerPools.Close()
	}
}

// ShiftTraffic updates traffic allocation for a Cloud Run Service revision.
func (c *Controller) ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error {
	slog.InfoContext(ctx, "gcp: shifting traffic",
		"project", project, "location", location, "service", serviceName, "revision", revision, "percent", percent)

	servicePath := fmt.Sprintf("projects/%s/locations/%s/services/%s",
		project, location, serviceName)

	// Cloud Run API v2 requires template in UpdateServiceRequest.
	svc, err := c.services.GetService(ctx, &runpb.GetServiceRequest{Name: servicePath})
	if err != nil {
		return fmt.Errorf("gcp: get service: %w", err)
	}

	entries := make([]trafficEntry, len(svc.Traffic))
	for i, t := range svc.Traffic {
		entries[i] = trafficEntry{revision: t.Revision, percent: t.Percent}
	}

	if isTrafficAlreadyMatching(entries, revision, percent) {
		slog.InfoContext(ctx, "gcp: traffic already at desired state, skipping update",
			"service", serviceName, "revision", revision, "percent", percent)
		return nil
	}

	activeRevision := selectActiveRevision(entries, revision)
	if activeRevision == "" {
		activeRevision = svc.GetLatestReadyRevision()
	}

	traffic := []*runpb.TrafficTarget{
		{
			Type:     runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION,
			Revision: revision,
			Percent:  percent,
		},
	}
	if percent < 100 && activeRevision != "" && activeRevision != revision {
		traffic = append(traffic, &runpb.TrafficTarget{
			Type:     runpb.TrafficTargetAllocationType_TRAFFIC_TARGET_ALLOCATION_TYPE_REVISION,
			Revision: activeRevision,
			Percent:  100 - percent,
		})
	}
	svc.Traffic = traffic

	op, err := c.services.UpdateService(ctx, &runpb.UpdateServiceRequest{Service: svc})
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

// ExecuteJob runs a Cloud Run Job with additional arguments appended to the
// job's existing Args.
//
// Cloud Run RunJobRequest の ContainerOverrides.Args は既存 Args を**完全に
// 置換**する。そのため `extraArgs` だけを渡すと job spec に書かれた entry
// script path (例: `packages/bootstrap/dist/migrate.js`) が消えて、
// `node --mode=apply` のように Node flag として誤解釈される bug が過去に発生
// した。これを避けるため、本メソッドは:
//
//  1. GetJob で現在の Args を取得
//  2. `existing + extraArgs` で ContainerOverride.Args を構築
//
// という "append 意味論" に寄せて API gap を隠蔽する。呼び出し側は「script の
// 既存 args に何を追加するか」だけを意識すれば良い。
//
// extraArgs が空の場合は existing のみ (= job の default 起動) を使用。
func (c *Controller) ExecuteJob(ctx context.Context, project, location, jobName string, extraArgs []string) error {
	slog.InfoContext(ctx, "gcp: executing job", "project", project, "location", location, "job", jobName, "extra_args", extraArgs)

	jobPath := fmt.Sprintf("projects/%s/locations/%s/jobs/%s",
		project, location, jobName)

	// 1. 既存 Args 取得
	job, err := c.jobs.GetJob(ctx, &runpb.GetJobRequest{Name: jobPath})
	if err != nil {
		return fmt.Errorf("gcp: get job for args merge: %w", err)
	}
	var existingArgs []string
	if job.Template != nil && job.Template.Template != nil && len(job.Template.Template.Containers) > 0 {
		existingArgs = job.Template.Template.Containers[0].Args
	}

	mergedArgs := make([]string, 0, len(existingArgs)+len(extraArgs))
	mergedArgs = append(mergedArgs, existingArgs...)
	mergedArgs = append(mergedArgs, extraArgs...)

	slog.InfoContext(ctx, "gcp: merged job args",
		"job", jobName, "existing", existingArgs, "extra", extraArgs, "merged", mergedArgs)

	req := &runpb.RunJobRequest{
		Name: jobPath,
		Overrides: &runpb.RunJobRequest_Overrides{
			ContainerOverrides: []*runpb.RunJobRequest_Overrides_ContainerOverride{
				{Args: mergedArgs},
			},
		},
	}

	op, err := c.jobs.RunJob(ctx, req)
	if err != nil {
		return fmt.Errorf("gcp: run job: %w", err)
	}
	if _, err := op.Wait(ctx); err != nil {
		return fmt.Errorf("gcp: wait run job LRO: %w", err)
	}

	slog.InfoContext(ctx, "gcp: job execution complete", "job", jobName)
	return nil
}

// UpdateWorkerPool shifts instance allocation for a Cloud Run Worker Pool revision.
func (c *Controller) UpdateWorkerPool(ctx context.Context, project, location, poolName, revision string, percent int32) error {
	slog.InfoContext(ctx, "gcp: updating worker pool",
		"project", project, "location", location, "pool", poolName, "revision", revision, "percent", percent)

	poolPath := fmt.Sprintf("projects/%s/locations/%s/workerPools/%s",
		project, location, poolName)

	pool, err := c.workerPools.GetWorkerPool(ctx, &runpb.GetWorkerPoolRequest{Name: poolPath})
	if err != nil {
		return fmt.Errorf("gcp: get worker pool: %w", err)
	}

	entries := make([]trafficEntry, len(pool.InstanceSplits))
	for i, s := range pool.InstanceSplits {
		entries[i] = trafficEntry{revision: s.Revision, percent: s.Percent}
	}

	if isTrafficAlreadyMatching(entries, revision, percent) {
		slog.InfoContext(ctx, "gcp: worker pool already at desired state, skipping update",
			"pool", poolName, "revision", revision, "percent", percent)
		return nil
	}

	activeRevision := selectActiveRevision(entries, revision)
	if activeRevision == "" {
		activeRevision = pool.GetLatestCreatedRevision()
	}

	splits := []*runpb.InstanceSplit{
		{
			Type:     runpb.InstanceSplitAllocationType_INSTANCE_SPLIT_ALLOCATION_TYPE_REVISION,
			Revision: revision,
			Percent:  percent,
		},
	}
	if percent < 100 && activeRevision != "" && activeRevision != revision {
		splits = append(splits, &runpb.InstanceSplit{
			Type:     runpb.InstanceSplitAllocationType_INSTANCE_SPLIT_ALLOCATION_TYPE_REVISION,
			Revision: activeRevision,
			Percent:  100 - percent,
		})
	}
	pool.InstanceSplits = splits

	op, err := c.workerPools.UpdateWorkerPool(ctx, &runpb.UpdateWorkerPoolRequest{WorkerPool: pool})
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
