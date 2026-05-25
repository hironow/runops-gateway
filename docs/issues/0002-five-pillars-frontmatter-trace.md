# Issue 0002: 5本柱が D-Mail frontmatter から traceparent を読み span を再開

**Repos:** `hironow/sightjack`, `hironow/paintress`, `hironow/amadeus`, `hironow/dominator` (5本柱本体、本リポ範囲外)
**Status:** 📝 未着手
**Depends on:** Issue 0001 (dmail-receiver deploy)

## Why

ADR 0020 で「Pub/Sub bus を 1 つ跨ぐ範囲までは 1 trace_id で繋ぐ」を達成範囲とした。**ファイル境界 (5本柱が phonewave outbox から `.md` を読む時) の trace 伝搬は意図的に範囲外** とした (`docs/handover.md` ハマりどころ集 7 参照)。

5本柱 (sightjack / paintress / amadeus / dominator) が D-Mail .md の frontmatter から `traceparent` を読んで OTel span を再開すれば、**Slack /runops → Pub/Sub → dmail-receiver → outbox → 5本柱処理 までを 1 trace で繋げる**。

## What

各 5本柱で以下を実装:

1. **D-Mail .md の frontmatter に `traceparent` を含める**
   - dmail-receiver (本リポ) が phonewave outbox に書く際、Pub/Sub message attribute (`googclient_traceparent`) を frontmatter にも書き込む。Issue 0001 と同時実装
2. **5本柱が D-Mail を処理する際**
   - frontmatter を parse して `traceparent` を取得
   - W3C Trace Context propagator で context を復元
   - その context 配下で span を start (例: `paintress.process_specification`)
3. **span attribute** に kind / target / source / idempotency_key 等を付与 (本リポの `dmail.outbound.on_message` と同じ semconv で揃える)

## 実装上の注意

- frontmatter の追加 field は backward-compatible に (`traceparent:` 行が無い既存 D-Mail も処理続行可能)
- W3C Trace Context は OTel 標準 propagator (Go なら `propagation.TraceContext{}`) を使う
- 5本柱本体は Python / Rust / Bun などツールごとに言語が異なる前提で、各リポで OTel SDK を選択

## 受入基準

1. Slack `/runops paintress fix M-42` 1 回で、Cloud Trace UI に **gateway → Pub/Sub → dmail-receiver → paintress** までの 4 service が同 trace_id で繋がる
2. 各 service span の親子関係が Jaeger UI でも同様に確認できる (local 検証時)
3. 既存 frontmatter 形式 (`traceparent` なし) を読む 5本柱本体テストが pass し続ける

## 関連 ADR / docs (本リポ側)

- ADR 0020 (Direct OTLP)、Open question #1 「fsnotify event の trace 起点 / link 判断」
- ADR 0021 (Pub/Sub trace context は library 任せ、bus 1 跨ぎまで)
- `docs/handover.md` ハマりどころ集 7 (trace context propagation)
- 各 5本柱 README:
    - `/Users/nino/tap/sightjack/README.md`
    - `/Users/nino/tap/paintress/README.md`
    - `/Users/nino/tap/amadeus/README.md`
    - `/Users/nino/tap/dominator/README.md`
    - `/Users/nino/tap/phonewave/README.md`

## 実装後に本リポ側で更新するもの

- `docs/handover.md` ハマりどころ集 7 を「✅ 解消済」に書き直す
- `docs/local-verification.md` Pattern G/H を実体験ベースに更新 (5本柱起動状態での 1 trace 確認手順)
- `docs/adr/` に「frontmatter trace propagation 仕様」の新 ADR を起票
