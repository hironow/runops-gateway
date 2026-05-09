# D-Mail DLQ Triage Runbook

D-Mail Pub/Sub bridge (ADR 0013 / 0018) で `max_delivery_attempts=5` を
超えた message は dead-letter topic に転送される。本 runbook は alert
発火後 5 分以内に operator が取るべき triage 手順を定める。

関連:

- 設計の意図: `docs/intent.md`
- bridge 構成: `docs/adr/0013-pubsub-bridge-for-outbox.md`
- DLQ sink 採用根拠: `experiments/2026-05-05_pubsub-dlq-terminal-sink.md`
- alert 定義: `tofu/monitoring.tf`
- 運用全体像: `docs/handover.md`

## Trigger

Cloud Monitoring が以下のいずれかで発火:

- **D-Mail DLQ message forwarded** — `dead_letter_message_count` の 5 分 delta が 1 件超えで即発火 (consumer が pull → nack を 5 回繰り返した結果)
- **D-Mail subscription backlog stale** — `oldest_unacked_message_age` が **1 日** 超で発火 (consumer が居ない / 止まっている時の検知。DLQ に流れずに backlog に残る場合をカバー)

> **重要な仕様**: Pub/Sub の `max_delivery_attempts=5` は consumer が pull
> → nack を 5 回繰り返した時に DLQ 転送される。**consumer が居ないと backlog
> に蓄積されるだけで DLQ には行かない**。そのため 2 つの alert が相補的:
>
> - DLQ alert: consumer 動作中に poison message を検知
> - backlog alert: consumer 不在 / 停止を検知

監視対象 subscription はどちらも `dmail-inbound-receiver` /
`dmail-outbound-gateway` (working subscription)。

## Triage (5 min 以内)

```bash
PROJECT=gen-ai-hironow

# inbound 側 (gateway -> exe-coder VM が消費失敗)
gcloud pubsub subscriptions pull dmail-inbound-dlq-pull \
  --project=${PROJECT} --auto-ack=false --limit=10

# outbound 側 (exe-coder VM -> gateway が消費失敗)
gcloud pubsub subscriptions pull dmail-outbound-dlq-pull \
  --project=${PROJECT} --auto-ack=false --limit=10
```

確認するべき attribute (ADR 0013 schema):

- `kind` / `target_tool` / `source` — どの D-Mail kind の何向けが落ちたか
- `idempotency_key` — 同じ key が複数回出ているなら poison message 確定
- `googclient_traceparent` — Jaeger / Cloud Trace の trace_id 抽出元
  (16 進 32 文字の中央 16 文字が trace-id)
- 元 publish 時の Slack metadata (`slack_channel_id` /
  `slack_thread_ts` / `parent_idempotency_key` / `requester_id` /
  `severity`)

trace を遡る:

```bash
# trace-id を抽出 (例)
TRACEPARENT="00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
TRACE_ID=$(echo "$TRACEPARENT" | cut -d- -f2)

# Cloud Trace 上で該当 trace を開く
gcloud trace traces describe "$TRACE_ID" --project="$PROJECT"
# あるいは UI: https://console.cloud.google.com/traces/list?tid=...
```

## Decision

| 種別 | 対応 |
|---|---|
| **Receiver のバグ** (deserialize 失敗、5 retry 全部 5xx 等) | Linear で issue を切る。DLQ message を ack。バグ fix 後に再発防止確認 |
| **Transient failure** (downstream が一時停止していたが復旧済み) | source topic に手動 republish (下記)、その後 DLQ message を ack |
| **Poison message** (仕様上処理不可能) | DLQ message を ack。事象を `docs/handover.md` のハマりどころに 1 行追記 |

### 手動 republish

```bash
PROJECT=gen-ai-hironow

# (1) DLQ から body と attribute を取得 (上の pull で記録)
# (2) source topic に再投入
gcloud pubsub topics publish dmail-inbound \
  --project=${PROJECT} \
  --message="$BODY" \
  --attribute=kind=$KIND,target_tool=$TARGET,source=$SOURCE,dmail_schema_version=1,idempotency_key=$KEY

# (3) ADR 0013 の idempotency_key dedup により、同じ key の二重配送は
#     receiver 側で no-op になる。安全に再投入できる
```

## Don't

- **`gcloud pubsub subscriptions seek` を source subscription に対して使うな**。
  指定時刻の **すべての** message が再配送される。idempotency_key dedup
  はあるが load spike は本物。1 件 republish のために 1 週間分を再生する
  のは過剰
- **DLQ message を ack せず放置するな**。retention 14 日で蒸発するが、
  alert の auto_close (30 分) を超えても未読のままだと incident-dashboard
  が荒れる
- **Slack や Linear に DLQ の生 body を貼るな**。`docs/intent.md` で
  D-Mail body は specification を含むため、社外チャネルに出すと情報漏洩。
  代わりに `idempotency_key` と `traceparent` だけを共有する

## First-time setup (initial subscription 作成直後のみ)

DLQ pull subscription は tofu apply で新規作成された直後、過去の
DLQ 配送 (もしあれば) を遡って読み出すには `seek` が必要 (新 subscription
の backlog は作成時点以降の message のみ):

```bash
PROJECT=gen-ai-hironow

# topic retention 14 日のうち取り戻せる範囲を最大化
SEEK_TIME=$(date -u -d '14 days ago' '+%Y-%m-%dT%H:%M:%SZ')
gcloud pubsub subscriptions seek dmail-inbound-dlq-pull \
  --project=${PROJECT} --time="$SEEK_TIME"
gcloud pubsub subscriptions seek dmail-outbound-dlq-pull \
  --project=${PROJECT} --time="$SEEK_TIME"
```

これは tofu apply 後 1 度だけ実行する。以降は通常運用 (alert 発火
→ pull → 判断) で OK。

## なぜ DLQ pull subscription が必要か (簡潔版)

GCP 公式 "Handle message failures" doc の警告:

> "Remember to attach a subscription to the dead letter topic because
> messages published to a topic with no subscriptions are lost."

PR #8 では DLQ topic だけ作って subscription を作っていなかったため、
`max_delivery_attempts=5` 超過で DLQ に転送された message は **誰にも
読まれずに 14 日で蒸発する** 状態だった。本 runbook と
`tofu/subscriptions.tf` の `dmail_*_dlq_pull` がそれを解消する。

詳細: `experiments/2026-05-05_pubsub-dlq-terminal-sink.md`
