# Architecture / アーキテクチャ

runops-gateway は Ports and Adapters (Hexagonal Architecture) を採用する。 コアドメイン (`internal/core/`) とユースケース (`internal/usecase/`) は外部依存ゼロで、 GCP / Slack / Pub/Sub / phonewave / OTel の各依存は `internal/adapter/` の inbound (driving) / outbound (driven) アダプタを通じてのみ接続する。

## 全体図

```
                                                    [ workspace VM ]
                                                      host-OS systemd:
cmd/server                              cmd/runops      - dmail-receiver  (docker run)
    |  HTTP                                 |  CLI      - dmail-emitter   (docker run)
    |  /slack/{command,interactive}         |          (ADR 0023)
    +-------- [ UseCase ] -------+----------+              ^
                  |                                        |
                  | (RunOps + Dispatch + DispatchResult)   | Pub/Sub
                  v                                        v
       +------+------+------+------+------+        +-------------------+
       | GCP  Slack Auth State Pubsub Phonewave|   | dmail-inbound     |
       +------+------+------+------+------+    +-->| dmail-outbound    |
                                                   | + DLQ * 2         |
                  ^                                +-------------------+
                  |       OTLP gRPC (ADR 0020/0042)
                  +------> dotfiles tel collector :4317 (local) / telemetry.googleapis.com (prod)
```

Legend / 凡例:

- UseCase: コアユースケース層 (`RunOpsService` / `DispatchService` / `DispatchResultHandler`)
- Phonewave: 5 本柱への file 受け渡し (atomic write + fsnotify)
- Pubsub: Cloud Pub/Sub bridge (ADR 0013/0018、 `EnableOpenTelemetryTracing` 有効)
- OTLP gRPC: OpenTelemetry trace export (ADR 0020/0021)
- workspace VM: Coder workspace の GCE VM (per-user)。 dmail container は host-OS systemd unit から `docker run --rm` で起動 (= 5 本柱と同 VM。 ADR 0023)

## レイヤー構成

- **Driving adapters (inbound)**: Slack HTTP Handler (Slash / Interactive)、 Cobra CLI、 Pub/Sub StreamingPull (gateway 内 OutboundReceiver / dmail-receiver / dmail-emitter)
- **Driven adapters (outbound)**: GCP Controller、 Slack Notifier (response_url + `chat.postMessage` fallback / ApprovalRequester)、 EnvAuthChecker、 MemoryStore + ConsumedTokenStore、 Pub/Sub Publisher、 OutboxWriter (phonewave)
- **Cross-cutting**: `internal/adapter/observability` (OTel TracerProvider + ADC + Sampler)

## ディレクトリ構成

```
runops-gateway/
├── cmd/
│   ├── server/             # HTTP サーバー (Slack /slack/{command,interactive} 受信)
│   ├── runops/             # CLI ツール (Cobra)
│   ├── dmail-receiver/     # workspace VM 上の docker run: Pub/Sub -> phonewave outbox (ADR 0023)
│   └── dmail-emitter/      # workspace VM 上の docker run: 5 本柱 archive watch -> Pub/Sub (ADR 0023)
├── internal/
│   ├── core/
│   │   ├── domain/         # ResourceType, Action, ApprovalRequest, DMail (外部依存なし)
│   │   └── port/           # インターフェース定義 (Dispatcher / DMailPublisher / Notifier 等)
│   ├── usecase/            # ApproveAction / DenyAction / DispatchAgentTask / DispatchResultHandler
│   └── adapter/
│       ├── input/
│       │   ├── slack/      # HTTP Handler + HMAC 署名検証 + dispatch_/approval_ action 分岐
│       │   ├── cli/        # Cobra コマンド (approve / deny)
│       │   ├── pubsub/     # Pub/Sub Receiver (dmail-receiver 用) / OutboundReceiver (gateway 内)
│       │   └── phonewave/  # fsnotify Watcher + Emitter (dmail-emitter 用)
│       ├── output/
│       │   ├── gcp/        # Cloud Run + Cloud SQL API クライアント
│       │   ├── slack/      # FallbackNotifier (response_url + chat.postMessage) + ApprovalRequester
│       │   ├── auth/       # EnvAuthChecker (allowlist + 有効期限)
│       │   ├── state/      # MemoryStore + ConsumedTokenStore (4-eyes one-time)
│       │   ├── dispatcher/ # StubDispatcher / PubsubDispatcher (DISPATCHER_BACKEND で切替)
│       │   ├── pubsub/     # Pub/Sub Publisher (EnableOpenTelemetryTracing on)
│       │   └── phonewave/  # OutboxWriter (atomic temp+rename)
│       └── observability/  # OTel TracerProvider + ADC + Sampler (ADR 0020)
├── scripts/
│   ├── notify-slack.sh     # Cloud Build から呼ばれる Slack 通知スクリプト
│   ├── init-app.sh / check-app.sh  # 管理対象アプリの初期化 / 検証
│   └── smoke/              # Pub/Sub 手動 smoke スクリプト (init-pubsub.sh は ADR 0041 で廃止)
├── tofu/                   # GCP インフラ定義 (OpenTofu) ← gateway 自体のインフラ
│   ├── pubsub.tf           # dmail-* topics + DLQ
│   ├── subscriptions.tf    # working subscriptions + DLQ pull subscriptions
│   ├── iam_pubsub.tf       # chatops_sa + workspace VM SA への IAM
│   ├── telemetry.tf        # Cloud Trace API + tracesWriter
│   ├── monitoring.tf       # DLQ forwarding alert
│   └── tests/              # `tofu test` (ADR 0024) — variable validation の動的検証
├── docs/
│   ├── adr/                # Architecture Decision Records (immutable history)
│   ├── runbooks/           # 運用 runbook (例 dlq.md)
│   └── ...
├── experiments/            # 設計判断の調査ノート (OTel / CloudEvents / DLQ / IaC test 等)
├── tests/
│   ├── integration/        # testcontainers で firebase emulator を起動 (build tag: integration、 ADR 0041)
│   └── runn/               # シナリオテスト (runn)
├── cloudbuild.yaml         # 管理対象アプリ用 CI/CD パイプラインのテンプレート
├── Dockerfile              # multi-stage build (distroless、 runops-gateway 本体)
├── docker/                 # multi-stage Dockerfile (dmail-receiver / dmail-emitter、 ADR 0023)
└── justfile                # タスクランナー (test / test-integration / lint / test-iac 等)
```

## 関連 ADR

- ADR 0001 (CI/CD と state change の分離)
- ADR 0002 (Slack 3 秒応答ルール、 async pattern)
- ADR 0005 (Ports and Adapters 採用)
- ADR 0013 (Pub/Sub bridge for outbox)
- ADR 0015 (dmail-receiver / dmail-emitter は本リポで管理、 deploy は dotfiles 側)
- ADR 0017 (Slack Bot Token fallback)
- ADR 0018 (dmail-outbound pull subscription)
- ADR 0020 / 0021 (OTel direct OTLP + Pub/Sub trace context)
- ADR 0023 (dmail daemon を OCI image + workspace VM 配置)
- ADR 0024 (IaC test split: tofu test / pytest)
- ADR 0041 (testcontainers-only integration tests)
- ADR 0042 (local trace backend = 共有 dotfiles tel スタック、 自前 Jaeger 廃止)

詳細は [`docs/adr/`](adr/) 配下の各 ADR を参照。
