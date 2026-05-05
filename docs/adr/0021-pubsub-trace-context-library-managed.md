# 0021. Pub/Sub の trace context propagation は v2 ライブラリに委譲する (ADR 0013 を一部 supersede)

**Date:** 2026-05-05
**Status:** Accepted (2026-05-05, feat/otel-direct-otlp 実装で動作確認済 — pubsub/v2 v2.6.0 の `EnableOpenTelemetryTracing: true` を 3 client に適用、ADR 0013 schema から traceparent を実質削除)

## Context

ADR 0013 で Pub/Sub message attribute schema を以下のように定義した:

```
kind                 string
target_tool          string
source               string
dmail_schema_version string
idempotency_key      string
traceparent          string  ← ★ 本 ADR の対象
```

`traceparent` は W3C Trace Context (https://www.w3.org/TR/trace-context/) を
publisher 側で手動 inject、subscriber 側で手動 extract する想定で書かれて
いた。当時 (Phase 0 設計時) は `cloud.google.com/go/pubsub` v1 を念頭に
置いており、v1 では trace context propagation はアプリ側で書く必要があった。

ADR 0020 (Direct OTLP gRPC export) で OpenTelemetry を導入する方針を
決定し、その実装にあたり 2026-05 時点の公式情報を再調査した結果、
`cloud.google.com/go/pubsub/v2 v2.5.1+` は
**`ClientConfig.EnableOpenTelemetryTracing: true` で W3C Trace Context を
自動 inject/extract する**ことが判明した
(https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing)。

具体的には:

- **Publisher**: `publish <topic>` span を自動生成し、message attribute に
  `googclient_traceparent` (および関連属性) を自動 inject
- **Subscriber**: message attribute から context を自動 extract し、
  `receive <subscription>` span を立てる
- 属性 prefix `googclient_*` は予約語化されており、ユーザー attribute と
  衝突しない設計

ADR 0013 の `traceparent` attribute をそのまま残すと、publisher は
ライブラリ自動 inject (`googclient_traceparent`) と手動 inject
(`traceparent`) の **二重書き** をすることになり、subscriber は
どちらを正と見るかの曖昧さが生じる。さらに schema として「W3C Trace
Context は手動で運用する」という誤った契約を後続実装者に伝える。

5本柱との連結 (receiver が phonewave outbox に書く際に D-Mail
frontmatter に `traceparent` を入れて 5本柱で再開する) は **別レイヤ
の問題** であり、Pub/Sub message attribute と混同すべきではない。
frontmatter 側の trace propagation は ADR 0020 の Open questions に
残してあり、5本柱側の対応状況を見ながら別 ADR で決める。

ADR 0020 の達成目標 ("1 trace_id で繋ぐ範囲") も同じ理由で **Pub/Sub
message bus を 1 つ跨ぐ範囲まで** に明示的に絞ってある。本 ADR は
その範囲内で wire 上の trace context propagation がライブラリ任せで
完結することを定める。range の境界 (file 書き込み以降) は別問題なので
本 ADR の影響範囲外。

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | ADR 0013 の schema から `traceparent` を削除し、ライブラリ任せにする |
| B | 手動 inject を残し、ライブラリ自動 inject を `WithTelemetryDisabled()` で抑制 |
| C | 両方付け、subscriber は手動 inject の方を優先する |

### 案 B の問題点

- `EnableOpenTelemetryTracing: true` の主な恩恵は trace context propagation
  だけでなく **publisher span / subscriber span の自動生成** も含む。これを
  捨てるとアプリ側で span を書き起こす量が大幅に増える
- 公式が library 対応を「実験段階だが推奨」と明示しているのに自前で
  抜け道を作るのは、後続のバージョンアップに追随しづらくなる

### 案 C の問題点

- 二重書きで wire 量が microscopic に増える (~50 bytes/message)
- subscriber 側で「どちらが正か」を毎回判断するロジックが必要になり、
  バグの温床

## Decision

**案 A を採用する。ADR 0013 の Pub/Sub message attribute schema から
`traceparent` を削除する。** trace context propagation は
`cloud.google.com/go/pubsub/v2` v2.5.1+ の
`ClientConfig.EnableOpenTelemetryTracing: true` に委譲する。

具体的な実装影響:

- `internal/adapter/output/pubsub.Publisher` 構築時に
  `pubsub.NewClientWithConfig(ctx, projectID, &pubsub.ClientConfig{
  EnableOpenTelemetryTracing: true})` を使う
- 同様に `internal/adapter/input/pubsub.Receiver` /
  `OutboundReceiver` も同 config で client を構築
- publisher / subscriber コードから手動 attribute set/get
  (`attrs["traceparent"] = ...`) があれば削除
- ADR 0013 の Pub/Sub message 仕様セクションから `traceparent` 行を削除
  (本 ADR で supersede したと明示)
- 既存テスト (`internal/adapter/output/pubsub/publisher_test.go` 等) で
  `traceparent` attribute を assertion している箇所があれば削除

ADR 0013 のステータスは「Accepted (Phase 2a 着手で publish 経路を実装、
2026-05-05) | trace 関連は ADR 0021 で superseded」と書き換える
(immutability の唯一許される変更: status のみの追記)。

## Consequences

### Positive

- アプリコードに trace propagation のボイラープレートが残らない
- publisher / subscriber span が自動で立つので、`messaging.gcp_pubsub.*`
  semconv 属性 (delivery_attempt 等) も library が埋めてくれる
- Pub/Sub library がバージョンアップで semconv を追従しても、こちらは
  追加実装が不要
- ADR 0013 の attribute schema が短くなり、message 仕様の意図が明確になる

### Negative

- `EnableOpenTelemetryTracing` の trace は公式が **EXPERIMENTAL** と明示。
  span 名や属性が予告なく変わる可能性があり、Jaeger UI の見え方が
  バージョンアップで変わるかもしれない
- 5本柱の `archive/` から phonewave 経由で D-Mail を再加工する際、
  Pub/Sub message attribute では context が継承できても、**ファイル
  ベースの境界では trace context は渡らない**。frontmatter に書く運用は
  別 ADR で決める必要がある (本 ADR の対象外)
- v2 ライブラリのバージョンを v2.5.1 以上に固定する必要がある
  (現在 v2.4.0 → bump 必須)

### Neutral

- subscriber 側の `Receive` callback の `ctx` が subscriber span を親に
  持つようになるので、callback 内で `tracer.Start(ctx, "...")` するだけで
  child span が綺麗に繋がる。これは ADR 0020 で示した span boundary
  ポリシーと整合
- Cloud Trace / Jaeger 双方で `googclient_traceparent` 属性が message
  attribute viewer に表示されるが、debug 用途であって user-facing では
  ないので問題なし

## 関連 ADR

- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する (本 ADR が
  trace 関連を supersede)
- ADR 0020: OpenTelemetry trace は直接 OTLP gRPC で export する (本 ADR の
  前提)

## 参照

- https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing (last
  updated 2026-05-01) — `EnableOpenTelemetryTracing` の公式仕様
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-publish-otel-tracing
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-subscribe-otel-tracing
- https://opentelemetry.io/docs/specs/semconv/messaging/gcp-pubsub/ —
  semantic conventions
- https://www.w3.org/TR/trace-context/ — W3C Trace Context spec
- [`experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md`](../../experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md)
  — 調査ノート (公式 URL リスト付き)
