# Cross-Project GCP Operations Support

## Problem

runops-gateway の GCP Controller は `GOOGLE_CLOUD_PROJECT` 環境変数（gateway 自身のプロジェクト）を使って全てのリソースパスを構築する。管理対象アプリが別プロジェクトにある場合（1:N 構成）、Cloud Run / Cloud SQL の API 呼び出しが gateway プロジェクトを参照してしまい、404 / PermissionDenied エラーになる。

## Decision

ボタンの value JSON に `project` と `location` を埋め込む。各ボタンが自己完結型になり、gateway はステートレスなルーターとして複数の APP_PROJECT を同時に管理できる。

後方互換性は不要（既存のボタンは全て無効化して再生成する前提）。

## Design

### 1. Domain Model

`ApprovalRequest` に2フィールドを追加（必須、フォールバックなし）:

```go
type ApprovalRequest struct {
    Project  string // GCP project ID of the target resource
    Location string // GCP region of the target resource
    // ... existing fields unchanged
}
```

### 2. Port Interface

GCPController の各メソッドに `project` と `location` を引数として追加:

```go
type GCPController interface {
    ShiftTraffic(ctx context.Context, project, location, serviceName, revision string, percent int32) error
    ExecuteJob(ctx context.Context, project, location, jobName string, args []string) error
    TriggerBackup(ctx context.Context, project, instanceName string) error
    UpdateWorkerPool(ctx context.Context, project, location, poolName, revision string, percent int32) error
}
```

- `TriggerBackup` は Cloud SQL API のためプロジェクトレベル操作。`location` 不要
- Controller の `Config` 構造体は不要になる（`ProjectID` / `Location` をリクエストごとに受け取る）

### 3. Data Flow

```
notify-slack.sh (Cloud Build)
  |  PROJECT_ID, REGION を引数で受け取り
  |  ボタン value JSON に project / location を埋め込む
  v
Slack (Block Kit message with buttons)
  |  ユーザーがボタンクリック
  v
handler.go (POST /slack/interactive)
  |  actionValue から project / location をパース
  |  ApprovalRequest に載せる
  v
usecase/runops.go
  |  req.Project, req.Location を GCPController に渡す
  v
gcp/controller.go
  |  引数の project / location でリソースパスを構築
  v
Cloud Run / Cloud SQL API
```

### 4. Button Value JSON

変更前:

```json
{
  "resource_type": "service",
  "resource_names": "nn-makers",
  "targets": "nn-makers-00017-xyz",
  "action": "canary_10",
  "issued_at": 1775270586,
  "migration_done": true
}
```

変更後:

```json
{
  "project": "trade-non",
  "location": "asia-northeast1",
  "resource_type": "service",
  "resource_names": "nn-makers",
  "targets": "nn-makers-00017-xyz",
  "action": "canary_10",
  "issued_at": 1775270586,
  "migration_done": true
}
```

gz 圧縮後のサイズ増加は約 30 文字。Slack の 2,000 文字制限に対して問題なし。

### 5. OfferContinuation (Canary Step Propagation)

ユースケース層でカナリア段階の次ボタンを生成する際、`req.Project` と `req.Location` を `nextReq` / `stopReq` にコピーする。`marshalActionValue` が自動的に JSON に含める。

影響箇所:

- `approveService` の nextReq / stopReq 構築
- `approveWorkerPool` の nextReq / stopReq 構築
- `approveJob` の nextReq / denyReq 構築

### 6. notify-slack.sh

引数を2つ追加:

```bash
# Usage:
#   notify-slack.sh SERVICE_NAMES MIGRATION_JOB_NAME BRANCH_NAME COMMIT_SHA REVISIONS PROJECT_ID REGION
```

全ボタンの value JSON に `--arg p "${PROJECT_ID}"` と `--arg l "${REGION}"` を渡す。

### 7. cloudbuild.yaml

notify-slack ステップに `${PROJECT_ID}` と `${_REGION}` を追加:

```yaml
/workspace/scripts/notify-slack.sh \
  "${_SERVICE_NAMES}" \
  "${_MIGRATION_JOB_NAME}" \
  "${BRANCH_NAME}" \
  "${COMMIT_SHA}" \
  "$$(cat /workspace/revisions.txt)" \
  "${PROJECT_ID}" \
  "${_REGION}"
```

### 8. OperationKey の更新

`OperationKey`（重複実行防止キー）に `Project` を含める:

```go
func OperationKey(req domain.ApprovalRequest) string {
    return fmt.Sprintf("%s/%s/%s/%s/%d",
        req.Project, req.ResourceType, req.ResourceNames, req.Action, req.IssuedAt)
}
```

異なるプロジェクトで同名サービスが同時にカナリア実行されても衝突しない。

### 9. 入力バリデーション

`Project` と `Location` が空の場合はエラーを返す。バリデーションは入力アダプター層（handler.go / CLI）で行い、ユースケース層には有効な値のみが到達することを保証する。

- **handler.go**: `parseActionValue` 後に `Project` / `Location` が空なら `slog.Warn` + 200 OK（Slack リトライ防止）
- **CLI**: `--project` / `--location` フラグに `MarkFlagRequired` を設定

### 10. CLI

`cmd/runops` の `approve` / `deny` コマンドに `--project` と `--location` フラグを追加（必須、`MarkFlagRequired`）。

### 11. Environment Variables

| 変数 | 変更後 |
|---|---|
| `GOOGLE_CLOUD_PROJECT` | gateway 自身の設定のみ（Slack 署名検証のコンテキスト等）。GCP 操作には使わない |
| `CLOUD_RUN_LOCATION` | 削除。ボタン value / CLI フラグから取得 |

### 12. Test Strategy

- **usecase/runops_test.go**: 全テストの `ApprovalRequest` に `Project` / `Location` を設定。mock GCPController のシグネチャ更新。`project` / `location` が controller に正しく渡されるか検証
- **controller_test.go**: 新シグネチャに合わせて更新
- **handler_test.go**: ボタン value JSON の `project` / `location` パーステスト追加
- **blockkit_test.go**: `marshalActionValue` が `project` / `location` を含めるか検証
- **notify_script_test.go**: 引数に `PROJECT_ID` / `REGION` を追加。デコードしたボタン value に `project` / `location` が入ってるか検証
- **tests/runn/**: シナリオテストの YAML に `project` / `location` を追加

### 13. Affected Files

| ファイル | 変更内容 |
|---|---|
| `internal/core/domain/domain.go` | `ApprovalRequest` に `Project`, `Location` 追加 |
| `internal/core/port/port.go` | `GCPController` メソッドシグネチャ変更 + `OperationKey` に `Project` 追加 |
| `internal/adapter/output/gcp/controller.go` | `Config` 削除、各メソッドが引数から project/location を使用 |
| `internal/usecase/runops.go` | `req.Project` / `req.Location` を controller に渡す。nextReq/stopReq に伝搬 |
| `internal/adapter/input/slack/handler.go` | `actionValue` に `Project` / `Location` 追加 |
| `internal/adapter/output/slack/blockkit.go` | `progressActionValue` 構造体と `marshalActionValue` に `Project` / `Location` 追加 |
| `internal/adapter/input/cli/approve.go` | `--project` / `--location` フラグ追加 |
| `internal/adapter/input/cli/deny.go` | `--project` / `--location` フラグ追加 |
| `cmd/server/main.go` | Controller 初期化から Config 削除 |
| `cmd/runops/main.go` | Controller 初期化から Config 削除、環境変数依存を除去 |
| `scripts/notify-slack.sh` | `PROJECT_ID` / `REGION` 引数追加、JSON value に埋め込み |
| `cloudbuild.yaml` | notify-slack ステップに引数追加 |
| `scripts/init-app.sh` | cloudbuild.yaml の substitution 置換は既存ロジックで対応 |
| テストファイル全般 | シグネチャ変更に追従 + 新フィールド検証追加 |
