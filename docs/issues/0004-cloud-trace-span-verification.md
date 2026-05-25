# Issue 0004: Cloud Trace UI で実 span tree を確認 + experiments / handover にスクショ添付

**Repo:** `hironow/runops-gateway` (本リポ完結)
**Status:** 📝 未着手
**Depends on:** PR #22 deploy 後の indexing 完了 (~30 min)

## Why

PR #21 で OTel resource に `gcp.project_id` を付与する修正を入れ、PR #22 で本番反映済。Cloud Run log 上では「`traces export: rpc error ...`」エラーが消えたが、**Cloud Trace UI 上で実 span tree の visual を確認できていない** (本 session 中の API listing は indexing 遅延で 0 件表示)。

ADR 0020 の達成目標 ("Slack 受信 → Pub/Sub publish 1 跨ぎまで 1 trace_id で繋ぐ") が production で実際に成立しているかを **目視で確認** する。

## What

1. **Cloud Trace UI** (<https://console.cloud.google.com/traces/list?project=gen-ai-hironow>) を開く
2. Service フィルタで `runops-gateway` を選択、過去 1-6 時間の trace を listing
3. Slack `/runops sightjack ...` を 1 件投入 → 数分後に trace 反映を待つ
4. 該当 trace を開いて span tree を確認:

    ```
    POST /slack/interactive  (otelhttp root)
     |- slack.verify_signature
     |- slack.handle_dispatch_action
     |- usecase.dispatch_agent_task
          |- send dmail-inbound  (auto, pubsub/v2)
    ```

5. inbound trace tree のスクショを取得
6. 同様に outbound trace (dmail-emitter 起点、Issue 0001 deploy 後) の確認
7. `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md` の末尾、または `docs/handover.md` ハマりどころ集 7 に **スクショ画像を添付** (`docs/images/` を新設するか PR description に inline 貼り付けして commit message から参照)

## 受入基準

- Cloud Trace UI に `runops-gateway` service が 1 件以上の trace を持つ状態で表示される
- inbound 5 span (otelhttp root → verify → handle → usecase → send) が 1 trace_id 内で連結している
- experiments / handover にスクショ or text dump が残されており、将来同等性確認の reference になる

## (オプション) 自動化アイデア

- post-deploy smoke 後に Cloud Trace API を叩いて「直近 1 分以内に少なくとも 1 trace が project に届いているか」を assert する step を `cd.yaml` に追加。fail なら deploy job fail
- ただし indexing 遅延を考慮すると現実的には sleep 30s 必要 → PR #15 で導入した smoke と一貫性を保つには別 job (post-smoke) として組む

## 関連

- ADR 0020 (Direct OTLP + 達成目標範囲)
- ADR 0021 (Pub/Sub trace 委譲)
- `docs/handover.md` ハマりどころ集 7 (trace context propagation)
- `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md`
- PR #21 / #22 (gcp.project_id 修正)
