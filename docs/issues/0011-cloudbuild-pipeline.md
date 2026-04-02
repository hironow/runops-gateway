# Issue 0011: Cloud Build パイプライン

## Goal

アプリケーションのビルド・デプロイと Slack Block Kit 通知を行う `cloudbuild.yaml` を作成する。

## パイプラインステップ

1. **Build & Push**: アプリコンテナのビルドと Artifact Registry への Push
2. **Deploy Service**: Cloud Run Service へのデプロイ（`--no-traffic`）、リビジョン名を `revision.txt` に保存
3. **Update Job**: Cloud Run Jobs のテンプレート更新（マイグレーション用）
4. **Notify Slack**: `jq` でペイロードを生成し `curl` で Slack Webhook に POST

## Block Kit ペイロード仕様

Issue 0012（Block Kit テンプレート）で定義されたテンプレートを使用する。
ペイロードの `value` フィールドに以下の JSON を埋め込む:

```json
// Service (canary) 用
{
  "resource_type": "service",
  "resource_name": "frontend-service",
  "target": "<REVISION>",
  "action": "canary_10",
  "issued_at": <UNIX_TIMESTAMP>
}

// Job (migration) 用
{
  "resource_type": "job",
  "resource_name": "db-migrate-job",
  "target": "",
  "action": "migrate_apply",
  "issued_at": <UNIX_TIMESTAMP>
}
```

## Secret Manager 参照

```yaml
availableSecrets:
  secretManager:
    - versionName: projects/$PROJECT_ID/secrets/slack-webhook-url/versions/latest
      env: SLACK_WEBHOOK_URL
```

## Definition of Done (DoD)

- [ ] デプロイステップ失敗時に Slack への通知ステップが実行されないこと
- [ ] `issued_at` に現在の Unix タイムスタンプが埋め込まれること
- [ ] `revision.txt` から正確なリビジョン名が取得されること
- [ ] `jq` を使ったペイロード生成で JSON エスケープが正しく行われること
- [ ] `cloudbuild.yaml` の yaml 構文バリデーションが通ること

## 非機能要件

- **セキュリティ**: `SLACK_WEBHOOK_URL` が `cloudbuild.yaml` に平文で書かれていないこと（Secret Manager 参照）
- **信頼性**: ステップ間の依存関係（`waitFor`）が明示されていること
- **可観測性**: 各ステップに `id` が設定され、Cloud Build ログで識別可能なこと
