# Issue 0007: Adapter - Output - Slack Notifier

## Goal

`Notifier` ポートを実装する Driven Adapter。
`response_url` または Slack API（`chat.update`）を使ってメッセージを更新する。
**Slack ダウン時や `--no-slack` 時は `StdoutNotifier` にフォールバックする**（ADR 0007）。

## 実装内容

### SlackNotifier（response_url 使用）

```go
// UpdateMessage: 処理中ステータスを更新
func (n *SlackNotifier) UpdateMessage(ctx context.Context, target NotifyTarget, text string) error

// ReplaceMessage: ボタンを消去した完了メッセージに置き換える
func (n *SlackNotifier) ReplaceMessage(ctx context.Context, target NotifyTarget, blocks interface{}) error

// SendEphemeral: 本人のみに見えるエラーメッセージを送信
func (n *SlackNotifier) SendEphemeral(ctx context.Context, target NotifyTarget, userID, text string) error
```

### StdoutNotifier（Slack ダウン / `--no-slack` 時）

```go
// 全メソッドが Slack API を呼ばず、標準出力にログを書く
type StdoutNotifier struct{}
```

### Notifier ファクトリ

```go
// target.Mode に応じて適切な実装を返す
func NewNotifier(target domain.NotifyTarget, slackToken string) port.Notifier
```

## Definition of Done (DoD)

- [ ] `SlackNotifier` の各メソッドのユニットテストが存在する（HTTP クライアントはモック）
- [ ] `replace_original: true` が `ReplaceMessage` で設定されるテスト
- [ ] `replace_original: false` かつ `response_type: "ephemeral"` が `SendEphemeral` で設定されるテスト
- [ ] `StdoutNotifier` が Slack API を呼ばずにログ出力することを確認するテスト
- [ ] `SLACK_BOT_TOKEN` 未設定時に `StdoutNotifier` が使われるテスト
- [ ] Slack API が 5xx を返したとき（一時的障害）にエラーを返すテスト

## 非機能要件

- **可用性（最重要）**: Slack がダウンしていても UseCase が動き続けられるよう、Notifier のエラーが UseCase の中断原因にならない設計（通知失敗はログのみ）。ただし UseCase の実装側で判断する（ADR 0007）
- **セキュリティ**: `SLACK_BOT_TOKEN` がログに出力されないこと
- **冪等性**: 同一 `response_url` に複数回 POST しても Slack UI が崩れないこと
