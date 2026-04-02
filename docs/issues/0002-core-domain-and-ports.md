# Issue 0002: Core Domain & Ports

## Goal

外部依存を一切持たないコアドメインとポートインターフェースを定義する。
UseCase はこの層のみに依存し、Slack / GCP / CLI の知識を持たない。

## Structs & Interfaces

### Domain (`internal/core/domain/`)

```go
type ResourceType string
const (
    ResourceTypeService    ResourceType = "service"
    ResourceTypeJob        ResourceType = "job"
    ResourceTypeWorkerPool ResourceType = "worker-pool"
)

type ApprovalRequest struct {
    ResourceType ResourceType
    ResourceName string
    Target       string  // revision name (optional for jobs)
    Action       string  // "canary_10", "migrate_apply", etc.
    ApproverID   string  // Slack user ID or email address
    Source       string  // "slack" or "cli"
    IssuedAt     int64   // Unix timestamp for expiry check
    ResponseURL  string  // Slack response_url (empty when CLI mode)
}
```

### Ports (`internal/core/port/`)

```go
// Primary Port (入力)
type RunOpsUseCase interface {
    ApproveAction(ctx context.Context, req domain.ApprovalRequest) error
    DenyAction(ctx context.Context, req domain.ApprovalRequest) error
}

// Secondary Ports (出力)
type GCPController interface {
    ShiftTraffic(ctx context.Context, serviceName, revision string, percent int32) error
    ExecuteJob(ctx context.Context, jobName string, args []string) error
    TriggerBackup(ctx context.Context, instanceName string) error
}

type Notifier interface {
    UpdateMessage(ctx context.Context, target NotifyTarget, text string) error
    ReplaceMessage(ctx context.Context, target NotifyTarget, blocks interface{}) error
    SendEphemeral(ctx context.Context, target NotifyTarget, userID, text string) error
}

// NotifyTarget は Slack response_url と CLI stdout を抽象化する
type NotifyTarget struct {
    ResponseURL string // Slack 経由の場合
    Mode        string // "slack" or "stdout"
}

type AuthChecker interface {
    IsAuthorized(approverID string) bool
    IsExpired(issuedAt int64) bool
}
```

## Definition of Done (DoD)

- [ ] インターフェース定義がコンパイルされる
- [ ] `internal/core/` 配下に外部パッケージの import がない（`context` のみ許可）
- [ ] `domain.ApprovalRequest` の各フィールドにコメントが付いている
- [ ] `NotifyTarget` が Slack/CLI 両モードを表現できる構造になっている

## 非機能要件

- **テスタビリティ**: UseCase のユニットテストがモックのみで書けること（実 GCP/Slack 不要）
- **拡張性**: 将来の ResourceType 追加が `domain.go` の定数追加のみで対応できること
- **CLI モード対応**: `ResponseURL` が空の場合（CLI モード）でも動作する設計であること（ADR 0007）
