# マルチプロジェクト構成ガイド

runops-gateway を **GATEWAY_PROJECT** に、複数の管理対象アプリをそれぞれ **別々の APP_PROJECT** に配置する構成（3 つ以上の GCP プロジェクト）。

## この構成が適しているケース

- 複数のプロダクト・チームが独立した GCP プロジェクトを持つ場合
- マルチテナント環境で 1 つの gateway から複数アプリを管理したい場合
- 組織レベルでインフラ管理を一元化したい場合

## アーキテクチャ

```
+-----------------------------------------------+
|  GATEWAY_PROJECT                              |
|                                               |
|  +------------------------+                   |
|  | runops-gateway         |                   |
|  | (Cloud Run)            |                   |
|  |  SA: slack-chatops-sa  |                   |
|  +------+---------+-------+                   |
|         |         |                           |
|  +------+------+  |  +--------------------+   |
|  | Secret Mgr  |  |  | Artifact Registry  |   |
|  | - signing   |  |  | (runops)           |   |
|  | - webhook   |  |  +--------------------+   |
|  +------+------+  |                           |
|         ^         |                           |
+---------+---------+---------------------------+
          |         |
          |         +------ roles/run.developer (per resource) ------+
          |         |                                                |
          |         v                                                v
+---------+--------------------+    +-------------------------------+--------+
|  APP_PROJECT_A               |    |  APP_PROJECT_B                         |
|                              |    |                                        |
|  +----------------+         |    |  +----------------+  +---------------+  |
|  | Cloud Run Svc  |         |    |  | Cloud Run Svc  |  | Cloud Run     |  |
|  | (app-a)        |         |    |  | (app-b)        |  | Worker Pool   |  |
|  +----------------+         |    |  +----------------+  +---------------+  |
|  +----------------+         |    |  +----------------+                     |
|  | Cloud Run Jobs |         |    |  | Cloud Run Jobs |                     |
|  | (app-a-migrate)|         |    |  | (app-b-migrate)|                     |
|  +----------------+         |    |  +----------------+                     |
|  +----------------+         |    |  +----------------+                     |
|  | Cloud Build SA +--+      |    |  | Cloud Build SA +--+                  |
|  +----------------+  |      |    |  +----------------+  |                  |
|                      |      |    |                      |                  |
+----------------------+------+    +----------------------+------------------+
                       |                                  |
                       +-- secretAccessor on webhook -----+
                           (each Cloud Build SA)

Legend / 凡例:
- GATEWAY_PROJECT: runops-gateway が稼働する GCP プロジェクト（1 つだけ）
- APP_PROJECT_A / B: 管理対象アプリがそれぞれ稼働する GCP プロジェクト（N 個）
- slack-chatops-sa: runops-gateway のランタイム SA（全 APP_PROJECT のリソースを操作）
- roles/run.developer: Cloud Run Service / Jobs / Worker Pool の操作権限（各 APP_PROJECT で個別に付与）
- roles/artifactregistry.reader: Artifact Registry の読み取り権限（トラフィック切り替え時のイメージ pull に必要、各 APP_PROJECT で付与）
- secretAccessor: 各 APP_PROJECT の Cloud Build SA に GATEWAY_PROJECT のシークレット読み取り権限を付与
- Secret Mgr: slack-signing-secret と slack-webhook-url を管理
```

## セットアップ手順

### 1. runops-gateway を GATEWAY_PROJECT にデプロイ（1 回だけ）

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

Slack App の設定は [docs/slack-setup.md](slack-setup.md) を参照。Slack App は 1 つで全プロジェクトに対応する。

### 2. 各 APP_PROJECT のセットアップ（アプリごとに繰り返す）

以下の手順を **管理対象アプリごと** に実行する。

#### 2-1. クロスプロジェクト IAM の設定

```bash
GATEWAY_PROJECT=YOUR_GATEWAY_PROJECT
APP_PROJECT=YOUR_APP_PROJECT_X   # アプリごとに変更
CHATOPS_SA="slack-chatops-sa@${GATEWAY_PROJECT}.iam.gserviceaccount.com"
```

**chatops SA に APP_PROJECT のリソース操作権限を付与（APP_PROJECT 側で実行）:**

```bash
# Cloud Run Service
gcloud run services add-iam-policy-binding your-service-x \
  --project=${APP_PROJECT} \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Run Jobs
gcloud run jobs add-iam-policy-binding your-migrate-job-x \
  --project=${APP_PROJECT} \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Run Worker Pool（使用する場合）
gcloud run worker-pools add-iam-policy-binding your-worker-pool-x \
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

**APP_PROJECT の Cloud Build SA に GATEWAY_PROJECT のシークレット読み取り権限を付与（GATEWAY_PROJECT 側で実行）:**

```bash
APP_PROJECT_NUMBER=$(gcloud projects describe ${APP_PROJECT} --format="value(projectNumber)")
gcloud secrets add-iam-policy-binding slack-webhook-url \
  --project=${GATEWAY_PROJECT} \
  --member="serviceAccount:${APP_PROJECT_NUMBER}@cloudbuild.gserviceaccount.com" \
  --role="roles/secretmanager.secretAccessor"
```

#### 2-2. 管理対象アプリのブートストラップ

```bash
just init-app /path/to/your-app-x YOUR_APP_PROJECT_X your-service-x your-migrate-job-x asia-northeast1 "" YOUR_GATEWAY_PROJECT
```

#### 2-3. APP_PROJECT に WIF + deployer SA を作成

各 APP_PROJECT に WIF と deployer SA を作成する。runops-gateway の `tofu/github.tf` を参考に構築する。

GitHub リポジトリ変数を設定:

```bash
REPO="YOUR_ORG/your-app-x"

gh variable set GCP_PROJECT_ID                 --body "YOUR_APP_PROJECT_X"                          --repo "${REPO}"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body "projects/APP_PROJECT_X_NUMBER/locations/global/workloadIdentityPools/github/providers/github" --repo "${REPO}"
gh variable set GCP_SERVICE_ACCOUNT            --body "your-deployer-sa@YOUR_APP_PROJECT_X.iam.gserviceaccount.com" --repo "${REPO}"
gh variable set CLOUD_BUILD_REGION             --body "asia-northeast1"                             --repo "${REPO}"
```

`.github/workflows/cd.yaml` の設定例は [README のセクション 2-1](../README.md) を参照。

---

## Slack の構成

Slack App は **1 つ** で全プロジェクトに対応する:

- **Incoming Webhook**: 1 つの Webhook URL で全アプリからの通知を同一チャンネルに送信できる。チャンネルを分けたい場合は Webhook を追加して `slack-webhook-url` シークレットの値を用途に応じて使い分ける。
- **Interactivity**: Request URL は runops-gateway の 1 エンドポイント（`/slack/interactive`）。ボタンの value に `project` と `location` が含まれるため、gateway は操作対象のプロジェクトを自動的に判別する。
- **Signing Secret**: 1 つの Slack App に対して 1 つ。

---

## セキュリティに関する考慮事項

マルチプロジェクト構成では、`slack-chatops-sa` が **全 APP_PROJECT のリソースに対する `run.developer` 権限を持つ**。

| リスク | 対策 |
|---|---|
| chatops SA の権限が広がりすぎる | `run.developer` はリソース単位（Service / Jobs / Worker Pool ごと）で付与する。プロジェクトレベルで付与しない。`iam.serviceAccountUser` はランタイム SA 単位で付与する |
| 1 つの SA 漏洩で全アプリに影響 | GATEWAY_PROJECT への最小権限アクセスを徹底する。gateway の Cloud Run は `allUsers` invoker だが、Slack 署名検証 + ユーザー許可リストで保護されている |
| `cloudsql.admin` はプロジェクトレベル | Cloud SQL のリソースレベル IAM がバックアップに対応していないため、プロジェクトレベルでの付与が必要。APP_PROJECT にアプリ DB のみが存在することを確認する |

管理対象アプリが増えるたびに chatops SA の IAM バインディングを棚卸しすることを推奨する:

```bash
# chatops SA が持つ全 IAM バインディングを確認
# (各 APP_PROJECT で実行)
gcloud run services get-iam-policy your-service \
  --project=YOUR_APP_PROJECT \
  --region=asia-northeast1 \
  --flatten="bindings[].members" \
  --filter="bindings.members:slack-chatops-sa"
```

---

## バリアント: 1 つの APP_PROJECT に複数アプリ

2 つ以上の管理対象アプリが **同一の APP_PROJECT** を共有するケース。

```
+---------------------------+    +----------------------------------------------+
|  GATEWAY_PROJECT          |    |  SHARED_APP_PROJECT                          |
|                           |    |                                              |
|  +-------------------+    |    |  +----------+  +----------+  +----------+   |
|  | runops-gateway    |    |    |  | app-a    |  | app-b    |  | app-a    |   |
|  | SA: chatops-sa  --+--->|    |  | (Service)|  | (Service)|  | -migrate |   |
|  +-------------------+    |    |  +----------+  +----------+  | (Jobs)   |   |
|                           |    |                              +----------+   |
|  +-------------------+    |    |  +----------+                               |
|  | Secret Manager  <-+----+----|--+ Cloud    |                               |
|  | slack-webhook-url |    |    |  | Build SA |                               |
|  +-------------------+    |    |  +----------+                               |
+---------------------------+    +----------------------------------------------+

Legend / 凡例:
- SHARED_APP_PROJECT: 複数アプリが同居する GCP プロジェクト
- app-a / app-b: 同一プロジェクト内の別々の Cloud Run Service
- Cloud Build SA: プロジェクト内で共有される CI/CD サービスアカウント
```

### IAM の違い

- **`run.developer`**: リソース単位（Service / Jobs ごと）で付与する。アプリが増えるたびに新しい Service / Jobs への binding を追加する。
- **`iam.serviceAccountUser`**: ランタイム SA 単位で付与する。同一 APP_PROJECT のアプリが同じランタイム SA（デフォルト Compute SA）を使っていれば binding は **1 回だけ** で済む。
- **`secretmanager.secretAccessor`**: Cloud Build SA はプロジェクトごとに 1 つ。同一 APP_PROJECT のアプリが増えても binding は **1 回だけ** で済む。
- **`cloudsql.admin`**: プロジェクトレベルの付与なので、同一 APP_PROJECT のアプリが増えても追加不要。

### ブートストラップ

`just init-app` はアプリリポジトリごとに 1 回実行する。`app_project` は同じ値を指定する。

```bash
# app-a
just init-app /path/to/app-a SHARED_APP_PROJECT app-a app-a-migrate asia-northeast1 "" YOUR_GATEWAY_PROJECT

# app-b
just init-app /path/to/app-b SHARED_APP_PROJECT app-b app-b-migrate asia-northeast1 "" YOUR_GATEWAY_PROJECT
```

### IAM 設定の追加分

```bash
CHATOPS_SA="slack-chatops-sa@YOUR_GATEWAY_PROJECT.iam.gserviceaccount.com"

# app-a の権限（1 アプリ目のセットアップ時に実行済み）
gcloud run services add-iam-policy-binding app-a \
  --project=SHARED_APP_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

gcloud run jobs add-iam-policy-binding app-a-migrate \
  --project=SHARED_APP_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# app-b の権限（2 アプリ目のセットアップ時に追加）
gcloud run services add-iam-policy-binding app-b \
  --project=SHARED_APP_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

gcloud run jobs add-iam-policy-binding app-b-migrate \
  --project=SHARED_APP_PROJECT \
  --region=asia-northeast1 \
  --member="serviceAccount:${CHATOPS_SA}" \
  --role="roles/run.developer"

# Cloud Build SA の Secret Accessor は 1 回だけ（同一 APP_PROJECT なので）
# 1 アプリ目のセットアップ時に実行済みであれば追加不要
```
