# Issue 0008: Adapter - Output - Auth Checker

## Goal

`AuthChecker` ポートを実装する。
環境変数のホワイトリストによるユーザー認可と有効期限チェックを行う。
Slack / CLI の両モードで共通して使用される。

## 実装内容

### IsAuthorized

```go
func (a *EnvAuthChecker) IsAuthorized(approverID string) bool
```

- 環境変数 `ALLOWED_SLACK_USERS`（カンマ区切り）と照合
- CLI モードの場合は `RUNOPS_ALLOWED_EMAILS` 環境変数も参照
- 各エントリは前後トリムして比較

### IsExpired

```go
func (a *EnvAuthChecker) IsExpired(issuedAt int64) bool
```

- `time.Now().Unix() - issuedAt > ExpirySeconds`（デフォルト 7200 秒 = 2時間）
- 環境変数 `BUTTON_EXPIRY_SECONDS` で上書き可能
- CLI モードで `issuedAt = 0` の場合は期限切れと判定しない（CLI には有効期限概念がない）

## Definition of Done (DoD)

- [ ] 許可ユーザーで `IsAuthorized` が `true` を返すテスト
- [ ] 未許可ユーザーで `false` を返すテスト
- [ ] 空白を含む `ALLOWED_SLACK_USERS` でも正しくトリムされるテスト
- [ ] 有効期限内で `IsExpired` が `false` を返すテスト
- [ ] 有効期限切れ（7200秒超過）で `true` を返すテスト
- [ ] CLI モード（`issuedAt = 0`）で `IsExpired` が `false` を返すテスト
- [ ] `BUTTON_EXPIRY_SECONDS` 環境変数で有効期限が変更されるテスト

## 非機能要件

- **CLI モード対応**: CLI 実行時は `issuedAt = 0` を渡すことで有効期限チェックをスキップできること（ADR 0007）
- **セキュリティ**: 環境変数が空（`""`)の場合はすべてのユーザーを拒否すること（デフォルト拒否）
- **テスタビリティ**: `time.Now()` を差し替えられるよう、時刻取得を注入可能な設計にすること
