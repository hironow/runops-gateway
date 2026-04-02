# Issue 0010: OpenTofu インフラ定義

## Goal

runops-gateway を稼働させるための GCP インフラを OpenTofu で定義する。

## 作成するリソース

### サービスアカウント

```hcl
resource "google_service_account" "chatops_sa" {
  account_id = "slack-chatops-sa"
}
```

### IAM（最小権限）

- `roles/run.developer` — 操作対象の Cloud Run リソースに対してのみ
- `roles/cloudsql.admin` — 対象 Cloud SQL インスタンスに対してのみ
- `roles/secretmanager.secretAccessor` — 指定シークレットに対してのみ

### Secret Manager

- `slack-signing-secret` シークレットの作成（値は手動投入）
- `slack-bot-token` シークレットの作成

### Cloud Run Service（runops-gateway 本体）

```hcl
resource "google_cloud_run_v2_service" "chatops_middleware" {
  name     = "runops-gateway"
  location = var.region

  template {
    annotations = {
      "run.googleapis.com/cpu-throttling" = "false"  # 必須（ADR 0003）
    }
    # Secret Manager からの環境変数注入
  }
}
```

### パブリックアクセス（Slack Webhook 受信用）

```hcl
resource "google_cloud_run_v2_service_iam_member" "public_invoker" {
  role   = "roles/run.invoker"
  member = "allUsers"
}
```

## ディレクトリ構成

```
opentofu/
├── main.tf
├── variables.tf
├── outputs.tf
└── versions.tf
```

## Definition of Done (DoD)

- [ ] `tofu validate` がエラーなく通る
- [ ] `tofu plan` が正常に実行できる（dry run）
- [ ] CPU スロットリング無効化が設定されている（ADR 0003）
- [ ] `SLACK_SIGNING_SECRET` が Secret Manager 参照で注入されている（平文不可）
- [ ] IAM ロールがプロジェクトレベルではなくリソースレベルで付与されている

## 非機能要件

- **セキュリティ**: シークレットは Secret Manager のみに保存し、`variables.tf` に平文で書かないこと
- **最小権限**: `roles/run.admin` や `roles/editor` などの過剰な権限を使わないこと（ADR 0004）
- **再現性**: `tofu apply` のみで全基盤が再現できること（手動操作が不要であること）
- **コスト**: 最小インスタンス数 0 をデフォルトとし、コールドスタートを許容すること
