# 2 プロジェクト構成ガイド

runops-gateway を **GATEWAY_PROJECT** に、管理対象アプリを **APP_PROJECT** に配置する構成。

## この構成が適しているケース

- 本番環境でインフラとアプリの GCP プロジェクトを分離したい場合
- セキュリティ境界を明確にしたい場合（アプリチームに gateway のシークレットを触らせない）
- 1 つの管理対象アプリを運用する場合

## アーキテクチャ

```
+-----------------------------------------------+    +--------------------------------------------------+
|  GATEWAY_PROJECT                              |    |  APP_PROJECT                                     |
|                                               |    |                                                  |
|  +------------------------+                   |    |  +------------------+  +-----------+             |
|  | runops-gateway         |                   |    |  | Cloud Run Service|  | Cloud Run |             |
|  | (Cloud Run)            |                   |    |  | (your-app)       |  | Jobs      |             |
|  |                        |  roles/           |    |  +------------------+  | (migrate) |             |
|  |  SA: slack-chatops-sa -+--run.developer--> |    |         ^              +-----------+             |
|  +------------------------+  (cross-project)  |    |         |                    ^                   |
|                               ----------------+----+-> grant on each resource ----+                   |
|  +------------------------+                   |    |                                                  |
|  | Secret Manager         |                   |    |  +------------------+                            |
|  | - slack-signing-secret |                   |    |  | Cloud SQL        |                            |
|  | - slack-webhook-url    |                   |    |  | (optional)       |                            |
|  +----------+-------------+                   |    |  +------------------+                            |
|             ^                                 |    |         ^                                        |
|             | roles/secretmanager              |    |         | roles/cloudsql.admin                   |
|             | .secretAccessor                  |    |         | (grant in APP_PROJECT)                 |
|             | (cross-project)                  |    |         |                                        |
|             +---------------------------------+----+----+    |                                        |
|                                               |    |    |    |                                        |
|                                               |    |  +-+----+-------------------+                    |
|                                               |    |  | Cloud Build SA (default) |                    |
|                                               |    |  +--------------------------+                    |
+-----------------------------------------------+    +--------------------------------------------------+

Legend / 凡例:
- GATEWAY_PROJECT: runops-gateway が稼働する GCP プロジェクト
- APP_PROJECT: 管理対象アプリが稼働する GCP プロジェクト
- slack-chatops-sa: runops-gateway のランタイムサービスアカウント
- roles/run.developer: Cloud Run Service / Jobs の操作権限（APP_PROJECT 側で付与）
- roles/iam.serviceAccountUser: ランタイム SA への actAs 権限（Cloud Run 操作に必須、APP_PROJECT 側で付与）
- roles/artifactregistry.reader: Artifact Registry の読み取り権限（トラフィック切り替え時のイメージ pull に必要、APP_PROJECT 側で付与）
- roles/cloudsql.admin: Cloud SQL バックアップ権限（APP_PROJECT 側で付与）
- roles/secretmanager.secretAccessor: Webhook URL 読み取り権限（GATEWAY_PROJECT 側で付与）
- Cloud Build SA: APP_PROJECT の CI/CD 実行主体（notify-slack.sh で Webhook URL を読み取る）
```

## セットアップ手順

### 1. runops-gateway を GATEWAY_PROJECT にデプロイ

[README のセクション 1-1](../README.md) に従い、GATEWAY_PROJECT に OpenTofu でインフラを構築する。

```bash
cd tofu
tofu init -backend-config="bucket=YOUR_TOFU_STATE_BUCKET"
tofu apply \
  -var="project_id=YOUR_GATEWAY_PROJECT" \
  -var="image=asia-northeast1-docker.pkg.dev/YOUR_GATEWAY_PROJECT/runops/runops-gateway:latest" \
  -var="allowed_slack_users=U0123ABCD,U0456EFGH" \
  -var="github_repo=YOUR_ORG/runops-gateway" \
  -var="tofu_state_bucket=YOUR_TOFU_STATE_BUCKET"
```

シークレットの実値を登録する:

```bash
read -rs SIGNING_SECRET && printf '%s' "$SIGNING_SECRET" | \
  gcloud secrets versions add slack-signing-secret \
    --project=YOUR_GATEWAY_PROJECT \
    --data-file=-

read -rs WEBHOOK_URL && printf '%s' "$WEBHOOK_URL" | \
  gcloud secrets versions add slack-webhook-url \
    --project=YOUR_GATEWAY_PROJECT \
    --data-file=-
```

### 2. Slack App の設定

[docs/slack-setup.md](slack-setup.md) に従い、Slack App を作成・設定する。

### 3. クロスプロジェクト IAM の設定

#### 3-1. chatops SA に APP_PROJECT のリソース操作権限を付与

APP_PROJECT 側で実行する。

```bash
GATEWAY_PROJECT=YOUR_GATEWAY_PROJECT
APP_PROJECT=YOUR_APP_PROJECT
CHATOPS_SA="slack-chatops-sa@${GATEWAY_PROJECT}.iam.gserviceaccount.com"

# Cloud Run Service のトラフィック切り替え権限
gcloud run services add-iam-policy-binding your-service \
  --project=${APP_PROJECT} \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Run Jobs のマイグレーション実行権限
gcloud run jobs add-iam-policy-binding your-migrate-job \
  --project=${APP_PROJECT} \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Run Worker Pool がある場合
gcloud run worker-pools add-iam-policy-binding your-worker-pool \
  --project=${APP_PROJECT} \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud SQL バックアップ権限（DB マイグレーションで backup を取る場合）
gcloud projects add-iam-policy-binding ${APP_PROJECT} \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/cloudsql.admin"

# chatops SA がランタイム SA として act する権限（Cloud Run 操作に必須）
# Cloud Run はサービス更新時（トラフィック切り替え、ジョブ実行等）に、
# 呼び出し元がランタイム SA に対する iam.serviceAccounts.actAs 権限を持つことを要求する。
# この権限がないと ShiftTraffic / ExecuteJob が PermissionDenied で失敗する。
APP_PROJECT_NUMBER=$(gcloud projects describe ${APP_PROJECT} --format="value(projectNumber)")
gcloud iam service-accounts add-iam-policy-binding \
  ${APP_PROJECT_NUMBER}-compute@developer.gserviceaccount.com \
  --project=${APP_PROJECT} \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/iam.serviceAccountUser"

# Artifact Registry の読み取り権限（トラフィック切り替え時にイメージを pull するため）
gcloud artifacts repositories add-iam-policy-binding YOUR_AR_REPO \
  --project=${APP_PROJECT} \
  --location=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/artifactregistry.reader"
```

#### 3-2. APP_PROJECT の Cloud Build SA に GATEWAY_PROJECT のシークレット読み取り権限を付与

GATEWAY_PROJECT 側で実行する。`notify-slack.sh` が Slack Webhook URL を読み取るために必要。

```bash
APP_PROJECT_NUMBER=$(gcloud projects describe ${APP_PROJECT} --format="value(projectNumber)")
gcloud secrets add-iam-policy-binding slack-webhook-url \
  --project=${GATEWAY_PROJECT} \
  --member="serviceAccount:${APP_PROJECT_NUMBER}@cloudbuild.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

### 4. 管理対象アプリのブートストラップ

`just init-app` で CI/CD ファイルを配置する。2 プロジェクト構成では `gateway_project` 引数に GATEWAY_PROJECT を指定する。

```bash
just init-app /path/to/your-app YOUR_APP_PROJECT your-service your-migrate-job asia-northeast1 "" YOUR_GATEWAY_PROJECT
```

これにより、生成される `cloudbuild.yaml` の `_GATEWAY_PROJECT` substitution に GATEWAY_PROJECT が設定され、`notify-slack.sh` が正しいプロジェクトからシークレットを読み取る。

### 5. APP_PROJECT に WIF + deployer SA を作成

管理対象アプリのリポジトリから `gcloud builds submit` で Cloud Build を起動するために、APP_PROJECT に WIF と deployer SA を作成する。
runops-gateway の `tofu/github.tf` を参考に構築する。

GitHub リポジトリ変数を設定:

```bash
REPO="YOUR_ORG/your-app"

gh variable set GCP_PROJECT_ID                 --body "YOUR_APP_PROJECT"                            --repo "${REPO}"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body "projects/APP_PROJECT_NUMBER/locations/global/workloadIdentityPools/github/providers/github" --repo "${REPO}"
gh variable set GCP_SERVICE_ACCOUNT            --body "your-deployer-sa@YOUR_APP_PROJECT.iam.gserviceaccount.com" --repo "${REPO}"
gh variable set CLOUD_BUILD_REGION             --body "asia-northeast1"                             --repo "${REPO}"
```

`.github/workflows/cd.yaml` の設定例は [README のセクション 2-1](../README.md) を参照。

### 6. 動作確認

```bash
# runops-gateway が起動しているか確認
gcloud run services describe runops-gateway \
  --project=YOUR_GATEWAY_PROJECT \
  --region=asia-northeast1 \
  --format="value(status.url)"

# APP_PROJECT のリポジトリで main に push し、
# Cloud Build → Slack 通知 → ボタンクリック → トラフィック切り替え
# のフローが動作するか確認する

# IAM が正しく設定されているか確認
gcloud run services get-iam-policy your-service \
  --project=YOUR_APP_PROJECT \
  --region=asia-northeast1

gcloud secrets get-iam-policy slack-webhook-url \
  --project=YOUR_GATEWAY_PROJECT
```
