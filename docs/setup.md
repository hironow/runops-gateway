# Setup / セットアップと更新

runops-gateway 自体を動かすための作業を記す。 管理対象アプリ側のデプロイ設定は [docs/guide-single-project.md](guide-single-project.md) / [docs/guide-two-projects.md](guide-two-projects.md) / [docs/guide-multi-project.md](guide-multi-project.md) を参照。

## 前提

- `gcloud` CLI がログイン済み
- 対象 GCP プロジェクトのオーナー相当の権限
- `tofu` (OpenTofu)、 `gh` (GitHub CLI)、 `docker` がローカルにあること

## 初回セットアップ

### (1) OpenTofu リモートステート用 GCS バケットを作成

`tofu init` の前に存在している必要があるため、 手動で作成する (bootstrap constraint)。

```bash
gcloud storage buckets create gs://YOUR_TOFU_STATE_BUCKET \
  --project=YOUR_PROJECT \
  --location=asia-northeast1 \
  --uniform-bucket-level-access
```

### (2) OpenTofu でインフラを一括構築

作成されるリソース:

- Artifact Registry リポジトリ (`runops`)
- Workload Identity Federation + `github-deployer` SA (GitHub Actions 用)
- `slack-chatops-sa` (Cloud Run ランタイム用)
- Secret Manager シークレット (`slack-signing-secret` / `slack-webhook-url` / `slack-bot-token`)
- Cloud Run サービス (`runops-gateway`)
- Pub/Sub topic / subscription (`dmail-inbound` / `dmail-outbound` + 各 DLQ + DLQ pull subscription)
- Cloud Trace + telemetry API enable + `roles/cloudtrace.agent`
- Cloud Monitoring alert (DLQ forwarding) — `dlq_alert_email` 設定時のみ
- 各種 IAM バインディング

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

任意 (Phase 4b 機能を有効化する場合):

```bash
  -var="slack_default_channel_id=C0123ABCD"  # FallbackNotifier (ADR 0017)
  -var="otel_traces_sampler_arg=0.1"         # OTEL_TRACES_SAMPLER_ARG (ADR 0020)
  -var="exe_coder_vm_sa_email=exe-workspace@YOUR_PROJECT.iam.gserviceaccount.com"
  # 値は workspace VM SA (ADR 0023)。 変数名は ADR 0015 era から
  # 維持しているが、 中身は workspace SA を入れる。 validation block
  # が pattern を強制 (ADR 0024)
  -var="dlq_alert_email=oncall@example.com"  # Cloud Monitoring 通知先
  -var="cloud_run_min_instances=1"           # Phase 3 outbound (ADR 0018) を有効化するとき
  -var="cloud_run_max_instances=3"           # default 3 (必要に応じて調整)
```

### (3) シークレットの実値を登録

`tofu` はリソースのみ作成し、 値は手動で追加する。

```bash
gcloud secrets versions add slack-signing-secret \
  --data-file=<(echo -n "YOUR_SLACK_SIGNING_SECRET") \
  --project=YOUR_PROJECT

gcloud secrets versions add slack-webhook-url \
  --data-file=<(echo -n "https://hooks.slack.com/services/YOUR/WEBHOOK/URL") \
  --project=YOUR_PROJECT

# Bot Token (xoxb-...) — ADR 0017 (FallbackNotifier) / ADR 0019 (HIGH severity 4-eyes approval)
gcloud secrets versions add slack-bot-token \
  --data-file=<(echo -n "xoxb-YOUR-BOT-TOKEN") \
  --project=YOUR_PROJECT
```

### (4) 初回イメージをビルドして Artifact Registry に push

(2) の `tofu apply` 時点では Cloud Run はプレースホルダーイメージで起動している。

```bash
IMAGE=$(cd tofu && tofu output -raw artifact_registry_repository)/runops-gateway
docker build -t ${IMAGE}:latest .
docker push ${IMAGE}:latest

# Cloud Run に初回イメージをデプロイ
gcloud run deploy runops-gateway \
  --image=${IMAGE}:latest \
  --region=asia-northeast1 \
  --project=YOUR_PROJECT
```

### (5) GitHub リポジトリ変数を設定

GitHub Actions CD パイプラインが使用する。 `tofu output` の値を `gh` CLI で直接流し込む (`tofu/` ディレクトリで実行)。

```bash
cd tofu
REPO="YOUR_ORG/runops-gateway"

gh variable set GCP_PROJECT_ID                 --body "YOUR_PROJECT"                                  --repo "${REPO}"
gh variable set GCP_WORKLOAD_IDENTITY_PROVIDER --body "$(tofu output -raw workload_identity_provider)" --repo "${REPO}"
gh variable set GCP_SERVICE_ACCOUNT            --body "$(tofu output -raw github_deployer_sa_email)"  --repo "${REPO}"
gh variable set ARTIFACT_REGISTRY_LOCATION     --body "asia-northeast1"                               --repo "${REPO}"
gh variable set TOFU_STATE_BUCKET              --body "YOUR_TOFU_STATE_BUCKET"                        --repo "${REPO}"
gh variable set CLOUD_RUN_LOCATION             --body "asia-northeast1"                               --repo "${REPO}"

# ALLOWED_SLACK_USERS は空文字非対応のため、 実際の Slack ユーザー ID が確定してから設定
# gh variable set ALLOWED_SLACK_USERS          --body "U0123ABCD,U0456EFGH"                           --repo "${REPO}"
```

任意の Phase 4b 機能を CD で有効化したい場合 (cd.yaml 上部のコメント参照):

```bash
gh variable set SLACK_DEFAULT_CHANNEL_ID  --body "C0123ABCD"                                              --repo "${REPO}"
gh variable set OTEL_TRACES_SAMPLER_ARG   --body "0.1"                                                    --repo "${REPO}"
gh variable set EXE_CODER_VM_SA_EMAIL     --body "exe-workspace@${PROJECT}.iam.gserviceaccount.com"       --repo "${REPO}"
gh variable set DLQ_ALERT_EMAIL           --body "oncall@example.com"                                     --repo "${REPO}"

# 設定確認
gh variable list --repo "${REPO}"
```

### (6) Slack App の設定

詳細は [docs/slack-setup.md](slack-setup.md) を参照。 最低限の設定:

- Interactivity & Shortcuts > Request URL: `https://<URL>/slack/interactive`
- Slash Commands: `/runops` → `https://<URL>/slack/command`
- OAuth Bot Token Scopes: `commands`, `chat:write`

URL は `tofu output` で確認:

```bash
cd tofu && tofu output runops_gateway_url
```

## 更新デプロイ

通常は `main` ブランチへの push で GitHub Actions (`.github/workflows/cd.yaml`) が自動実行される。 インフラ変更 (`tofu/` 配下のファイル変更) も同一パイプラインで検知して `tofu apply` まで実行する。

CD pipeline の **post-deploy smoke** が `/_healthz` 200 + `/slack/{interactive,command}` invalid-sig 401 を毎回検証する。 失敗時は `gcloud run services update-traffic ... --to-revisions=PREVIOUS=100` でロールバック ([docs/handover.md](handover.md) のハマりどころ集 9-pre 参照)。

### 手動デプロイ

```bash
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

## ブランチ運用と release promote

`feature → develop → main` の 2 段。 merge 方法は段により異なる:

| ブランチ移行 | merge 方式 |
|---|---|
| feature / fix / chore → `develop` | **squash merge** (`gh pr merge <N> --squash`) |
| `develop` → `main` (release / promote) | **merge commit** (`gh pr merge <N> --merge`) |

`main` への promote PR を間違えて squash で merge すると、 各 feature の個別 commit が `main` 側から消えるので **必ず** `--merge` を指定する。 詳細は [CLAUDE.md](../CLAUDE.md) の "Git / PR merge ポリシー"。

## 関連 docs

- [docs/architecture.md](architecture.md) — システム全体図 + ディレクトリ構成
- [docs/runops-gateway-env-vars.md](runops-gateway-env-vars.md) — runops-gateway 自身の env vars
- [docs/env-vars-and-config.md](env-vars-and-config.md) — 管理対象アプリ側の env 管理方針
- [docs/slack-setup.md](slack-setup.md) — Slack App セットアップ
- [docs/local-verification.md](local-verification.md) — ローカル動作確認
- [docs/runbooks/dlq.md](runbooks/dlq.md) — DLQ alert triage runbook
