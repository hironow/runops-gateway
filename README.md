# runops-gateway

Slack ChatOps + AgentOps gateway for GCP。 管理対象アプリの CI/CD パイプラインが新リビジョンを deploy した後、 Slack のボタンでカナリアリリース / Cloud SQL マイグレーション / 5 本柱 (paintress / amadeus / sightjack / dominator) への D-Mail dispatch を安全に実行する。

```
[ 管理対象アプリの CI/CD (Cloud Build 等) ]
    |  1. イメージビルド & deploy (traffic 0%)
    |  2. Slack に Block Kit ボタンを通知
    v
[ Slack ワークスペース ]                       承認者がボタンをクリック
    v
[ runops-gateway (Cloud Run) ]  ← このリポジトリ
    |  署名検証 → 認可 → 非同期実行
    v
[ GCP (Cloud Run / Cloud SQL) ] / [ workspace VM (5 本柱 + dmail container) ]
```

## 対応オペレーション

| リソース | アクション | 内容 |
|---|---|---|
| `service` | `canary_N` | Cloud Run Service のトラフィックを N% へ切り替え |
| `job` | `migrate_apply` | Cloud SQL バックアップ取得 → Cloud Run Jobs でマイグレーション実行 |
| `worker-pool` | `canary_N` | Cloud Run Worker Pool のインスタンス割り当てを N% へ切り替え |
| 5 本柱 dispatch | `/runops <request>` | Slack で `/runops paintress ...` を投げて 5 本柱に D-Mail を発行。 Block Kit 確認 → Approve で実行 |
| HIGH severity convergence | 4-eyes approval | `amadeus` の HIGH severity convergence は **元 dispatch 発行者以外** が Approve を押すまで実行されない (ADR 0019) |

## 状態 (production)

最新の deploy 状況・進行中の作業・受入基準は [docs/handover.md](docs/handover.md) に集約する (= session-level の SoT)。 残作業の cross-repo タスクは [docs/issues/README.md](docs/issues/README.md)。

## ドキュメント

| 目的 | 文書 |
|---|---|
| **何のサービスか / 設計意図** | [docs/intent.md](docs/intent.md) |
| **アーキテクチャ + ディレクトリ構成** | [docs/architecture.md](docs/architecture.md) |
| **セットアップ + 更新 deploy** | [docs/setup.md](docs/setup.md) |
| **runops-gateway 自身の env vars** | [docs/runops-gateway-env-vars.md](docs/runops-gateway-env-vars.md) |
| **管理対象アプリの env 管理方針** | [docs/env-vars-and-config.md](docs/env-vars-and-config.md) |
| **Slack App セットアップ** | [docs/slack-setup.md](docs/slack-setup.md) |
| **ローカル動作確認** | [docs/local-verification.md](docs/local-verification.md) |
| **管理対象アプリ deploy guide (同一 GCP project)** | [docs/guide-single-project.md](docs/guide-single-project.md) |
| **同 (gateway と app が別 project / 1:1)** | [docs/guide-two-projects.md](docs/guide-two-projects.md) |
| **同 (gateway 1 + app 複数 / 1:N)** | [docs/guide-multi-project.md](docs/guide-multi-project.md) |
| **DLQ alert triage** | [docs/runbooks/dlq.md](docs/runbooks/dlq.md) |
| **ADR (決定の history)** | [docs/adr/](docs/adr/) |
| **Issue tracker (未着手 work)** | [docs/issues/README.md](docs/issues/README.md) |
| **設計判断の調査ノート** | [experiments/README.md](experiments/README.md) |
| **引継ぎ (現セッション)** | [docs/handover.md](docs/handover.md) |
| **AI agent 向け project 規約** | [CLAUDE.md](CLAUDE.md) |

## 開発 quickstart

```bash
just                      # task list
just test                 # go test ./...
just test-integration     # testcontainers integration tests (Docker daemon が必要、ADR 0041)
just lint                 # go vet + golangci-lint + semgrep + tofu test
just check-all            # CI 同等 gate (prek hooks + check + test)

# 管理対象アプリ用 ファイルのコピー (init-app)
just init-app /path/to/your-app your-project your-service your-migrate-job
```

local の emulator / trace backend は本リポでは起動しない。テストは testcontainers
が内製する (ADR 0041)。手動 e2e smoke や trace 確認は **dotfiles** の共有スタックを
使う (ADR 0042): `just emu-up-only firebase-emulator` (Pub/Sub :9399 + Firestore
:8080) と `just tel-up` (OTLP :4317 → Grafana/Tempo)。詳細は
[docs/local-verification.md](docs/local-verification.md) のパターン C / E を参照。

詳細は [justfile](justfile) と [docs/setup.md](docs/setup.md) を参照。
