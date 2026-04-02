# Handover Document

**Last updated:** 2026-04-02

## 実装済み内容

全 16 Issue を TDD で実装完了。`just test && just build && just lint` がすべて通る状態。

### コミット履歴

| コミット | 内容 |
|---|---|
| `a3895a0` | feat: #0002 core domain & ports |
| `6b4ef99` | fix: #0002 use any instead of interface{} (Go 1.18+) |
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
| `136c1d4` | feat: #0013 domain.ParseAction (action 文字列パース) |
| `3cf83a2` | feat: #0015 in-memory StateStore (二重実行防止) |
| `f7306e0` | fix: #0014 UpdateWorkerPool in GCPController |
| `29565d2` | feat: #0016 runn scenario tests |
| `985ac60` | refactor: rename opentofu/ to tofu/ |
| `bf9783e` | fix: cloudbuild.yaml python3 → jq |
| `0739ecd` | test: edge case tests across all packages |

### ディレクトリ構成

```
runops-gateway/
├── cmd/
│   ├── server/main.go        # HTTP サーバー（Slack Webhook 受信）
│   └── runops/main.go        # CLI ツール
├── internal/
│   ├── core/
│   │   ├── domain/
│   │   │   └── domain.go     # ResourceType, Action (ParseAction), ApprovalRequest
│   │   └── port/
│   │       └── port.go       # RunOpsUseCase, GCPController, Notifier, AuthChecker, StateStore
│   ├── usecase/
│   │   └── runops.go         # ApproveAction/DenyAction オーケストレーション
│   └── adapter/
│       ├── input/
│       │   ├── slack/        # HTTP Handler + HMAC 署名検証
│       │   └── cli/          # Cobra コマンド (approve/deny)
│       └── output/
│           ├── gcp/          # Cloud Run (Service/Job/WorkerPool) + Cloud SQL API
│           ├── slack/        # response_url Notifier + Block Kit テンプレート
│           ├── auth/         # EnvAuthChecker (allowlist + 有効期限)
│           └── state/        # MemoryStore (TryLock/Release)
├── tests/
│   └── runn/                 # シナリオテスト (healthz/approve/deny/invalid_sig)
├── tofu/                     # GCP インフラ (Cloud Run, IAM, Secret Manager)
├── cloudbuild.yaml           # CI/CD パイプライン
├── Dockerfile                # multi-stage build (distroless)
└── justfile                  # タスクランナー
```

### テスト状況

`go test -race ./...` が全パッケージで通過。

| パッケージ | テストケース数 |
|---|---|
| `internal/core/domain` | 13 |
| `internal/core/port` | 3 |
| `internal/usecase` | 18 |
| `internal/adapter/input/slack` | 13 |
| `internal/adapter/input/cli` | 7 |
| `internal/adapter/output/gcp` | 7 |
| `internal/adapter/output/slack` | 11 |
| `internal/adapter/output/auth` | 17 |
| `internal/adapter/output/state` | 9 |
| `cmd/server` | 4 |

## 今後の課題

### 高優先度

1. **Block Kit テンプレートの画像 URL** — `buildkit.go` の `EnvironmentImageURL` が `placehold.co` の仮 URL を使用。GCS に本番用画像をホストして差し替えが必要
2. **Cloud SQL インスタンス名の設定** — `approveJob` で `TriggerBackup(ctx, req.ResourceName)` としているが、運用では Cloud SQL インスタンス名と Cloud Run ジョブ名が異なる場合がある。`ApprovalRequest` にフィールド追加またはマッピング設定の導入を検討

### 中優先度

3. **Slack `chat.update` API 対応** — CLI 実行時に既存 Slack メッセージを更新する `SlackAPINotifier` が未実装（ADR 0006）。現在は `--no-slack` 時に stdout のみ
4. **状態管理の永続化** — 現在の `MemoryStore` はプロセス再起動でリセットされる。Firestore または Redis を `StateStore` インターフェースの実装として差し替えることで対応可能
5. **自動ロールバック** — Cloud Monitoring 連携による `5xx` 閾値超過時の自動ロールバックが未実装

### 低優先度

6. **Four-Eyes Principle** — コミット者と承認者の同一人物チェックが未実装
7. **`cmd/runops` の統合テスト** — `cmd/runops/main.go` にテストなし（`no test files`）

## デプロイ手順

### 初回セットアップ

```bash
# 1. Secret Manager にシークレットを登録
gcloud secrets versions add slack-signing-secret \
  --data-file=<(echo -n "YOUR_SLACK_SIGNING_SECRET")

# 2. OpenTofu でインフラを構築
cd tofu
tofu init
tofu apply \
  -var="project_id=YOUR_PROJECT" \
  -var="image=PLACEHOLDER" \
  -var="allowed_slack_users=U0123ABCD"

# 3. Slack App の設定
#    - Interactivity & Shortcuts → Request URL を Cloud Run の URL に設定
#    - 例: https://runops-gateway-xxxx-an.a.run.app/slack/interactive
```

### 通常デプロイ

```bash
# Cloud Build をトリガー（GitHub push で自動実行）
gcloud builds submit --config=cloudbuild.yaml \
  --substitutions="_SERVICE_NAME=frontend-service,_REGION=asia-northeast1"
```

### CLI での緊急操作（Slack ダウン時）

```bash
export GOOGLE_CLOUD_PROJECT=your-project
export ALLOWED_SLACK_USERS=your-email@example.com

# カナリアリリース (10%)
runops approve service frontend-service \
  --action=canary_10 --target=REVISION_NAME --no-slack

# DB マイグレーション
runops approve job db-migrate-job --action=migrate_apply --no-slack

# 拒否
runops deny service frontend-service --no-slack
```
