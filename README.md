# runops-gateway

Slack ChatOps gateway for GCP operations.

CI/CD パイプライン（Cloud Build）が新しいリビジョンをデプロイした後、
Slack のボタンを押すだけでカナリアリリースや DB マイグレーションを安全に実行できる。

## 概要

```
Cloud Build
    │  デプロイ完了 → Slack に Block Kit ボタンを通知
    ▼
Slack ボタン押下
    │  POST /slack/interactive
    ▼
runops-gateway (Cloud Run)
    │  署名検証 → 認可 → 非同期実行
    ▼
GCP (Cloud Run / Cloud SQL)
```

対応オペレーション:

| リソース | アクション | 内容 |
|---|---|---|
| `service` | `canary_N` | Cloud Run Service のトラフィックを N% へ切り替え |
| `job` | `migrate_apply` | Cloud SQL バックアップ取得 → Cloud Run Jobs でマイグレーション実行 |
| `worker-pool` | `canary_N` | Cloud Run Worker Pool のインスタンス割り当てを N% へ切り替え |

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
├── tofu/               # GCP インフラ定義 (OpenTofu)
├── tests/runn/         # シナリオテスト (runn)
├── cloudbuild.yaml     # Cloud Build パイプライン
├── Dockerfile          # multi-stage build (distroless)
└── justfile            # タスクランナー
```

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

## CLI 使用例

```bash
export GOOGLE_CLOUD_PROJECT=my-project

# カナリアリリース (10%)
runops approve service frontend-service \
  --action=canary_10 --target=frontend-service-v2 --no-slack

# DB マイグレーション
runops approve job db-migrate-job --action=migrate_apply --no-slack

# Worker Pool インスタンス割り当て切り替え
runops approve worker-pool batch-pool \
  --action=canary_20 --target=batch-pool-v2 --no-slack

# 拒否
runops deny service frontend-service --no-slack
```

`--no-slack` を指定すると Slack 通知なしで stdout へ出力する（Slack ダウン時の緊急操作用）。

## デプロイ

```bash
# 初回: OpenTofu でインフラを構築
cd tofu
tofu init
tofu apply \
  -var="project_id=YOUR_PROJECT" \
  -var="image=PLACEHOLDER" \
  -var="allowed_slack_users=U0123ABCD"

# 通常デプロイ: Cloud Build をトリガー
gcloud builds submit --config=cloudbuild.yaml \
  --substitutions="_SERVICE_NAME=frontend-service,_REGION=asia-northeast1"
```

Slack App の Interactivity & Shortcuts で Request URL を Cloud Run のエンドポイント
`https://<SERVICE_URL>/slack/interactive` に設定する。

## シナリオテスト

`tests/runn/` に runn シナリオが4本ある。`SLACK_SIGNING_SECRET=test-secret` でサーバーを起動して実行する。

```bash
# ヘルスチェックのみ確認
runn run tests/runn/healthz.yaml --var endpoint:http://localhost:8080

# 全シナリオ実行
SLACK_SIGNING_SECRET=test-secret PORT=8080 just run &
just test-runn
```

## ドキュメント

- [`docs/intent.md`](docs/intent.md) — 設計意図・アーキテクチャ詳細
- [`docs/adr/`](docs/adr/) — Architecture Decision Records (0001–0007)
- [`docs/handover.md`](docs/handover.md) — 実装状況・デプロイ手順
