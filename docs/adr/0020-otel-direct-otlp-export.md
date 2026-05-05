# 0020. OpenTelemetry trace は直接 OTLP gRPC で export する (Collector sidecar 不採用)

**Date:** 2026-05-05
**Status:** Accepted (2026-05-05, feat/otel-direct-otlp 実装で動作確認済 — Jaeger v2 に trace 到達確認、3 binary とも `OTEL_EXPORTER_OTLP_ENDPOINT` 切替で動作)

## Context

Phase 1〜4a で Slack ChatOps + Pub/Sub bridge + 4-eyes approval gate まで
実装したが、observability は未配線である。本リポは 3 ノード構成:

- **runops-gateway** (Cloud Run, Go) — Slack 受信 + Pub/Sub publisher + 内部
  goroutine で `dmail-outbound` subscriber (ADR 0018)
- **dmail-receiver** (exe-coder VM 上 systemd) — `dmail-inbound` の
  StreamingPull subscriber → phonewave outbox に atomic write
- **dmail-emitter** (同 VM) — 5本柱 archive を fsnotify で監視 →
  `dmail-outbound` に publish

CLAUDE.md の `<observability-standards>` で「全 service は OTel
TracerProvider を init し、OTLP exporter で local Jaeger / prod Cloud Trace
の両方に出せる」ことが要求されている。`OTEL_EXPORTER_OTLP_ENDPOINT` を
切り替えるだけで両対応となる「同一コード・別 endpoint」を達成したい。

達成イメージ (本 ADR の範囲):

**Slack 受信 → gateway 内 publish span → Pub/Sub library が message
attribute へ trace context を自動 inject → subscriber 側 library が
extract して receive span を立て → atomic write までを 1 trace_id で繋ぐ**。

つまり **Pub/Sub message bus を 1 つ跨ぐ範囲** までは本 ADR と ADR 0021
で 1 trace に閉じる (gateway 起点の inbound 経路、emitter 起点の outbound
経路、それぞれ独立した 1 trace)。

範囲外 (別 ADR で扱う):

- **5本柱との連結**: receiver が phonewave outbox に書き込む `.md` の
  frontmatter に traceparent を埋め、5本柱 (paintress / amadeus 等) が
  読んで span を再開する file 境界の伝搬。これは 5本柱側の OTel 対応
  状況依存で、本リポ独立では決められない (関連: ADR 0021 Open questions)
- **gateway 内 outbound subscriber goroutine と inbound HTTP handler の
  trace 連結**: ADR 0018 で別 trigger としたので、別 trace になるのが正
  (publisher 側 emitter が起点)

詳細な調査は `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md` を
参照 (公式 URL 引用付き)。

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | **Direct OTLP gRPC export** — アプリ内 SDK から `telemetry.googleapis.com:443` (prod) / `localhost:4317` (Jaeger v2) に直接送信 |
| B | **Sidecar Collector** — Cloud Run multi-container で `otelcol-google` を sidecar 起動、アプリは `localhost:4317` 固定。VM も systemd で同じ pattern |
| C | **VM ホスト Collector + アプリ直 export** — gateway (Cloud Run) は直接、VM 側だけ Collector を 1 個共用 |

### 案 B (Sidecar) の問題点

- **本質的な不採用根拠**: Cloud Run の cold start にコンテナ 1 つ分の起動
  時間が乗る。Slack 3 秒ルール (ADR 0002) を満たす予算が削られる
- アプリと sidecar が落ちる障害ドメインを増やす — sidecar が死ぬと trace
  が全消しになるが、アプリ側は気付かない (silent failure)
- 設定要素が増える: service.yaml + IAM + Collector config 管理 vs env 変数
  1 つ
- **Cloud Run multi-container 自体は 2024-09 GA**
  (https://cloud.google.com/run/docs/deploying#sidecars) なので技術的には
  使える。ただし Google が公式に提供する `otelcol-google` イメージを
  使う Cloud Run 専用デプロイ recipe は依然 `launch-stage: ALPHA` 注釈
  必須 (2026-05 時点、公式 doc 記載)。このため「sidecar を自前管理する
  /Google 公式の ALPHA recipe に乗る」のどちらも余分な運用負荷を生む

### 案 C (VM 共用 Collector) の問題点

- 案 B の小型版だが、daemon が 2 つ (receiver / emitter) しか無い段階で
  Collector を別プロセス化する旨味が薄い (受け止めポイントが増えるだけ)
- 将来 daemon が増えたら案 C に移行する判断を別 ADR でできる

### 案 A の根拠 (公式の現状, 2026-05)

- Google 公式 "Migrate from the Trace exporter to the OTLP endpoint"
  (last updated 2026-05-04) は Telemetry API への OTLP 直接 export を推奨に
  転換: https://docs.cloud.google.com/trace/docs/migrate-to-otlp-endpoints
- `cloud.google.com/go/pubsub/v2 v2.5.1+` は
  `ClientConfig.EnableOpenTelemetryTracing: true` で publisher / subscriber
  の span と W3C Trace Context propagation を自動でやる。手動 inject
  不要: https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing
- Jaeger v2.17 (`cr.jaegertracing.io/jaegertracing/jaeger:2.17.0`) は OTLP
  受信が default ON (`COLLECTOR_OTLP_ENABLED=true` は v1 用): 
  https://www.jaegertracing.io/docs/2.17/getting-started/

## Decision

**案 A (Direct OTLP gRPC export) を採用する。** 全 3 ノードが
`OTEL_EXPORTER_OTLP_ENDPOINT` env でエンドポイントを切り替えるだけで
local Jaeger / prod Cloud Trace の両方に出せる。

具体的には:

- アプリは `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`
  で OTLP gRPC exporter を構成
- prod 環境は `OTEL_EXPORTER_OTLP_ENDPOINT=telemetry.googleapis.com:443`
  + ADC + `oauth.NewApplicationDefault()` 認証
- local 環境は `OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317` で Jaeger v2
  all-in-one (`cr.jaegertracing.io/jaegertracing/jaeger:2.17.0`) に直接
- `compose.yaml` に Jaeger サービスを追加し `just trace-up/down/view` で
  起動・停止・UI 開封 (CLAUDE.md `<local-jaeger>` 準拠)
- Resource attributes は `go.opentelemetry.io/contrib/detectors/gcp` の
  `gcp.NewDetector()` で Cloud Run / VM / dev macOS のどれでも安全に
  自動 detect。`service.name` は `OTEL_SERVICE_NAME` を必須環境変数化
- Pub/Sub trace context は `pubsub.NewClientWithConfig` の
  `EnableOpenTelemetryTracing: true` で library 任せにする。ADR 0013 の
  message attribute schema に記載していた `traceparent` は **ADR 0021
  で supersede して削除** する (library の `googclient_*` prefix と二重
  inject になるため)
- Sampling は env で実行時切替: prod は
  `OTEL_TRACES_SAMPLER=parentbased_traceidratio` + `OTEL_TRACES_SAMPLER_ARG=0.1`
  から開始、local は `parentbased_always_on`

将来 Collector が必要になる条件 (要 別 ADR):

- 複数 backend (例: Cloud Trace + Honeycomb) に同時送信したくなった時
- アプリ側で sampling せず Collector で tail-based sampling したくなった時
- envoy / nginx 等の non-OTel ソースを束ねたくなった時

## Consequences

### Positive

- env 変数 1 つで local/prod 切り替え。コードは同一
- Cloud Run cold start に sidecar 分の遅延が乗らない (ADR 0002 の 3 秒
  ルールに優しい)
- ALPHA 依存なし。本番運用上のリスクが低い
- Pub/Sub の自動 instrumentation により、cross-process trace
  propagation がアプリコードでゼロ
- Jaeger UI で local 動作確認時に Slack → Pub/Sub → file write まで
  1 trace で見える (debug 効率)

### Negative

- アプリ内 SDK 起動に `BatchSpanProcessor` 等の resource を持つので、
  process がクラッシュした際に flush 前の span が消える可能性。`defer
  tp.Shutdown(ctx)` を main で必ず呼び、`OTEL_BSP_SCHEDULE_DELAY=2000`
  (2秒) 等で短縮
- Cloud Trace の OTLP API には quota がある (~5000 span/sec default)。
  Pub/Sub の自動 span は数が多いので prod は `TraceIDRatioBased(0.1)`
  から開始する必要
- 複数 backend に同時送信する将来要件が出たら Collector に移行する移行
  コストが発生する (案 B / C を改めて評価する)
- v2 ライブラリの自動 trace は **EXPERIMENTAL**: span 名や属性は予告
  なく変わる旨、公式が明記している。semconv 互換性は注視が必要

### Neutral

- `OTEL_TRACES_SAMPLER_ARG` の初期値 (0.1) はトラフィックを見ながら調整
  する必要がある。`/agent` のような high-value path は per-handler で
  `AlwaysSample` に上書きできる
- VM 上の dmail-receiver / dmail-emitter は systemd unit に
  `Environment=OTEL_*` を書くか `EnvironmentFile=/etc/runops/env` に
  外出しするかは Phase 4b (tofu) で決める
- 旧 `texporter` (`opentelemetry-operations-go/exporter/trace`) は legacy
  扱いとなり本リポでは採用しない (公式の方針転換に追随)
