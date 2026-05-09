# 0022. CloudEvents は採用しない (再検討トリガーを明示)

**Date:** 2026-05-05
**Status:** Accepted (2026-05-05, Codex review 通過 + ADR 0020/0021 の Accepted 化と整合)

## Context

Pub/Sub + OpenTelemetry の構成 (ADR 0013 / 0021) が動き始めたタイミングで、
Pub/Sub message attribute schema を CloudEvents v1.0 に寄せる選択肢を
再検討する声が上がった。動機は次の 3 つ:

1. 自前 schema (ADR 0013 の `kind` / `target_tool` / `idempotency_key` /
   `dmail_schema_version` / その他 metadata) を CloudEvents context
   attribute (`type`, `source`, `id`, `subject`, ...) に寄せると **コード量
   が減る** 可能性
2. 将来 Eventarc trigger を Pub/Sub topic に張ったときに `ce-*` attribute
   が自然に揃う
3. GitHub Actions / Cloud Workflows 等の他システムから D-Mail を直接
   投げる将来計画があれば spec 標準化のメリットが効く

事実調査は `experiments/2026-05-05_cloudevents-adoption.md` にまとめた
(2026/05 公式 URL 引用付き)。要点は以下:

- **CloudEvents v1.0.2** が stable (2024-02-06)、CNCF Graduated。Go SDK は
  `github.com/cloudevents/sdk-go/v2 v2.16.2` (2025-09-22)
- **Pub/Sub Protocol Binding for CloudEvents** (Google `google-cloudevents`)
  は依然 "working draft" 表記
- `cloudevents/sdk-go/protocol/pubsub/v2` は `cloud.google.com/go/pubsub/v2`
  に依存しており、本リポと整合する。`WithClient(*pubsub.Client)` で
  `EnableOpenTelemetryTracing: true` の client を流し込める
- **CloudEvents attribute 命名規則は英小文字 + 数字のみ** (underscore 不可)。
  本リポの `idempotency_key` / `target_tool` / `slack_channel_id` /
  `slack_thread_ts` / `parent_idempotency_key` / `requester_id` /
  `dmail_schema_version` は **全部 rename が必要**
- **Distributed Tracing Extension** (`ce-traceparent`) と
  `googclient_traceparent` (ADR 0021 で library 任せにしたもの) が共存し、
  本リポの 1-hop 構成では二重 inject になる
- **5本柱は CloudEvents を読まない**。phonewave outbox に書くのは Markdown
  frontmatter なので、receiver で「`ce-*` → frontmatter」のマッピング層が
  新規発生する
- **Eventarc trigger は HTTP target 限定**。本リポの dmail-receiver は
  exe-coder VM 上の systemd daemon (HTTP server ではない) なので Eventarc
  には乗れない。CloudEvents 採用しても Eventarc 連携メリットは **本リポ
  では 0**

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | **不採用 (現状維持)** — 自前 schema (ADR 0013) を維持、`EnableOpenTelemetryTracing` (ADR 0021) を維持 |
| B | **完全採用** — sdk-go/v2 + protocol/pubsub/v2 + observability/opentelemetry/v2 を導入し、attribute schema を全部 CloudEvents 準拠に rename |
| C | **部分採用 (envelope のみ、distributed-tracing extension 無し)** — wire format を CloudEvents 化するが trace は library 任せ (ADR 0021 維持) |

### 案 B (完全採用) の致命的問題

- ADR 0021 で消した `traceparent` を distributed-tracing extension で
  実質復活させることになり、`googclient_traceparent` と二重 inject。
  ADR 0021 の決定理由 (二重書き回避、wire 量削減、subscriber の判定
  ロジック削減) と真っ向から矛盾
- ADR 0013 schema を **両端同時** に切り替える移行リスクが高い

### 案 C (部分採用) の致命的問題

- CloudEvents の主たるメリット (Eventarc 連携・spec 標準化) のうち
  Eventarc は exe-coder VM receiver で活かせない
- spec 標準化メリットは「外部協調が必要な場面」での説明コスト削減に
  限られ、本リポの現状はそれを必要としない (intent.md / handover.md に
  そういう計画なし)
- コード量が ±0 〜 微減で、attribute rename + receiver 側 mapping 層
  追加のコストに見合わない

### 案 A (現状維持) の根拠

- ADR 0013 schema + ADR 0021 の trace 委譲で **本リポの要件は完結している**
- 「コード量が減る」も「将来拡張性が上がる」も本リポでは成立しない (定量
  評価: 削減 ~25 行 vs 追加 ~35 行 + 新規 dependency 3 module)
- 5本柱本体への変更はゼロを維持できる (ADR 0012 の前提を保つ)

## Decision

**案 A (CloudEvents 不採用、現状維持) を採用する。**

実装影響: なし。ADR 0013 / 0021 の schema をそのまま運用する。

将来 CloudEvents 採用を **再検討すべきトリガー条件** (これらが揃ったら
本 ADR を supersede する新 ADR を起票する):

1. **5本柱外の publisher** (例: GitHub Actions / 別 Cloud Run service /
   外部 SaaS) が `dmail-inbound` topic に publish する具体プランが
   `docs/intent.md` に追加されたとき。spec 標準化メリットが初めて効く
2. **dmail-receiver を Cloud Run HTTP service 化** する大型リファクタが
   計画され、Eventarc trigger に乗せ替える話が出たとき。Eventarc 連携
   メリットが初めて活きる
3. **5本柱本体が Markdown frontmatter ではなく構造化 event を直接読む**
   よう改修される計画が出たとき。receiver の 2 種 serialization 問題が
   解消される

## Consequences

### Positive

- 既存実装 (Phase 1〜4a) を一切書き直さずに済む
- 依存追加なし。Go module graph はそのまま
- ADR 0021 の `EnableOpenTelemetryTracing` 一本化が綺麗に保たれる
- 5本柱の Markdown frontmatter schema との分岐コストが発生しない
- 採用判断の経緯が ADR + experiments ノートに残るので、再検討時に同じ
  議論を繰り返さなくて済む

### Negative

- 自前 schema を維持するため、外部システムと連携する場合は schema
  ドキュメントを別途整備する必要がある (現状 ADR 0013 + RenderMarkdown
  で足りているので問題は表面化していない)
- CloudEvents 準拠を期待する将来の協調者が現れたら、本 ADR の再検討
  トリガー条件 1 を満たして再評価することになる

### Neutral

- `cloudevents/sdk-go/protocol/pubsub/v2` の `WithClient` で OTel 設定
  を維持したまま CloudEvents binding を載せる「部分採用」パスは技術的
  に可能。再検討時は案 C を最初の候補にすると意思決定が早い

## 関連 ADR

- ADR 0012: 新しい D-Mail kind は追加しない (本 ADR の前提)
- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する (本 ADR が
  schema をそのまま維持する旨を確認)
- ADR 0021: Pub/Sub の trace context propagation は v2 ライブラリに
  委譲する (本 ADR の不採用判断の根拠の一つ)

## 参照

- [`experiments/2026-05-05_cloudevents-adoption.md`](../../experiments/2026-05-05_cloudevents-adoption.md)
  — 詳細な調査ノート (公式 URL リスト付き)
- <https://cloudevents.io/> — CloudEvents プロジェクト
- <https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md> — v1.0.2 spec
- <https://pkg.go.dev/github.com/cloudevents/sdk-go/protocol/pubsub/v2> — Go SDK Pub/Sub binding
- <https://github.com/googleapis/google-cloudevents/blob/main/docs/spec/pubsub.md> — Google Pub/Sub binding (working draft)
- <https://docs.cloud.google.com/eventarc/docs/cloudevents> — Eventarc CloudEvents format
