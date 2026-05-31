# tests/runn シナリオ — Slack 署名と timestamp 鮮度の扱い

> **状態: 解決済み (動的署名を実装)**。以前ここに記載していた「ハードコード
> timestamp=1700000000 + 事前計算署名により全シナリオが 401 で失敗する」既知
> 制約は解消した。各 runbook が **リクエスト毎に現在時刻で署名を動的計算** する
> ため、ADR 0016 の `now ± 5 分` 鮮度ウィンドウを正規に満たす。ファイル名は
> ADR 0016 からの参照アンカーとして維持している。

## 背景 (ADR 0016)

Slack 署名検証は **`X-Slack-Request-Timestamp` が `now ± 5 分`** の鮮度
ウィンドウ内であることを要求する (replay 攻撃拒否, fail-closed)。固定の
過去 timestamp + 事前計算署名では、この窓を満たせず 401 になる。

過去に検討して **却下** した代替案:

- **案 B: `SLACK_SKIP_FRESHNESS` env で鮮度チェックを無効化** — Issue 0019 で
  却下。fail-closed を堅持したく、production 誤設定リスクが高い。
- **案 C: シナリオ削除** — HMAC 経路 + 200 OK パターンの動作確認が失われ過剰。

採用したのは **案 A: runbook 内で timestamp と署名を動的計算** する方式。

## 仕組み (動的署名)

各署名付き runbook は最初の step で `exec` runner を使い、現在時刻の
timestamp と HMAC-SHA256 署名を計算して後続 step の header に注入する:

```yaml
runners:
  req: "${RUNN_ENDPOINT:-http://localhost:8080}"
vars:
  secret: "${SLACK_SIGNING_SECRET:-test-secret}"   # server の SLACK_SIGNING_SECRET と一致させる
  body: "payload=..."                              # 署名対象 = 送信 body と完全一致
steps:
  - exec:
      command: |
        ts=$(date +%s)
        sig=$(printf 'v0:%s:%s' "$ts" '{{vars.body}}' | openssl dgst -sha256 -hmac '{{vars.secret}}' | awk '{print $NF}')
        printf '%s v0=%s' "$ts" "$sig"
  - req:
      /slack/interactive:
        post:
          headers:
            X-Slack-Request-Timestamp: "{{ split(steps[0].stdout, ' ')[0] }}"
            X-Slack-Signature: "{{ split(steps[0].stdout, ' ')[1] }}"
          body:
            string: "{{vars.body}}"
    test: |
      current.res.status == 200
      && current.res.rawBody == ""
```

`dispatch_command.yaml` は 3 種の body それぞれに署名が要るため、1 つの `exec`
で `ts` + 3 署名を空白区切りで出力し、`split(steps[0].stdout, ' ')[1..3]` で
参照する。

## 実行

`exec` runner は runn のセキュリティスコープで保護されているため、
**`--scopes run:exec`** が必須。`just test-runn` recipe が付与済み:

```bash
# 1. dev サーバー起動 (別ターミナル)
SLACK_SIGNING_SECRET=test-secret just run

# 2. シナリオ実行
just test-runn        # = runn run --scopes run:exec tests/runn/*.yaml
```

ホストに `openssl` と `awk` が必要 (macOS / 標準 Linux に同梱)。

## シナリオ一覧と検証内容

| シナリオ | 検証 |
|---|---|
| `healthz.yaml` | `GET /_healthz` が 200 + `body.status == "ok"` |
| `approve_canary.yaml` | 署名付き approve interactive が 200 + 空 body (ack) |
| `deny_operation.yaml` | 署名付き deny interactive が 200 + 空 body (ack) |
| `dispatch_command.yaml` | valid `/agent` が ephemeral dispatch 確認 (approve/deny ボタン)、不明ロール/空 text が `unknown agent role` ephemeral、未署名が 401 |
| `invalid_signature.yaml` | 未署名 interactive が 401 で拒否 (replay/署名保護) |

## 関連

- ADR 0016: Slack request timestamp の鮮度を検証して replay attack を拒否する
- Issue 0019: 鮮度チェック (案 B の skip env はここで却下)
- `internal/adapter/input/slack/verify.go`
