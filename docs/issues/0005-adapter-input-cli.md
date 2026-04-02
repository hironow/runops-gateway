# Issue 0005: Adapter - Input - CLI

## Goal

Cobra を使った CLI ツール（`runops`）を実装する。
**Slack がダウン中でも完全に動作することが必須要件**（ADR 0007）。
Slack と同一の `RunOpsUseCase` を呼び出し、透過的に動作する。

## コマンド仕様

```sh
# 承認操作
runops approve <resource-type> <resource-name> \
  --action=<action> \
  [--target=<revision>] \
  [--approver=<id>] \
  [--no-slack]

# 拒否操作
runops deny <resource-type> <resource-name> \
  [--approver=<id>] \
  [--no-slack]
```

## フラグ

| フラグ | 必須 | 説明 |
|---|---|---|
| `--action` | approve 時のみ必須 | "canary_10", "migrate_apply" 等 |
| `--target` | 任意 | リビジョン名（service の場合） |
| `--approver` | 任意 | 実行者 ID（省略時は `RUNOPS_APPROVER_ID` 環境変数または `git config user.email`） |
| `--no-slack` | 任意 | Slack 通知を無効化し、stdout のみに出力（Slack ダウン時に使用） |

## CLI 実行時の挙動

- UseCase 呼び出しは **同期的**（完了まで待ち、結果を標準出力に返す）
- `--no-slack` なしの場合: `Notifier` を通じて Slack メッセージを更新（`SLACK_BOT_TOKEN` が必要）
- `--no-slack` ありの場合: `StdoutNotifier` を使用し、Slack API を一切呼ばない
- 成功: `✅ Successfully approved and executed.` を標準出力
- 失敗: `❌ Error: <message>` を標準エラー出力、exit code 1

## Definition of Done (DoD)

- [ ] `approve` / `deny` サブコマンドが機能するテストが存在する
- [ ] `--no-slack` フラグ使用時に Slack API が呼ばれないことを確認するテスト
- [ ] Slack ダウン（`SLACK_BOT_TOKEN` 未設定）でも `--no-slack` で全操作が完結するテスト
- [ ] 必須フラグ欠落時に適切なエラーメッセージと exit code 1 を返すテスト
- [ ] `--help` が各コマンドで機能すること（手動確認）
- [ ] `--approver` 省略時に `git config user.email` からフォールバックするテスト

## 非機能要件

- **可用性（最重要）**: `--no-slack` モードでは Slack への依存がゼロであること（ADR 0007）
- **セキュリティ**: `RUNOPS_APPROVER_ID` や認証情報がコマンド引数に露出しないこと（`--approver` は ID であり機密ではない）
- **UX**: 長時間処理中（LRO 待機中）は進捗スピナーまたは経過ログを stdout に出力すること
- **移植性**: macOS / Linux で動作すること（`GOOS=darwin`, `GOOS=linux`）
