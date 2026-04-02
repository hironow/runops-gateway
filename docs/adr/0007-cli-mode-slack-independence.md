# 0007. CLI モード - Slack 非依存での完全動作

**Date:** 2026-04-02
**Status:** Accepted

## Context

Slack がダウンしているとき（インシデント対応中など、Slack 障害は現実に発生する）、
runops-gateway が Slack にのみ依存していると本番環境へのデプロイ操作が完全に停止する。

特にインシデント対応中にこそ迅速なカナリア昇格・ロールバック・マイグレーションが必要になるため、
Slack への依存を「任意」とし CLI 単体で全操作が完結する設計が必須となる。

## Decision

CLI モードでは Slack との通信を一切必要としない。

- `Notifier` は CLI モードでは標準出力へのロギングのみを行う（`StdoutNotifier`）
- `SLACK_BOT_TOKEN` や `response_url` が未設定の場合、Notifier は Slack API を呼ばずにロギングのみにフォールバックする
- `AuthChecker` は環境変数（`ALLOWED_SLACK_USERS`）または OS ユーザー（`RUNOPS_ALLOWED_EMAILS`）を使用する
- `--no-slack` フラグで明示的に Slack 通知を無効化できる

これにより、Slack ダウン中でも以下のコマンドがすべて機能する:

```sh
runops approve service frontend-service --action=canary_10 --target=v001 --no-slack
runops deny service frontend-service --no-slack
runops approve job db-migrate-job --action=migrate_apply --no-slack
```

## Consequences

### Positive

- Slack 障害時でも本番運用が停止しない（Single Point of Failure の排除）
- インシデント対応時に迅速な操作が可能
- Notifier の依存関係が疎結合になり、テストが容易になる

### Negative

- Slack へのフィードバックが届かないため、チャンネルにボタンが残り続ける可能性がある
  （Slack 復旧後に `runops sync-slack` 等で後から更新するオペレーションが必要になる場合がある）
- 操作ログを別途保管する仕組みが必要（Cloud Logging へのダイレクト書き込みを推奨）

### Neutral

- `StdoutNotifier` と `SlackNotifier` を差し替え可能にする設計は ADR 0005 の Ports and Adapters が前提
