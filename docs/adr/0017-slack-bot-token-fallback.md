# 0017. Slack Bot Token を導入し chat.postMessage を response_url の fallback として使う

**Date:** 2026-05-05
**Status:** Accepted

## Context

Slack の `response_url` は便利だが 2 つのハード制約がある:

- **30 分の有効期限** — 発行から 30 分超過後の POST は 404
- **同一 URL 5 回まで** — 6 回目以降は 404 (rate-limited)

Phase 0 で `OfferContinuation` が intermittent に 404 を返す問題 (Issue 0017) は
両方の制約に当たっていると推測されている。Phase 1 で `/agent` 経路が追加され、
**dispatch 自体が長時間** になりうる (将来 Phase 2 で Pub/Sub publish + 5本柱が
処理する形になると、結果通知まで 30 分超のケースが日常的に発生する) ため、
response_url 単独では実用的に不十分になる。

### 検討した選択肢

| 案 | 内容 | 評価 |
|----|------|------|
| A | response_url のみ + retry / backoff | 制限自体を超えられないので根本解にならない |
| B | response_url を主に使い、失敗時に **Bot Token + chat.postMessage** に fallback | Slack 公式の長時間処理パターン |
| C | Block Kit を使わず常に webhook URL (Incoming Webhook) | thread reply ができない (チャンネルへの投稿のみ) |

### 案 A の問題点

response_url は HTTP 200 を返しても本当に届いているとは限らず (`invalid_blocks` の
silent failure 等)、また再送しても 5 回制限と 30 分制限を超えられない。

### 案 C の問題点

Incoming Webhook は thread_ts を指定できないため、確認 → 進捗 → 完了の **対話が
できない**。Phase 1 の Block Kit 確認フローも壊れる。

## Decision

**Bot Token を導入し、`chat.postMessage` API を response_url の fallback として
使う。** 当面は ChatOps 側 (`OfferContinuation` 等) と AgentOps 側 (Phase 2 以降の
長時間 dispatch 結果通知) の両方で同じ Notifier 拡張を共用する。

### 採用するアーキテクチャ

```
Notifier.UpdateMessage(target, text)
   │
   ├─ if target.CallbackURL (response_url) is set and within window:
   │     POST response_url (Phase 0 から既存)
   │     ├─ 200 OK 通常レスポンス → return nil
   │     ├─ 200 + invalid_blocks   → return error
   │     └─ 404 (expired / rate)   → fallback to chat.postMessage
   │
   └─ chat.postMessage fallback:
         POST https://slack.com/api/chat.postMessage
         Authorization: Bearer ${SLACK_BOT_TOKEN}
         body: {channel, thread_ts, blocks/text}
```

### 新規 secret

- `slack-bot-token` (Secret Manager) — `xoxb-...` の OAuth Bot Token
- gateway runtime SA に `secretmanager.secretAccessor` 付与
- 既存の `slack-signing-secret` / `slack-webhook-url` と独立管理

### Block Kit field length 制限への対応

ADR 0011 で導入した `compressButtonValue` / `safeTrunc` の制約は
chat.postMessage 経路でも変わらない (Slack 側の制約)。**Notifier 層で同じ
SlackPayload を使い回す** ので、format 変換は不要。

### thread_ts の伝搬

response_url が失効した時点で fallback に切り替えるためには、`NotifyTarget` に
**channel_id と thread_ts** を持たせる必要がある。Slack interactive payload には
`channel.id` と `message.ts` が含まれているので、`InteractiveHandler` 側で
`port.NotifyTarget` に追加して引き渡す。

```go
type NotifyTarget struct {
    CallbackURL string     // existing: response_url
    Mode        NotifyMode // existing
    ChannelID   string     // new (Phase 1 / Issue 0017)
    ThreadTS    string     // new (Phase 1 / Issue 0017)
}
```

CommandHandler は ChannelID = response から取れない (Slash Command の
response_url 経由の最初の確認は thread を持たない) ので、最初の Block Kit
ephemeral 確認には fallback を使わない。Approve クリック以降の
`/slack/interactive` 経路から ChannelID + ThreadTS が確実に取れる。

### ADR 0006 との関係

ADR 0006 は「CLI 操作時の Slack メッセージ同期」を扱い、`chat.update` を
将来導入する方針を述べていた。本 ADR が `chat.postMessage` を入れることで
**Bot Token 基盤が揃う** ため、`chat.update` の実装も同じ adapter で吸収可能に
なる。ADR 0006 の implementation 起票は本 ADR を依存先として書く。

### ResponseURLNotifier との関係

既存 `ResponseURLNotifier` は変更せず、新しい `FallbackNotifier` を作成して
両方を内包する form にする (decorator パターン):

```go
type FallbackNotifier struct {
    primary  port.Notifier        // ResponseURLNotifier
    fallback *ChatPostMessageClient // Bot Token-backed
}

func (n *FallbackNotifier) UpdateMessage(...) error {
    err := n.primary.UpdateMessage(...)
    if isResponseURLLimitErr(err) {
        return n.fallback.PostMessage(...)
    }
    return err
}
```

### スコープ外

- `chat.update` 既存メッセージ更新 (ADR 0006 で将来の別 PR で対応)
- 4-eyes 承認フロー (Phase 4 で別途)
- Bot Token rotation の自動化 (年 1 回手動でよい運用)

## Consequences

### Positive

- response_url の 30 分 / 5 回制限を実質的に回避できる
- 長時間 dispatch (Phase 2+) の結果通知が確実に届く
- ChatOps の OfferContinuation 404 問題 (Issue 0017) が解決
- 将来 ADR 0006 の `chat.update` を低コストで追加できる

### Negative

- Bot Token 管理が増える (新 secret、SA 権限、回転運用)
- Slack Web API の rate limit (Tier 3: 50/min) に引っかかる可能性 — 個人運用では問題なし
- Notifier 実装の複雑度が上がる (decorator chain)

### Neutral

- response_url を捨てるわけではない (3 秒以内の即時返信には依然として有効)
- chat.postMessage 経路の失敗時 (Bot Token 失効等) は ChatOps 全体が壊れるので
  monitoring が要る (OpenTelemetry counter で追跡)

## 関連 ADR

- ADR 0002: Slack 3秒ルールの回避 (response_url を使う動機)
- ADR 0006: CLI 操作時の Slack メッセージ同期 (chat.update 将来統合)
- ADR 0011: ボタン値の常時 gzip 圧縮 (Block Kit field 制約は本 ADR でも有効)
- ADR 0014: Slack 通知は runops-gateway に集約 (本 ADR でも維持)

## 参照

- [`docs/issues/0017-offer-continuation-404-fix.md`](../issues/0017-offer-continuation-404-fix.md)
- Slack docs: <https://api.slack.com/methods/chat.postMessage>
- Slack docs: <https://api.slack.com/interactivity/handling#message_responses>
