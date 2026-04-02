# runops-gateway

Slack ChatOps gateway for GCP operations.

管理対象アプリの CI/CD パイプラインが新しいリビジョンをデプロイした後、
Slack のボタンを押すだけでカナリアリリースや DB マイグレーションを安全に実行できる。

runops-gateway 自体は GitHub Actions (`cd.yaml`) で自動デプロイされる。

## 概要

```
[ 管理対象アプリの CI/CD (Cloud Build 等) ]
    |  1. イメージビルド & デプロイ (traffic 0%)
    |  2. Slack に Block Kit ボタンを通知
    v
[ Slack ワークスペース ]
    |  承認者がボタンをクリック
    v
[ runops-gateway (Cloud Run) ]  <- このリポジトリ
    |  署名検証 -> 認可 -> 非同期実行
    v
[ GCP (Cloud Run / Cloud SQL) ]
    トラフィック切り替え / DB マイグレーション
```

対応オペレーション:

| リソース | アクション | 内容 |
|---|---|---|
| `service` | `canary_N` | Cloud Run Service のトラフィックを N% へ切り替え |
| `job` | `migrate_apply` | Cloud SQL バックアップ取得 → Cloud Run Jobs でマイグレーション実行 |
| `worker-pool` | `canary_N` | Cloud Run Worker Pool のインスタンス割り当てを N% へ切り替え |

---

## 1. runops-gateway 自体のセットアップと更新

このセクションは **runops-gateway を動かすための作業** です。
管理対象アプリのデプロイ設定は「[2. 管理対象アプリのデプロイ設定](#2-管理対象アプリのデプロイ設定)」を参照してください。

### 1-1. 初回セットアップ

> **前提**: `gcloud` CLI がログイン済みで、対象 GCP プロジェクトのオーナー相当の権限があること。

```bash
# (1) OpenTofu リモートステート用 GCS バケットを作成
#     tofu init の前に存在している必要があるため手動で作成する (bootstrap constraint)
gcloud storage buckets create gs://YOUR_TOFU_STATE_BUCKET \
  --project=YOUR_PROJECT \
  --location=asia-northeast1 \
  --uniform-bucket-level-access
```

```bash
# (2) OpenTofu でインフラを一括構築
#     作成されるリソース:
#       - Artifact Registry リポジトリ (runops)
#       - Workload Identity Federation + github-deployer SA (GitHub Actions 用)
#       - slack-chatops-sa (Cloud Run ランタイム用)
#       - Secret Manager シークレット (slack-signing-secret, slack-webhook-url)
#       - Cloud Run サービス (runops-gateway)
#       - 各種 IAM バインディング
cd tofu
tofu init -backend-config="bucket=YOUR_TOFU_STATE_BUCKET"
tofu apply \
  -var="project_id=YOUR_PROJECT" \
  -var="image=asia-northeast1-docker.pkg.dev/YOUR_PROJECT/runops/runops-gateway:latest" \
  -var="allowed_slack_users=U0123ABCD,U0456EFGH" \
  -var="github_repo=YOUR_ORG/runops-gateway" \
  -var="tofu_state_bucket=YOUR_TOFU_STATE_BUCKET"
```

```bash
# (3) シークレットの実値を登録 (tofu はリソースのみ作成 — 値は手動で追加)
gcloud secrets versions add slack-signing-secret \
  --data-file=<(echo -n "YOUR_SLACK_SIGNING_SECRET") \
  --project=YOUR_PROJECT

gcloud secrets versions add slack-webhook-url \
  --data-file=<(echo -n "https://hooks.slack.com/services/YOUR/WEBHOOK/URL") \
  --project=YOUR_PROJECT
```

```bash
# (4) 初回イメージをビルドして Artifact Registry に push
#     (2 の tofu apply 時点では Cloud Run はプレースホルダーイメージで起動)
IMAGE=$(cd tofu && tofu output -raw artifact_registry_repository)/runops-gateway
docker build -t ${IMAGE}:latest .
docker push ${IMAGE}:latest

# Cloud Run に初回イメージをデプロイ
gcloud run deploy runops-gateway \
  --image=${IMAGE}:latest \
  --region=asia-northeast1 \
  --project=YOUR_PROJECT
```

```bash
# (5) GitHub リポジトリ変数を設定 (GitHub Actions CD パイプラインが使用)
#     tofu output の値を gh CLI で直接流し込む (tofu/ ディレクトリで実行)
cd tofu
REPO="YOUR_ORG/runops-gateway"

gh variable set GCP_PROJECT_ID                 --body "YOUR_PROJECT"                              --repo "${REPO}"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body "$(tofu output -raw workload_identity_provider)"   --repo "${REPO}"
gh variable set GCP_SERVICE_ACCOUNT            --body "$(tofu output -raw github_deployer_sa_email)"     --repo "${REPO}"
gh variable set ARTIFACT_REGISTRY_LOCATION     --body "asia-northeast1"                           --repo "${REPO}"
gh variable set TOFU_STATE_BUCKET              --body "YOUR_TOFU_STATE_BUCKET"                    --repo "${REPO}"
gh variable set CLOUD_RUN_LOCATION             --body "asia-northeast1"                           --repo "${REPO}"
# ALLOWED_SLACK_USERS は空文字非対応のため、実際の Slack ユーザー ID が確定してから設定:
# gh variable set ALLOWED_SLACK_USERS          --body "U0123ABCD,U0456EFGH"                       --repo "${REPO}"

# 設定確認:
gh variable list --repo "${REPO}"
```

```bash
# (6) Slack App の設定
#     Slack App 管理画面 > Interactivity & Shortcuts > Request URL に以下を設定:
#     https://<CLOUD_RUN_URL>/slack/interactive
#
#     URL は tofu output で確認:
cd tofu && tofu output runops_gateway_url
```

### 1-2. gateway 自体の更新デプロイ

通常は `main` ブランチへの push で GitHub Actions (`cd.yaml`) が自動実行されます。
インフラ変更 (`tofu/` 配下のファイル変更) も同一パイプラインで検知して `tofu apply` まで実行します。

手動でデプロイしたい場合:

```bash
# イメージを再ビルドして push
IMAGE=asia-northeast1-docker.pkg.dev/YOUR_PROJECT/runops/runops-gateway
SHA=$(git rev-parse --short HEAD)

docker build -t ${IMAGE}:${SHA} -t ${IMAGE}:latest .
docker push --all-tags ${IMAGE}

# Cloud Run に新リビジョンをデプロイ (即時 100% トラフィック)
gcloud run deploy runops-gateway \
  --image=${IMAGE}:${SHA} \
  --region=asia-northeast1 \
  --project=YOUR_PROJECT
```

---

## 2. 管理対象アプリのデプロイ設定

このセクションは **runops-gateway を使って自分のアプリをデプロイする** ための作業です。
runops-gateway 自体のセットアップは完了している前提です。

### 2-1. 初回設定（アプリごとに1回）

管理対象アプリのリポジトリに以下のファイルを配置します。

```bash
# runops-gateway リポジトリから CI/CD 用ファイルをコピー
cp /path/to/runops-gateway/cloudbuild.yaml  ./cloudbuild.yaml
cp /path/to/runops-gateway/scripts/notify-slack.sh ./scripts/notify-slack.sh
chmod +x ./scripts/notify-slack.sh
```

`cloudbuild.yaml` の substitutions を自分のアプリ用に編集します:

```yaml
substitutions:
  _IMAGE: asia-northeast1-docker.pkg.dev/YOUR_PROJECT/YOUR_REPO/YOUR_APP
  _SERVICE_NAMES: your-service-name          # 複数の場合はカンマ区切り
  _MIGRATION_JOB_NAME: your-db-migrate-job
  _REGION: asia-northeast1
```

Cloud Build トリガーを設定します:

```bash
# main ブランチへの push をトリガーに設定
gcloud builds triggers create github \
  --repo-name=YOUR_REPO \
  --repo-owner=YOUR_ORG \
  --branch-pattern=^main$ \
  --build-config=cloudbuild.yaml \
  --region=asia-northeast1
```

runops-gateway の Secret Manager に Webhook URL が登録済みであることを確認し、
Cloud Build のサービスアカウントに Secret Accessor 権限を付与します:

```bash
PROJECT_NUMBER=$(gcloud projects describe YOUR_PROJECT --format="value(projectNumber)")
gcloud projects add-iam-policy-binding YOUR_PROJECT \
  --member="serviceAccount:${PROJECT_NUMBER}@cloudbuild.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

### 2-2. 通常デプロイ（アプリ更新のたびに）

`main` ブランチへの push で Cloud Build が自動実行されます。以降の手順は:

1. Cloud Build が完了すると Slack にボタン付きメッセージが届く
2. 承認者がボタンをクリック
3. runops-gateway がトラフィック切り替え or DB マイグレーションを実行
4. 完了後 Slack メッセージが更新され、次の段階（canary_30 など）のボタンが表示される

```
[ git push → Cloud Build 完了 ]
        |
        v
[ Slack: "1. DBマイグレーション → Canary" | "2. Canary (skip migration)" | "Deny" ]
        | クリック
        v
[ runops-gateway: ShiftTraffic(10%) ]
        |
        v
[ Slack: "canary_30 に昇格" | "停止・ロールバック" ]
        | クリック
        v
[ runops-gateway: ShiftTraffic(30%) ]
        ...
        v
[ runops-gateway: ShiftTraffic(100%) → デプロイ完了 ]
```

#### 手動トリガー（テスト・緊急時）

```bash
# 単一サービス
gcloud builds submit --config=cloudbuild.yaml \
  --substitutions="_SERVICE_NAMES=your-service,_REGION=asia-northeast1"

# 複数サービス同時デプロイ
gcloud builds submit --config=cloudbuild.yaml \
  --substitutions="_SERVICE_NAMES=frontend-service,backend-service,_REGION=asia-northeast1"
```

#### CLI での緊急操作（Slack ダウン時）

Slack が使えない場合は `runops` CLI で直接操作できます:

```bash
export GOOGLE_CLOUD_PROJECT=your-project
export ALLOWED_SLACK_USERS=your-email@example.com

# カナリアリリース (10%)
runops approve service your-service \
  --action=canary_10 --target=YOUR_REVISION_NAME --no-slack

# 複数サービス同時カナリア
runops approve service "frontend-service,backend-service" \
  --action=canary_10 --target="frontend-v2,backend-v2" --no-slack

# DB マイグレーション
runops approve job db-migrate-job --action=migrate_apply --no-slack

# 拒否 (デプロイ中断)
runops deny service your-service --no-slack
```

`--no-slack` を指定すると Slack 通知なしで stdout へ出力します。

---

## アーキテクチャ

Ports and Adapters (Hexagonal Architecture) を採用。コアドメイン・ユースケースは外部依存ゼロ。

```
cmd/server          cmd/runops
    |                   |
    +---+   +---+-------+
        |   |
    [ UseCase ]          <- port.RunOpsUseCase
        |
  +-----+-----+
  |     |     |
GCP  Slack  Auth/State   <- secondary ports (interfaces)
```

- **Driving adapters**: Slack HTTP Handler、Cobra CLI
- **Driven adapters**: GCP Controller、Slack Notifier、EnvAuthChecker、MemoryStore

## ディレクトリ構成

```
runops-gateway/
├── cmd/
│   ├── server/         # HTTP サーバー (Slack Webhook 受信)
│   └── runops/         # CLI ツール
├── internal/
│   ├── core/
│   │   ├── domain/     # ResourceType, Action, ApprovalRequest (外部依存なし)
│   │   └── port/       # インターフェース定義
│   ├── usecase/        # ApproveAction / DenyAction オーケストレーション
│   └── adapter/
│       ├── input/
│       │   ├── slack/  # HTTP Handler + HMAC 署名検証
│       │   └── cli/    # Cobra コマンド (approve / deny)
│       └── output/
│           ├── gcp/    # Cloud Run + Cloud SQL API クライアント
│           ├── slack/  # response_url Notifier + Block Kit テンプレート
│           ├── auth/   # EnvAuthChecker (allowlist + 有効期限)
│           └── state/  # MemoryStore (二重実行防止)
├── scripts/
│   └── notify-slack.sh # Cloud Build から呼ばれる Slack 通知スクリプト
├── tofu/               # GCP インフラ定義 (OpenTofu) ← gateway 自体のインフラ
├── tests/runn/         # シナリオテスト (runn)
├── cloudbuild.yaml     # 管理対象アプリ用 CI/CD パイプラインのテンプレート
├── Dockerfile          # multi-stage build (distroless)
└── justfile            # タスクランナー
```

## 環境変数

### サーバー (`cmd/server`)

| 変数 | 必須 | デフォルト | 説明 |
|---|---|---|---|
| `SLACK_SIGNING_SECRET` | ✓ | — | Slack App の Signing Secret |
| `GOOGLE_CLOUD_PROJECT` | ✓ | — | GCP プロジェクト ID |
| `CLOUD_RUN_LOCATION` | — | `asia-northeast1` | Cloud Run のリージョン |
| `PORT` | — | `8080` | HTTP ポート |
| `ALLOWED_SLACK_USERS` | — | `""` (全拒否) | 承認許可ユーザーの Slack ID (カンマ区切り) |
| `BUTTON_EXPIRY_SECONDS` | — | `7200` | ボタン有効期限（秒） |

### CLI (`cmd/runops`)

| 変数 | 必須 | 説明 |
|---|---|---|
| `GOOGLE_CLOUD_PROJECT` | ✓ | GCP プロジェクト ID |
| `ALLOWED_SLACK_USERS` | — | 承認許可ユーザー (CLI ではメールアドレスを使用) |

## 開発

```bash
# テスト
just test

# リント
just lint

# フォーマット
just fmt

# ビルド
just build

# シナリオテスト (要サーバー起動)
just test-runn

# notify-slack.sh のペイロード構造テスト (bash/Go 圧縮ラウンドトリップ確認)
# 必要ツール: bash, gzip, base64, jq, curl
just test-scripts
```

## シナリオテスト

`tests/runn/` に runn シナリオが5本ある。`SLACK_SIGNING_SECRET=test-secret` でサーバーを起動して実行する。

```bash
# 全シナリオ実行
SLACK_SIGNING_SECRET=test-secret PORT=8080 just run &
just test-runn
```

## ドキュメント

- [`docs/local-verification.md`](docs/local-verification.md) — ローカル動作確認ガイド（GCP なし / Tailscale Funnel E2E）
- [`docs/intent.md`](docs/intent.md) — 設計意図・アーキテクチャ詳細
- [`docs/adr/`](docs/adr/) — Architecture Decision Records (0001–0011)
- [`docs/handover.md`](docs/handover.md) — 実装状況・テストカバレッジ・今後の課題
