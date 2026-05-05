# ローカル動作確認ガイド

このドキュメントは runops-gateway をローカルで起動し、動作を確認する手順をまとめる。

確認したい内容によって 2 つのパターンに分かれる。

| パターン | 用途 | GCP 不要 | Slack 不要 |
|---|---|---|---|
| **A. 操作対象なし** | 署名検証・ルーティング・ペイロード構造の確認 | ✓ | ✓ |
| **B. 操作対象あり** | 実 GCP リソースへの操作 + Slack E2E | — | — |

---

## 共通: サーバーの起動

### 最低限の環境変数

```bash
export SLACK_SIGNING_SECRET=test-secret        # パターン A は任意の値でよい
export ALLOWED_SLACK_USERS=UTEST123            # 承認を許可する Slack ユーザー ID
```

サーバーは `SLACK_SIGNING_SECRET` のみで起動できる。
GCP プロジェクト ID とリージョンはサーバーの環境変数ではなく、
Slack ボタンの値（`ApprovalRequest.Project` / `ApprovalRequest.Location`）から取得される。
GCP への実操作（`ShiftTraffic` 等）が走ったときに初めて認証エラーになる。

### 起動コマンド

```bash
just run
# または
go run ./cmd/server
```

起動確認:

```bash
curl http://localhost:8080/healthz
# → {"status":"ok"}
```

---

## パターン A: 操作対象のデプロイ物がない場合

GCP も Slack も不要。署名検証・ペイロード解析・ルーティングの正しさを確認する。

### A-1. runn シナリオテスト

サーバーを起動した状態で、あらかじめ用意されたシナリオを一括実行する。

```bash
# 別ターミナルでサーバーを起動
SLACK_SIGNING_SECRET=test-secret just run

# シナリオ実行
just test-runn
```

カバーされるシナリオ:

| ファイル | 確認内容 |
|---|---|
| `healthz.yaml` | `/healthz` が 200 + `{"status":"ok"}` を返す |
| `invalid_signature.yaml` | 署名なしリクエストが 401 になる |
| `approve_canary.yaml` | 正しい署名の approve ペイロードが 200 になる |
| `deny_operation.yaml` | 正しい署名の deny ペイロードが 200 になる |
| `approve_canary.yaml` (canary 進行) | canary ステップの継続ボタンが生成される |

### A-2. curl で手動確認

HMAC 署名を自分で計算して叩く。

```bash
SIGNING_SECRET=test-secret
TIMESTAMP=$(date +%s)
BODY='payload=%7B%22type%22%3A%22block_actions%22%2C%22user%22%3A%7B%22id%22%3A%22UTEST123%22%7D%2C%22actions%22%3A%5B%5D%7D'

SIG="v0=$(printf "v0:%s:%s" "$TIMESTAMP" "$BODY" \
  | openssl dgst -sha256 -hmac "$SIGNING_SECRET" \
  | awk '{print $2}')"

curl -s -X POST http://localhost:8080/slack/interactive \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -H "X-Slack-Request-Timestamp: $TIMESTAMP" \
  -H "X-Slack-Signature: $SIG" \
  --data-raw "$BODY"
# → (空レスポンス、HTTP 200)
```

### A-3. notify-slack.sh の dry-run でペイロード確認

実際に Slack へ送信せず、生成される Block Kit JSON ペイロードを標準出力に出力する。

```bash
bash scripts/notify-slack.sh --dry-run \
  "frontend-service,backend-service" \
  "db-migrate-job" \
  "main" \
  "abc1234567890" \
  "frontend-service-00001-abc,backend-service-00001-def" | jq .
```

ボタン値が `gz:` で始まることを確認:

```bash
bash scripts/notify-slack.sh --dry-run \
  "frontend-service" "db-migrate-job" "main" "abc1234" "frontend-service-00001-abc" \
  | jq '[.blocks[] | select(.type=="actions") | .elements[].value]'
# → ["gz:H4sIAAAA...", "gz:H4sIAAAA...", "gz:H4sIAAAA..."]
```

### A-4. Go テスト (ユニット + bash/Go ラウンドトリップ)

```bash
# 全ユニットテスト
just test

# notify-slack.sh に関するテストのみ (bash, gzip, base64, jq, curl が必要)
just test-scripts
```

---

## パターン B: 操作対象のデプロイ物がある場合

実際の Cloud Run Service / Job / Worker Pool を操作する。
**B-1** は Slack 不要で GCP のみ確認、**B-2** は Slack E2E 全体を確認する。

### 前提: GCP 認証

```bash
gcloud auth application-default login
export ALLOWED_SLACK_USERS=your-email@example.com
```

### B-1. CLI で GCP 操作を直接確認（Slack 不要）

`--no-slack` を指定すると Slack 通知なしで GCP のみ操作する。
`--project` と `--location` で対象プロジェクトとリージョンを指定する。

```bash
go run ./cmd/runops approve service YOUR_SERVICE_NAME \
  --project=YOUR_PROJECT --location=asia-northeast1 \
  --action=canary_10 \
  --target=YOUR_REVISION_NAME \
  --no-slack

go run ./cmd/runops approve job YOUR_MIGRATION_JOB \
  --project=YOUR_PROJECT --location=asia-northeast1 \
  --action=migrate_apply \
  --no-slack

go run ./cmd/runops deny service YOUR_SERVICE_NAME \
  --project=YOUR_PROJECT --location=asia-northeast1 \
  --no-slack
```

Cloud Run コンソール または以下で結果を確認:

```bash
gcloud run services describe YOUR_SERVICE_NAME \
  --project=YOUR_PROJECT \
  --region=asia-northeast1 \
  --format="value(status.traffic)"
```

### B-2. Tailscale Funnel で Slack E2E を確認

ローカルサーバーを Tailscale Funnel で公開し、実際の Slack ボタンクリックから
GCP 操作までの全パスを確認する。

#### ステップ 1: Tailscale Funnel でローカルポートを公開

```bash
tailscale funnel 8080
```

出力例:

```
Available on the internet:
https://your-hostname.tailnet-name.ts.net/ ✓
```

この URL (`https://your-hostname.tailnet-name.ts.net`) を控える。

#### ステップ 2: サーバーをローカルで起動

```bash
export SLACK_SIGNING_SECRET=your-real-signing-secret
export ALLOWED_SLACK_USERS=YOUR_SLACK_USER_ID   # Slack の自分の ID (U0XXXXXX)

just run
```

#### ステップ 3: Slack App の Request URL を一時的に切り替える

Slack App 管理画面 → **Interactivity & Shortcuts** → Request URL を以下に変更:

```
https://your-hostname.tailnet-name.ts.net/slack/interactive
```

> 確認後は本番の Cloud Run URL に戻すこと。

#### ステップ 4: notify-slack.sh で Slack にボタン付きメッセージを送信

```bash
export SLACK_WEBHOOK_URL=https://hooks.slack.com/services/YOUR/WEBHOOK/URL

bash scripts/notify-slack.sh \
  "YOUR_SERVICE_NAME" \
  "YOUR_MIGRATION_JOB" \
  "local-test" \
  "$(git rev-parse HEAD)" \
  "YOUR_REVISION_NAME"
```

#### ステップ 5: Slack でボタンをクリックして動作確認

1. Slack に届いたメッセージのボタンをクリック
2. ローカルサーバーのログで `gcp: shifting traffic` が出ることを確認
3. Cloud Run コンソールでトラフィック割合が変わったことを確認
4. Slack のメッセージが次のカナリアステップのボタンに更新されることを確認

#### ステップ 6: 確認後のクリーンアップ

```bash
# Tailscale Funnel を停止
tailscale funnel --bg=false 8080  # または Ctrl-C

# Slack App の Request URL を本番 Cloud Run URL に戻す
# Cloud Run のトラフィックをロールバックする場合:
go run ./cmd/runops approve service YOUR_SERVICE_NAME \
  --action=rollback --target=YOUR_REVISION_NAME --no-slack
```

---

## パターン C: Pub/Sub bridge (Phase 2a/b/c)

`/agent` Slash Command 経路 → Pub/Sub → phonewave outbox → 5本柱、および
逆向きの 5本柱 archive → Pub/Sub → gateway 経路を、Firebase Pub/Sub emulator
で完全にローカル検証する。GCP も Slack も実体は不要。

### C-0. emulator + topic 初期化

```bash
just pubsub-up        # Firebase Pub/Sub emulator (Docker Compose)
just pubsub-init      # dmail-inbound / dmail-outbound + DLQ + subscriptions
```

`http://localhost:9399` (Pub/Sub) と `http://localhost:4000` (Web UI) で生きている
ことを確認。停止は `just pubsub-down`。

### C-1. 自動 integration test (CI 想定)

```bash
just test-integration
```

`PUBSUB_EMULATOR_HOST=localhost:9399` を立てて `tests/integration/` 配下の
`//go:build integration` テストを走らせる。Phase 2a publish / Phase 2b receiver /
Phase 2c emitter の 3 ラウンドトリップを 1 行で確認できる。

### C-2. 手動 smoke (Phase 2b 受信側)

```bash
SMOKE=$(mktemp -d /tmp/runops-smoke-XXXXXX)
mkdir -p "$SMOKE/outbox"

# 1) receiver を別ターミナルで起動
PUBSUB_EMULATOR_HOST=localhost:9399 \
PUBSUB_PROJECT_ID=runops-local \
PUBSUB_DMAIL_INBOUND_SUB=dmail-receiver-sub \
PHONEWAVE_OUTBOX_DIR="$SMOKE/outbox" \
go run ./cmd/dmail-receiver

# 2) もう一つのターミナルから 1 件 publish
PUBSUB_EMULATOR_HOST=localhost:9399 \
go run ./scripts/smoke/dispatch.go --target paintress --text "hello phase 2b"

# 3) outbox に .md が atomic write される
ls "$SMOKE/outbox/"   # → <pubsub_message_id>.md
```

### C-4. 手動 smoke (Phase 3: dmail-outbound → Slack thread reply)

gateway を `PUBSUB_DMAIL_OUTBOUND_SUB=runops-gateway-sub` 込みで起動し、
mock Slack server (or 実 Slack の test channel) を立てて、emulator に
publish した結果が thread reply として届くまで確認できる。

```bash
# 1) mock Slack chat.postMessage receiver (port 18888)
python3 -c '
from http.server import HTTPServer, BaseHTTPRequestHandler
class H(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        print(f"[mock slack] {body.decode()}")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b"{\"ok\":true,\"ts\":\"1700000000.000100\"}")
HTTPServer(("0.0.0.0", 18888), H).serve_forever()
' &

# 2) gateway を Phase 3 設定で起動
SLACK_SIGNING_SECRET=test-secret \
SLACK_BOT_TOKEN=xoxb-fake \
PUBSUB_EMULATOR_HOST=localhost:9399 \
PUBSUB_PROJECT_ID=runops-local \
PUBSUB_DMAIL_OUTBOUND_SUB=runops-gateway-sub \
go run ./cmd/server &
# (chat.postMessage URL を mock に向けたい場合は cmd/server の slackChatPostMessageURL
#  定数を一時的に "http://localhost:18888/" に変えてから go run、もしくは
#  一時的に integration test を直接実行して挙動を確認するのが現実的)

# 3) Phase 2c emitter が無くても、Pub/Sub に直接 publish して経路を確認できる
# (scripts/smoke/dispatch.go の Target を amadeus 等に変えるなど任意)
```

### C-3. 手動 smoke (Phase 2c 送信側)

```bash
mkdir -p "$SMOKE/archive"

# 1) emitter を別ターミナルで起動
PUBSUB_EMULATOR_HOST=localhost:9399 \
PUBSUB_PROJECT_ID=runops-local \
PUBSUB_DMAIL_OUTBOUND_TOPIC=dmail-outbound \
PHONEWAVE_ARCHIVE_DIRS="$SMOKE/archive" \
go run ./cmd/dmail-emitter

# 2) D-Mail .md を archive に直接 write
cat > "$SMOKE/archive/report.md" <<'EOF'
---
dmail-schema-version: "1"
id: smoke-001
kind: report
target: amadeus
source: paintress
idempotency_key: smoke-001
slack_thread_ts: "1700000000.000050"
---

PR #42 merged.
EOF

# 3) outbound subscription から pull して中身を確認
curl -s -X POST "http://localhost:9399/v1/projects/runops-local/subscriptions/runops-gateway-sub:pull" \
  -H "Content-Type: application/json" -d '{"maxMessages":5}' | python3 -m json.tool
```

---

## トラブルシューティング

### `SLACK_SIGNING_SECRET is required` で起動しない

```bash
export SLACK_SIGNING_SECRET=test-secret
```

### runn シナリオが `signature mismatch` で失敗する

`approve_canary.yaml` の署名はタイムスタンプ `1700000000` で事前計算されている。
サーバーの `SLACK_SIGNING_SECRET` が `test-secret` になっているか確認する。

```bash
echo $SLACK_SIGNING_SECRET   # test-secret であること
```

### Tailscale Funnel の URL に繋がらない

```bash
tailscale status           # Funnel が有効か確認
tailscale funnel status    # 公開ポートの確認
```

### `just test-integration` が flaky に fail する (Pub/Sub bridge)

最も多い原因 3 つ。

**(1) stale daemon が subscription を hijack している**

`go run ./cmd/dmail-receiver` や `dmail-emitter` を Ctrl-C / kill した直後でも、
`go run` が子バイナリに SIGTERM を伝播しないことがある (macOS 上の go ツール
チェーンの既知挙動)。subscription を背景で pull し続けるプロセスが居ると、
integration test の subscriber が message を奪われる:

```bash
pgrep -af "dmail-receiver|dmail-emitter"   # stale プロセスを探す
pkill -KILL -f "exe/dmail-receiver"        # 子バイナリを直接 kill
pkill -KILL -f "exe/dmail-emitter"
```

**(2) 前 run のメッセージが subscription に残っている**

emulator は subscription を再起動しても永続化される。手動 smoke の途中だった
場合は drain してから再 run する:

```bash
for sub in dmail-receiver-sub runops-gateway-sub; do
  curl -s -X POST "http://localhost:9399/v1/projects/runops-local/subscriptions/$sub:pull" \
    -H "Content-Type: application/json" -d '{"maxMessages":50}' > /tmp/pull-$sub.json
  ack=$(python3 -c "import json; r=json.load(open('/tmp/pull-$sub.json')); print(','.join(['\"'+m['ackId']+'\"' for m in r.get('receivedMessages',[])]))")
  [ -n "$ack" ] && curl -s -X POST "http://localhost:9399/v1/projects/runops-local/subscriptions/$sub:acknowledge" \
    -H "Content-Type: application/json" -d "{\"ackIds\":[$ack]}"
done
```

**(3) test cache が古い結果を返している**

`go test` は同じソース + 同じ env で cached PASS を返すため、emulator 状態の
変化を検知しない。順序依存の flake を即発見するには `-count=1`:

```bash
PUBSUB_EMULATOR_HOST=localhost:9399 PUBSUB_PROJECT_ID=runops-local \
  go test -tags=integration -count=1 ./tests/integration/...
```
