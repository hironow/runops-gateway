# 0018. dmail-outbound は Cloud Run 内 pull subscription で受信する

**Date:** 2026-05-05
**Status:** Accepted

## Context

Phase 2c で `dmail-outbound` topic に 5本柱の結果 D-Mail (`report` /
`design-feedback` / `implementation-feedback` / `convergence` 等) が流れる
ようになった。Phase 3 はこれを runops-gateway (Cloud Run) 側で受信し、
最初の `/agent` 発行時の Slack thread に reply で returnsing する。

Cloud Pub/Sub の subscriber 形式は 2 つ:

| 形式 | 仕組み | Cloud Run との相性 |
|----|------|------|
| **pull (StreamingPull)** | gateway が長寿命 gRPC stream を保持して Subscriber.Receive で受け取る | min-instances=1 必須、autoscale 中は 1 instance のみが pull |
| **push** | Pub/Sub が gateway の HTTP endpoint に POST する (OIDC 認証付き) | autoscale が活きる、ただし Pub/Sub 側で endpoint URL + service account を設定する必要 |

### 検討した選択肢

#### 案 A: pull subscription (本 ADR で採用)

Cloud Run 内に StreamingPull の goroutine を常駐させる。

- メリット:
    - **emulator サポートが素直**: Firebase Pub/Sub emulator も Subscriber.Receive
    が普通に動く → local TDD で完結
    - 実装がシンプル: `dmail-receiver` daemon と同じパターンの再利用
    - 認証経路が単一 (Cloud Run runtime SA に subscriber 権限)
    - Slack 経路の失敗を Pub/Sub に nack して再配送できる (retry 容易)
- デメリット:
    - **Cloud Run min-instances=1 必須** (autoscale 中は他 instance に分散しない)
    - 1 instance の preempt 中は受信が止まる (Pub/Sub 側に貯まるだけなので message loss は無い)

#### 案 B: push subscription

Pub/Sub から gateway の HTTP endpoint (`/pubsub/dmail-outbound` 等) に POST。

- メリット:
    - Cloud Run の autoscale と相性が良い (任意 instance で受信可能)
    - 受信負荷に応じて自動スケール
- デメリット:
    - **emulator サポートが弱い**: Firebase Pub/Sub emulator の push は endpoint
    URL を起動時に設定する必要があり、local TDD のセットアップが煩雑
    - OIDC token 検証ロジックを gateway 側に実装する必要 (Slack HMAC とは別経路)
    - Pub/Sub の retry policy と HTTP 接続失敗の挙動を別々に把握する必要

### 案 B の問題点

local TDD で完結することは `feat/long-running-dispatch` ブランチの基本方針
(ADR 0017 / Issue 0017 / Phase 2a/b/c のいずれも emulator + httptest で
完結している)。push subscription は本番化フェーズ (Phase 4) で
**autoscale が必要になった時点で再評価** すれば良い。

## Decision

**Phase 3 は pull subscription で実装する。**
Cloud Run runtime SA に `pubsub.subscriber` 権限を `runops-gateway-sub`
(dmail-outbound 用) に対して付与し、`cmd/server` の起動時に
`Subscriber.Receive` を goroutine で常駐させる。

### 採用するアーキテクチャ

```
+-----------------------+        +------------------+        +-----------------------+
| 5本柱 (exe-coder VM)  |        |  Cloud Pub/Sub   |        | runops-gateway        |
|  archive/             | watch  |  dmail-outbound  | pull   | (Cloud Run, min=1)    |
|  └── *.md             | -----> |                  | -----> | OutboundSubscriber    |
+-----------------------+        +------------------+        |   ↓ goroutine         |
                                                              | DispatchResultHandler |
                                                              |   ↓                   |
                                                              | FallbackNotifier      |
                                                              |   ↓ chat.postMessage  |
                                                              | Slack thread reply    |
                                                              +-----------------------+
```

Legend / 凡例:

- 5本柱 → archive: 既存 phonewave delivery (Phase 2c で外への bridge)
- dmail-outbound: Phase 2c で publish される topic
- OutboundSubscriber: 新規 (Phase 3 実装)
- DispatchResultHandler: 新規 use case、DMail kind に応じて Slack 文言を組む
- FallbackNotifier: 既存 (ADR 0017)、`metadata.slack_thread_ts` を NotifyTarget に
  詰めて chat.postMessage で thread reply

### 設計詳細

#### thread_ts の伝搬

Phase 1 で gateway が dispatch を発行する時、`dispatchActionValue.IdempotencyKey` を
払い出している。dmail-outbound から戻ってくる D-Mail には:

- `metadata.parent_idempotency_key`: 元 dispatch の idempotency_key
- `metadata.slack_thread_ts`: 元 Slack message の ts (gateway が Phase 2a
  publish 時に metadata に追加する必要 → **本 ADR で前提化**)
- `metadata.slack_channel_id`: 元 Slack channel.id (同上)

これらが揃っていれば NotifyTarget を組み立てて FallbackNotifier に渡せる。
揃っていない D-Mail (CI 起点等) は **drop し ack** (運用ログに残す)。

#### kind ごとの Slack 文言

| kind | Slack 表示 |
|---|---|
| `report` | "✅ {target} 完了 — {body の最初 200 文字}" + PR link 抽出 |
| `design-feedback` | "🎨 {source} → {target}: 設計フィードバック\n{body}" |
| `implementation-feedback` | "🔧 {source} → {target}: 実装フィードバック\n{body}" |
| `convergence` | "🌐 {source} → {target}: 世界線収束\n{body}" + HIGH severity なら approval ボタン (Phase 4) |
| `ci-result` | "🚦 CI: {body}" (ただし parent_idempotency_key がある時のみ) |

#### 失敗時の挙動

- Slack 送信失敗 → message を **nack** (Pub/Sub redelivery)
- 5回連続 redelivery で **DLQ (`dmail-outbound-dlq`)** へ (Pub/Sub subscription 側で設定)
- 致命: parent_idempotency_key 不在 → ack して drop (再送しても直らない)

#### Cloud Run min-instances

本 ADR 採用により **min-instances=1 が必須** になる。Phase 4 (本番化) で
tofu に明記する。追加コスト ~$5/月。

#### 観測性

- 受信件数 / Slack 送信成功・失敗 / DLQ 行きを OpenTelemetry counter で計装
- pubsub.message.id / parent_idempotency_key / slack_thread_ts を log で
  trace に紐付け

## Consequences

### Positive

- local TDD で完結 (emulator + httptest の組み合わせは既存 Phase 2 と同じ)
- 実装が `dmail-receiver` daemon と対称 (両方 Subscriber.Receive)
- Slack 通知経路は既存 FallbackNotifier (ADR 0017) を再利用
- Pub/Sub の at-least-once + ack/nack で retry 制御が標準装備

### Negative

- Cloud Run min-instances=1 必須 (常時コスト発生)
- 1 instance の preempt 中は受信が止まる (Pub/Sub に貯まるだけだが、Slack の
  thread reply 30 分窓に間に合わない可能性 → FallbackNotifier の chat.postMessage
  経路で `slack_thread_ts` を直接指定すれば 30 分窓は無視できる)
- スケール上限が 1 instance の StreamingPull capacity に縛られる (Phase 1 想定の
  個人運用では十分、SaaS 化するなら push に切替)

### Neutral

- 将来 push subscription に切り替える場合、新しい HTTP handler を追加するだけで
  pull 側を残したまま並走できる。本 ADR は **置き換え不能な judgement** ではない

## 関連 ADR

- ADR 0013: Pub/Sub bridge 全体設計 (本 ADR は dmail-outbound 側の詳細)
- ADR 0014: Slack 通知は runops-gateway に集約 (本 ADR の前提)
- ADR 0015: dmail-receiver / dmail-emitter は本リポで管理 (本 ADR の subscriber も同じ方針で本リポ管理)
- ADR 0017: Bot Token + chat.postMessage fallback (本 ADR の Slack 経路で再利用)

## 参照

- `internal/adapter/input/pubsub/receiver.go` (`dmail-receiver` の実装、本 ADR でも参考)
- exe.hironow.dev の `dmail-emitter` systemd unit (`/Users/nino/dotfiles/exe`)
