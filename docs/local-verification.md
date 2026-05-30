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
curl http://localhost:8080/_healthz
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
| `healthz.yaml` | `/_healthz` が 200 + `{"status":"ok"}` を返す |
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

## パターン C: Pub/Sub bridge (Phase 2a/b/c) — testcontainers

`/runops` Slash Command 経路 → Pub/Sub → phonewave outbox → 5本柱、および逆向きの
5本柱 archive → Pub/Sub → gateway 経路は、**testcontainers が起動する firebase
emulator container の中で** integration test として検証する (ADR 0041)。外部
emulator・docker compose・`just pubsub-up` は不要。GCP も Slack も実体は不要。

```bash
just test-integration   # testcontainers が emulator を起動し全ラウンドトリップを検証
```

`go test -tags=integration` が `tests/integration/setup_test.go` の `TestMain` で
firebase emulator container を起動 (`docker/firebase-emulator/Dockerfile` から
build)、`PUBSUB_EMULATOR_HOST` を動的注入、topic を初期化して Phase 2a publish /
2b receiver / 2c emitter / Phase 3 outbound (chat.postMessage を httptest mock で
受ける) / Phase 4a approval の全経路を1コマンドで検証する。**Docker daemon が
起動していることだけが前提**で、env 未設定でも skip されない (fail-loud)。

> 旧 `just pubsub-up` + 手動 `go run ./cmd/dmail-receiver` / `dmail-emitter` /
> `cmd/server` を emulator に向ける smoke 手順は ADR 0041 で integration test に
> 統合され、廃止された。testcontainers の container は `go test` プロセス内に閉じる
> ため外部から接続できない — 手動経路確認は `tests/integration/` のテストが代替する。

---

## パターン D: HIGH severity 4-eyes approval (Phase 4a)

`amadeus` の HIGH severity convergence (ADR 0019) が gateway で
`approval_approve` / `approval_deny` ボタン付き chat.postMessage に
化けることを確認する。Slack 実体は不要 (mock receiver で代替)。

HIGH severity convergence → 4-eyes approval (chat.postMessage に
`approval_approve` / `approval_deny` ボタン付き Block Kit) の経路は
`tests/integration/pubsub_approval_test.go`
(`TestIntegration_HighSeverityConvergence_PostsApprovalRequestBlocks`) が
testcontainers で emulator を起動し、httptest mock Slack で chat.postMessage を
受けて Block Kit payload まで検証する (ADR 0041)。

```bash
just test-integration   # 上記テストを含む全 integration test を testcontainers で実行
```

手動で mock Slack receiver + `go run ./cmd/server` + convergence publish を組む旧
smoke (D-1〜D-3) は emulator 起動手段 (`just pubsub-up`) の廃止に伴い廃止した。
D-Mail frontmatter は flat key:value (`dmail-schema-version` / トップレベルに
`severity: high` 等) で、parser は `metadata:` ネストを解釈しない点に注意。

---

## パターン E: OTel trace を共有 dotfiles `tel` で確認 (ADR 0020 / 0042)

3 binary すべてが `OTEL_EXPORTER_OTLP_ENDPOINT` 切替で local の OTLP backend に
trace を送れることを確認する。runops-gateway は自前の trace backend を持たない
(ADR 0042、`compose.yaml` / `just trace-up` は廃止)。local backend は **dotfiles
の `tel` スタック** (otel-collector `:4317` → Tempo/Grafana) を再利用する。

> **prod との差**: prod (Cloud Run) では `GOOGLE_CLOUD_PROJECT` env が
> 自動セットされ、`internal/adapter/observability` で resource
> attribute `gcp.project_id` に転用される (PR #21、Cloud Trace OTLP の
> 必須属性)。**local の collector は `gcp.project_id` を要求しない**ので
> 空のまま動く。

### E-1. dotfiles の `tel` を立てる (trace backend)

```bash
# dotfiles checkout で:
just tel-up             # otel-collector :4317/:4318 -> Tempo/Grafana
# Grafana UI: http://localhost:3010 (dotfiles portless 経由なら https://grafana.localhost)
```

### E-2. cmd/server を OTel 込みで起動 → Slack POST 1 件

```bash
SLACK_SIGNING_SECRET=test-secret \
  OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317 \
  OTEL_SERVICE_NAME=runops-gateway-local \
  OTEL_TRACES_SAMPLER=parentbased_traceidratio \
  OTEL_TRACES_SAMPLER_ARG=1.0 \
  go run ./cmd/server &

curl -sf http://localhost:8080/_healthz   # /_healthz は trace から除外される
# pattern A-2 の curl で /slack/interactive を叩く (HMAC 計算込み)
```

### E-3. Grafana/Tempo で確認

```
open http://localhost:3010    # dotfiles Grafana (admin/admin)
# Explore -> Tempo datasource -> Service Name = 'runops-gateway-local' で検索
# 'POST /slack/interactive' root span 配下に slack.verify_signature →
# slack.handle_dispatch_action → usecase.dispatch_agent_task → send dmail-inbound
# が 1 trace_id で繋がっていることを確認
```

### E-4. Pub/Sub bridge を跨ぐ連携 trace

Pub/Sub bridge を跨ぐ trace (publish→receive が 1 trace_id で繋がること) は
`tests/integration/` の testcontainers integration test (ADR 0041) が emulator を
起動して検証する。3 binary を手動で emulator に並走させて trace を見る旧手順は、
emulator 起動手段 (`just pubsub-up`) の廃止に伴い廃止した。trace 自体の確認は
E-2/E-3 (cmd/server → dotfiles `tel` の Tempo) で行い、bridge 跨ぎの trace 連結は
integration test の span assertion に委ねる。

詳細な span tree は `docs/handover.md` の「ハマりどころ 7. trace context propagation」を参照。

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

### `just test-integration` が fail する

testcontainers (ADR 0041) 化により、外部 emulator 前提の flake (stale daemon /
前 run のメッセージ残存 / 手動 smoke 干渉) は解消した。残る原因は次の 3 つ。

**(1) Docker daemon が起動していない**

testcontainers は Docker daemon に container を起こす。daemon (OrbStack /
Docker Desktop 等) が落ちていると、skip ではなく hard fail する (fail-loud)。

```bash
docker info >/dev/null 2>&1 && echo "docker up" || echo "start your Docker daemon (e.g. orb start)"
```

**(2) 初回の emulator image build が遅い / timeout**

firebase emulator image を `docker/firebase-emulator/Dockerfile` から build する
初回は 30-60s かかる。`KeepImage` で 2 回目以降は cache される。起動待ちで
timeout する場合は image を一度 build し切ってから再実行する。

**(3) test cache が古い結果を返している**

`go test` は同じソースで cached PASS を返す。順序依存の flake を即発見するには
`-count=1`:

```bash
go test -tags=integration -count=1 ./tests/integration/... ./internal/adapter/output/state/...
```
