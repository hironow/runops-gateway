# シングルプロジェクト構成ガイド

runops-gateway と管理対象アプリを **同一の GCP プロジェクト** で運用する構成。

## この構成が適しているケース

- 小規模チームで 1 つのアプリだけを管理する場合
- 開発・ステージング環境で手軽に動かしたい場合
- クロスプロジェクト IAM の複雑さを避けたい場合

## アーキテクチャ

```
+----------------------------------------------------------------------+
|  YOUR_PROJECT (single GCP project)                                   |
|                                                                      |
|  +------------------------+    +------------------+  +-----------+   |
|  | runops-gateway         |    | Cloud Run Service|  | Cloud Run |   |
|  | (Cloud Run)            |    | (your-app)       |  | Jobs      |   |
|  |                        |    +------------------+  | (migrate) |   |
|  |  SA: slack-chatops-sa -+--> roles/run.developer   +-----------+   |
|  +------------------------+    (same project)              ^         |
|                                                            |         |
|  +------------------------+    +------------------+        |         |
|  | Secret Manager         |    | Cloud Build SA   |--------+         |
|  | - slack-signing-secret |    | (default)        |                  |
|  | - slack-webhook-url  <-+----+ roles/secretmanager.secretAccessor  |
|  +------------------------+    +------------------+                  |
|                                                                      |
|  +------------------------+    +------------------+                  |
|  | Artifact Registry      |    | Cloud SQL        |                  |
|  | (runops)               |    | (optional)       |                  |
|  +------------------------+    +------------------+                  |
+----------------------------------------------------------------------+

Legend / 凡例:
- YOUR_PROJECT: runops-gateway と管理対象アプリが同居する GCP プロジェクト
- slack-chatops-sa: runops-gateway のランタイムサービスアカウント
- roles/run.developer: Cloud Run Service / Jobs の操作権限
- roles/secretmanager.secretAccessor: Webhook URL 読み取り権限
- Cloud Build SA: 管理対象アプリの CI/CD 実行主体
```

## セットアップ手順

### 1. runops-gateway のデプロイ

[README のセクション 1-1](../README.md) に従い、OpenTofu でインフラを構築する。

```bash
cd tofu
tofu init -backend-config="bucket=YOUR_TOFU_STATE_BUCKET"
tofu apply \
  -var="project_id=YOUR_PROJECT" \
  -var="image=asia-northeast1-docker.pkg.dev/YOUR_PROJECT/runops/runops-gateway:latest" \
  -var="allowed_slack_users=U0123ABCD,U0456EFGH" \
  -var="github_repo=YOUR_ORG/runops-gateway" \
  -var="tofu_state_bucket=YOUR_TOFU_STATE_BUCKET"
```

シークレットの実値を登録する:

```bash
read -rs SIGNING_SECRET && printf '%s' "$SIGNING_SECRET" | \
  gcloud secrets versions add slack-signing-secret \
    --project=YOUR_PROJECT \
    --data-file=-

read -rs WEBHOOK_URL && printf '%s' "$WEBHOOK_URL" | \
  gcloud secrets versions add slack-webhook-url \
    --project=YOUR_PROJECT \
    --data-file=-
```

### 2. Slack App の設定

[docs/slack-setup.md](slack-setup.md) に従い、Slack App を作成・設定する。

### 3. 管理対象アプリのブートストラップ

`just init-app` で CI/CD ファイルを配置する。シングルプロジェクト構成では `gateway_project` 引数は不要（デフォルトで `${PROJECT_ID}` = 同一プロジェクトになる）。

```bash
just init-app /path/to/your-app YOUR_PROJECT your-service your-migrate-job
```

複数サービスの場合:

```bash
just init-app /path/to/your-app YOUR_PROJECT "frontend,backend" db-migrate
```

### 4. IAM の確認

シングルプロジェクト構成では、tofu apply で以下の権限が **同一プロジェクト内に** 自動的に付与されている:

| SA | ロール | 対象 |
|---|---|---|
| `slack-chatops-sa` | `roles/run.developer` | Cloud Run Service（tofu で指定した `cloud_run_target_service`） |
| `slack-chatops-sa` | `roles/cloudsql.admin` | プロジェクトレベル |
| Cloud Build デフォルト SA | `roles/secretmanager.secretAccessor` | `slack-webhook-url` シークレット |

追加の Cloud Run Service / Jobs に対して `run.developer` が必要な場合は手動で付与する:

```bash
CHATOPS_SA="slack-chatops-sa@YOUR_PROJECT.iam.gserviceaccount.com"

# Cloud Run Service
gcloud run services add-iam-policy-binding your-service \
  --project=YOUR_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Run Jobs
gcloud run jobs add-iam-policy-binding your-migrate-job \
  --project=YOUR_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"
```

Cloud Build SA への Secret Accessor 権限も同一プロジェクト内で付与する:

```bash
PROJECT_NUMBER=$(gcloud projects describe YOUR_PROJECT --format="value(projectNumber)")
gcloud secrets add-iam-policy-binding slack-webhook-url \
  --project=YOUR_PROJECT \
  --member="serviceAccount:${PROJECT_NUMBER}@cloudbuild.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

### 5. GitHub Actions + WIF の設定

管理対象アプリのリポジトリから `gcloud builds submit` で Cloud Build を起動するために、WIF と deployer SA を作成する。
runops-gateway の `tofu/github.tf` を参考に、同一プロジェクトに作成する。

GitHub リポジトリ変数を設定:

```bash
REPO="YOUR_ORG/your-app"

gh variable set GCP_PROJECT_ID                 --body "YOUR_PROJECT"                                --repo "${REPO}"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body "projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/github" --repo "${REPO}"
gh variable set GCP_SERVICE_ACCOUNT            --body "your-deployer-sa@YOUR_PROJECT.iam.gserviceaccount.com" --repo "${REPO}"
gh variable set CLOUD_BUILD_REGION             --body "asia-northeast1"                             --repo "${REPO}"
```

`.github/workflows/cd.yaml` の設定例は [README のセクション 2-1](../README.md) を参照。

### 6. 動作確認

```bash
# runops-gateway が起動しているか確認
gcloud run services describe runops-gateway \
  --project=YOUR_PROJECT \
  --region=asia-northeast1 \
  --format="value(status.url)"

# 管理対象アプリのリポジトリで main に push し、
# Cloud Build → Slack 通知 → ボタンクリック → トラフィック切り替え
# のフローが動作するか確認する
```
