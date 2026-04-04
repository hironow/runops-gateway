# 管理対象アプリの環境変数・設定管理

runops-gateway はカナリアデプロイ（トラフィックシフト）のみを担当する。
Cloud Run サービスの構成（環境変数、secrets、CPU/メモリ、スケーリング）は管理対象外。

## runops-gateway が触るもの・触らないもの

| 操作 | runops が担当 | 備考 |
|---|---|---|
| トラフィック配分の変更 | ✅ | `ShiftTraffic` / `UpdateWorkerPool` |
| 新リビジョンの作成 | ❌ | Cloud Build の `gcloud run deploy --no-traffic` が担当 |
| 環境変数の設定 | ❌ | 前リビジョンから自動継承 |
| Secret Manager 参照の設定 | ❌ | 同上 |
| CPU / メモリ / スケーリング | ❌ | 同上 |

## 環境変数が自動継承される仕組み

Cloud Build の deploy ステップは `--image` のみを指定する:

```bash
gcloud run deploy "$SVC" \
  --image ${_IMAGE}:${COMMIT_SHA} \
  --no-traffic
```

`--set-env-vars` や `--clear-env-vars` を指定しない場合、`gcloud run deploy` は
現在のサービス構成を維持したまま新しいイメージだけを差し替える。
環境変数、secrets、CPU/メモリ設定はすべて前のリビジョンから引き継がれる。

## 環境変数を変更する場合

### 追加・更新（非破壊）

```bash
gcloud run services update SERVICE_NAME \
  --project=PROJECT_ID --region=REGION \
  --update-env-vars "KEY1=value1,KEY2=value2"
```

`--update-env-vars` は指定したキーのみ変更し、既存の環境変数を保持する。
`--set-env-vars` は指定しなかったキーを**削除する**ため、通常は使わない。

### 削除

```bash
gcloud run services update SERVICE_NAME \
  --project=PROJECT_ID --region=REGION \
  --remove-env-vars "KEY_TO_DELETE"
```

### secrets の追加

```bash
gcloud run services update SERVICE_NAME \
  --project=PROJECT_ID --region=REGION \
  --set-secrets "ENV_NAME=SECRET_NAME:latest"
```

Secret Manager に事前に値を登録し、Cloud Run のランタイム SA に
`roles/secretmanager.secretAccessor` を付与しておくこと。

## service.yaml パターンを採用しない理由

`gcloud run services replace service.yaml` は宣言的で GitOps に適しているが、
runops-gateway のカナリアデプロイと相性が悪い:

1. `services replace` は `--no-traffic` オプションを持たない。
   新リビジョンが即座に 100% トラフィックを受け取り、段階的カナリアができない。
2. image タグが YAML に含まれるため、デプロイのたびに YAML の書き換えが必要。
3. revision 名が YAML に含まれ、immutable な revision と宣言的定義が矛盾する。

runops-gateway は `gcloud run deploy --image --no-traffic` で新リビジョンを作成し、
`ShiftTraffic` で段階的にトラフィックを移行する設計のため、
`services replace` は採用しない。

## Terraform / OpenTofu で Cloud Run 構成を管理しない理由

`google_cloud_run_v2_service` リソースで環境変数を管理すると、
Cloud Build の `gcloud run deploy` と tofu の `tofu apply` が同じリソースを操作し、
状態が競合する。

- Cloud Build が新リビジョンを作成 → tofu の state と乖離
- `tofu apply` が古いイメージに戻す可能性
- トラフィック設定も tofu と runops-gateway で競合

tofu は WIF、IAM、Artifact Registry など**インフラ基盤**の管理に使い、
Cloud Run サービスの構成管理には使わない。
