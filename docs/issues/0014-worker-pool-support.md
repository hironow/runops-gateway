# Issue 0014: Worker Pool 対応

## Goal

`GCPController` に `UpdateWorkerPool` を追加し、UseCase の worker-pool フローを完成させる。

## 変更内容

### port に追加 (`internal/core/port/port.go`)

```go
type GCPController interface {
    ShiftTraffic(ctx context.Context, serviceName, revision string, percent int32) error
    ExecuteJob(ctx context.Context, jobName string, args []string) error
    TriggerBackup(ctx context.Context, instanceName string) error
    // 追加
    UpdateWorkerPool(ctx context.Context, poolName, revision string, percent int32) error
}
```

### GCP adapter に実装 (`internal/adapter/output/gcp/controller.go`)

```go
func (c *Controller) UpdateWorkerPool(ctx context.Context, poolName, revision string, percent int32) error
```

- `cloud.google.com/go/run/apiv2` の `WorkerPoolsClient.UpdateWorkerPool` を使用
- Worker Pool は HTTP エンドポイントを持たないためトラフィックではなく
  `InstanceAllocation` をリビジョン間で分割する

### UseCase の worker-pool ハンドラを修正 (`internal/usecase/runops.go`)

現在の stub `ShiftTraffic` 呼び出しを `UpdateWorkerPool` に置き換える。

## Definition of Done (DoD)

- [ ] `port.GCPController` に `UpdateWorkerPool` が追加されコンパイルが通る
- [ ] 既存の全モック（`usecase/runops_test.go` 等）に `UpdateWorkerPool` stub が追加される
- [ ] GCP adapter のテストに `TestUpdateWorkerPool_CancelledContext` が存在する
- [ ] UseCase の `TestApproveAction_WorkerPool_Success` が `UpdateWorkerPool` を呼ぶよう修正される
- [ ] `just test && just lint` が通る

## 非機能要件

- Worker Pool API が Go SDK に未対応の場合は REST API を直接呼ぶフォールバック実装を用意する
