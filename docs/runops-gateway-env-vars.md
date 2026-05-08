# runops-gateway 自身の環境変数

runops-gateway を構成する 4 つの binary (`cmd/server` / `cmd/runops` / `cmd/dmail-receiver` / `cmd/dmail-emitter`) が読む env を一覧する。

> **本ドキュメントの対象**: runops-gateway **自身** の env (= Cloud Run service / dmail container / CLI が読む env)。
> runops-gateway が **管理対象とするアプリ** の env 管理方針は [`docs/env-vars-and-config.md`](env-vars-and-config.md) を参照。

## サーバー (`cmd/server`)

| 変数 | 必須 | デフォルト | 説明 |
|---|---|---|---|
| `SLACK_SIGNING_SECRET` | ✓ | — | Slack App の Signing Secret |
| `PORT` | — | `8080` | HTTP ポート |
| `ALLOWED_SLACK_USERS` | — | `""` (全拒否) | 承認許可ユーザーの Slack ID (カンマ区切り) |
| `BUTTON_EXPIRY_SECONDS` | — | `7200` | ボタン有効期限 (秒) |
| `SLACK_BOT_TOKEN` | △ | — | `xoxb-...`、 ADR 0017 (FallbackNotifier) と ADR 0019 (4-eyes approval) で必須。 空なら fallback 無効 |
| `SLACK_DEFAULT_CHANNEL_ID` | — | `""` | response_url 切れ時の `chat.postMessage` 既定チャンネル |
| `DISPATCHER_BACKEND` | — | `stub` | `stub` (Phase 1 Slack 内完結) / `pubsub` (Phase 2a 以降、 5 本柱と Pub/Sub bridge) |
| `PUBSUB_PROJECT_ID` | △ | — | `DISPATCHER_BACKEND=pubsub` 時に必須 |
| `PUBSUB_DMAIL_INBOUND_TOPIC` | △ | — | 同上 |
| `PUBSUB_DMAIL_OUTBOUND_SUB` | — | `""` | 設定すると Phase 3 OutboundReceiver を gateway 内 goroutine で起動 (ADR 0018) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | `""` | 空なら no-op TracerProvider。 `http://localhost:4317` (Jaeger v2 local) / `telemetry.googleapis.com:443` (prod Cloud Trace) |
| `OTEL_SERVICE_NAME` | — | `runops-gateway` | resource attribute `service.name` |
| `OTEL_SERVICE_VERSION` | — | — | resource attribute `service.version` (build pipeline で `-ldflags` 経由) |
| `OTEL_TRACES_SAMPLER` | — | `parentbased_always_on` | `parentbased_always_on` / `parentbased_traceidratio` |
| `OTEL_TRACES_SAMPLER_ARG` | — | — | ratio 値 (例 `0.1`) |
| `OTEL_BSP_SCHEDULE_DELAY` | — | — | BatchSpanProcessor flush 間隔 (ms)。 Cloud Run の SIGTERM ロス対策で `2000` 推奨 |
| `GOOGLE_CLOUD_PROJECT` | △ | — | Cloud Run が自動セット。 OTel resource attribute `gcp.project_id` に転用される (PR #21)。 **Cloud Trace OTLP 必須** で、 空だと `InvalidArgument` で span が reject される。 Local Jaeger では空で OK |

### Token broker (#0007)

`POST /broker/token` mount を opt-in 化する env。 `BROKER_AUDIENCE` が空または `ProjectRegistry` 未配線時はマウントせず、 既存 Slack / admin endpoint のみで動作する (Phase 3b-3b-2)。

| 変数 | 必須 | デフォルト | 説明 |
|---|---|---|---|
| `BROKER_AUDIENCE` | ✓ (broker 有効化時) | — | 全 caller の identity token の `aud` claim にピンする broker URL。 設定されない限り broker は登録されない |
| `BROKER_GATEWAY_SERVICE_SAS` | ✓ (broker 有効化時) | — | gateway-service caller の SA email allowlist (CSV)。 空 CSV は ctor 拒否 (= 設定漏れで startup fail) |
| `BROKER_WORKSPACE_DAEMON_SAS` | ✓ (broker 有効化時) | — | workspace-daemon caller の SA email allowlist (CSV)。 同上、 空は ctor 拒否 |
| `BROKER_OPERATOR_EMAILS` | — | — | human-operator caller の email allowlist (CSV)。 空 = 全 verified Google ID 受容 (bootstrap config のみ、 production は必須) |
| `GITHUB_APP_ID` | ✓ (broker 有効化時) | — | GitHub App の数値 ID (positive int64) |
| `GITHUB_APP_PRIVATE_KEY_PATH` | △ | — | dev / staging で PEM 鍵をマウントするファイルパス。 `GITHUB_APP_PRIVATE_KEY_SECRET_NAME` と排他 |
| `GITHUB_APP_PRIVATE_KEY_SECRET_NAME` | △ | — | production で Secret Manager から fetch する resource name (`projects/<p>/secrets/<s>/versions/<v>`)。 `GITHUB_APP_PRIVATE_KEY_PATH` と排他 |
| `BROKER_USE_FIRESTORE_REGISTRY` | — | `false` | `true` / `1` で Firestore-backed agent session registry を選択 (Cloud Run multi-instance 安全)。 `false` (default) で in-memory registry。 `true` 時は `GOOGLE_CLOUD_PROJECT` 必須 |
| `GOOGLE_STS_ISSUER` | — | `https://accounts.google.com` | 4 verifier すべてが pin する issuer URL |
| `GOOGLE_JWKS_URL` | — | `https://www.googleapis.com/oauth2/v3/certs` | JWKsVerifier (Phase 2d-2b) が公開鍵を fetch する URL |

#### Production rollout 手順

ADR 0032 の grant matrix と plan v8 §5.5 の token leakage policy に沿って、 4 step:

```bash
# 1. Secret Manager + Cloud Run IAM binding を作成 (Phase 4-2 IaC)
cd tofu && tofu apply

# 2. GitHub App private key を out-of-band で upload (Terraform state 外)
gcloud secrets versions add github-app-private-key --data-file=/path/to/github-app.pem

# 3. Cloud Run service env vars を上記表に従ってセット
gcloud run services update runops-gateway \
  --update-env-vars=BROKER_AUDIENCE=https://broker.example.com,BROKER_GATEWAY_SERVICE_SAS=...,BROKER_WORKSPACE_DAEMON_SAS=...,GITHUB_APP_ID=12345,GITHUB_APP_PRIVATE_KEY_SECRET_NAME=projects/proj/secrets/github-app-private-key/versions/latest,BROKER_USE_FIRESTORE_REGISTRY=true

# 4. Re-deploy。 構造化ログ "token broker registered (#0007)" が出れば activation 成功
```

ロールバック: 上記 env を全て unset すると broker は disable され、 既存 Slack / admin endpoint は影響を受けない (= opt-in pattern)。

## dmail-receiver / dmail-emitter

ADR 0023 の workspace VM placement で deploy される。 dotfiles 側 `exe/coder/templates/dotfiles-devcontainer/main.tf` の systemd unit が `Environment=...` で attach する。

| 変数 | 必須 | 説明 |
|---|---|---|
| `PUBSUB_PROJECT_ID` | ✓ | GCP project (emulator 時は `runops-local`) |
| `PUBSUB_DMAIL_INBOUND_SUB` (receiver) / `PUBSUB_DMAIL_OUTBOUND_TOPIC` (emitter) | ✓ | subscription / topic 名 |
| `PHONEWAVE_OUTBOX_DIR` (receiver) / `PHONEWAVE_ARCHIVE_DIRS` (emitter) | ✓ | watch / write 対象のローカル dir (emitter は `:` 区切り複数可) |
| `PUBSUB_EMULATOR_HOST` | — | `localhost:9399` で local emulator に向ける |
| `OTEL_*` | — | サーバーと同じ env で OTel 配線 (`OTEL_SERVICE_NAME` の default は `dmail-receiver` / `dmail-emitter`) |
| `GOOGLE_CLOUD_PROJECT` | △ | OTel resource attribute `gcp.project_id` に転用 (PR #21)。 Cloud Trace OTLP 利用時必須 |

## CLI (`cmd/runops`)

| 変数 | 必須 | 説明 |
|---|---|---|
| `ALLOWED_SLACK_USERS` | — | 承認許可ユーザー (CLI ではメールアドレスを使用) |

プロジェクト ID とリージョンは `--project` / `--location` フラグで指定する。

## 関連

- [docs/setup.md](setup.md) — env をセットする tofu var / GitHub variable の流れ
- [docs/env-vars-and-config.md](env-vars-and-config.md) — **管理対象アプリ側** の env 管理方針 (= 本 doc とは別ターゲット)
- ADR 0020 / 0021 (OTel direct OTLP + Pub/Sub trace context)
- ADR 0023 (dmail container を docker run + systemd unit で deploy、 `Environment=...` の値が固定される箇所)
