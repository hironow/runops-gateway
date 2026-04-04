# Issue 0006: Adapter - Output - GCP Controller

## Goal

`GCPController` ポートを実装する Driven Adapter。
Cloud Run Service / Jobs と Cloud SQL Admin API を操作する。

## 実装内容

### ShiftTraffic (Cloud Run Service)

```go
func (c *GCPControllerImpl) ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error
```

- `cloud.google.com/go/run/apiv2` の `ServicesClient.UpdateService` を使用
- `TrafficTarget` を新リビジョン（percent%）と旧リビジョン（100-percent%、tag="previous"）で構成
- LRO の `op.Wait(ctx)` で完了を待機

### ExecuteJob (Cloud Run Jobs)

```go
func (c *GCPControllerImpl) ExecuteJob(ctx context.Context, project, location, jobName string, args []string) error
```

- `JobsClient.RunJob` を使用
- `Overrides.ContainerOverrides[0].Args` で引数をオーバーライド
- LRO の `op.Wait(ctx)` で完了を待機

### TriggerBackup (Cloud SQL)

```go
func (c *GCPControllerImpl) TriggerBackup(ctx context.Context, project, instanceName string) error
```

- `google.golang.org/api/sqladmin/v1` の `BackupRuns.Insert` を使用
- `Operations.Get` のポーリング（10秒間隔）で完了を待機
- `BackupRun.Description` に `"Triggered by runops-gateway before migration"` を設定

## 設定

Controller は固定の設定を持たない。project と location は各操作の引数として受け取る（クロスプロジェクト対応）。

## Definition of Done (DoD)

- [ ] `ShiftTraffic` のユニットテストが存在する（GCP API はモック使用）
- [ ] `ExecuteJob` のユニットテストが存在する（引数オーバーライドの検証を含む）
- [ ] `TriggerBackup` のユニットテストが存在する
- [ ] LRO 失敗時（`op.Wait` エラー）でエラーが返るテストが存在する
- [ ] `context.Context` キャンセル時に処理が中断されるテストが存在する

## 非機能要件

- **セキュリティ**: Workload Identity / Application Default Credentials を使用すること（明示的なキーファイル不要）
- **信頼性**: LRO の `op.Wait` がコンテキストキャンセルに反応すること（ADR 0003 の Goroutine と対）
- **可観測性**: 各 API 呼び出しの開始・完了・エラーが structured log に出力されること
- **最小権限**: このアダプターのサービスアカウントには必要最小限の IAM ロールのみが付与されること（ADR 0004）
