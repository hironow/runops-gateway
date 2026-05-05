# 0016. Slack request timestamp の鮮度を検証して replay attack を拒否する

**Date:** 2026-05-05
**Status:** Accepted

## Context

`internal/adapter/input/slack/verify.go` の `VerifySignature` は HMAC SHA-256 で
`X-Slack-Signature` を検証していた。Slack 公式仕様
(<https://api.slack.com/authentication/verifying-requests-from-slack>) に従い
basestring に `X-Slack-Request-Timestamp` を含めているが、**timestamp の値が
現在時刻からどれくらい離れているかをチェックしていなかった**。

つまり攻撃者が一度ネットワーク上で正当な Slack リクエストを盗聴できれば、
そのリクエスト全体 (body + headers + signature) を保存しておき、後日
同じバイト列を gateway に送るだけで、HMAC 検証を **無期限に** 通過できる
状態だった。

### Phase 1 の実装で攻撃面が拡大した

Phase 0 までは `/slack/interactive` のみで、replay できる行動は:

- 既に承認されたカナリアの **再シフト** (一度通った操作を再実行)
- これは MemoryStore の TryLock で 1 instance 内では再実行を防げる

であり、影響は限定的だった。

Phase 1 で `/slack/command` (Slash Command) が追加され、replay できる行動は:

- 任意の `/agent <role> <task>` の **再実行** = AI agent の再起動
- 過去の dispatch を別タイミングで再生されると、**同じ task が予期せぬ時刻に
  再実行される**
- DispatchService の MemoryStore は `IssuedAt` を含む OperationKey を使うため、
  時刻が違えば同じ task でも別ロックとして dispatch されてしまう
  (これは ADR 0017 として別途記録予定)

つまり Phase 1 の追加により、**replay protection の欠落は致命度が高い** 状態に
変わった。Codex Review (round 2) でこの点が **唯一の致命的指摘** として
報告された。

### 検討した選択肢

| 案 | 内容 | 評価 |
|----|------|------|
| A | 何もしない (Slack TLS と signing secret で十分とみなす) | 上記の理由により不十分 |
| B | timestamp が `now ± 5 分` の範囲外なら 401 | Slack 公式推奨、業界標準 |
| C | nonce + replay cache で完全に 1 回限りに | 実装コスト大、Cloud Run のステートレスで分散キャッシュが必要 |

### 案 A の問題点

TLS は **転送中** の盗聴を防ぐが、**捕獲済みリクエスト** の再送は防げない。
signing secret 単独では timestamp の鮮度を保証できない (basestring に含めるだけでは
attacker から見て可変パラメータ化できない)。

### 案 C の問題点

Cloud Run の autoscale 下で nonce を完全に 1 回限りにするには Memorystore
(Redis) や Cloud SQL の追加が必要。本 ADR の対象 (Phase 1 完了直後) としては
過剰。Phase 4 (本番化) 時に再検討する余地は残す。

## Decision

**`VerifySignature` に timestamp 鮮度チェックを追加する。
許容窓は `±5 分 (300 秒)` で Slack 公式推奨に揃える。**

- 検証順序: signature header 存在チェック → **timestamp 鮮度チェック** → HMAC 等価チェック
- 鮮度チェックの基準時刻はテスト容易性のため Clock 抽象を inject (引数で `time.Now` 関数を受け取る)
- 既存呼び出し側 (InteractiveHandler / CommandHandler) はデフォルト `time.Now` を渡す
- 逸脱時は既存の `error` 返却で `http.Handler` 側が 401 にする (パスは変更なし)

### timestamp 形式

Slack は Unix epoch 秒の文字列で送る。`strconv.ParseInt` で整数化し、
`time.Unix(ts, 0)` を `now` と比較。パース失敗は既存の `invalid signature`
と同等扱い (拒否)。

### テスト容易性

`VerifySignature` は **clock を引数で受けない** 既存シグネチャを保つ:

```go
func VerifySignature(header http.Header, body []byte, signingSecret string) error
```

代わりに **package-private の `verifySignatureAt(now time.Time, ...)`** に
ロジックを抽出し、公開関数は `verifySignatureAt(time.Now(), ...)` を呼ぶ薄い
ラッパとする。テストは `verifySignatureAt` を直接呼んで `now` を制御する。

### runn シナリオへの影響

既存の `tests/runn/*.yaml` は timestamp を `1700000000` (2023-11-15) で固定し、
事前計算した署名を埋め込んでいた。本 ADR の決定により **これらのシナリオは
新実装の鮮度チェックで全て 401 に落ちる**。対処:

- runn シナリオの timestamp を **動的に生成** する仕組みは runn 側に標準では
  ないため、本 PR では runn シナリオを `tests/runn/STALE_TIMESTAMPS_README.md`
  で「known limitation: 2026-05-05 以降に再生成が必要」と明記する
- 既存 `just test-runn` パスは Phase 0 のための smoke test として価値があったため、
  完全に消すのではなく **timestamp 検証 skip 用の env var (`SLACK_SKIP_FRESHNESS=1`)** を
  development mode 限定で導入 → development 用シナリオは引き続き動く
- production では `SLACK_SKIP_FRESHNESS` は **絶対に設定しない** (cmd/server で
  起動時に設定されていればログに WARN を出して目立たせる)

## Consequences

### Positive

- replay attack に対する第 1 防衛線が立つ
- Slack 公式推奨に準拠
- 攻撃者が 5 分窓を超えてリクエストを保存しても、再送できない
- Phase 1 で広がった攻撃面 (任意 agent 起動) を実質的に塞ぐ

### Negative

- gateway server とクライアント (Slack) の **時計ズレが 5 分以上** あると正当な
  リクエストも 401 になる。Cloud Run / Slack 両方の clock skew は通常 1 秒未満
  なので実害は無いが、運用上「時刻が合っていない場合に gateway が静かに壊れる」
  単一障害点が増える
- 既存 runn シナリオ (固定 timestamp) が development mode env を要する形に変わる
- production で誤って `SLACK_SKIP_FRESHNESS=1` を有効化するリスクが増える
  (mitigation: 起動時 WARN ログ + cd.yaml で設定不可にする)

### Neutral

- timestamp 鮮度チェックは **完全な replay 防止ではない** (5 分窓内の再送は通る)。
  完全な 1 回限り保証が必要になった場合は本 ADR を superseded する別 ADR
  (おそらく Memorystore Redis での nonce cache) を起票する

## 関連 ADR

- ADR 0002: Slack 3秒ルールの回避 (本 ADR は同じ verify.go を変更するが、
  3 秒ルールへの影響はない — 鮮度チェックは sub-millisecond で完了する)
- ADR 0017 (予定): Dispatch OperationKey に IssuedAt を含める判断
  (本 ADR と関連: replay 防止策が時刻ベースになる根拠)

## 参照

- [`docs/issues/0019-slack-replay-protection.md`](../issues/0019-slack-replay-protection.md) — TDD 実装計画
- Slack docs: <https://api.slack.com/authentication/verifying-requests-from-slack>
- Codex Review (round 2, 2026-05-05): 唯一の致命的指摘として本欠落を報告
