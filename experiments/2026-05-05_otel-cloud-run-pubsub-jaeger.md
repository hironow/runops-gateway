# OTel ベスプラ調査 (Cloud Run + Cloud Pub/Sub + Jaeger, 2026年5月)

**Date:** 2026-05-05
**Objective:** Go (1.26) で書かれた runops-gateway / dmail-receiver / dmail-emitter の 3 ノードを、local 開発時は Jaeger に、Cloud Run / VM 本番時は Cloud Trace に集約できる「同一コードで両対応」の OpenTelemetry 配線を確立するためのベスプラ調査。
**Status:** 🟢 Complete (実装は別ブランチで実施予定)

## Background

Phase 1〜4a で Slack ChatOps + Pub/Sub bridge + 4-eyes approval gate まで実装したが、observability は未配線。CLAUDE.md の `<observability-standards>` で「全サービスは OTel TracerProvider を init し、OTLP exporter で local Jaeger / prod Cloud Trace の両方に出せる」ことが要求されている。Cloud Pub/Sub は process boundary を跨ぐので、trace context propagation の仕組みも同時に決める必要がある。

## Hypothesis

1. **Direct OTLP export** が新規 Cloud Run プロジェクトの推奨で、Collector sidecar は overkill な気配が濃厚 (Google 公式 blog 系の論調から)
2. `cloud.google.com/go/pubsub/v2` には自動 OTel tracing があるはずで、ADR 0013 で定義した `traceparent` 属性は **library 任せ** にできる可能性が高い
3. Jaeger は v2 (otel collector framework がコア) で `COLLECTOR_OTLP_ENABLED` が default ON のはず

## 推奨構成 (一行)

**Go アプリは OTLP gRPC で `OTEL_EXPORTER_OTLP_ENDPOINT` を切り替えるだけにし、local は Jaeger v2 (`localhost:4317`) に直接、cloud は Cloud Trace の OTLP API (`telemetry.googleapis.com:443`) に直接 export する。Pub/Sub は v2 の `EnableOpenTelemetryTracing: true` で publisher↔subscriber の trace context を自動伝搬。** Collector sidecar は「メトリクスも一括で扱いたい」「複数 backend にファンアウトしたい」ようになるまで導入しない。

---

## 推奨される依存関係 (go.mod 追加分)

```
go.opentelemetry.io/otel                                                      v1.43.0
go.opentelemetry.io/otel/sdk                                                  v1.43.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace                             v1.43.0
go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc               v1.43.0
go.opentelemetry.io/otel/semconv/v1.26.0                                      (incl. in otel)
go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp                 v0.68.0
go.opentelemetry.io/contrib/detectors/gcp                                     v1.43.0
google.golang.org/grpc/credentials/oauth                                      (already in)
```

注: 既存 `go.opentelemetry.io/otel v1.43.0` と整合する必要がある (otel と otelhttp はリリースサイクル別、otelhttp は v0.x 系で SDK は v1.43 系)。

`cloud.google.com/go/pubsub/v2` は **v2.5.1 以上** であれば `ClientConfig.EnableOpenTelemetryTracing` が使える。現在 go.mod は v2.4.0 なので bump が必要。

---

## 1. Cloud Run の export 戦略

### 1-a. 公式の現状 (2026-05 時点)

公式ドキュメント "Migrate from the Trace exporter to the OTLP endpoint" (last updated 2026-05-04 UTC) は明確に **Telemetry API への OTLP 直接 export を推奨** に転換している。要点:

- エンドポイントは `https://telemetry.googleapis.com` (gRPC は `telemetry.googleapis.com:443`)
- `texporter` (`github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace`, v1.32.0, 2026-04-06) は legacy 扱いで「OTLP 直接 export だと data transformation が不要で field loss が起きない」と明言
- 認証は ADC + gRPC の `oauth.NewApplicationDefault()` を使うのが Go の標準パターン

参照:
- https://docs.cloud.google.com/trace/docs/migrate-to-otlp-endpoints
- https://docs.cloud.google.com/stackdriver/docs/reference/telemetry/overview
- https://cloud.google.com/blog/products/management-tools/opentelemetry-now-in-google-cloud-observability

### 1-b. Sidecar Collector パターン (代替案)

公式ドキュメント "Deploy Google-Built OpenTelemetry Collector on Cloud Run" は次の image を提供:

```
us-docker.pkg.dev/cloud-ops-agents-artifacts/google-cloud-opentelemetry-collector/otelcol-google:0.148.0
```

Cloud Run multi-container (sidecar) で立て、アプリ側は `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317` を向けるだけ。**ただし `run.googleapis.com/launch-stage: ALPHA` 注釈が必要** (2026-05 時点でまだ ALPHA)。

参照:
- https://docs.cloud.google.com/stackdriver/docs/instrumentation/opentelemetry-collector-cloud-run
- https://github.com/GoogleCloudPlatform/opentelemetry-cloud-run

### 1-c. 本プロジェクトでの推奨判断

**直接 OTLP export を推奨。** 理由:

| 観点 | 直接 OTLP | Sidecar Collector |
|---|---|---|
| Cloud Run cold start | +0ms (アプリ内 SDK のみ) | +コンテナ 1 つ起動 (Cloud Run min-instance なしだと cold start が遅延) |
| 設定の単純性 | env 変数 1 つ | service.yaml + Secret Manager 1 つ + IAM + ALPHA |
| 障害ドメイン | アプリと同居 | sidecar が落ちると trace 全消し |
| ALPHA 依存 | なし | 現状 ALPHA |
| メトリクス同時扱い | OTLP で telemetry.googleapis.com に metrics も投げられる | より柔軟 (transform / multi-backend) |
| local との対称性 | endpoint だけ Jaeger に向け替えれば同じコード | local も collector を立てる必要あり |

ただし以下のいずれかになったら Collector を導入する判断とする (要 ADR):
- 複数 backend (例: Cloud Trace + Honeycomb) に同時送信したい
- アプリ側で sampling せず Collector で tail-based sampling したい
- envoy / nginx 等の non-OTel ソースを束ねたい

### 1-d. Cold start で trace が落ちる問題

Cloud Run instance が SIGTERM で 10 秒以内に shutdown される時に batch span processor の queue が flush されないと trace が消える。対策:

1. `defer tp.Shutdown(ctx)` を main で必ず呼ぶ
2. Cloud Run は `cloud.google.com/go/run` を入れるだけでは shutdown hook は来ない。HTTP server の `Shutdown(ctx)` を SIGTERM ハンドラから呼び、その後に `tp.Shutdown(ctx)` する
3. CPU always allocated (ADR 0003) を有効化することで shutdown 中の CPU 削減を回避できる ← 本プロジェクトは既に有効
4. `BatchSpanProcessor` の `WithBatchTimeout` を短め (1-2s) にして溜め込みを減らす

### 1-e. Sampling 戦略

`ParentBased(TraceIDRatioBased(ratio))` が公式推奨 (`go.opentelemetry.io/otel/sdk/trace`)。

- **local (Jaeger)**: `ParentBased(AlwaysSample())` で全件
- **prod (Cloud Trace)**: `ParentBased(TraceIDRatioBased(0.1))` 程度から始める。Slack interactive endpoint は QPS が低いので 100% でも料金的に問題は出にくいが、Pub/Sub の per-message 自動 span は数が多いので注意
- ratio は env `OTEL_TRACES_SAMPLER=parentbased_traceidratio` + `OTEL_TRACES_SAMPLER_ARG=0.1` で実行時切替

参照: https://opentelemetry.io/docs/languages/go/sampling/

---

## 2. Cloud Pub/Sub の context propagation

### 2-a. v2 ライブラリのネイティブ対応

`cloud.google.com/go/pubsub/v2 v2.5.1+` は `ClientConfig.EnableOpenTelemetryTracing: true` を渡すだけで:

- **Publisher**: `publish <topic>` span を自動生成。flow control / batching / publish RPC をそれぞれ child span 化
- **Subscriber**: `receive <subscription>` span を自動生成。lease / ack / modack を child span 化
- **W3C Trace Context は message attribute に自動 inject/extract**。属性 prefix は `googclient_` (公式が「これは将来変わる可能性あり」と注記)

つまり **アプリ側で traceparent を手で attribute に詰める必要は無い**。ADR 0013 の Pub/Sub message attributes に `traceparent` を明示してあるが、v2 ライブラリの自動 inject と二重で入ることになる。**重複を避けるため、ADR 0013 の `traceparent` 属性は削除して library 任せにする** ことを推奨 (要 ADR 更新)。

参照:
- https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing (last updated 2026-05-01)
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-publish-otel-tracing
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-subscribe-otel-tracing
- https://github.com/googleapis/google-cloud-go/issues/4665

### 2-b. Semantic conventions

`messaging.system = "gcp_pubsub"` は MUST。span 名は publisher が `send <topic>` / `create <topic>`、subscriber が `receive <subscription>` / `ack <subscription>` / `modack <subscription>`。これらは v2 ライブラリが自動で付ける。手動で `messaging.message.id`, `messaging.gcp_pubsub.message.ordering_key`, `messaging.gcp_pubsub.message.delivery_attempt` を補強すると subscriber 側のデバッグが楽。

参照:
- https://opentelemetry.io/docs/specs/semconv/messaging/gcp-pubsub/
- https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/

公式は実験段階なので `OTEL_SEMCONV_STABILITY_OPT_IN=messaging` を将来明示する選択肢も握っておく。

### 2-c. 注意点

- v2 の trace は **EXPERIMENTAL**: span 名や属性が予告なく変わる旨、公式ドキュメントが明記
- Pub/Sub topic / subscription の span name にトピック名が含まれるので **PII を topic 名に入れない** (本プロジェクトは大丈夫)
- `Receive` callback 内の処理を child span にしたい場合は `tracer.Start(ctx, "...")` で context 引き継ぎ可能 (Receive callback の `ctx` は subscriber span を親として持つ)

---

## 3. Jaeger (local) との接続

### 3-a. Jaeger v2 推奨設定

公式の Getting Started (v2.17, 2026-04-01 last modified) によれば:

- **Image**: `cr.jaegertracing.io/jaegertracing/jaeger:2.17.0` (Docker Hub mirror: `jaegertracing/jaeger:latest`)
- **OTLP は default で有効**: v2 は OTel Collector framework がコアなので、`COLLECTOR_OTLP_ENABLED` 環境変数は **不要** (v1 では必要だったが v2 では default ON)
- **expose ports**:
  - `4317` OTLP gRPC ← **default で使う**
  - `4318` OTLP HTTP
  - `16686` Jaeger UI
  - (`5778` config server, `9411` Zipkin compat — 任意)
- **UI**: http://localhost:16686

参照:
- https://www.jaegertracing.io/docs/2.17/getting-started/
- https://www.jaegertracing.io/docs/2.17/architecture/
- https://github.com/jaegertracing/jaeger/releases/

### 3-b. compose.yaml 追加例

本プロジェクトの `compose.yaml` に Jaeger サービスを追加。pubsub-emulator と並走させる。

```yaml
services:
  jaeger:
    image: cr.jaegertracing.io/jaegertracing/jaeger:2.17.0
    container_name: runops-jaeger
    ports:
      - "16686:16686"  # Jaeger UI
      - "4317:4317"    # OTLP gRPC
      - "4318:4318"    # OTLP HTTP
    restart: unless-stopped
```

`COLLECTOR_OTLP_ENABLED=true` は Jaeger v2 では不要 (v1 用なので削る)。CLAUDE.md の observability-standards で示されている `jaegertracing/all-in-one` は v1 系なので、本プロジェクトは v2 系 (`jaegertracing/jaeger:2.17.0`) を採用する旨 ADR で記録する価値あり。

### 3-c. gRPC vs HTTP

**gRPC (4317) を default にする。** Go の `otlptracegrpc` exporter は HTTP/2 multiplexing と back-pressure が効く。`telemetry.googleapis.com` も gRPC native で運用が楽。env 変数 `OTEL_EXPORTER_OTLP_PROTOCOL=grpc` を併設して将来 4318 に切り替えられるようにする。

---

## 4. OTel Collector を挟むかどうか

### 4-a. 2026-05 時点の Google 公式スタンス

OTLP 直接 export が **新規プロジェクトの推奨**。理由 (公式 blog "OTLP for Cloud Monitoring" より):

> "For extremely high-volume, high-cardinality metric sources, it can be prohibitively expensive to have an OpenTelemetry collector in the pipeline. Collectors can get overloaded with excessive volume of metrics, and horizontally or vertically scaling them is a lot of work for developers."

ただし Cloud Run multi-container (sidecar) のサポートが GA 化したため、必要なら Collector も合理的な選択肢。

参照:
- https://cloud.google.com/blog/products/management-tools/otlp-opentelemetry-protocol-for-google-cloud-monitoring-metrics
- https://cloud.google.com/blog/products/management-tools/opentelemetry-now-in-google-cloud-observability

### 4-b. 本プロジェクトの判断 (3 ノードとも直接 export)

| ノード | 推奨 | 理由 |
|---|---|---|
| runops-gateway (Cloud Run) | 直接 OTLP → telemetry.googleapis.com | cold start を増やしたくない、Slack 3秒ルール、ADR 0003 で CPU always allocated 済み |
| dmail-receiver (VM systemd) | 直接 OTLP → telemetry.googleapis.com (prod) / Jaeger (local) | systemd unit がシンプル、運用要素を増やさない |
| dmail-emitter (VM systemd) | 同上 | 同上 |

VM 上で複数 daemon が動くなら **VM ホストレベルで 1 個だけ Collector を立てて全 daemon が localhost:4317 に投げる** のはアリ。今は dmail-receiver / dmail-emitter の 2 つだけなので overkill 判定。

---

## 5. Resource attributes と semantic conventions

### 5-a. service.* の付け方

- `service.name`: Cloud Run の `K_SERVICE` env と一致させる (`runops-gateway`, `dmail-receiver`, `dmail-emitter`)。systemd 側は明示 set
- `service.namespace`: `runops` (組織 / domain 識別)
- `service.instance.id`: Cloud Run は GCP detector が `faas.id` 経由で取得。VM は `os.Hostname()` でも UUID でも可
- `service.version`: build-time に `-ldflags '-X main.version=...'` で埋める

`semconv` のバージョンは **v1.26.0** が公式 sample で使われている (Pub/Sub サンプルも同じ)。本プロジェクトでも `go.opentelemetry.io/otel/semconv/v1.26.0` で統一。

参照: https://opentelemetry.io/docs/specs/semconv/

### 5-b. GCP resource detector

`go.opentelemetry.io/contrib/detectors/gcp v1.43.0` の `gcp.NewDetector()` を使う:

- Cloud Run / GCE / GKE / Cloud Run Job / Cloud Functions すべてで動く統合 detector
- `cloud.provider`, `cloud.platform`, `cloud.region`, `cloud.account.id`, `faas.name`, `faas.version`, `faas.id` を自動で埋める
- 旧 `gcp.NewCloudRun()` は **deprecated** (2026-05 時点)。`service.namespace="cloud-run-managed"` をハードコードしてしまうので避ける

```go
res, err := resource.New(ctx,
    resource.WithDetectors(gcp.NewDetector()),
    resource.WithTelemetrySDK(),
    resource.WithFromEnv(), // OTEL_RESOURCE_ATTRIBUTES を取り込む
    resource.WithAttributes(
        semconv.ServiceName("runops-gateway"),
        semconv.ServiceNamespace("runops"),
        semconv.ServiceVersion(version),
    ),
)
```

参照:
- https://pkg.go.dev/go.opentelemetry.io/contrib/detectors/gcp
- https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/detectors/gcp/cloud-run.go

---

## 6. Slack 受信側の trace 開始 + Pub/Sub への伝搬

### 6-a. otelhttp v0.68.0 (2026-04-07) でラップ

本プロジェクトは `cmd/server/main.go` で `net/http` を直接 listen しているはず。既存 mux を `otelhttp.NewHandler` で wrap するだけで、Slack の HTTP POST に対する root span が自動で立つ。

```go
mux := http.NewServeMux()
mux.HandleFunc("/slack/commands", slackHandler.HandleCommand)
mux.HandleFunc("/slack/interactive", slackHandler.HandleInteractive)

handler := otelhttp.NewHandler(mux, "runops-gateway",
    otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
        return r.Method + " " + r.URL.Path
    }),
    otelhttp.WithFilter(func(r *http.Request) bool {
        return r.URL.Path != "/healthz"
    }),
)
http.ListenAndServe(":"+port, handler)
```

参照: https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp

### 6-b. Span boundary 設計 (ADR 0013/0017/0018 を踏まえて)

```
[runops-gateway]                                        [exe-coder VM]

POST /slack/interactive  (otelhttp root span)
  |
  +-- verify Slack signature           (manual span: "slack.verify")
  |
  +-- decode payload                   (manual span: "slack.decode")
  |
  +-- usecase: dispatch                (manual span: "usecase.dispatch")
  |     |
  |     +-- Pub/Sub publish dmail-inbound
  |          (auto span: "send dmail-inbound" by pubsub/v2)
  |          messaging.system=gcp_pubsub
  |          messaging.destination.name=dmail-inbound
  |          messaging.message.id=<server-assigned>
  |
  +-- response_url POST  / chat.postMessage fallback (ADR 0017)
        (manual span "slack.notify"
         + child "slack.response_url" or "slack.chat.postMessage")

      ~~~~~~~~~ Pub/Sub message bus boundary ~~~~~~~~~

                                           [dmail-receiver]
                                           StreamingPull
                                              |
                                              +-- (auto span: "receive dmail-inbound")
                                                    messaging.gcp_pubsub.message.delivery_attempt
                                                    |
                                                    +-- atomic write (manual span: "outbox.write")
                                                          file.path attribute
                                                    |
                                                    +-- ack (auto span: "ack dmail-inbound")

                                           [dmail-emitter]
                                           fsnotify watch (background span)
                                              |
                                              +-- detect file (manual span: "fsnotify.event")
                                                    fs.event.name attribute
                                                    |
                                                    +-- read + parse (manual span: "dmail.parse")
                                                    |
                                                    +-- Pub/Sub publish dmail-outbound
                                                          (auto span: "send dmail-outbound")

      ~~~~~~~~~ outbound bridge ~~~~~~~~~

[runops-gateway: outbound subscriber goroutine] (ADR 0018)
  StreamingPull dmail-outbound
   |
   +-- (auto span: "receive dmail-outbound")
         |
         +-- usecase.notify_slack       (manual span)
               |
               +-- chat.postMessage      (manual span)
```

Legend / 凡例:
- root span: ルート span (リクエストの起点)
- auto span: pubsub/v2 が自動生成する span (自動生成スパン)
- manual span: アプリで明示的に start する span (手動スパン)
- attribute: span 属性
- StreamingPull: gRPC long-lived 配信 (gRPC の長時間配信)

### 6-c. Span boundary の置き方ポリシー

1. **HTTP / Pub/Sub / fsnotify event の 1 トリガー = 1 root or 1 child** をルールにする。fsnotify の各イベントは独立した dmail を表すので新 root にする (linkable to publish span via `messaging.message.id` if needed)
2. **ビジネス境界** (Slack signature verify / dispatch / notify など) は manual span を切る。usecase 層に `tracer.Start(ctx, "usecase.dispatch")` を置く
3. **Cloud SQL / Slack API などの outbound** は別 child span。`http.method`, `http.url`, `peer.service` を付ける (Slack API なら `peer.service=slack.com`)
4. **失敗パス** は span の `Status` を `codes.Error` に。ADR 0017 の response_url 失敗 → chat.postMessage fallback は **2 つの sibling span にして** どちらが効いたか可視化
5. ADR 0018 の outbound subscriber は **runops-gateway 内で goroutine 起動** なので、subscriber span が gateway server の HTTP root span とは別 trace になる (cross-process & cross-trigger なので link で繋ぐ手もあるが、**emitter 側 publish と subscriber 側 receive は同 trace_id で繋がる**ので 1 trace で見える)

---

## 提案する実装手順 (本リポへの導入)

TDD と Tidy First を踏まえ、**structural → behavioral の 2 commit** を 1 機能ごとに分けて以下の順:

1. **chore(deps)**: `go.mod` を bump
   - `cloud.google.com/go/pubsub/v2` を v2.4.0 → 最新 (v2.5.1+)
   - 直接 require 追加: `go.opentelemetry.io/otel/sdk`, `otlptrace`, `otlptracegrpc`, `otelhttp`, `contrib/detectors/gcp`

2. **refactor(internal/observability)**: `internal/adapter/observability/` パッケージを新設
   - `otel.go`: `SetupTracerProvider(ctx, cfg) (*sdktrace.TracerProvider, error)` を export
   - `cfg` は env から `OTEL_EXPORTER_OTLP_ENDPOINT` / `OTEL_EXPORTER_OTLP_PROTOCOL` / `OTEL_SERVICE_NAME` / `OTEL_TRACES_SAMPLER` / `OTEL_TRACES_SAMPLER_ARG` を読む
   - GCP detector を unconditional に呼ぶ (Cloud Run / VM / dev macOS どこでも安全)
   - **テスト**: `tests/unit/observability/otel_test.go` で endpoint 解決の挙動 (env なし時の no-op exporter fallback) を testable に

3. **test(observability)**: failing test → green の TDD 各 cycle
   - 「endpoint 未設定なら exporter は構成されないがエラーにならない」
   - 「endpoint 設定時に gRPC 接続が tested URL にダイヤルされる」(httptest 系で)

4. **feat(server)**: `cmd/server/main.go` で SetupTracerProvider を呼び `defer tp.Shutdown()`、`http.ListenAndServe` の handler を `otelhttp.NewHandler` でラップ
5. **feat(dmail-receiver)**: 同様
6. **feat(dmail-emitter)**: 同様
7. **feat(pubsub)**: `internal/adapter/output/pubsub` の `pubsub.NewClientWithConfig` 呼び出しに `EnableOpenTelemetryTracing: true` を渡す。同じく `internal/adapter/input/pubsub`
8. **feat(usecase)**: usecase 関数の入口に `tracer.Start(ctx, "usecase.<name>")` を入れる。Slack signature verify, response_url POST, chat.postMessage を child span にする
9. **build(compose)**: `compose.yaml` に `jaeger` サービス (image: `cr.jaegertracing.io/jaegertracing/jaeger:2.17.0`) 追加
10. **build(justfile)**: `trace-up` / `trace-down` / `trace-view` タスクを追加 (CLAUDE.md 準拠)
11. **docs(adr)**: `docs/adr/0020-otel-direct-otlp-export.md` で「直接 OTLP 採用、Collector sidecar は不採用」の決定を記録
12. **docs(adr)**: `docs/adr/0013-pubsub-bridge-for-outbox.md` を **新 ADR で supersede** して `traceparent` attribute を message attribute schema から外す (v2 ライブラリの自動 inject に統一)
13. **build(tofu)**: `roles/telemetry.tracesWriter` を Cloud Run / VM SA に grant (Terraform で)、`telemetry.googleapis.com` API を enable
14. **ci**: `.semgrep/rules/` に「`pubsub.NewClient` (v1) を使うな、必ず `pubsub.NewClientWithConfig` で `EnableOpenTelemetryTracing` を渡せ」というルールを追加 (CLAUDE.md の semgrep-guidelines に沿う)

---

## 注意点 / 罠 / open questions

### 注意点

- **`cloud.google.com/go/pubsub/v2` v2.4.0 では `EnableOpenTelemetryTracing` が無い可能性**: v2.5.1 で確認されている。go.mod bump は事前検証 (`go doc cloud.google.com/go/pubsub/v2.ClientConfig`) してから
- **trace context の重複 inject**: ADR 0013 の `traceparent` 属性は v2 ライブラリの自動 inject (`googclient_*` prefix) と二重になる。**ADR 0013 を新 ADR で supersede** して message attribute から `traceparent` を削除。逆向き (publisher が手動で W3C inject、subscriber が手動で extract) は v2 のドキュメントが「自動でやる」と明言しているので不要
- **otelhttp と otel/sdk のバージョン整合性**: otelhttp は v0.68.0 (otel v1.43 系と互換)。バンプ時はまとめて整合させる
- **`gcp.NewCloudRun()` は使わない**: deprecated。`gcp.NewDetector()` を使う
- **Cloud Trace の OTLP API は 2026-05 時点で GA だが、span 数 quota がある**: 1秒あたり ~5000 span がデフォルト。Pub/Sub の delivery_attempt が増えると quota に引っかかりうるので、prod では `TraceIDRatioBased(0.1)` 等で絞る
- **`telemetry.googleapis.com` と Pub/Sub の自動 telemetry の衝突**: Cloud Pub/Sub クライアント自体も内部的に gRPC telemetry を出すため、`option.WithTelemetryDisabled()` を渡すのが公式サンプルのパターン (publish 時に二重 trace が出るのを避ける)
- **VM 上 systemd の env**: `Environment=OTEL_EXPORTER_OTLP_ENDPOINT=...` を unit に入れるか、`EnvironmentFile=/etc/runops/env` で外出しに

### Open questions

1. dmail-emitter の fsnotify event は **新 trace を作るか / dmail-receiver 側で書いた時の trace と link するか**? receiver が書いて emitter が拾う直接対応関係はないので新 trace で良いという判断だが、要 ADR 検討
2. Slack response_url の 30分制限・5回制限を踏まえると、**fallback の chat.postMessage span を sibling にするか child にするか**: 観測上 fallback が発火したかが分かるよう **sibling + status code 情報を attribute にする** 方が retry rate のメトリクス化に向く
3. 本番で **`OTEL_TRACES_SAMPLER_ARG`** を最初いくつにするか: 0.1 で start し、quota 監視しながら調整。`/agent` のような high-value path は always sampled を child mux で書ける (`AlwaysSample` を per-handler)
4. Cloud Run の **CPU always allocated (ADR 0003) のまま、batch span processor の queue 設定はどうするか**: default で OK だが `OTEL_BSP_SCHEDULE_DELAY=2000` (2秒) くらいにすると instance shutdown 時のロスが減る

---

## Conclusion

Hypothesis 1〜3 すべて確認された。次のアクション:

1. ADR 0020 (Direct OTLP export 採用) を起票
2. ADR 0013 を supersede する新 ADR で `traceparent` attribute を schema から削除
3. 上記「提案する実装手順」を別ブランチで TDD 着手 (Phase 4b 本番化と並行 or 直前)

---

## 参照した公式 URL リスト

### Cloud Run / Cloud Trace
- https://docs.cloud.google.com/trace/docs/setup/go-ot (last updated 2026-05-04)
- https://docs.cloud.google.com/trace/docs/migrate-to-otlp-endpoints (last updated 2026-05-04)
- https://docs.cloud.google.com/stackdriver/docs/reference/telemetry/overview (last updated 2026-05-04)
- https://docs.cloud.google.com/stackdriver/docs/instrumentation/opentelemetry-collector-cloud-run (last updated 2026-05-04)
- https://cloud.google.com/blog/products/management-tools/opentelemetry-now-in-google-cloud-observability
- https://cloud.google.com/blog/products/management-tools/otlp-opentelemetry-protocol-for-google-cloud-monitoring-metrics
- https://github.com/GoogleCloudPlatform/opentelemetry-cloud-run

### Pub/Sub
- https://docs.cloud.google.com/pubsub/docs/open-telemetry-tracing (last updated 2026-05-01)
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-publish-otel-tracing
- https://docs.cloud.google.com/pubsub/docs/samples/pubsub-subscribe-otel-tracing
- https://github.com/googleapis/google-cloud-go/issues/4665
- https://docs.cloud.google.com/go/docs/reference/cloud.google.com/go/pubsub/v2/latest (v2.5.1)

### Jaeger
- https://www.jaegertracing.io/docs/2.17/getting-started/ (page last modified 2026-04-01)
- https://www.jaegertracing.io/docs/2.17/architecture/
- https://github.com/jaegertracing/jaeger/releases/
- https://www.cncf.io/blog/2024/11/12/jaeger-v2-released-opentelemetry-in-the-core/

### OpenTelemetry semantic conventions / SDK
- https://opentelemetry.io/docs/specs/semconv/messaging/gcp-pubsub/
- https://opentelemetry.io/docs/specs/semconv/messaging/messaging-spans/
- https://opentelemetry.io/docs/specs/semconv/registry/attributes/messaging/
- https://opentelemetry.io/docs/concepts/semantic-conventions/
- https://opentelemetry.io/docs/languages/go/sampling/
- https://opentelemetry.io/docs/languages/go/getting-started/

### Go OTel libraries
- https://pkg.go.dev/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp (v0.68.0, 2026-04-07)
- https://pkg.go.dev/go.opentelemetry.io/contrib/detectors/gcp (v1.43.0)
- https://pkg.go.dev/github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/trace (v1.32.0, 2026-04-06)
- https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/detectors/gcp/cloud-run.go
- https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/instrumentation/net/http/otelhttp/example/server/server.go
- https://medium.com/google-cloud/simplify-your-open-telemetry-tracing-in-google-cloud-2242ec782410 (2025-07-11)
