# Issue 0003: Phase 3 outbound StreamingPull を実運用化する

**Repo:** `hironow/runops-gateway` (本リポ完結) + `hironow/dotfiles` (Issue 0001 完了が前提)
**Status:** 📝 未着手 (Issue 0001 Phase 3 受入基準 verify 完了待ち)
**Depends on:** Issue 0001 Phase 3 (= dmail-emitter が production workspace VM 上で稼働し、 `dmail-outbound` topic に publish が始まること)。 Issue 0001 自体は 2026-05-06 時点で Phase 1 + 2 + 3 (IAM apply / templates push / workspace 起動) 完了済、 outbox permission denied fix を出した段階 (dotfiles PR #90 / runops-gateway PR #37)。 fix merge → 受入基準 verify が完了次第 Issue 0003 へ進む

## Why

Phase 3 (ADR 0018) では gateway 内 goroutine で `dmail-outbound` subscription を StreamingPull し、5本柱から戻ってきた D-Mail (report / convergence 等) を Slack thread に reply する設計。実装は完了済 (`internal/adapter/input/pubsub/outbound_subscriber.go`、`internal/usecase/dispatch_result_handler.go`) で env (`PUBSUB_DMAIL_OUTBOUND_SUB`) 経由で有効化されるが、**現在 production の Cloud Run scaling は `min_instance_count=0`** (PR #10 で `var.cloud_run_min_instances` のデフォルト値) に保ったまま。

ADR 0018 は「StreamingPull は warm instance を要求する」ため、`min=0` のままでは cold start で gRPC stream が切れて trace gap が起きる。dmail-emitter が deploy されて outbound traffic が来始めたら、warm instance に切り替える必要。

## What

dmail-emitter が稼働 (Issue 0001) して outbound topic にpublish が始まる確認が取れたら:

```bash
gh variable set CLOUD_RUN_MIN_INSTANCES 1 --repo hironow/runops-gateway
```

を実行 → 次の release で CD が tofu apply 経由で `min_instance_count=1` を反映、Cloud Run service が常時 1 instance を維持。

`var.cloud_run_max_instances` はデフォルト 3 を維持 (本番 traffic 数十/日想定で十分)。

## 検証手順

1. dmail-emitter (Issue 0001) deploy 完了後、`gcloud pubsub subscriptions describe dmail-outbound-gateway` で `numUndeliveredMessages > 0` を確認 (5本柱が完了 D-Mail を吐いている)
2. `gh variable set CLOUD_RUN_MIN_INSTANCES 1` を設定
3. 任意の commit を main に push (ダミーでも可) して CD を発火 → tofu apply で `min_instance_count: 0 -> 1` を反映
4. `gcloud run services describe runops-gateway --format="value(spec.template.metadata.annotations[autoscaling.knative.dev/minScale])"` で `1` を確認
5. **Cloud Trace UI** で「inbound trace (Slack→Pub/Sub→dmail-receiver) と outbound trace (dmail-emitter→Pub/Sub→gateway OutboundReceiver→chat.postMessage) の 2 経路** が両方 1 trace_id で繋がることを確認

## 受入基準

- Cloud Run min=1 で常時 1 instance 稼働
- `dmail-outbound-gateway` subscription の oldest_unacked_message_age が 1 day を超えなくなる (PR #19 の backlog-stale alert が静まる)
- HIGH severity 4-eyes approval (ADR 0019) が一連の Pub/Sub 経路で動作する
- 課金影響 (常時 1 instance) を本人が許容できる範囲

## 関連 ADR / docs

- ADR 0013 (Pub/Sub bridge)
- ADR 0018 (dmail-outbound pull subscription、warm instance 必須)
- `docs/handover.md` Phase 3 row + 「Phase 4b (tofu コード)」row の scaling 記述
- `tofu/variables.tf` `cloud_run_min_instances` (default 0)
- `tofu/main.tf` `scaling { min_instance_count = var.cloud_run_min_instances }`
