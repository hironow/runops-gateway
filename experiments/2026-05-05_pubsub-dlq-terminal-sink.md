# Pub/Sub DLQ terminal sink ベスプラ (2026 年 5 月)

**Date:** 2026-05-05
**Objective:** PR #8 で追加した DLQ topic (`dmail-inbound-dlq` / `dmail-outbound-dlq`) には subscription を 1 つも設定しておらず、retention 期限 (14 日) でメッセージが蒸発する。これが妥当か / 公式ベスプラに従って改善が必要かを判断する。
**Status:** 🟢 Complete (実装は本ブランチで進行中)

## Background

ADR 0013 (Pub/Sub bridge) の DLQ wiring は最低限揃ったが、「最後の行き先」が定義されていない。Pub/Sub 公式 doc が「subscription のない topic に publish されたメッセージは失われる」と明示しているため、現状は実質「気づかれない死蔵」状態の可能性がある。本リポは public repo・GCP project は `gen-ai-hironow`。

## Hypothesis

1. 公式は「DLQ topic にも subscription を必ず付ける」を推奨している
2. 本リポの想定 traffic (数十件/日、message body は人間可読 Markdown text) では BigQuery / Cloud Storage / push subscription は overengineering で、pull subscription + Cloud Monitoring alert で十分
3. 公式 platform metric `pubsub.googleapis.com/subscription/dead_letter_message_count` があるので log-based metric は不要

## 結論 (一行)

**「pull subscription を 1 個ずつ追加 + `subscription/dead_letter_message_count` の Cloud Monitoring alert」まで構築すべき。** 現状 (subscription なし、14 日で蒸発) は **公式に明示的に推奨されない構成** で、Pub/Sub のドキュメントが繰り返し警告している「subscription のない topic に publish されたメッセージは失われる」状態に該当する。ただし変更は数十行の tofu と 1 つの notification channel で済むので、**ADR 0023 を起票するほどではない** — `docs/runbooks/dlq.md` 程度の運用ノートで十分。重要度は「ADR より下、handover.md より上」。

---

## 1. 公式 best-practice の現状 (2026 年 5 月)

### 1.1 「DLQ topic には subscription を必ず付ける」は明文化されている

GCP 公式 Dead-letter topics doc は同じ警告を **複数言語のコードサンプル中に重複して** 置いている:

> "**Remember to attach a subscription to the dead letter topic because messages published to a topic with no subscriptions are lost.**"
> "**To process dead letter messages, remember to add a subscription to your dead letter topic.**"

つまり「subscription なし DLQ」は公式が明示的に "messages are lost" と書いている **アンチパターン**。本リポの現状はこの状態。`handling-failures` 側にも同じ警告が引用されており、これが Pub/Sub team の一貫した立場。

### 1.2 retention の上限 / 既定値

- **Topic の `message_retention_duration` の最大値は 31 日** (公式 quotas / replay-overview)。本リポの DLQ は 14 日 = `1209600s` で、最大値の半分弱
- **Subscription の retention 上限も 31 日**。default は 7 日
- ただし `message_retention_duration` は subscription が "存在しない" 場合の救済にはならない。topic retention は **publish 履歴を保持して seek-by-time ができる** 機能であって、"subscription なしで届いた message を後から救出" できる保険ではない。実質、subscription が無ければ **配送時点で捨てられる** (Pub/Sub 内部で対応する subscription backlog が作られないため)

### 1.3 「DLQ message が来たら何で観測するか」の公式手段

公式 Pub/Sub monitoring doc は **server-side metric** `pubsub.googleapis.com/subscription/dead_letter_message_count` を提供している。
- 説明: "the number of undeliverable messages that Pub/Sub forwards from a subscription"
- これは **元 subscription** (= dmail-inbound-receiver / dmail-outbound-gateway) に紐づく metric であり、**DLQ topic に subscription が無くてもカウントは取れる**
- 推奨検証パターンは "compare against `topic/send_request_count` on the dead-letter topic to verify that Pub/Sub is forwarding undeliverable messages"

つまり「気づくこと」だけなら subscription 0 個でも一応できる。ただし「中身を見る」ためには subscription が必要。

### 1.4 max_delivery_attempts

- 範囲は `[5, 100]`、default 5。本リポは 5 = 最小値
- Slack 3 秒ルール上、消費者 (Cloud Run / VM) の一時的な再起動を吸収するには 5 はやや tight
- 今回の調査の主題ではないが、operator runbook で "DLQ に流れた = poison message の可能性大" と判断する根拠にはなる

---

## 2. Sink オプション比較

| オプション | メリット | デメリット | 本リポ適合 |
|---|---|---|---|
| **A. pull subscription + 手動運用 (gcloud)** | 0 円ランニング、最小構成、message_retention_duration を 31 日に伸ばすだけで実質 1 ヶ月の triage 窓ができる、`gcloud pubsub subscriptions pull` で operator が中身を即座に読める (D-Mail body は Markdown text → 人間可読) | ack を忘れると永遠に残る、自動化したいなら別手段 | **○ 本命** (量が数十/日、message body が Markdown text、人間が triage する想定) |
| B. BigQuery subscription | 長期保存 + ad-hoc SQL、失敗パターンを統計的に分析しやすい | Pub/Sub schema (Avro/Protobuf) が要求される。**本リポは Markdown 生 body** で schema を持たない (raw bytes column に流す `use_topic_schema = false` モードはあるが metadata の構造化は不要)。コスト: BQ ingest + storage が地味に乗る | **△** 数十/日 では BQ の overhead に対するリターンが薄い |
| C. Cloud Storage subscription | 最低コストの archive、JSON / Avro / text を吐ける、無期限保存可 | ファイル単位の batching (デフォルト 5 分 or 1KB 達成で flush) があり、低レート (数十/日) だと **1 ファイル 1 message** の散文ができやすく、検索性が悪い。GCS bucket を新設する追加 IaC が必要 | **△** archive 専用にしては運用コストが pull より高い |
| D. Cloud Run trigger (push subscription) → Slack/Linear | リアルタイム通知、operator の手間なし | endpoint を Cloud Run に生やす必要があり Phase 2 の責務範囲を広げる。push の認証 (OIDC) を別途設定。Slack 通知は ADR 0014 (notification 集約) と整合させる必要 | **△** ADR 0014 と齟齬を起こす可能性、Phase 2 完了後の "extra" 候補に留める |
| E. seek + republish utility | DLQ を再投入できる "リカバリ" 経路 | 上の A-D のどれかと併存する補助機能、単独 sink にはならない | **○ 補助** (operator runbook に CLI snippet を載せる程度) |

**本リポ (D-Mail bridge、量数十/日、body は人間可読 Markdown text、operator は人間 1 名)** では **A + E (pull subscription + 必要時 seek/republish の operator runbook)** が圧倒的に正解。BigQuery / GCS / push は overengineering。

---

## 3. observability / alert

### 3.1 推奨パターン: log-based metric "ではなく" platform metric

公式 metric `pubsub.googleapis.com/subscription/dead_letter_message_count` が **既に存在** しているので、log-based metric を新設する必要はない。これは元 subscription (dmail-inbound-receiver / dmail-outbound-gateway) のリソースラベルで dispatch される。

> 一般論として、log-based metric は「platform metric が無い」場合の最後の手段。今回は不要。

### 3.2 推奨 alert policy (skeleton)

```hcl
resource "google_monitoring_notification_channel" "dlq_email" {
  display_name = "runops-gateway DLQ"
  type         = "email"
  labels = { email_address = var.dlq_alert_email }
}

resource "google_monitoring_alert_policy" "dmail_dlq_forwarding" {
  display_name = "D-Mail DLQ message forwarded"
  combiner     = "OR"
  conditions {
    display_name = "Any message forwarded to a DLQ in last 5 min"
    condition_threshold {
      filter = join(" AND ", [
        "resource.type = \"pubsub_subscription\"",
        "metric.type = \"pubsub.googleapis.com/subscription/dead_letter_message_count\"",
        "(resource.label.subscription_id = \"dmail-inbound-receiver\" OR resource.label.subscription_id = \"dmail-outbound-gateway\")",
      ])
      comparison      = "COMPARISON_GT"
      threshold_value = 0
      duration        = "0s"
      aggregations {
        alignment_period   = "300s"
        per_series_aligner = "ALIGN_DELTA"   # delta over 5 min, not gauge
      }
    }
  }
  notification_channels = [google_monitoring_notification_channel.dlq_email.id]
  alert_strategy { auto_close = "1800s" }
}
```

ポイント:

- `ALIGN_DELTA` で「直近 5 分に **増えた** 件数」を見る。`ALIGN_MAX` だと累積カウンタの最大値で、過去のインシデントが残り続けて auto_close が効かない
- threshold = 0、duration = 0s で「1 件でも来たら即発火」。本リポでは想定 traffic が数十/日なので、DLQ への配送はそもそも「異常事象」
- `auto_close = 1800s` で 30 分 silent なら自動で閉じる (operator が `gcloud pubsub subscriptions pull` を打つまでの猶予)

### 3.3 OTel との関係

ADR 0020 で OTLP 直 export しているが、**Pub/Sub の DLQ forwarding は server-side action なので client OTel span は出ない**。OTel 公式 semconv (`gcp_pubsub`) も "Send" / "Receive" / "Process" は定義しているが "DeadLetterForward" は無い。

代替: 本リポの `PubsubDispatcher` (publisher) と receiver 側の subscriber library が ADR 0021 通りに traceparent を attribute に載せている前提で、**DLQ topic に着いた message にも traceparent (`googclient_traceparent`) が残る**。pull subscription でメッセージを取り出した時、その traceparent を span link として手動で繋げば「元 publish との因果」を Jaeger 上で追える。これは "DLQ triage CLI" を Go で薄く書く時の機能要件 (P1) として runbook に書く。

---

## 4. 本リポへの推奨

### 4.1 必要な tofu 差分 (skeleton)

`tofu/subscriptions.tf` に追記:

```hcl
# DLQ terminal sink: pull subscription so messages survive past 14d retention
# and can be inspected by an operator. Without this, messages forwarded to the
# DLQ topic are dropped on the floor (see ADR 0013 + Pub/Sub handling-failures
# doc).
resource "google_pubsub_subscription" "dmail_inbound_dlq_pull" {
  name  = "dmail-inbound-dlq-pull"
  topic = google_pubsub_topic.dmail_inbound_dlq.id

  ack_deadline_seconds       = 60
  message_retention_duration = "1209600s" # 14 days, same as topic
  retain_acked_messages      = false

  expiration_policy { ttl = "" } # never expire

  labels = {
    component = "dmail-bridge"
    role      = "dlq-terminal-sink"
  }
}

resource "google_pubsub_subscription" "dmail_outbound_dlq_pull" {
  name  = "dmail-outbound-dlq-pull"
  topic = google_pubsub_topic.dmail_outbound_dlq.id

  ack_deadline_seconds       = 60
  message_retention_duration = "1209600s"
  retain_acked_messages      = false

  expiration_policy { ttl = "" }

  labels = {
    component = "dmail-bridge"
    role      = "dlq-terminal-sink"
  }
}
```

`tofu/monitoring.tf` (新規 or `telemetry.tf` 拡張) に上記の `google_monitoring_alert_policy` + `google_monitoring_notification_channel` を追記。`variable "dlq_alert_email"` を `tofu/variables.tf` に追加。

### 4.2 retention を 14d → 31d に伸ばす検討

- 現状の DLQ topic 14 日は **subscription を付けない前提では** Pub/Sub の最大に近い意味があった (subscription 経由で 7 日 retention が default だから topic で 14 日確保)
- subscription を付けると **subscription 側 retention が支配的** になる。subscription default 7 日では前回の triage から 1 週間しか猶予がないので、運用安心のため **subscription も 14 日 (= topic と同じ)** に揃えた
- 31 日まで伸ばす案もあるが、本リポは数十/日 想定で 14 日でも十分
- 31 日にすると BQ archive 不要論がさらに強くなる

### 4.3 operator runbook (最小形)

`docs/runbooks/dlq.md` (新規、~30 行):

```
# D-Mail DLQ Triage Runbook

## Trigger
- Cloud Monitoring alert "D-Mail DLQ message forwarded" fires.

## Triage (5 min)
1. `gcloud pubsub subscriptions pull dmail-inbound-dlq-pull \
     --project=gen-ai-hironow --auto-ack=false --limit=10`
   (or dmail-outbound-dlq-pull)
2. Inspect attributes: kind, target_tool, idempotency_key, traceparent.
3. Open Jaeger and search by traceparent's trace-id to see the failing
   delivery chain (ADR 0021).

## Decision
- Bug in receiver (deserialize failed, 5 retries exhausted)
  → file a Linear issue, ack the DLQ message, fix the bug.
- Transient failure (downstream was down, has since recovered)
  → republish to the source topic, then ack the DLQ message:
     gcloud pubsub topics publish dmail-inbound \
       --message="$(...recovered body...)" \
       --attribute=kind=...,idempotency_key=...,...
- Poison message (cannot be processed, by design)
  → ack and document the case in docs/handover.md.

## Don't
- Don't `seek` the *source* subscription back in time -- that re-delivers
  *all* messages in the window, not just the failed one (ADR 0013 idempotency
  key dedup will help but the load spike is real).
```

`republish utility` を Go で `cmd/dmail-dlq-republish/` に書くのは future work で良い。最初は gcloud で十分。

### 4.4 まとめ: tofu 差分の規模

- 新規 `google_pubsub_subscription` x 2 (~30 行)
- 新規 `google_monitoring_alert_policy` x 1 + `notification_channel` x 1 (~30 行)
- 新規 `variable "dlq_alert_email"` (~5 行)
- 新規 `docs/runbooks/dlq.md` (~30 行)

= **~95 行の追加** で完結。Phase 2 をブロックしない、独立 commit 1 個で完了する規模。

---

## 5. ADR 化の判断

**結論: ADR 0023 は起票しない。** 代わりに **ADR 0013 を改訂しない** で、`docs/runbooks/dlq.md` + tofu 変更 + commit message でカバーする。理由:

1. **ADR は「決定の理由」を残すための immutable history**。今回の変更は ADR 0013 の決定 (Pub/Sub bridge を採用、DLQ あり) を **実装する 1 ステップ** であって、新規 architecture decision ではない。"DLQ には subscription を付ける" は GCP 公式が明示的に書いているデフォルト推奨であり、本リポ固有の tradeoff が無い
2. ADR 化が正当化されるケース: BigQuery sub にする / push to Slack にする / republish を自動化する など、tradeoff が複数あり選択した理由を残す価値がある場合。pull + alert は default なので残す価値が薄い
3. ADR を増やすほど `docs/adr/` が「self-evident な default を ADR 化したノイズ」で薄まる。CLAUDE.md の adr-guidelines は "Decisions that future developers might question" を trigger に挙げているが、これは future developer が疑問に思わない (公式 default に従っているだけ)

ただし **コミットメッセージ** には backfill の趣旨を残す:

```
feat(pubsub): add DLQ pull subscriptions and forwarding alert

Without a subscription on the DLQ topic, forwarded messages are dropped
silently after the 14-day retention window expires. This wires up a pull
subscription for each DLQ topic and a Cloud Monitoring alert on
pubsub.googleapis.com/subscription/dead_letter_message_count.

Refs: ADR 0013 (Pub/Sub bridge), docs/runbooks/dlq.md
```

別 commit で `docs/runbooks/dlq.md` を追加 (= `docs:` 1 commit)、tofu 差分は `feat(pubsub):` 1 commit。tofu の `google_monitoring_alert_policy` は state を持つので tidy-first 的には structural だが、新しい挙動 (= alert 発火) を導入するので **behavioral commit**。

---

## 注意点 / 罠

1. **`ALIGN_MAX` は罠**: いくつかの 2026 年の blog (oneuptime 等) が `ALIGN_MAX` の例を出しているが、`dead_letter_message_count` は cumulative counter ではなく rate / delta gauge として扱う方が「新着があった時だけ発火」の意図に合う。`ALIGN_DELTA` over 5min を推奨
2. **`duration = "60s"` は遅い**: 1 件の poison message に 1 分待つ理由がない。`duration = "0s"` で 1 件即発火。ノイズが心配なら 5 分 alignment の delta で実質的に集約される
3. **subscription を付けたあと、過去の蒸発済みメッセージは戻ってこない**。topic seek できる猶予は topic の `message_retention_duration` (= 14 日) のみ。tofu apply 時点で過去 14 日に DLQ 配送が起きていたなら、新 subscription を作った直後に `gcloud pubsub subscriptions seek dmail-inbound-dlq-pull --time=$(date -u -d '14 days ago' '+%Y-%m-%dT%H:%M:%SZ')` で巻き戻すと取れる。これも runbook の "first-time setup" 章に書いておく
4. **subscription IAM**: DLQ subscription を pull する operator (人間 / CI) には `roles/pubsub.subscriber` を bind する必要がある。本リポは個人開発なので `gcloud auth login` で十分だが、後で CI で republish する場合は SA に必要
5. **enable_message_ordering は DLQ subscription で OFF** にする。元 subscription (`dmail-inbound-receiver`) では ADR 0013 通り ON だが、DLQ は 1 件 1 件失敗していて順序の意味が消えている
6. **OTel span の繋がり**: traceparent が attribute に残っているので Jaeger では追えるが、**publish span と DLQ pull span は同じ trace_id では繋がらない可能性** がある (Pub/Sub library の OTel instrumentation が new trace を起こすか、span link を使うかは library 実装による)。本リポは ADR 0021 で library-managed traceparent を採用しているので、library 任せ。runbook には「trace_id でフィルタ」と書けば十分

---

## Conclusion

**実装に進む** (ADR 起票はしない、runbook で十分):

1. `tofu/subscriptions.tf` に DLQ pull subscription x 2 を追加
2. `tofu/monitoring.tf` を新設、`google_monitoring_alert_policy` + `google_monitoring_notification_channel`
3. `tofu/variables.tf` に `dlq_alert_email` (default 空でも optional として動く設計推奨)
4. `docs/runbooks/dlq.md` を新設
5. `docs/handover.md` の Phase 4b 行に「DLQ terminal sink 実装済」と追記

---

## 参照した公式 URL リスト

- [Pub/Sub: Handle message failures](https://docs.cloud.google.com/pubsub/docs/handling-failures) — 「subscription なし topic は messages are lost」を明示
- [Pub/Sub: Dead-letter topics](https://docs.cloud.google.com/pubsub/docs/dead-letter-topics) — `max_delivery_attempts` 範囲 `[5, 100]`、IAM (publisher on DLQ topic + subscriber on source subscription) を規定
- [Pub/Sub: Best practices](https://docs.cloud.google.com/pubsub/docs/best-practices) — 上記 2 文書と同じ警告を再掲
- [Pub/Sub: Monitor in Cloud Monitoring](https://docs.cloud.google.com/pubsub/docs/monitoring) — `subscription/dead_letter_message_count` の正式定義、検証パターン (vs `topic/send_request_count`)
- [Pub/Sub: Replay and purge messages with seek](https://docs.cloud.google.com/pubsub/docs/replay-overview) — topic / subscription `message_retention_duration` 上限 31 日
- [Pub/Sub: Quotas](https://docs.cloud.google.com/pubsub/quotas) — retention 上限の根拠
- [Pub/Sub: BigQuery subscription](https://docs.cloud.google.com/pubsub/docs/bigquery) — 適合せず (本リポの message body は schema-less Markdown)
- [Pub/Sub: Cloud Storage subscription](https://docs.cloud.google.com/pubsub/docs/cloudstorage) — 適合せず (低レートでファイル散文)
- [Cloud Monitoring: Manage incidents for log-based alerting policies](https://docs.cloud.google.com/monitoring/alerts/log-based-incidents) — 今回は platform metric があるので不要、参考のみ
- [OpenTelemetry: gcp_pubsub semantic conventions](https://opentelemetry.io/docs/specs/semconv/messaging/gcp-pubsub/) — `messaging.system = "gcp_pubsub"`、DLQ forwarding 用の span 種別は **未定義** (server-side のため)
- [Terraform: google_pubsub_subscription](https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/pubsub_subscription) — `dead_letter_policy`, `expiration_policy`, `message_retention_duration` の HCL field 仕様
