# Issue 0005: HTTP handler の goroutine 内 span が Cloud Run idle shutdown までに flush されず lost する

**Repo:** `hironow/runops-gateway` (本リポ完結)
**Status:** 🟡 RED test 起票済 (`internal/adapter/observability/async_flush_test.go`、PR #N で `t.Skip`)、GREEN 実装 + 現行コードへの適用は別 PR
**Depends on:** OTel goroutine flush ベスプラ調査 (background research、結果は `experiments/` に保存予定)

## Why

production の Cloud Trace に `/slack/command` (Block Kit return まで完結する同期処理) の trace は届くが、 `/slack/interactive` (Approve click → goroutine で Pub/Sub publish) の trace が届かないことを観察 (実例: dispatch idem `d06bb726c5b21ee1b48f9d6aaa9023cb`)。

仕組み:

1. Slack 3 秒応答ルール (`docs/slack-setup.md`) に従い、handler は HTTP 200 を即時返してから goroutine で重い処理を実行
2. PR #8 で `context.WithoutCancel(r.Context())` 経由で trace context を goroutine に引き継ぐ
3. goroutine 内で `tracer.Start(ctx, "usecase.dispatch_agent_task")` などの span を生成
4. **`BatchSpanProcessor`** は `OTEL_BSP_SCHEDULE_DELAY=2000` (2 秒) で flush
5. Cloud Run min_instance_count=0 で idle shutdown が走ると、BSP が flush 前に container 終了
6. `tp.Shutdown(ctx)` は queue 内の span を flush するだけで、**まだ End されていない / goroutine 内で生成中の span は救えない**

## RED テスト

`internal/adapter/observability/async_flush_test.go` の
`TestAsyncSpan_LostOnShutdownBeforeGoroutineEnds`:

- in-memory exporter + BSP (`WithBatchTimeout(2s)`)
- httptest server で handler が 200 → goroutine spawn → 150ms sleep → span Start → 50ms sleep → span End
- handler 直後に `tp.Shutdown(500ms)` (= Cloud Run idle-kill のシミュレーション)
- expected: span が exporter に届いていない

現状 `t.Skip` で一時保留中。GREEN PR で Skip 削除 → pass で TDD 完了。

## What (GREEN 化候補)

OTel ベスプラ調査 (進行中) を踏まえて 1 案を選ぶ。 候補:

- **A. WaitGroup-based PendingTracker** — handler goroutine 起動時に register、`tp.Shutdown` 前に main で wait
- **B. goroutine 末尾で `tp.ForceFlush(ctx)` 明示呼び出し** — dispatch ごとに 1 回 flush
- **C. `OTEL_BSP_SCHEDULE_DELAY` を 200ms に縮める** — flush 頻度 10x、quota 圧迫リスク
- **D. SimpleSpanProcessor に切り替え** — 即時 export、prod 非推奨だが goroutine pattern 親和性高
- **E. `CLOUD_RUN_MIN_INSTANCES=1`** — warm instance、Issue 0003 と統合可能

## 受入基準

1. RED テストの `t.Skip` を削除しても pass
2. production で `/slack/interactive` Approve 経路の `usecase.dispatch_agent_task` span + `send dmail-inbound` (auto) span が Cloud Trace に届く (Issue 0004 の確認手順で再現)
3. handler の Slack 3 秒応答が壊れない (post-deploy smoke で `/slack/interactive` invalid-sig 401 が変わらず)
4. CD pipeline と互換性あり (新 env の追加 / `CLOUD_RUN_MIN_INSTANCES` 変更が必要なら docs / handover に明記)

## 関連

- ADR 0017 (FallbackNotifier、 chat.postMessage の goroutine 同期処理問題と類似)
- ADR 0018 (OutboundReceiver も in-process goroutine で StreamingPull する → 同様の flush 問題が outbound にも潜在)
- ADR 0020 / 0021 (OTel direct OTLP + Pub/Sub trace 委譲)
- `docs/handover.md` ハマりどころ集 7 (trace context propagation)
- `docs/issues/0004-cloud-trace-span-verification.md`
- `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md` の Open question #4
  ("Cloud Run の CPU always allocated のまま、 batch span processor の queue 設定はどうするか")
