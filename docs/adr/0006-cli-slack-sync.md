# 0006. CLI 操作時の Slack メッセージ同期

**Date:** 2026-04-02
**Status:** Accepted

## Context

CLI から `runops approve` を実行した場合、`response_url` が存在しない。
このとき、Slack チャンネルに投稿されている「承認待ちボタン」がそのまま残り続けると:

- 後から別の人間が同じボタンを押して二重実行が発生する
- オペレーション履歴が Slack 上に残らない

## Decision

CLI から ApproveAction が実行された場合も、`Notifier` の実装（Driven Adapter）が
Slack Web API の `chat.update` を叩いて、チャンネルに投稿されている元のメッセージのボタンを消去し
「[CLI 経由] 承認済み: <ApproverID>」に更新する。

CLI アダプターは `--response-url` フラグまたは `--slack-ts`（タイムスタンプ）フラグで
更新対象のメッセージを特定する。

## Consequences

### Positive

- CLI 実行後も Slack 上の状態と一致する
- 二重実行を防止できる
- オペレーション履歴が Slack チャンネルに一元化される

### Negative

- CLI 使用時に `SLACK_BOT_TOKEN` と更新対象の特定情報が必要になる
- `chat.update` を使うには Slack App に `chat:write` スコープが必要

### Neutral

- response_url と chat.update の両方に対応する Notifier 実装が必要になる
