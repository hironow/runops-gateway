# tests/runn シナリオの既知制約 — timestamp 鮮度チェック

## 制約サマリ

`tests/runn/*.yaml` 内の **`X-Slack-Request-Timestamp` は固定値 `1700000000`
(2023-11-15 22:13:20 UTC)** で、署名 (`X-Slack-Signature`) も事前計算された
ハードコード値が埋め込まれている。

ADR 0016 / Issue 0019 (本 PR で導入) により、Slack 署名検証は **`now ± 5 分`**
の鮮度ウィンドウを強制する。
**現在 (2026-05-05) 時点で既存 runn シナリオを `just test-runn` で実行すると、
全シナリオが 401 Unauthorized で失敗する。**

```
desc: "Operator dispatches /agent paintress fix M-42 — accepted, empty 200"
...
expected: current.res.status == 200
actual:   current.res.status == 401
reason:   slack timestamp out of replay window
```

## 影響範囲

| シナリオ | 影響 |
|---|---|
| `approve_canary.yaml` | ✗ 全 step 401 |
| `deny_operation.yaml` | ✗ 全 step 401 |
| `dispatch_command.yaml` | ✗ 4 step 中 3 step 401 (unsigned step は元から 401) |
| `invalid_signature.yaml` | ✓ pass (401 が期待値、署名なしで先に弾かれる) |
| `healthz.yaml` | ✓ pass (Slack 署名不要) |

## なぜ修正していないか

3 案を検討して見送り:

### 案 A: runn シナリオで動的に timestamp と署名を計算する

runn の `runners` で `executor:` script を呼べば bash + openssl で計算可能だが:

- 全シナリオに共通の前処理を入れる boilerplate が大きい
- runn 標準の宣言的シナリオの読みやすさを損なう
- HMAC を bash で組むとテスト失敗時の原因切り分けが面倒

### 案 B: gateway server に "freshness skip" env var を追加

`SLACK_SKIP_FRESHNESS=1` で鮮度チェックを無効化できるようにする案。
**Issue 0019 で却下** (`fail-closed` を保ちたい、production 誤設定リスクが高い)。

### 案 C: シナリオファイルを完全に削除

既存の Phase 0 動作確認 (HMAC 経路 + 200 OK パターン) が消えるため過剰。

## 実用上の代替手段

ローカルで `/slack/command` をテストしたい場合:

```bash
# 1. サーバー起動
SLACK_SIGNING_SECRET=test-secret PORT=8080 go run ./cmd/server

# 2. 別ターミナルで current timestamp で署名計算してリクエスト送信
ts=$(date +%s)
body="command=%2Fagent&text=paintress+fix+M-42&user_id=U0123ABCD&response_url=http%3A%2F%2Flocalhost%2Fcallback"
sig="v0=$(printf 'v0:%s:%s' "$ts" "$body" | openssl dgst -sha256 -hmac test-secret -binary | xxd -p -c 256)"
curl -X POST http://localhost:8080/slack/command \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "X-Slack-Request-Timestamp: ${ts}" \
  -H "X-Slack-Signature: ${sig}" \
  -d "${body}"
# -> 200 OK, body 空 (StubDispatcher の slog 出力をサーバーログで確認)
```

`/slack/interactive` (既存 ChatOps) も同パターンで再現可能。

## 復旧手順 (シナリオを再生可能にしたい場合)

将来 runn シナリオを生かしたくなった場合の選択肢:

1. **案 A を実装** (script executor で動的計算)
2. **gateway 側に freshness skip env を追加** (Issue 0019 を re-open)
3. **fakeclock を全プロセスに inject** (clock パッケージを domain 層に追加し、
   テスト用 binary を別ビルド)

いずれも Phase 1 の本流ではないため、**当面は手動 curl でローカル動作確認** する
方針を維持する。

## 関連

- ADR 0016: Slack request timestamp の鮮度を検証して replay attack を拒否する
- Issue 0019: Slack request timestamp 鮮度チェックで replay attack を拒否する
- Codex Review (round 2, 2026-05-05): replay protection 欠落を指摘
- `internal/adapter/input/slack/verify.go`
- `docs/handover.md` Phase 1 review findings F-6
