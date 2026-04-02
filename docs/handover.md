# Handover Document

**Last updated:** 2026-04-02

## 実装済み内容

全 12 Issue を TDD で実装完了。`just test && just build && just lint` がすべて通る状態。

### コミット履歴

| コミット | 内容 |
|---|---|
| `a3895a0` | feat: #0002 core domain & ports |
| `6b4ef99` | fix: #0002 use any instead of interface{} |
| `666b51c` | feat: #0003 usecase approve/deny orchestration |
| `ae487a8` | feat: #0004 slack http handler adapter |
| `abac6d1` | feat: #0005 cli adapter (cobra) |
| `3080ae9` | feat: #0006 gcp controller adapter |
| `ca814e0` | feat: #0007 slack notifier adapter (stdout fallback) |
| `d5d82ad` | feat: #0008 auth checker adapter |
| `871b351` | refactor: #0008 simplify IsAuthorized with slices.Contains |
| `20e21bc` | feat: #0009 cmd/server http server with wiring |
| `1cbdb58` | feat: #0010 opentofu infrastructure |
| `58f7def` | feat: #0011 cloud build pipeline |
| `084e301` | feat: #0012 block kit templates |
| `475f57e` | feat: wire cmd/runops cli entrypoint |

### ディレクトリ構成

```
runops-gateway/
├── cmd/
│   ├── server/main.go        # HTTP サーバー（Slack Webhook 受信）
│   └── runops/main.go        # CLI ツール
├── internal/
│   ├── core/
│   │   ├── domain/domain.go  # ResourceType, ApprovalRequest
│   │   └── port/port.go      # RunOpsUseCase, GCPController, Notifier, AuthChecker
│   ├── usecase/runops.go     # ApproveAction/DenyAction オーケストレーション
│   └── adapter/
│       ├── input/
│       │   ├── slack/        # HTTP Handler + HMAC 署名検証
│       │   └── cli/          # Cobra コマンド (approve/deny)
│       └── output/
│           ├── gcp/          # Cloud Run + Cloud SQL API
│           ├── slack/        # response_url Notifier + Block Kit テンプレート
│           └── auth/         # EnvAuthChecker (allowlist + expiry)
├── tofu/                     # GCP インフラ (Cloud Run, IAM, Secret Manager)
├── cloudbuild.yaml           # CI/CD パイプライン
├── Dockerfile                # multi-stage build (distroless)
└── justfile                  # タスクランナー
```

## 未実装・今後の課題

### 高優先度

1. **`cmd/runops` のテスト** — `cmd/runops/main.go` にテストなし（`no test files`）。統合テストを追加推奨
2. **Block Kit テンプレートの画像 URL** — 現在 `placehold.co` の仮 URL を使用。GCS に本番用画像をホストして差し替えが必要
3. **Cloud SQL インスタンス名の設定** — `TriggerBackup` に渡す `instanceName` が `req.ResourceName` から来ているが、実際の運用では Cloud SQL インスタンス名と Cloud Run ジョブ名は別物の可能性あり。設定方法の確立が必要

### 中優先度

4. **Worker Pool 対応** — `GCPController` に `UpdateWorkerPool` メソッドが未実装（UseCase 側は stub のまま）
5. **Slack `chat.update` API 対応** — CLI 実行時に既存 Slack メッセージを更新する `SlackAPINotifier` が未実装（ADR 0006）。現在は stdout のみ
6. **状態管理 (Firestore/Redis)** — 二重実行防止のための承認状態永続化が未実装（ADR 0006 の前提）
7. **自動ロールバック** — Cloud Monitoring 連携による `5xx` 閾値超過時の自動ロールバックが未実装

### 低優先度

8. **Four-Eyes Principle** — コミット者と承認者の同一人物チェックが未実装
9. **`codex` によるプランレビュー** — 大きな変更前のレビューフローを CI に組み込む
10. **`tests/runn/`** — シナリオベースの E2E テストが空

## デプロイ手順

### 初回セットアップ

```bash
# 1. Secret Manager にシークレットを登録
gcloud secrets versions add slack-signing-secret --data-file=<(echo -n "YOUR_SLACK_SIGNING_SECRET")

# 2. OpenTofu でインフラを構築
cd tofu
tofu init
tofu apply -var="project_id=YOUR_PROJECT" -var="image=PLACEHOLDER" -var="allowed_slack_users=U0123ABCD"

# 3. Slack App の設定
#    - Interactivity Request URL を runops-gateway の URL に設定
#    - `actions` スコープを付与
```

### 通常デプロイ

```bash
# Cloud Build をトリガー（GitHub push で自動実行される）
gcloud builds submit --config=cloudbuild.yaml \
  --substitutions="_SERVICE_NAME=frontend-service,_REGION=asia-northeast1"
```

### CLI での緊急操作（Slack ダウン時）

```bash
export GOOGLE_CLOUD_PROJECT=your-project
export ALLOWED_SLACK_USERS=your-email@example.com
export RUNOPS_APPROVER_ID=your-email@example.com

# カナリアリリース
runops approve service frontend-service --action=canary_10 --target=REVISION_NAME --no-slack

# DB マイグレーション
runops approve job db-migrate-job --action=migrate_apply --no-slack

# 拒否
runops deny service frontend-service --no-slack
```

## 既知の技術的負債

- `internal/usecase/runops.go` の `ShiftTraffic` に渡す `percent` が action 文字列（`"canary_10"` → `10`）のパースに依存しており、脆弱。`domain.Action` を構造体化することを推奨
- `cloudbuild.yaml` の `notify-slack` ステップで `python3` を使って JSON エスケープしているが、`jq` に統一する方が安全
