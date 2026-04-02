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
export GOOGLE_CLOUD_PROJECT=dummy-project      # パターン A は dummy でよい
export ALLOWED_SLACK_USERS=UTEST123            # 承認を許可する Slack ユーザー ID
```

`NewController` は起動時にネットワーク接続しないため、`GOOGLE_CLOUD_PROJECT` が
実在しないプロジェクトでもサーバーは正常に起動する。
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
SLACK_SIGNING_SECRET=test-secret GOOGLE_CLOUD_PROJECT=dummy just run

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
export GOOGLE_CLOUD_PROJECT=your-real-project
export ALLOWED_SLACK_USERS=your-email@example.com
```

### B-1. CLI で GCP 操作を直接確認（Slack 不要）

`--no-slack` を指定すると Slack 通知なしで GCP のみ操作する。

```bash
go run ./cmd/runops approve service YOUR_SERVICE_NAME \
  --action=canary_10 \
  --target=YOUR_REVISION_NAME \
  --no-slack

go run ./cmd/runops approve job YOUR_MIGRATION_JOB \
  --action=migrate_apply \
  --no-slack

go run ./cmd/runops deny service YOUR_SERVICE_NAME \
  --no-slack
```

Cloud Run コンソール または以下で結果を確認:

```bash
gcloud run services describe YOUR_SERVICE_NAME \
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
export GOOGLE_CLOUD_PROJECT=your-real-project
export ALLOWED_SLACK_USERS=YOUR_SLACK_USER_ID   # Slack の自分の ID (U0XXXXXX)
export CLOUD_RUN_LOCATION=asia-northeast1

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

## トラブルシューティング

### `SLACK_SIGNING_SECRET is required` で起動しない

```bash
export SLACK_SIGNING_SECRET=test-secret
```

### `GOOGLE_CLOUD_PROJECT is required` で起動しない

```bash
export GOOGLE_CLOUD_PROJECT=dummy   # パターン A は dummy でよい
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
