# Issue 0001: exe-coder VM に dmail-receiver / dmail-emitter を systemd unit として deploy

**Repo:** `hironow/dotfiles` (本リポ範囲外)
**Status:** 📝 未着手
**Blocker for:** Issue 0003 (Phase 3 outbound 実運用化), Issue 0002 (5本柱 frontmatter trace 連結)

## Why

本リポ (runops-gateway) は `cmd/dmail-receiver` と `cmd/dmail-emitter` の binary を提供するが、**実 deploy は別リポ管轄**。現状 production の Pub/Sub `dmail-inbound-receiver` subscription には backlog が積み上がる一方で消費者なし (`docs/handover.md` ハマりどころ集 8-prepre 参照)。

このまま 14 日経つと message が retention で消失する。

## What

`hironow/dotfiles` の exe-coder VM 起動スクリプト (例: `tofu/exe/startup-script.tpl` 等、現状の構造に合わせる) に以下を追加:

1. **dmail-receiver の systemd unit**

    ```ini
    [Unit]
    Description=runops-gateway dmail-receiver (Pub/Sub -> phonewave outbox)
    After=network-online.target
    Wants=network-online.target

    [Service]
    Type=simple
    User=exe-coder
    Environment=PUBSUB_PROJECT_ID=gen-ai-hironow
    Environment=PUBSUB_DMAIL_INBOUND_SUB=dmail-inbound-receiver
    Environment=PHONEWAVE_OUTBOX_DIR=/var/lib/phonewave/outbox
    Environment=GOOGLE_CLOUD_PROJECT=gen-ai-hironow
    Environment=OTEL_EXPORTER_OTLP_ENDPOINT=telemetry.googleapis.com:443
    Environment=OTEL_EXPORTER_OTLP_PROTOCOL=grpc
    Environment=OTEL_SERVICE_NAME=dmail-receiver
    Environment=OTEL_TRACES_SAMPLER=parentbased_traceidratio
    Environment=OTEL_TRACES_SAMPLER_ARG=1.0
    ExecStart=/usr/local/bin/dmail-receiver
    Restart=on-failure
    RestartSec=10s

    [Install]
    WantedBy=multi-user.target
    ```

2. **dmail-emitter の systemd unit** (同様、env は `PUBSUB_DMAIL_OUTBOUND_TOPIC=dmail-outbound`、`PHONEWAVE_ARCHIVE_DIRS=/Users/nino/tap/sightjack/.siren/archive:/Users/nino/tap/paintress/.expedition/archive:/Users/nino/tap/amadeus/.gate/archive:/Users/nino/tap/dominator/.pass/archive` 等)

3. **binary build pipeline**: `runops-gateway` リポの `cmd/dmail-{receiver,emitter}` を Go build → exe-coder VM に SSH 経由で配布、または GitHub Actions artifact pull

## Service Account の前提条件 (本リポで apply 済)

- `exe-coder@gen-ai-hironow.iam.gserviceaccount.com` には以下が grant 済み (本リポ `tofu/iam_pubsub.tf` + `tofu/telemetry.tf`):
  - `roles/pubsub.subscriber` on `dmail-inbound-receiver`
  - `roles/pubsub.publisher` on `dmail-outbound` topic
  - `roles/cloudtrace.agent` (project-level)
- VM の ADC が `exe-coder` SA を impersonate していれば認証 OK

## 受入基準

1. `systemctl status dmail-receiver` が `active (running)` で表示される
2. `dmail-inbound-receiver` の backlog が消化される (`gcloud pubsub subscriptions describe dmail-inbound-receiver` で `numUndeliveredMessages: 0`)
3. phonewave outbox dir に `.md` ファイルが atomic write される (5本柱が consume できる形式)
4. Cloud Trace に `dmail-receiver` service の span が出る (gcp.project_id 修正済、本リポ PR #21)
5. `docs/runbooks/dlq.md` の "First-time setup" の seek を 1 度実行して過去 backlog を取り戻す

## 関連 ADR / docs (本リポ側)

- ADR 0013 (Pub/Sub bridge)
- ADR 0015 (dmail-receiver / dmail-emitter は本リポで管理、deploy は別リポ)
- ADR 0018 (outbound pull subscription)
- ADR 0020 / 0021 (OTel direct OTLP + Pub/Sub trace)
- `docs/handover.md` ハマりどころ集 8-prepre (DLQ は consumer 必須)
- `docs/runbooks/dlq.md`
