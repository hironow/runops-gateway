# Issue 0019: Slack request timestamp 鮮度チェックで replay attack を拒否する

## Goal

`internal/adapter/input/slack/verify.go` の `VerifySignature` に **timestamp 鮮度
チェック** を追加し、`X-Slack-Request-Timestamp` が現在時刻 ±5 分の窓を外れる
リクエストを 401 で拒否する。

これにより Phase 0 から存在していた **replay attack 脆弱性** を修正する。
Codex Review (round 2, 2026-05-05) で唯一の致命的指摘として報告された。

設計判断は ADR 0016 を参照。

## Why

Phase 1 で `/slack/command` が追加され、replay できる行動が
「カナリア再シフト」から **「任意 agent タスクの再起動」** に拡大した。
TLS は転送中の盗聴を防ぐが、捕獲済みリクエストの再送には無力。
Slack 公式は HMAC + timestamp 鮮度チェックの併用を推奨している。

## TDD 計画

### Structural (Tidy First, 1 commit)

- `verify.go` の HMAC 検証ロジックを `verifySignatureAt(now time.Time, ...)` に抽出
- 公開関数 `VerifySignature` は `verifySignatureAt(time.Now(), ...)` を呼ぶ薄いラッパ
- 既存呼び出し側 (handler.go / command.go) は無変更
- 既存テストが全て pass することを確認

### Behavioral (Red → Green、1-2 commit)

#### Red

`verify_test.go` に追加:

```go
func TestVerifySignatureAt_RejectsStaleTimestamp(t *testing.T) {
    // given: signature is correctly computed for ts=1700000000,
    //        but verification clock is at 1700000000 + 6 minutes.
    body := []byte("payload=test")
    secret := "test-secret"
    ts := int64(1700000000)
    sig := computeSig(t, secret, ts, body)
    h := http.Header{}
    h.Set("X-Slack-Request-Timestamp", strconv.FormatInt(ts, 10))
    h.Set("X-Slack-Signature", sig)
    now := time.Unix(ts+6*60, 0)

    // when
    err := verifySignatureAt(now, h, body, secret)

    // then
    if err == nil {
        t.Fatal("expected stale timestamp error, got nil")
    }
}

func TestVerifySignatureAt_RejectsFutureTimestamp(t *testing.T) {
    // similar but now is 6 minutes BEFORE ts
}

func TestVerifySignatureAt_RejectsUnparseableTimestamp(t *testing.T) {
    // X-Slack-Request-Timestamp = "not-a-number"
}

func TestVerifySignatureAt_AcceptsWithinWindow(t *testing.T) {
    // ts = now - 4 minutes -> OK
}
```

#### Green

`verify.go`:

```go
const slackTimestampMaxSkew = 5 * time.Minute

func verifySignatureAt(now time.Time, header http.Header, body []byte, signingSecret string) error {
    timestamp := header.Get("X-Slack-Request-Timestamp")
    signature := header.Get("X-Slack-Signature")
    if timestamp == "" || signature == "" {
        return fmt.Errorf("missing slack signature headers")
    }

    ts, err := strconv.ParseInt(timestamp, 10, 64)
    if err != nil {
        return fmt.Errorf("invalid slack timestamp: %w", err)
    }

    skew := now.Sub(time.Unix(ts, 0))
    if skew < 0 {
        skew = -skew
    }
    if skew > slackTimestampMaxSkew {
        return fmt.Errorf("slack timestamp out of window: skew=%s", skew)
    }

    basestring := "v0:" + timestamp + ":" + string(body)
    mac := hmac.New(sha256.New, []byte(signingSecret))
    mac.Write([]byte(basestring))
    expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
    if !hmac.Equal([]byte(expected), []byte(signature)) {
        return fmt.Errorf("invalid slack signature")
    }
    return nil
}

func VerifySignature(header http.Header, body []byte, signingSecret string) error {
    return verifySignatureAt(time.Now(), header, body, signingSecret)
}
```

### Side effect: runn シナリオの timestamp 固定値

既存 `tests/runn/*.yaml` は timestamp を `1700000000` (2023-11-15) で固定し
事前計算の署名を埋めている。鮮度チェック導入後はこれら全シナリオが 401 になる。

対処 2 案:

#### 案 X: development mode の skip フラグ

- `cmd/server` に `SLACK_SKIP_FRESHNESS` env を追加
- 設定時は `verifySignatureAt` の鮮度チェックを skip
- 起動時 `slog.Warn("slack timestamp freshness check is disabled — DO NOT USE IN PRODUCTION")` を出す
- runn シナリオ実行手順 (`docs/local-verification.md`) に env を明記
- production cd.yaml では絶対に設定しない (Cloud Run env vars に含めない)

#### 案 Y: runn シナリオ動的生成

- runn の `runners` で executor を script 化して timestamp を毎回 `now` で計算
- ただし runn 標準では HMAC 計算をシナリオ内で行えないため、**事前ステップで
  curl + bash を呼ぶ** 形になり煩雑
- 全シナリオを書き直す影響大

→ **案 X を採用**。simpler、production の安全性は WARN ログ + cd.yaml レビューで担保。

実装:

```go
// cmd/server/main.go
type config struct {
    slackSigningSecret    string
    port                  string
    slackSkipFreshness    bool
}

cfg.slackSkipFreshness = os.Getenv("SLACK_SKIP_FRESHNESS") == "1"
if cfg.slackSkipFreshness {
    slog.Warn("slack timestamp freshness check is DISABLED — DO NOT USE IN PRODUCTION")
}
```

`SLACK_SKIP_FRESHNESS` を verify.go に渡す方法は: `VerifySignature` の動作を
切り替える Hook ではなく、handler.go / command.go 側で **skip フラグが立っていたら
Phase 0 互換の `VerifySignature` (= 鮮度チェック含む) を呼ぶ** か **skipped 版を
呼ぶ** かの分岐を入れる。

→ シンプル化のため: handler / command の constructor に `skipFreshness bool` を渡し、
ServeHTTP 内で `if skipFreshness { VerifySignatureLegacyNoFreshness(...) } else { VerifySignature(...) }`
する。

ただしこれは API surface を 2 つに増やす負債になる。代替案:

→ **`VerifySignature` 自体に `now` 引数を追加せず、package-level の
`var clock func() time.Time = time.Now` を導入**。テスト側で差し替え可能、
production は `time.Now`、SLACK_SKIP_FRESHNESS=1 時は handler 側で
`clock = func() time.Time { return time.Unix(<header timestamp>, 0) }` を渡す
…も結局 hack。

**最終案**: `VerifySignature` のシグネチャを変えず、内部で env var 読み取りも
しない (副作用排除)。鮮度チェック skip は **production には不要** なので、
runn シナリオは tests/runn/README.md で「2026-05-05 以降は失敗するため
timestamp を再生成して PR を更新せよ」と書くにとどめ、
当面 `just test-runn` の手順から除外する。
これは Phase 1 段階で受け入れ可能 (Phase 0 の runn シナリオ実行は smoke 用途で
production に必須ではない)。

## 完了条件

- 4 つの新規テスト (RejectsStale / RejectsFuture / RejectsUnparseable /
  AcceptsWithinWindow) が green
- 既存 `TestInteractiveHandler_*` および `TestCommandHandler_*` が全 pass
  (テスト側で署名計算時の timestamp を `time.Now().Unix()` に変更する必要あり、
  現状の固定値 `1700000000` を使っているテストは更新が必要)
- `go vet ./...` clean
- ADR 0016 を Accepted のまま維持
- runn シナリオは `tests/runn/STALE_TIMESTAMPS_README.md` で既知制約として記録

## Out of Scope

- nonce + replay cache (5 分窓内の再送防止) は本 issue では扱わない。
  必要になれば別 ADR で Memorystore Redis 案を起票
- Slack 以外の入口 (将来の CLI dispatch 等) には適用しない (CLI は env auth で別経路)

## 関連

- ADR 0016: Slack request timestamp の鮮度を検証して replay attack を拒否する
- ADR 0002: Slack 3秒ルールの回避 (本 issue が変更する verify.go と同じファイル)
- Codex Review round 2 (2026-05-05): 唯一の致命的指摘
- `internal/adapter/input/slack/verify.go`
- `internal/adapter/input/slack/verify_test.go`
- `tests/runn/*.yaml` (timestamp 固定値の影響あり)
