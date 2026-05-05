# OTel goroutine span flush on Cloud Run (2026 年 5 月)

**Date:** 2026-05-06
**Objective:** Slack 3 秒応答のために handler から spawn された goroutine 内の OTel span が、Cloud Run min_instance_count=0 の idle shutdown で flush されずに失われる問題のベスプラ調査。
**Status:** 🟢 Complete (実装は別ブランチで Issue 0005 として進行)

## Background

production の Cloud Trace に `/slack/command` (Block Kit return まで完結する同期処理) の trace は届くが、 `/slack/interactive` (Approve click → goroutine で Pub/Sub publish) の trace が届かない事象を観察 (実例: dispatch idem `d06bb726c5b21ee1b48f9d6aaa9023cb`)。

PR #8 で `context.WithoutCancel(r.Context())` で trace context を goroutine に引き継いでいるが、handler が ServeHTTP から return した時点では goroutine の span は **まだ End() されていない**。

## Hypothesis

- BatchSpanProcessor の flush schedule (2 秒) と Cloud Run の idle shutdown が race している
- `tp.Shutdown(ctx)` は queue 内の **End() 済み span** だけを flush する仕様で、まだ End されていない span は救えない

## 結論 (一行)

**HTTP handler が spawn した goroutine を `sync.WaitGroup` で main にぶら下げ、 `srv.Shutdown` → `wg.Wait` → `tp.Shutdown` の順で停止する pattern A が唯一の正解。**

根拠: BSP は `span.End()` されていない in-flight span を Shutdown 時に flush しないことが [opentelemetry-go の SpanProcessor 仕様で明文化](https://pkg.go.dev/go.opentelemetry.io/otel/sdk/trace#BatchSpanProcessor) されており ("ForceFlush exports all *ended* spans")、goroutine が走り終わる前に container が SIGKILL されると spec 上 span は確実に消える。 schedule_delay 短縮 (案 C) や ForceFlush 散布 (案 B) は races を狭めるだけで根本解決にならない。 Cloud Run の SIGTERM grace は 10 秒固定なので、goroutine 全体を 10 秒 budget に収め main が wait することは現実的に可能。本リポは既に CPU always-allocated (ADR 0003) なので CPU 不足は無く、唯一の課題はライフサイクルの待ち合わせだけである。

---

## 1. Cloud Run の shutdown 挙動 (2026/05 公式)

**Service の lifecycle (公式 container-contract):**
- 「Cloud Run sends a `SIGTERM` signal to all the containers in an instance, indicating the start of a **10 second period** before the actual shutdown occurs, at which point Cloud Run sends a `SIGKILL` signal.」
  Source: https://docs.cloud.google.com/run/docs/container-contract

**Idle shutdown:**
- 「Unless an instance must be kept idle due to the minimum number of instances configuration setting, **it won't be kept idle for longer than 15 minutes**.」
- min_instance_count=0 では request 完了後 (idle 検知ベース) いつでも shutdown されうる。本リポの問題はまさにここ。

**Background work と CPU 割当:**
- request-based billing: 「when the Cloud Run service finishes handling a request, the instance's access to CPU will be **disabled or severely limited**.」
- instance-based billing (旧 "CPU always allocated"): 「CPU is allocated for the entire container instance lifecycle.」 「allocates CPU even outside of request processing, letting you execute short-lived background tasks and other asynchronous processing work after returning responses.」 「Using Go's Goroutines, Node.js async, Java threads, and Kotlin coroutines.」
  Sources: https://docs.cloud.google.com/run/docs/configuring/billing-settings, https://docs.cloud.google.com/run/docs/tips/general

**SIGTERM 中も課金される:**
- about-execution-environments: 「When a Cloud Run instance is shutting down, it receives a `SIGTERM` signal to enable a 10 second graceful shutdown … perform any necessary cleanup tasks such as flushing logs before exiting.」

本リポは ADR 0003 で `cpu-throttling=false` (= instance-based billing 相当) を採用済なので、goroutine の CPU 不足は問題ではない。問題は「instance がいつ死ぬか」のライフサイクル管理だけ。

## 2. OTel Go SDK BSP / Shutdown の動作 (2026/05 公式)

**SpanProcessor 仕様 (godoc 直接引用):**
- `OnEnd` (= `span.End()`) で初めて queue に投入される。 `OnStart` は何もしない
- `Shutdown`: 「Calls to OnStart, OnEnd, or ForceFlush after this has been called should be ignored.」
- `ForceFlush`: 「**exports all ended spans** to the configured Exporter that have not yet been exported.」
- spec 上「**Shutdown MUST include the effects of ForceFlush.**」

**重要: in-flight span (Start 済み・End 未呼び出し) は Shutdown では flush されない**

[OTel Spec - Tracing SDK](https://opentelemetry.io/docs/specs/otel/trace/sdk/) と PR [opentelemetry-go #2335](https://github.com/open-telemetry/opentelemetry-go/pull/2335) (2021-11-05 merged、既に v1.x で確定動作) より、ForceFlush は「すでに OnEnd された span」だけを保証対象にする。 つまり goroutine が `span.End()` を呼ぶ前に main が `tp.Shutdown` した場合、その span は仕様上必ず失われる。これは bug ではなく仕様。

**OTEL_BSP_* 環境変数 (公式 spec 既定値):**

| 変数 | Default |
|---|---|
| `OTEL_BSP_SCHEDULE_DELAY` | 5000 ms |
| `OTEL_BSP_EXPORT_TIMEOUT` | 30000 ms |
| `OTEL_BSP_MAX_QUEUE_SIZE` | 2048 |
| `OTEL_BSP_MAX_EXPORT_BATCH_SIZE` | 512 |

Source: https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/

本リポが `OTEL_BSP_SCHEDULE_DELAY=2000` で短縮しているのは Tail flush window を絞る目的では正しいが、 goroutine の `span.End()` がそのウィンドウより遅いと依然消える。 SCHEDULE_DELAY を縮めるのは「flush までの待ち」を縮めるだけで「span が enqueue されるかどうか」には無関係。

## 3. 対応パターン比較

| パターン | メリット | デメリット | 本リポ適合 |
|---|---|---|---|
| **A. WaitGroup + ordered shutdown** | spec 上唯一確実。 BSP/SimpleSpan どちらでも動く。 Cloud Run の 10 秒 grace に自然にハマる | handler の毎 `go func()` を `wg.Add/Done` で囲む構造改修。漏れると leak | **◎ 採用** |
| B. goroutine 末尾で `tp.ForceFlush(ctx)` | 既存コードに 1 行追加で済む | (1) goroutine が SIGKILL で死んだら ForceFlush も走らない (2) 各 dispatch ごとに同期 export RPC 発生 → Cloud Trace quota 圧迫 | △ 補強策、単独不可 |
| C. BSP `SCHEDULE_DELAY` を 200ms 等に短縮 | 1 行設定変更 | (1) span.End 前に死ぬケースには無効 (2) export 頻度 25x で `telemetry.googleapis.com` の 5000 spans/sec quota 侵食 | × 根本解決にならない |
| D. SimpleSpanProcessor | span.End() 即同期 export → 消えない | (1) handler latency が export RPC に同期 = Slack 3 秒ルールに直接効く (2) 公式が prod で非推奨 | × Slack 3 秒制約と衝突 |
| E. min_instance_count=1 | shutdown 自体起こらない | (1) 月額 inst-hour 課金常時発生 (2) revision 切替時には必ず idle shutdown 起きる (公式: 「Minimum instances can be **restarted** at any time」) | △ 補強、単独不十分 |
| F. goroutine 廃止 | 構造単純 | Slack 3 秒ルールと物理的に矛盾 | × ADR 0002 を覆すのは過剰 |

## 4. Slack 3 秒応答ルール下での Web App 一般的パターン

公式 https://docs.slack.dev/interactivity/handling-user-interaction より:

- 「acknowledgment must be sent within 3 seconds of receiving the payload」 → 200 OK 厳守
- 「response_url responses can be sent **up to 5 times within 30 minutes**」 → 後続処理は async 必須
- 公式は「ack を返してから後で response_url で update せよ」と明記しており、 goroutine pattern は Slack 公式が想定する正規パターン

業界の Go 実装では `slack-go/slack` ベースの async は WaitGroup or worker channel で main にぶら下げる pattern が一般的。 bolt-python は ProcessPoolExecutor + lazy listeners で抽象化しているが Go 版 Bolt SDK は存在しない。

つまり本リポの goroutine pattern (ADR 0002 + `context.WithoutCancel` + `cpu-throttling=false`) は Slack 公式・OTel 仕様双方と整合しているが、 **ライフサイクル待ち合わせだけが欠けている** 状態。

## 5. 本リポへの推奨 (採用案 A: PendingTracker)

ADR 0017/0018/0020/0021 を変更せず、handler 層に `PendingTracker` を 1 つ挿入するだけで完結する。 CPU 設定 (ADR 0003) は既に正しい。 BSP の 2 秒 schedule (ADR 0020) も維持してよい (短い方が grace 期間中の export 成功率が上がる)。

### Skeleton (`internal/adapter/observability/pending.go` 新規)

```go
package observability

import (
    "context"
    "sync"
    "time"
)

type PendingTracker struct {
    wg sync.WaitGroup
}

// Go runs fn in a tracked goroutine.
func (p *PendingTracker) Go(fn func()) {
    p.wg.Add(1)
    go func() {
        defer p.wg.Done()
        fn()
    }()
}

// Wait blocks until all tracked goroutines exit, or until ctx is cancelled.
func (p *PendingTracker) Wait(ctx context.Context) {
    done := make(chan struct{})
    go func() {
        p.wg.Wait()
        close(done)
    }()
    select {
    case <-done:
    case <-ctx.Done():
    }
}

func (p *PendingTracker) WaitWithDeadline(parent context.Context, d time.Duration) {
    ctx, cancel := context.WithTimeout(parent, d)
    defer cancel()
    p.Wait(ctx)
}
```

### handler 改修 (`internal/adapter/input/slack/handler.go`)

`InteractiveHandler` に `*observability.PendingTracker` を持たせ、現在の素の `go func() {...}()` を全部 `h.pending.Go(func() { ... })` に置換する。

### main 改修 (`cmd/server/main.go`)

```go
shutdownCtx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
defer cancel()

httpCtx, cancelHTTP := context.WithTimeout(shutdownCtx, 4*time.Second)
defer cancelHTTP()
_ = srv.Shutdown(httpCtx)              // (1) HTTP graceful

pending.WaitWithDeadline(shutdownCtx, 4*time.Second)  // (2) goroutine wait

tpCtx, cancelTP := context.WithTimeout(shutdownCtx, 2*time.Second)
defer cancelTP()
_ = tp.Shutdown(tpCtx)                 // (3) BSP final OTLP export
```

### 副作用

| 観点 | 影響 |
|---|---|
| Latency (handler) | 0 (`wg.Add/Done` の ns 単位のみ) |
| 課金 | 0 (min=0 維持、CPU always allocated 維持) |
| Cloud Trace quota | 0 (BSP 設定そのまま) |
| Slack 3 秒ルール | 維持 |
| 実装複雑性 | 中 (handler の `go func()` ~6 箇所置換、 semgrep rule で再発防止可能) |

### 既存 ADR との整合性

- **ADR 0002 (Slack 3 秒 + goroutine)**: 200 OK 即時返却維持。pattern 自体を補強する形で互換
- **ADR 0003 (CPU always-allocated)**: そのまま
- **ADR 0017 (Bot Token fallback)**: notifier 呼び出しも `pending.Go` 配下に入るだけで挙動変化なし
- **ADR 0018 (OutboundReceiver pull)**: `Receive` callback 内の goroutine は subscriber library 側が管理しているのでこの pattern と独立
- **ADR 0020 (Direct OTLP) / 0021 (Pub/Sub trace 委譲)**: BSP 設定変更なし

新規 ADR の必要性: 小さい構造改修なので新 ADR 不要。 ADR 0002 の「Goroutine には `context.Background()` を渡し」の運用補足として CLAUDE.md と handover に追記で足りる。

## 注意点 / 罠

- **`context.WithoutCancel` だけでは不足**: trace context を切り離す目的であって「goroutine が main より長生きする保証」ではない
- **WaitGroup deadline は厳守**: deadline を超えると goroutine は SIGKILL で死に span はやはり消える。 dispatch 経路に 25 分 (`responseURLTimeout`) の context.WithTimeout がついているので、shutdown 時は 4 秒 deadline で **早期 cancel** する必要 (response_url 通知ロスは許容、ADR 0017 Fallback で別経路カバー)
- **subscriber goroutine (`OutboundReceiver`) は別系統**: 本案は HTTP handler 層のみが対象。 subscriber 側は `pubsub.Receive()` の ctx で管理されているので、main shutdown 時にその ctx を cancel すれば自然に span が立て終わる
- **PR opentelemetry-go #2335 は v1.x で merged 済 (2021-11)**: `go.opentelemetry.io/otel/sdk v1.43.0` (本リポ) は当然これを含む
- **ADR 0020 の `OTEL_BSP_SCHEDULE_DELAY=2000` は維持してよいが必須ではなくなる**
- **semgrep rule 化推奨**: `internal/adapter/input/slack/` 配下で生 `go func() {...}()` を禁止し `pending.Go` のみ許可

## 参照した公式 URL リスト

### Cloud Run
- https://docs.cloud.google.com/run/docs/container-contract — SIGTERM 10秒 grace
- https://docs.cloud.google.com/run/docs/configuring/billing-settings — request-based vs instance-based CPU
- https://docs.cloud.google.com/run/docs/tips/general — background activity guidance
- https://docs.cloud.google.com/run/docs/about-execution-environments — graceful shutdown / SIGTERM cleanup
- https://docs.cloud.google.com/run/docs/configuring/min-instances — 「Minimum instances can be restarted at any time」

### OpenTelemetry
- https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/ — `OTEL_BSP_*` 既定値
- https://opentelemetry.io/docs/specs/otel/trace/sdk/ — Shutdown / ForceFlush 仕様
- https://pkg.go.dev/go.opentelemetry.io/otel/sdk/trace#BatchSpanProcessor — godoc
- https://github.com/open-telemetry/opentelemetry-go/blob/main/sdk/trace/batch_span_processor.go — `OnEnd` / `drainQueue` 実装
- https://github.com/open-telemetry/opentelemetry-go/pull/2335 — ForceFlush guarantee (2021-11 merged)
- https://github.com/open-telemetry/opentelemetry-go/issues/2080 — 元 bug report

### Slack
- https://docs.slack.dev/interactivity/handling-user-interaction — 3秒 ack ルール、response_url 30分 / 5回

### Google Cloud OTel sample
- https://github.com/GoogleCloudPlatform/opentelemetry-cloud-run/blob/main/golang/app/main.go — 公式 Go sample (同期 handler のみ、async pattern は対象外)
