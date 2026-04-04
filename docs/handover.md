# Handover Document

**Last updated:** 2026-04-02

## 実装済み内容

全 16 Issue を TDD で実装完了。その後、ADR 0008〜0011 に基づく追加機能を実装。
クロスプロジェクト対応により、GCP Controller は固定の Config（ProjectID/Location）を持たず、
各操作が Slack ボタン値または CLI フラグから project/location を受け取る設計に変更済み。
`just test && just build && just lint` がすべて通る状態。

### コミット履歴（主要なもの）

| コミット | 内容 |
|---|---|
| `a3895a0` | feat: #0002 core domain & ports |
| `666b51c` | feat: #0003 usecase approve/deny orchestration |
| `ae487a8` | feat: #0004 slack http handler adapter |
| `abac6d1` | feat: #0005 cli adapter (cobra) |
| `3080ae9` | feat: #0006 gcp controller adapter |
| `ca814e0` | feat: #0007 slack notifier adapter (stdout fallback) |
| `d5d82ad` | feat: #0008 auth checker adapter |
| `20e21bc` | feat: #0009 cmd/server http server with wiring |
| `1cbdb58` | feat: #0010 opentofu infrastructure |
| `58f7def` | feat: #0011 cloud build pipeline |
| `084e301` | feat: #0012 block kit templates |
| `136c1d4` | feat: #0013 domain.ParseAction (action 文字列パース) |
| `3cf83a2` | feat: #0015 in-memory StateStore (二重実行防止) |
| `f7306e0` | fix: #0014 UpdateWorkerPool in GCPController |
| `29565d2` | feat: #0016 runn scenario tests |
| `300d609` | feat: CanarySteps, NextCanaryPercent, OfferContinuation port (ADR 0008/0009) |
| `8d1ef2d` | feat: blockkit RequireConfirm, BuildProgressMessage, OfferContinuation notifier |
| `61fe93a` | feat: approveService/Job/WorkerPool with canary progression and rollback |
| `903778b` | feat: handler actionValue CSV fields and migration_done/confirm (ADR 0009) |
| `15c593b` | feat: multi-resource atomic deployment with compensating rollback (ADR 0010) |
| `9a03c58` | feat: enforce Slack Block Kit field length limits |
| `8e8b58e` | fix: surface explicit error when button value exceeds Slack 2,000-char limit |
| `8761c04` | feat: always compress button values (gzip+base64url) (ADR 0011) |
| `4f15e0a` | refactor: extract cloudbuild Slack notification to scripts/notify-slack.sh |
| `aeda34a` | test: end-to-end test for notify-slack.sh via mock Slack server |
| `efcff4f` | ci: add GitHub Actions CI/CD workflows (ci.yaml / cd.yaml) |
| `55feb39` | ci: integrate OpenTofu into CD workflow and add GCS remote state |
| `8c743c5` | feat(tofu): complete initial setup — WIF, Artifact Registry, deployer SA |
| `8d77426` | feat(tofu): cost-optimize and fix bootstrap issues |
| `7477242` | fix(cd): correct variable names and add missing TF_VAR_ for tofu apply |
| `b71eb4f` | fix(cd): remove invalid --traffic flag from deploy-cloudrun step |

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
│       │   ├── slack/        # HTTP Handler + HMAC 署名検証 + parseActionValue (gz: 展開)
│       │   └── cli/          # Cobra コマンド (approve/deny)
│       └── output/
│           ├── gcp/          # Cloud Run (Service/Job/WorkerPool) + Cloud SQL API
│           ├── slack/        # response_url Notifier + Block Kit テンプレート + compressButtonValue
│           ├── auth/         # EnvAuthChecker (allowlist + 有効期限)
│           └── state/        # MemoryStore (TryLock/Release)
├── scripts/
│   └── notify-slack.sh       # Cloud Build から呼ばれる Slack 通知スクリプト (--dry-run 対応)
├── tests/
│   └── runn/                 # シナリオテスト (healthz/approve/deny/invalid_sig/approve_canary)
├── tofu/                     # GCP インフラ (Cloud Run, IAM, Secret Manager)
├── cloudbuild.yaml           # CI/CD パイプライン (multi-service CSV 対応)
├── Dockerfile                # multi-stage build (distroless)
└── justfile                  # タスクランナー
```

### テスト状況

`go test ./...` が全パッケージで通過。総カバレッジ **77.3%**。

| パッケージ | テスト数 | カバレッジ |
|---|---|---|
| `internal/core/domain` | 23 | 100% |
| `internal/core/port` | 2 | — |
| `internal/usecase` | 30 | 88.7% |
| `internal/adapter/input/slack` | 24 | 87.5% |
| `internal/adapter/input/cli` | 7 | 82.5% |
| `internal/adapter/output/gcp` | 7 | 58.8% |
| `internal/adapter/output/slack` | 36 | 92.9% |
| `internal/adapter/output/auth` | 17 | 94.7% |
| `internal/adapter/output/state` | 9 | 100% |
| `cmd/server` | 4 | 25.6% |

`output/gcp` が低い理由: 実際の Cloud SDK を呼ぶためユニットテストでのカバーは不適切。
`cmd/server` が低い理由: `main()` 関数は計装されない仕様。

### 実装済み主要機能

#### マルチリソース対応（ADR 0010）

- `ApprovalRequest.ResourceNames / Targets` が CSV 形式で複数リソースを保持
- `approveService` / `approveWorkerPool` が CSV を展開して逐次実行
- 途中失敗時は **補償ロールバック**（先行成功分を 0% に戻す）を実行
- `handler.go` に後方互換フォールバック（`resource_name` 単数形 → `resource_names` 複数形）

#### Slack Block Kit フィールド長制限の強制

- `maxHeaderText=150`, `maxSectionText=3000`, `maxButtonValue=2000`, `maxButtonLabel=75`
- `safeTrunc(s, max)` — rune 単位の安全な切り捨て（`…` サフィックス付き）
- `buttonValueError` — 圧縮後も 2,000 文字超の場合に専用エラーメッセージを Slack 投稿

#### ボタン値の常時 gzip 圧縮（ADR 0011）

- `compressButtonValue(s)` — `gz:` + gzip + base64url (RawURLEncoding) を**常に**適用
- `parseActionValue(s)` — `gz:` プレフィックスを検出して透過的に展開、旧 JSON も互換
- bash 側 `compress_gz()` (`scripts/notify-slack.sh`) と Go 側で同一アルゴリズム

#### Cloud Build 通知の外部化

- Slack 通知ロジックを `scripts/notify-slack.sh` に抽出
- `--dry-run` フラグで標準出力にペイロードを出力（テスト用）
- `TestNotifyScript_EndToEnd_PostToMockSlack_ButtonValuesDecodable` で bash→Go のラウンドトリップを保証

## 2026-04-04 セッション引き継ぎ

### 実施した作業

1. **クロスプロジェクト対応** — `ApprovalRequest` に `Project`/`Location` 追加、GCPController インターフェースを per-call 引数に変更、Config 構造体を廃止。ボタン value JSON に `project`/`location` を埋め込み、gateway がステートレスルーターとして 1:N のプロジェクト構成をサポート
2. **nn-makers (trade-non) を初の管理対象アプリとして設定** — WIF, IAM, Cloud Build, GHA 全て構築済み
3. **`just init-app` / `just check-app` コマンド追加** — 管理対象アプリの初期化と設定検証
4. **デプロイトポロジガイド作成** — `docs/guide-single-project.md`, `guide-two-projects.md`, `guide-multi-project.md`
5. **多数のバグ修正** — Block Kit の action_id 重複、completionBlocks のネスト、cloudbuild.yaml の bash 変数エスケープ、Cloud Run API v2 の template 必須フィールド等

### 未解決の問題（最優先）

1. **OfferContinuation が 404 を返す問題**
   - トラフィックシフト自体は成功するが、次のカナリアステップボタンを表示する `OfferContinuation` が Slack response_url への POST で 404 を返す
   - `notifier.go` にレスポンスボディのログ出力を追加済み（`5e1e8db`）だが、まだ原因未特定
   - 次のデプロイ後にログを確認して原因を特定する必要がある
   - 可能性: response_url の使用回数制限（5回）超過、Block Kit ペイロード構造の問題

2. **Slack API モックテストの構築が必要**
   - 手動での動作確認が限界を超えている
   - `httptest.NewServer` で mock Slack server を構築し、response_url への POST の全応答パターンをテスト
   - テスト対象: 200 ok, 200 invalid_blocks, 404, 5xx, UpdateMessage成功→OfferContinuation失敗のフロー
   - リトライボタン（`offerRetry`）のペイロード構造検証も含む

### リトライボタン（実装済み・未コミット → コミット済み `98f587f`）

エラー発生時に `UpdateMessage` でテキストエラーを表示する代わりに、`offerRetry` ヘルパーが `OfferContinuation` を使ってリトライボタン付きエラーメッセージを表示する。ただし OfferContinuation 自体が 404 で失敗する問題があるため、この機能の動作確認はまだ。

### IAM 学び

| 権限 | レベル | 理由 |
|---|---|---|
| `roles/run.developer` | **プロジェクトレベル** | サービスレベルだと `run.operations.get`（LRO ポーリング）が含まれない |
| `roles/iam.serviceAccountUser` | ランタイム SA 単位 | Cloud Run がサービス更新時にランタイム SA を act as する必要がある |
| `roles/artifactregistry.reader` | リポジトリ単位 | トラフィックシフト時に新リビジョンのイメージを pull する必要がある |

## 今後の課題

### 最優先

1. **OfferContinuation 404 問題の原因特定と修正** — ログ改善済み、次のテストで原因を特定

2. **Slack API モックテスト構築** — response_url への POST の全応答パターンを `httptest.NewServer` でテスト

### 高優先度

1. **Cloud SQL インスタンス名の設定** — `approveJob` で `TriggerBackup(ctx, req.Project, req.ResourceNames)` としているが、Cloud SQL インスタンス名と Cloud Run ジョブ名が異なる場合がある

### 中優先度

1. **Slack `chat.update` API 対応** — CLI 実行時に既存 Slack メッセージを更新する `SlackAPINotifier` が未実装（ADR 0006）

2. **状態管理の永続化** — `MemoryStore` はプロセス再起動でリセットされる

3. **自動ロールバック** — Cloud Monitoring 連携による自動ロールバックが未実装

### 低優先度

1. **Four-Eyes Principle** — コミット者と承認者の同一人物チェックが未実装

2. **`output/gcp` の統合テスト** — emulator またはモックサーバーでの integration テストが必要

## ローカル動作確認

詳細は [`docs/local-verification.md`](local-verification.md) を参照。

| パターン | 概要 |
|---|---|
| **A. 操作対象なし** | GCP・Slack 不要。`just test-runn` + `--dry-run` + curl で署名検証とペイロード構造を確認 |
| **B. 操作対象あり (CLI)** | `go run ./cmd/runops approve ... --no-slack` で実 GCP を操作 |
| **B. 操作対象あり (Slack E2E)** | `tailscale funnel 8080` でローカルサーバーを公開し、実 Slack ボタンから GCP 操作まで全パスを確認 |

## デプロイ手順

### runops-gateway 自体のデプロイ

`main` ブランチへの push で GitHub Actions (`cd.yaml`) が自動実行される。

```
git push origin main
  -> ci.yaml: go test / go build
  -> cd.yaml:
       check-changes: tofu/** 変更検知
       infra job    : tofu apply (tofu/ 変更時のみ)
       deploy job   : docker build & push -> deploy-cloudrun@v2
```

詳細な初回セットアップ手順は [README.md](../README.md#1-runops-gateway-自体のセットアップと更新) を参照。

### 管理対象アプリのデプロイ（Cloud Build）

管理対象アプリのリポジトリに `cloudbuild.yaml` と `scripts/notify-slack.sh` を配置して使用する。詳細は [README.md](../README.md#2-管理対象アプリのデプロイ設定) を参照。

### CLI での緊急操作（Slack ダウン時）

```bash
export ALLOWED_SLACK_USERS=your-email@example.com

# カナリアリリース (10%)
runops approve service frontend-service \
  --project=your-project --location=asia-northeast1 \
  --action=canary_10 --target=REVISION_NAME --no-slack

# 複数サービス同時カナリア
runops approve service "frontend-service,backend-service" \
  --project=your-project --location=asia-northeast1 \
  --action=canary_10 --target="frontend-v2,backend-v2" --no-slack

# DB マイグレーション
runops approve job db-migrate-job \
  --project=your-project --location=asia-northeast1 \
  --action=migrate_apply --no-slack

# 拒否
runops deny service frontend-service \
  --project=your-project --location=asia-northeast1 --no-slack
```
