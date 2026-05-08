# handover — 5本柱 D-Mail Dispatcher 拡張の実装状況と引き継ぎ

## このドキュメントの位置づけ

`docs/intent.md` が「なぜ・何を」を扱うのに対し、本ドキュメントは
「どこまで実装済みで、何が残っていて、どこに罠があるか」を扱う。

新しい session を開始するとき、または将来このリポジトリに戻ってくるとき、
最初に読むべきページとして書く。日付ベースで上書き更新する想定。

最終更新: 2026-05-08

## ブランチ運用ポリシー (重要)

**`main` は production の稼働中ブランチ**。Cloud Run に自動デプロイ (`cd.yaml`) される。

**全ての feature / fix / docs PR は `develop` を base にする**。
2026-05-05 時点の合意事項。`gh pr create` 時に `-B develop` を必ず指定する。

| 役割 | branch | base への merge |
|---|---|---|
| 開発 PR | `feat/*` `fix/*` `docs/*` `refactor/*` 等 | → `develop` |
| Release PR | `release/*` | `develop` → `main` |
| Hotfix | `hotfix/*` | `main` + `develop` 両方 cherry-pick |

理由: `main` は production 稼働中のため、開発中の Phase 1 (シンプル経路) や
今後の Phase 2 以降の実装が中途半端な状態で `main` に積まれることを避ける。
`develop` を統合ブランチとし、phase 単位 (or リリース単位) で `main` に rebase
or merge する運用とする。

---

## 全体ステータス

> **進捗 (2026-05-05)**: Phase 1 / Phase 2a-c / Phase 3 / Phase 4a は develop
> へ squash merge 済 (PR #6 / #7)。続けて **OpenTelemetry 配線** を
> `feat/otel-direct-otlp` (PR #8, draft) で実装。3 binary とも
> `OTEL_EXPORTER_OTLP_ENDPOINT` 切替で local Jaeger v2 / prod Cloud Trace
> の両対応。Pub/Sub は v2 ライブラリの `EnableOpenTelemetryTracing` で
> trace context を自動 inject。
>
> ADR は 0013/0015/0017/0018/0019/0020/0021/0022 が **Accepted**。
> 0012 のみ Proposed (kind 増設は Phase 4 以降に再評価)。次は Phase 4b
> (tofu / 本番化) と、必要なら handler/receiver 内の細かい span 追加。

| Phase | 状態 | 内容 |
| --- | --- | --- |
| Phase 0 | ✅ 完了 | 既存 ChatOps（Cloud Run カナリア・DB マイグレ） |
| **Phase 1** | ✅ 完了 (2026-05-05, develop merged) | **シンプル経路**: `/slack/command` → **Block Kit 確認 → Approve クリック (`/slack/interactive`)** → DispatchAgentTask → thread reply。Codex Review round 2/3/4 の致命指摘 **5 件** はすべて修正済み |
| **Phase 2a** | ✅ 完了 (2026-05-05, `feat/long-running-dispatch`) | Issue 0017 chat.postMessage fallback (ADR 0017) + PubsubDispatcher publish 経路 + `DISPATCHER_BACKEND=stub\|pubsub` 切替 + emulator 統合テスト |
| **Phase 2b** | ✅ 完了 (2026-05-05, 同 branch) | `cmd/dmail-receiver` daemon: Pub/Sub StreamingPull → phonewave outbox に atomic write (`OutboxWriter`) |
| **Phase 2c** | ✅ 完了 (2026-05-05, 同 branch) | `cmd/dmail-emitter` daemon: 5本柱 archive を fsnotify 監視 → Pub/Sub `dmail-outbound` publish (`Watcher` + `Emitter`) |
| **Phase 3** | ✅ 完了 (2026-05-05, 同 branch) | gateway 内 `OutboundReceiver` (StreamingPull) → `DispatchResultHandler` → FallbackNotifier (chat.postMessage) で Slack thread reply。ADR 0018 (pull subscription)、metadata propagation (parent_idempotency_key / slack_channel_id / slack_thread_ts) |
| **Phase 4a** | ✅ 完了 (2026-05-05, 同 branch) | HIGH severity convergence の 4-eyes approval gate (ADR 0019)。`ApprovalRequester` で Block Kit、`approval_approve` / `approval_deny` action_id を `InteractiveHandler` に追加、ConsumedTokenStore で one-time consume、approver != original_requester 強制、承認後 dmail-inbound に convergence ack を publish |
| **OTel 配線** | ✅ 完了 (2026-05-05, `feat/otel-direct-otlp` PR #8 draft) | ADR 0020 (Direct OTLP) + ADR 0021 (Pub/Sub trace 委譲) + ADR 0022 (CloudEvents 不採用)。`internal/adapter/observability` に SetupTracerProvider / NormalizeEndpoint / BuildResource / 自動 noop fallback。3 binary に TracerProvider + otelhttp wrap、Pub/Sub 3 client に `EnableOpenTelemetryTracing: true`、`compose.yaml` に Jaeger v2.17、`just trace-up/down/view`。手動 span: `slack.verify_signature` / `slack.handle_dispatch_action` / `slack.handle_approval_action` / `usecase.dispatch_agent_task` / `usecase.dispatch_result_handle` / `dmail.receiver.on_message` / `dmail.outbound.on_message` / `dmail.emitter.publish_file`。goroutine 跨ぎは `context.WithoutCancel` で trace context 引き継ぎ |
| **Phase 4b (tofu コード)** | ✅ develop merged (2026-05-05, PR #8) | 本番化用 tofu: `tofu/pubsub.tf` (dmail-inbound / dmail-outbound + 各 DLQ)、`tofu/subscriptions.tf` (dmail-inbound-receiver / dmail-outbound-gateway + Pub/Sub service agent IAM)、`tofu/iam_pubsub.tf` (chatops_sa + 任意の exe-coder VM SA)、`tofu/telemetry.tf` (cloudtrace + telemetry API enable + tracesWriter)。`main.tf` の Cloud Run service に `SLACK_BOT_TOKEN` / `DISPATCHER_BACKEND=pubsub` / `PUBSUB_DMAIL_INBOUND_TOPIC` / `PUBSUB_DMAIL_OUTBOUND_SUB` / OTEL 一式の env を注入。`slack-bot-token` Secret Manager + accessor も追加。**scaling は `var.cloud_run_min_instances` (default 0) / `var.cloud_run_max_instances` (default 3) で制御**。Phase 3 outbound (ADR 0018) を有効化する際は `cloud_run_min_instances=1` に上げる |
| **DLQ terminal sink** | ✅ tofu コード完了 (2026-05-05, `chore/dlq-terminal-sink`) | PR #8 後追い。`tofu/subscriptions.tf` に `dmail-inbound-dlq-pull` / `dmail-outbound-dlq-pull` を追加 (DLQ topic に subscription が無いと message が retention で蒸発する公式アンチパターン解消)。`tofu/monitoring.tf` (新設) に `subscription/dead_letter_message_count` の Cloud Monitoring alert + email notification channel (`dlq_alert_email` 空なら count=0 で skip)。`docs/runbooks/dlq.md` (新設) に triage 手順と republish snippet。詳細: `experiments/2026-05-05_pubsub-dlq-terminal-sink.md` |
| **Phase 4b (実 apply)** | ✅ 完了 (2026-05-05, gen-ai-hironow) | ローカル `tofu apply -var-file=tofu/gen-ai-hironow.tfvars` で **25 add + 2 change** が成功。Pub/Sub 4 topic + 4 subscription (working 2 + DLQ pull 2)、IAM 一式 (chatops_sa publisher/subscriber + exe-coder VM SA + Pub/Sub service agent + tracesWriter)、`slack-bot-token` secret + accessor、Cloud Monitoring DLQ alert (`hironow365@gmail.com` 通知) + backlog-stale alert (PR #19)、Cloud Run env 12 個追加 (Phase 1-4a + OTel) を反映。`slack-bot-token` 実値 (xoxb-...) v2 投入済 |
| **main promote (実 image)** | ✅ 完了 (2026-05-05, PR #12 → #15 → #18 → #20) | 4 release を経て本番 Cloud Run image を Phase 1-4b 全部入りに rollout。実動作確認: `/runops sightjack approve-test-1` → Block Kit → Approve → `pubsub_message_id=18812416980158651` payload 確認 (idempotency_key=d23e2d029ee93dd650857933fec1493c)。`/runops sightjack mention-test-1` → 同 (msg_id=19471312983780761, idem=e28e5f762bdd3ec981c55de4e57b6fd2)。CD post-deploy smoke green、Slack 上で `*依頼者:* @hironow` mention 表示確認 |

「設計済 / 未着手」は intent.md と本ドキュメントに方針が書かれているが
コードに手がついていない状態。
「draft」は Phase 4a 完了済みの今 (2026-05-05) は Phase 4b (tofu / 本番化) を指す。
Phase 4a 実装内容そのものは git ログ (`feat(pubsub):` / `feat(usecase):` /
`feat(slack):` の squash 前 commit) を読むのが早い。

---

## 5本柱と Phonewave の前提

intent.md の「5本柱と D-Mail Protocol の前提」を読んでいることが本セクションの前提。
ここでは実装に効く具体的な事実だけ列挙する。

### 既に動いているもの（変更しない）

- `/Users/nino/tap/sightjack` — Designer (`.siren/`)
- `/Users/nino/tap/paintress` — Implementer (`.expedition/`)
- `/Users/nino/tap/amadeus` — Verifier (`.gate/`)
- `/Users/nino/tap/dominator` — NFR Judge (`.pass/`)
- `/Users/nino/tap/phonewave` — Courier daemon (fsnotify based, atomic write)

### exe-coder VM に追加する 2 daemon（このリポで管理）

- **dmail-receiver** — Pub/Sub から phonewave outbox に書き出す
- **dmail-emitter** — 各ツールの outbox/archive を fsnotify で見て Pub/Sub に流す

### D-Mail kind は追加しない（決定 A）

新規 kind は作らない。既存の `specification` / `report` / `design-feedback` /
`implementation-feedback` / `convergence` / `ci-result` に payload で
sender 情報を付けて識別する。

---

## Phase 0 — 既存 ChatOps の現状

### 動いているもの

- `cmd/server`: Slack Webhook 受信 (HMAC 検証含む)
- `cmd/runops`: Cobra CLI（approve/deny の2コマンド）
- `internal/core/domain`: ResourceType (service/job/worker-pool), Action (canary_N/migrate_apply)
- `internal/core/port`: AuthChecker, Notifier, RunOpsUseCase, StateStore, GCPController
- `internal/usecase`: ApproveAction, DenyAction
- `internal/adapter/output/gcp`: Cloud Run + Cloud SQL クライアント
- `internal/adapter/output/slack`: response_url Notifier + Block Kit テンプレート
- `internal/adapter/output/auth`: EnvAuthChecker (allowlist + 有効期限)
- `internal/adapter/output/state`: MemoryStore（in-process dedup）
- `tofu/`: WIF + Cloud Run + Secret Manager + Artifact Registry
- `.github/workflows/cd.yaml`: main push で自動デプロイ
- `tests/runn/`: シナリオテスト 5本

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

### Phase 0 の未解決課題（拡張前に潰すべきか判断する）

1. **OfferContinuation が 404 を返す問題** — トラフィックシフト自体は成功するが、
   次のカナリアステップボタンを表示する `OfferContinuation` が Slack response_url
   への POST で 404 を返すことがある。

   調査結果（2026-05-05、コード読み取り + 仕様照合）:

   - **post 層の 404 ハンドリングは正常**: `internal/adapter/output/slack/notifier.go:132`
     で 404 を error に変換しており、`TestPost_404_ReturnsErrorWithBody` でカバー済み
   - **不明なのは「なぜ 404 が起きるか」**: Slack response_url の仕様上の制約に抵触している可能性
   - **仮説 1 (最有力)**: `response_url` の **30 分有効期限** 超過。
     handler.go 側は 25 分タイムアウトを設けているが (`responseURLTimeout = 25 * time.Minute`)、
     `approveShift` の途中で UpdateMessage → 長時間 LRO → OfferContinuation の
     順で 30 分を超えうる構造になっている。特にマルチリソースの逐次処理 (ADR 0010) で顕在化
   - **仮説 2**: 同一 response_url の **5 回使用制限** 超過。
     `offerOrFallback` のフォールバックチェーン (UpdateMessage + OfferContinuation +
     fallback UpdateMessage) で 1 回の操作で 3-4 回消費する。連続失敗時は 5 回を超える
   - **仮説 3**: `BuildProgressMessage` が生成する Block Kit の構造不正で
     200 OK を返しつつ `invalid_blocks` エラー（`TestPost_200InvalidBlocks_NoErrorButLogsWarning`
     が示す silent failure パスと類似）

   **次のアクション** (別ブランチで TDD):

   - `internal/usecase/runops_test.go` に **シーケンス全体** の再現テストを追加
     - `TestApproveShift_OfferContinuationFailsAfter5Calls` — 6 回目で 404 を返す mock server
     - `TestApproveShift_OfferContinuationFailsAfter30Min` — 仮想時計で 30 分超過を再現
   - 修正方向: (a) operator visible な expiry warning の挿入、
     (b) response_url 使用回数のメトリクス化、(c) chat.postMessage への自動 fallback (ADR 0006 と関連)

2. **Slack API モックテスト** — `httptest.NewServer` での response_url 応答パターン
   テストが未整備。Phase 1 と並行して `internal/usecase/runops_test.go` に
   シーケンステストを追加することで部分的にカバーされる。
3. **`MemoryStore` の永続化** — プロセス再起動でリセット（intent.md にて
   best-effort 扱いと割り切ったため、Phase 2 までは現状維持）。
4. **Slack `chat.update` API 対応** — CLI 実行時の既存 Slack メッセージ無効化
   （ADR 0006）が未実装。OfferContinuation 404 問題の修正方向 (c) と統合される可能性あり。

---

## Phase 1 (新) — シンプル経路の確立

目的: D-Mail / Pub/Sub に着手する前に、**Slack で受け、認可し、確認し、応答する
最小の循環** を runops-gateway 単体で完結させる。実処理 (5本柱への投入) は stub。

詳細な設計判断は ADR 0014 (Slack 通知集約) と本ドキュメント末尾の
"Phase 1 review findings" を参照。実装の最終形は git ログ
(`feat: Phase 1 ...` の squash commit) を見るのが早い。

### Phase 1 のスコープ要約

- 新規エンドポイント `POST /slack/command` (Slash Command Request URL)
- HMAC 検証は既存 `verify.go` を流用
- Block Kit で「依頼内容を承認しますか?」を表示
- 承認時は `slog` 出力 + thread reply (`":eyes: 受付"`) のみ
- D-Mail / Pub/Sub / 5本柱への投入は **すべて Out of Scope**

### Phase 1 を最初に通す理由

Pub/Sub bridge と receiver / emitter の実装は粒度が大きく、最初に組むと
バグの切り分け（Slack 経路・Pub/Sub・receiver のどこか）に時間が取られる。
Slash Command 経路と Block Kit + thread reply の枠組みを **stub で先に通す** ことで:

- Slack App の Slash Command Request URL 設定が動作確認済みになる
- HMAC + auth + Block Kit + response_url の組み合わせが新エンドポイントで動く
- `dispatch_approve` / `dispatch_deny` action_id の規約が確立する
- Phase 2 の Pub/Sub bridge 実装時、Slack 経路は debug 対象から外せる

### Phase 1 完了後の Phase 2 への接続

Phase 1 で実装する `Dispatcher` port の Stub 実装を、Phase 2 で `PubsubDispatcher` に
差し替える設計にする (Hexagonal の典型的な進化)。Phase 1 のコードは破棄せず
そのまま Phase 2 に積み上がる。

---

## Phase 1 review findings (2026-05-05)

> Phase 1 実装完了後の Codex Review (round 2) と self-review で、Issue 0018 の
> 計画には含まれていなかった事項が複数浮上した。各項目の追跡先を以下に記す。

### F-1: Slack request timestamp 鮮度チェック欠落 (致命)

| | |
|---|---|
| 重大度 | **致命**（Codex Review round 2 唯一の指摘） |
| 影響範囲 | `internal/adapter/input/slack/verify.go` (Phase 0 から存在) |
| Phase 1 での増悪 | `/slack/command` 追加で攻撃面が「カナリア再シフト」から「任意 agent 起動」に拡大 |
| 追跡先 | ADR 0016 / Issue 0019 |
| 対応方針 | **本 PR 内で同時修正**（Phase 1 と同じブランチで TDD 実装） |

### F-2: cmd/server で MemoryStore を 2 つ作成している (設計判断、ログ要)

`cmd/server/main.go` で:

```go
svc := usecase.NewRunOpsService(gcpCtrl, notifier, authChecker, state.NewMemoryStore())
dispatchSvc := usecase.NewDispatchService(dispatcher, notifier, authChecker, state.NewMemoryStore())
```

ApproveAction 用と DispatchAgentTask 用で **別の MemoryStore インスタンス** を
使っている。これは意図的:

- 既存 ApproveAction の `OperationKey` (`Project/ResourceType/...`) と
  DispatchRequest の `OperationKey` (`dispatch/Role/...`) は名前空間が異なるが、
  共有 store にしたら lock 解放タイミングの相互干渉が起きうる
- 両 service は独立進化すべき (Phase 2 で DispatchService が Pub/Sub に移ると
  store 自体不要になる可能性も)

**致命度**: 低（intent.md で MemoryStore は best-effort と明記済み）。
ただし Cloud Run autoscale 下では instance-local なので、いずれにせよ
真の冪等性は **Pub/Sub の idempotency_key** で担保する想定 (Phase 2 で対応)。

### F-3: DispatchRequest.OperationKey が IssuedAt を含む (設計判断、要明示)

```go
// internal/core/domain/dispatch_request.go
func (r DispatchRequest) OperationKey() string {
    return fmt.Sprintf("dispatch/%s/%s/%s/%d",
        r.Role, r.RequesterID, r.IdempotencyKey, r.IssuedAt)
}
```

IssuedAt が含まれるため、**同じ IdempotencyKey でも秒が違えば別キー扱い**。
これは:

- **意図的な振る舞い**: 既存 `port.OperationKey(req)` (ApprovalRequest 用) も
  IssuedAt を含む規約に揃えた
- **Phase 1 での意味**: `command.go` の `newIdempotencyKey` は crypto/rand で
  毎回別値を出すので、IssuedAt の有無は実質的に効いていない
- **Phase 2 以降のリスク**: 上流 (Slack) が同じ `idempotency_key` を retry した
  場合、IssuedAt が変わるとキーが変わって TryLock が通ってしまう。本来 retry
  なら通すべき/弾くべきの判断は use case 側の責務だが、現実装では考慮していない

**追跡先**: ADR 0017 (予定、Phase 2 着手時に判断)。本 PR ではコードに
コメントを残すにとどめる。

### F-4: StubDispatcher の slog 出力に dispatch text が含まれる ✅ 解決済み (2026-05-05)

Codex Review round 3 が **致命に昇格** したため、**本 PR 内で修正完了**。

```go
// internal/adapter/output/dispatcher/stub.go (修正後)
d.logger.LogAttrs(ctx, slog.LevelInfo, "dispatched stub",
    slog.String("role", string(req.Role)),
    slog.Int("text_len", len(req.Text)),
    slog.String("text_sha256", textFingerprint(req.Text)), // 8 hex chars
    slog.String("requester_id", req.RequesterID),
    slog.String("idempotency_key", req.IdempotencyKey),
    slog.Int64("issued_at", req.IssuedAt),
)
```

- 32 bit fingerprint で 2 行のログ間で同一 dispatch を相関できる
- 生 text は **絶対にログに出ない** ことを `TestStubDispatcher_DoesNotEmitTextLiteralOrSecret` で固定
- 関連コミット: `3c08f90 fix(dispatcher): redact dispatch text from StubDispatcher logs (F-4)`

将来の追加: dispatch text 全体に対するサイズ上限・サニタイズポリシーは
Issue 0020 (Phase 4 起票予定) で扱う。

### F-7: dispatch_* で clicker 本人性未検証 ✅ 解決済み (2026-05-05)

Codex Review round 4 が **致命と指摘** したため、**本 PR 内で修正完了**。

修正内容:

- `handleDispatchAction` で `clickerUserID != dv.RequesterID` の場合、
  ephemeral message で reject (UseCase は呼ばれない)
- `dispatch_deny` も同じ guard を適用 (griefer による横やり cancel を防ぐ)
- DispatchRequest.RequesterID には **clicker 本人の Slack user.id** を入れ、
  payload 値を信頼しない (defense in depth)
- 関連コミット: `5c1fa5d fix(slack): require clicker == original requester on dispatch_*`

Phase 4 の HIGH severity 4-eyes フローは「clicker ≠ requester が intended」なので、
本 guard を **flag で disable できる形** に拡張する設計余地がある。

### F-8: dispatch_approve の button replay ✅ 解決済み (2026-05-05)

Codex Review round 4 が **致命と指摘** したため、**本 PR 内で修正完了**。

修正内容:

- 新規 port `port.ConsumedTokenStore` を導入
- `state.MemoryConsumedStore` (TTL 付き sync.Map ベース、in-process) で実装
- `dispatchApproveToken(dv)` を作って `RequesterID/IdempotencyKey/IssuedAt`
  ベースの consumed key を生成
- 同一 token を 2 回 MarkConsumed すると 2 回目は false → ephemeral reject
- TTL は cmd/server で 1 時間 (Slack response_url 30 分窓に余裕を持たせた値)
- 関連コミット:
  - `64d7b07 feat(state): add ConsumedTokenStore port + MemoryConsumedStore`
  - `e467b2b fix(slack): one-time consume guard on dispatch_approve`

**注意**: in-process なので Cloud Run autoscale 下では instance ごとに別の
consumed set。1 instance 内で再クリック → 防げる、別 instance に当たった retry →
防げない。Phase 2 で Pub/Sub message ID dedup に置き換える前提 (intent.md の
best-effort 規約に沿った Phase 1 段階の実装)。

### F-5: Phase 1 で Block Kit 確認ステップ ✅ 解決済み (2026-05-05)

Codex Review round 3 が **致命に昇格** したため、**本 PR 内で確認ステップを復活**。

修正内容:

- CommandHandler は **直接 dispatch せず、Block Kit ephemeral 確認** を返す
- Approve ボタンには Slack confirm dialog を付与 (誤クリックでも 1 段階確認)
- Approve クリック → `/slack/interactive` → `dispatch_approve` → DispatchAgentTask
- Deny クリック → "🚫 Dispatch をキャンセルしました" をエフェメラルに更新 (UseCase は呼ばれない)
- Phase 1 では requester == approver (4-eyes は Phase 4 で導入)
- 関連コミット:
  - `18331f9 feat(slack): add dispatch confirmation building blocks`
  - `ba7407f fix(slack): require Block Kit confirmation before dispatch (F-5)`

これにより Issue 0018 の元計画 (確認ボタン込み) を完全に実装した形になった。
Issue 0018 冒頭の「実装後の差分メモ」は無効化済み (詳細は Issue 0018 を参照)。

### F-6: runn シナリオの timestamp 固定値 (F-1 修正で破壊される)

`tests/runn/*.yaml` は timestamp を `1700000000` で固定し事前計算した署名を
埋めている。F-1 の鮮度チェック導入で **全 runn シナリオが 401 で fail する**。

**追跡先**: Issue 0019 で対処方針を記述。「`tests/runn/STALE_TIMESTAMPS_README.md`
で既知制約として記録」する案を採用。

---

## Phase 2 (旧 Phase 1) — Pub/Sub topology + dmail-receiver (draft)

> **draft (2026-05-05)**: 本セクションは Phase 1 完了後の議論。ADR 0013/0015 が
> Proposed のままなので、ここに書かれた具体実装は確定事項ではない。
> 設計の輪郭だけ残しておくため削除はしない。

目的: 「runops-gateway → Pub/Sub → exe-coder → phonewave outbox に .md が現れる」
までの 1 方向パイプラインを動かす。Slack 連携は Phase 1 で完了済みの想定。

### Pub/Sub topology

```
+--------------------+         +------------------+          +-------------------+
|  runops-gateway    |  pub    |  Pub/Sub topic   |  pull    |  dmail-receiver   |
|  (Cloud Run)       | ------> |  dmail-inbound   | -------> |  (exe-coder VM)   |
+--------------------+         +------------------+          +-------------------+
                                       |                              |
                                       v                              v
                                  Dead Letter Topic           atomic write to
                                  (dmail-inbound-dlq)         tap-router/outbox/
                                                              (or any phonewave-watched outbox)
```

Legend / 凡例:
- pub: Cloud Pub/Sub publish (synchronous RPC)
- pull: Cloud Pub/Sub StreamingPull subscriber
- DLQ: Dead Letter Queue (max delivery attempts 超過後)

トピック設計:

| トピック | 方向 | 用途 |
|---|---|---|
| `dmail-inbound` | gateway → exe-coder | Slack/CI から 5本柱への D-Mail 投入 |
| `dmail-outbound` | exe-coder → gateway | 5本柱から Slack 通知への結果配送 (Phase 3) |
| `dmail-inbound-dlq` | DLQ | publish 失敗の格納 |
| `dmail-outbound-dlq` | DLQ | 同上 |

Subscription:
- `dmail-receiver-sub` (dmail-inbound, exe-coder で pull)
- `runops-gateway-sub` (dmail-outbound, Cloud Run で push subscription, Phase 3)

### Pub/Sub message 仕様

```
Message attributes:
  kind                 string (specification|report|...)
  target_tool          string (paintress|sightjack|amadeus|dominator|*)
  source               string (runops-gateway-slack|runops-gateway-ci|<tool>)
  dmail_schema_version string ("1")
  idempotency_key      string (SHA-256, dedup)
  traceparent          string (W3C Trace Context)

Message data:
  D-Mail .md ファイルの完全な中身 (frontmatter + body)
```

### 追加するコード

```
runops-gateway/
  cmd/
    dmail-receiver/        [新規] Pub/Sub から phonewave outbox に書く daemon
      main.go
  internal/
    core/
      domain/
        dmail.go           [新規] DMail (kind, target, payload), schema v1
      port/
        dmail_publisher.go [新規] DMailPublisher IF (Pub/Sub publish)
    adapter/
      output/
        pubsub/            [新規] Cloud Pub/Sub クライアント
          publisher.go
          subscriber.go    (receiver で利用)
        phonewave/         [新規] outbox atomic write (temp + rename)
          writer.go
```

### Tofu 側の差分

```hcl
# tofu/pubsub.tf [新規]
resource "google_pubsub_topic" "dmail_inbound" {
  name = "dmail-inbound"
  message_retention_duration = "604800s"  # 7 days
}

resource "google_pubsub_topic" "dmail_inbound_dlq" {
  name = "dmail-inbound-dlq"
}

resource "google_pubsub_subscription" "dmail_receiver" {
  name  = "dmail-receiver-sub"
  topic = google_pubsub_topic.dmail_inbound.name
  ack_deadline_seconds = 60
  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dmail_inbound_dlq.id
    max_delivery_attempts = 5
  }
}

# runops-gateway SA に publisher 権限
resource "google_pubsub_topic_iam_member" "gateway_publisher" {
  topic  = google_pubsub_topic.dmail_inbound.name
  role   = "roles/pubsub.publisher"
  member = "serviceAccount:${google_service_account.chatops_sa.email}"
}

# dmail-receiver SA (exe-coder VM の workload identity) に subscriber 権限
resource "google_pubsub_subscription_iam_member" "receiver_subscriber" {
  subscription = google_pubsub_subscription.dmail_receiver.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:${var.exe_workspace_sa_email}"
}
```

### exe-coder VM 側の準備（hironow/dotfiles を別 PR で先行）

`tofu/exe/startup-script.tpl` に systemd unit を追加:

```ini
[Unit]
Description=D-Mail receiver (Pub/Sub -> phonewave outbox)
After=network.target

[Service]
ExecStart=/usr/local/bin/dmail-receiver \
  --subscription=projects/<PROJECT>/subscriptions/dmail-receiver-sub \
  --outbox-dir=/var/lib/phonewave/runops-gateway/outbox
Restart=always
User=phonewave

[Install]
WantedBy=multi-user.target
```

`/var/lib/phonewave/runops-gateway/outbox` は phonewave init で routing 対象に追加。
runops-gateway 自身が新しい SKILL.md を持つ "送信専用" tool としてふるまう。

### runops-gateway 用 SKILL.md（exe-coder VM 上に配置）

```yaml
# /var/lib/phonewave/runops-gateway/skills/dmail-sendable/SKILL.md
---
name: dmail-sendable
description: External-origin D-Mails injected via Pub/Sub bridge.
license: Apache-2.0
metadata:
  dmail-schema-version: "1"
  produces:
    - kind: specification
      description: Issue specification originated from Slack /agent or CI webhook
    - kind: ci-result
      description: CI/CD pipeline result forwarded by gateway
---
```

phonewave は `init` 時にこの outbox + SKILL.md を発見してルーティング表に追加する。

### 動作確認手順

```bash
# 1. ローカルで Pub/Sub emulator
gcloud beta emulators pubsub start --project=local
PUBSUB_EMULATOR_HOST=localhost:8085 \
  go run ./cmd/dmail-receiver --subscription=... --outbox-dir=/tmp/test-outbox

# 2. runops-gateway 側からテスト publish
PUBSUB_EMULATOR_HOST=localhost:8085 \
  go test ./internal/adapter/output/pubsub/... -v

# 3. /tmp/test-outbox に .md が書き出されることを確認

# 4. phonewave を /tmp/test-outbox 配下を含めて init してみて、
#    paintress の inbox に届くまでを e2e で確認
phonewave init /tmp/test-outbox /Users/nino/tap/paintress
phonewave run -v
```

---

## Phase 3 (旧 Phase 2) — `/agent` の stub を Pub/Sub publish に差し替え + CLI (draft)

> **draft (2026-05-05)**: Phase 1 で実装した stub `Dispatcher` を Pub/Sub 実装に
> 差し替えるフェーズ。Phase 2 の Pub/Sub topology が動いた前提で着手する。

目的: 人間が Slack/CLI から D-Mail を投入できるようにする。

### Slack 側

- 既存の Slack App に **Slash Command `/agent`** を追加（Request URL: `/slack/command`）
- Phase 0 の `/slack/interactive` とは別エンドポイント
- HMAC 検証は同じ signing secret で OK
- response_url の挙動も Interactive と同じ（30分有効、ephemeral 切替可）

### コマンド仕様

```
/agent <role> <task description>

例:
/agent paintress fix M-42
/agent sightjack scan ENG project
/agent amadeus check --base main
```

役割名 (role) は `target_tool` attribute にそのまま入る。

### 追加するコード

```
internal/
  adapter/
    input/
      slack/
        command.go            [新規] Slash Command handler
    cli/
      dispatch.go             [新規] runops dispatch agent <role> --task=...
  usecase/
    dispatch_agent_task.go    [新規] DispatchAgentTask UseCase
  core/
    port/
      dmail_publisher.go      (Phase 1 で追加済み)
```

### UseCase 概略

```
DispatchAgentTask:
  1. AuthChecker.IsAuthorized(approver) — 既存の EnvAuthChecker 流用
  2. domain.NewDMail(kind=specification, target=role, payload=...)
  3. DMailPublisher.Publish(ctx, dmail)
  4. Slack に「:eyes: dispatched (id=<idempotency_key>)」を即返信
```

### 動作確認手順

```bash
# /agent paintress "test from slack"
# → :eyes: paintress に dispatch (id=...) が即返る (3秒以内)
# → exe-coder の dmail-receiver ログで Pub/Sub pull を確認
# → /var/lib/phonewave/runops-gateway/outbox/ に .md が現れる
# → phonewave 経由で paintress inbox に到達
# → paintress のログで specification 受信を確認
```

---

## Phase 4 (旧 Phase 3) — dmail-emitter + Slack thread 通知 (draft)

> **draft (2026-05-05)**: 5本柱からの逆流経路を確立するフェーズ。Phase 3 の publish 経路と
> 対称的に設計する。

目的: 5本柱が出した結果（report 等）を Slack thread に逆流させる。

### dmail-emitter

```
runops-gateway/
  cmd/
    dmail-emitter/         [新規] 5本柱の outbox/archive を fsnotify で監視
      main.go
```

設定:

- 監視対象: 5本柱それぞれの `archive/` ディレクトリ（outbox は phonewave が
  rename して消すが、archive は永続化されるため）
- 検出した .md を読んで Pub/Sub `dmail-outbound` トピックに publish
- attribute に元 tool 名 (`source=paintress` 等) を入れる

### runops-gateway 側

- 新規 endpoint `/pubsub/dmail-outbound` を追加（Pub/Sub push subscription）
- Phase 2 で発行した specification の `idempotency_key` を MemoryStore (or Cloud SQL)
  に記録しておき、戻ってきた report の親 specification を辿れるようにする
- Slack thread に reply（response_url が 30分以内なら使う、超えそうなら
  chat.postMessage 経由で direct post）

### 親子関係の追跡（design choice）

D-Mail は `metadata.parent_idempotency_key` を frontmatter に持てるよう拡張する
（既存 5本柱は未知フィールドを無視するので互換）。
発行時は Slack thread_ts も `metadata.slack_thread_ts` として埋めておくと、
逆流時に直接 thread を辿れる。

---

## Phase 5 (旧 Phase 4) — HIGH severity approval gate + 本番化 (draft)

> **draft (2026-05-05)**: Phase 4 の逆流経路完了後、人間承認の挿入と本番化を行う。

目的: paintress の docs/approval-contract.md に対応する人間承認を gateway 側で実装。

### 流れ

1. paintress が HIGH severity な D-Mail を archive に出す
2. dmail-emitter が Pub/Sub に流す
3. runops-gateway が Slack に **承認ボタン** を出す
4. 人間がクリックすると、`convergence` kind (or 新しい payload で `approval` 意味)
   の D-Mail を発行
5. dmail-receiver 経由で paintress inbox に届く
6. paintress が approval を受けて続行

### その他本番化作業

- exe-coder VM の preemptible 解除 (or HA 化)
- Pub/Sub 用 Cloud SQL 分離（Phase 1-3 では Coder 用と相乗り）
- `*.sandbox.hironow.dev` の preview ingress 整備（PR preview 表示用）

---

## ハマりどころ集

実装に着手したとき、これらは確実に踏むので先に書いておく。

### 1. Pub/Sub publish の同期 RPC レイテンシ

Pub/Sub publish は同期 RPC で 50-100ms (asia-northeast1)。
**Slack 3秒ルールに対しては余裕がある** が、Cloud Run cold start (1-2秒) と
合わさると危険ゾーン。`min-instances=1` で warm を維持（追加コスト ~$5/月）。

### 2. exe-coder VM の preempt

`preemptible = true` の VM は 24時間以内に preempt される。preempt 中の挙動:

- `dmail-receiver` が止まる → Pub/Sub には積まれ続ける（OK）
- VM 復旧後、subscriber が起動して滞留分を pull
- ack 期限 (60s) 超過分は自動 redelivery

→ subscriber 側は **再起動時に同じ message を再処理しても安全** であること
（idempotency_key + ファイル名重複チェック）が必須。

### 3. atomic write (temp + rename) の徹底

phonewave は fsnotify で outbox を監視している。
`open → write → close` の途中で fsnotify が発火すると、phonewave が **不完全な
.md を読んで delivery エラーキューに入れる** 事故が起きる。

→ dmail-receiver は必ず:

```go
tmp := outboxDir + "/.tmp-" + name
os.WriteFile(tmp, data, 0644)
os.Rename(tmp, outboxDir + "/" + name)  // atomic
```

### 4. dmail-receiver の dedup 戦略

Pub/Sub は at-least-once 配送。同じ message が 2 回届くことがある。
dedup は 2 段階で行う:

- (a) idempotency_key を attribute に入れて、receiver 側で recently-seen set を持つ
  （メモリ上、TTL 1時間）
- (b) outbox に書く際のファイル名に idempotency_key を含める。既存ファイルがあれば skip

### 5. D-Mail .md の文字数制限

phonewave 自体には制限はないが、D-Mail を読む側 (paintress 等) の prompt window が
限度になる。Pub/Sub message 自体は 10MB まで OK だが、5本柱の prompt budget を
考えると **1 D-Mail あたり 100KB を上限の目安** とする。
超える場合は Cloud Storage に payload を載せて URL 参照 (将来の拡張)。

### 6. dmail-emitter の archive watch の罠

phonewave は archive に書いた後で outbox の元ファイルを消す。
このタイミングで emitter が両方の event を拾うと、同じ D-Mail を 2 回 publish する。
→ emitter は **archive のみ** を watch する（outbox は phonewave のもの）。

### 7. trace context propagation

✅ 実装済 (PR #8, ADR 0020 / 0021 / 0022)。詳細は
`experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md` と
`experiments/2026-05-05_cloudevents-adoption.md`。要点:

- 3 binary とも `OTEL_EXPORTER_OTLP_ENDPOINT` 切替で local Jaeger v2 と
  prod Cloud Trace の両対応 (`OTEL_SERVICE_NAME` の default も per-binary)
- Pub/Sub は `cloud.google.com/go/pubsub/v2 v2.6.0` の
  `EnableOpenTelemetryTracing: true` で W3C Trace Context を自動
  inject/extract (`googclient_*` attributes)。ADR 0013 の `traceparent` は
  ADR 0021 で実質削除済 (二重 inject 回避)
- 手動 span 一覧 (PR #8 完成形):

```
inbound (Slack -> 5 pillars):
  POST /slack/interactive             (otelhttp root)
   |- slack.verify_signature
   |- slack.handle_{dispatch,approval}_action  (sync validation only)
        +-- (goroutine, context.WithoutCancel で trace context 引き継ぎ)
            |- usecase.dispatch_agent_task
                 |- send dmail-inbound          (auto, pubsub/v2)
                    ~~ Pub/Sub bus boundary ~~
                    |- receive dmail-inbound    (auto, dmail-receiver 側)
                         |- dmail.receiver.on_message
                              attrs: outbox.filename / pubsub.message_id

outbound (5 pillars -> Slack):
  dmail.emitter.publish_file          (root, fsnotify event 起点)
   |- send dmail-outbound             (auto, pubsub/v2)
      ~~ Pub/Sub bus boundary ~~
      |- receive dmail-outbound       (auto, gateway 内 subscriber)
           |- dmail.outbound.on_message
                |- usecase.dispatch_result_handle
                     |- (chat.postMessage / approval publish のいずれか)
```

  Legend / 凡例:
  - root: 起点 span (上流 trace なし)
  - auto: pubsub/v2 が自動生成する span (自動生成スパン)
  - goroutine: HTTP response 終了後も走り続ける処理 (HTTP応答後に走り続けるゴルーチン)
- `just trace-up` で Jaeger 起動、`just trace-view` で UI 開封
- **5本柱との連結**: receiver が phonewave outbox に書く際に D-Mail
  frontmatter にも traceparent を書き込む必要 → **5本柱側の対応待ち**。
  本リポ側は Pub/Sub bus 1 跨ぎ範囲までは 1 trace で繋がる
- CloudEvents は ADR 0022 で **不採用** (現状維持)。再検討トリガーは ADR 内
- **goroutine 内 span flush** (Issue 0005): Slack 3 秒応答のため handler
  から spawn する goroutine の span が Cloud Run idle shutdown で lost
  する問題は ✅ 解消済 (PendingTracker pattern)。 handler の `go func()`
  は semgrep rule (`runops-concurrency-bare-goroutine-in-slack-handler`)
  で禁止、 必ず `h.goAsync(...)` 経由。 main shutdown 順序は
  `srv.Shutdown → pending.Wait → tp.Shutdown` で [4s/4s/2s] 予算配分
  (Cloud Run 10 秒 grace 内)。詳細は
  `experiments/2026-05-06_otel-goroutine-flush-cloudrun.md`

### 8. Slack response_url と chat.postMessage の使い分け

- `/agent paintress ...` の即時返信は response_url 一択
- 30分超えたら chat.postMessage 経由
- 失敗時の通知は両方試す（fallback）

### 8-prepre. Pub/Sub DLQ は consumer が居ないと発火しない (実体験)

PR #18 直後の prod 動作確認で `/runops sightjack approve-test-1` →
Approve で Pub/Sub publish 成功 (msg id 取得)。`max_delivery_attempts=5`
を超えれば DLQ alert が来ると期待したが、**consumer (exe-coder VM の
dmail-receiver) が deploy されていない**ので message は subscription
backlog に蓄積されるだけ。DLQ には流れず、`dmail-inbound-dlq-pull` も
0 件のまま。

これは Pub/Sub の正規仕様 (consumer の pull→nack のサイクルが無いと
delivery_attempt がカウントされない)。alert は 2 種類で相補的:

- `D-Mail DLQ message forwarded` — consumer 動作中の poison 検知
- `D-Mail subscription backlog stale` — consumer 不在を `oldest_unacked_message_age`
  > 1 日で検知 (PR #19 で追加)

retention が 14 日なので 14 日以上 dmail-receiver が deploy されない
場合は backlog から消失する。docs/runbooks/dlq.md の Trigger 節に詳細。

### 8-prepre2. Cloud Trace OTLP は `gcp.project_id` resource attribute を要求

実本番 deploy 後の Cloud Run ログで以下が連発:

```
traces export: rpc error: code = InvalidArgument
  desc = Resource is missing required attribute "gcp.project_id"
```

Cloud Trace の OTLP API (`telemetry.googleapis.com:443`) は **OTel
semconv の `cloud.account.id` ではなく、literal な `gcp.project_id`**
を resource attribute として要求する。`gcp.NewDetector()` を呼ぶ手も
あるが unit test が GCE metadata server に依存するので避け、`Config`
に `GCPProjectID` フィールドを追加して caller (3 binary) が
`os.Getenv("GOOGLE_CLOUD_PROJECT")` を渡す方式に統一 (PR #21)。

判別:
- Cloud Trace UI に **service が出てこない** + Cloud Run log に
  `Resource is missing required attribute "gcp.project_id"` がある
  → resource に gcp.project_id 不足 (PR #21 で修正)

### 8-pre. `/healthz` は Cloud Run / Knative の予約 path

Cloud Run の GFE (Google Front End) は `/healthz` を **system reserved**
として扱い、container に届く前に 404 (HTML 形式の Google generic error) を
返す。Phase 0 image 時代から同じだったが、PR #15 の post-deploy smoke で
初めて顕在化した。

**回避策**: 健全性確認 path は **`/_healthz`** を使う (`cmd/server/main.go`
で登録、`cd.yaml` の post-deploy smoke も `/_healthz` を叩く)。

判別方法 (再発時):
- `curl <URL>/<path>` の response が `<title>Error 404 (Not Found)!!1</title>`
  HTML だったら GFE が返している (container に届いていない)
- `text/plain; charset=utf-8` + `404 page not found` だったら container 側
  の Go ServeMux が返している (handler が登録されていないだけ)

### 9-pre. main promote 時の smoke と rollback

CD pipeline (`.github/workflows/cd.yaml`) の `deploy` job 末尾に
**post-deploy smoke** が入っている。3 項目を read-only で検査:

1. `/_healthz` 200 (Cloud Run / Knative の予約 path `/healthz` は GFE が intercept するので使わない)
2. `/slack/interactive` に invalid signature で 401 (Phase 0 regression)
3. `/slack/command` に invalid signature で 401 (Phase 1 regression)

どれか fail したら deploy job が fail し、main の HEAD revision は
古いままになる。Cloud Run の latest が新 revision に切り替わって
**しまった**直後に smoke が落ちた場合は、operator が手で trafifc を
戻す:

```bash
PROJECT=gen-ai-hironow
REGION=asia-northeast1
PREV=$(gcloud run revisions list --service=runops-gateway \
  --project=${PROJECT} --region=${REGION} \
  --filter='status.conditions.type=Active AND status.conditions.status=True' \
  --format='value(metadata.name)' --limit=2 --sort-by='~metadata.creationTimestamp' | tail -1)
gcloud run services update-traffic runops-gateway \
  --project=${PROJECT} --region=${REGION} --to-revisions=${PREV}=100
```

main 側の機能 (canary / migrate) を壊していない確認は CD smoke が担う。
Phase 1+ 機能 (Slash Command / dispatch / 4-eyes / OTel) の通電確認は
`docs/local-verification.md` の Pattern C/D/E で local 完結できる。

### 9. emitter は archive イベントだけを watch する (実体験)

Phase 2c で fsnotify Create が file open 完了より先に発火し、parse 失敗 → dedup
record で再試行を阻害するレースを smoke で踏んだ (commit `2fa02c7`)。教訓:

- `Create` だけでなく `Write` も watch する (atomic temp+rename だと最終 Write が来る)
- parse / read 失敗時は `seen` map から abs path を消す `clearOnFailure` で
  fsnotify 再来を許す
- integration test では **macOS 上の `go run` が SIGTERM を子に伝えない** ため
  `pkill -KILL -f "exe/dmail-..."` で殺し切る運用を `docs/local-verification.md`
  に書いておく (再走で stale daemon が subscription を hijack する)

### 10. 5本柱への変更を絶対に入れない

intent.md の優先順位 1 を破ると、自分で書いた roadmap を自分で壊すことになる。
5本柱の挙動が気に食わなくても、対応は **runops-gateway 側のラッパー** で行う。
5本柱の修正 PR は別の repo に切る。

### 11. dmail daemon を Coder workspace **container 内** に置きそうになる罠

Issue 0001 着手時、 当初は `cmd/dmail-receiver` / `cmd/dmail-emitter` を
ADR 0015 通り「exe-coder VM の host OS に GitHub Release バイナリで配布 +
systemd」 想定だった。 しかし dotfiles 側の構造調査で 2 つの構造ミスマッチが
判明:

- **5本柱 archive は exe-coder VM (control plane) には無い**。 各 workspace VM
  の中 (devcontainer) で 5本柱が動くので archive は per-workspace VM。 emitter は
  archive を fsnotify watch するので watch 対象が見えない場所には置けない
- **devcontainer 内 daemon は best practice 違反**。 supervisord は PID-1 問題で
  daemon 死を container 健康と誤認させる、 s6-overlay は devcontainer image
  rebuild が必要、 systemd-user は cgroup v2 / dbus 整備が要る = いずれも次善策

最終決定 (ADR 0023): 各 workspace VM の **host OS systemd** から
`docker run --rm` で別 container として起動 (再起動は systemd `Restart=on-failure`
が担当、 `--rm` と `--restart` は docker engine が同時指定を拒否する mutually
exclusive オプション)。 archive / outbox は host OS dir 経由で devcontainer +
dmail container 双方に bind mount。

→ **ハマる罠**: 「dotfiles repo を `coder dotfiles -y` で workspace に clone する
仕組みがあるから、 そこで runops-gateway も clone & go build すれば良いのでは」
と一見思える。 しかしそれは workspace **container 内** の話で、 daemon を
container 内に置く罠に直結する。 配置先はあくまで workspace VM の **host OS**
(= devcontainer の外側)、 binary は OCI image で AR から pull、 systemd unit が
`docker run` で起動。

Pub/Sub の挙動として、 同 subscription への複数 puller attach は **load-balanced**
(= 複数 workspace VM が同時稼働しても receiver は自動 multiplex、 race ではなく
正常動作)。 emitter 側は per-VM = 1 archive set なので watch 重複ゼロ。

詳細は [`experiments/2026-05-06_dotfiles-dmail-daemon-placement.md`](../experiments/2026-05-06_dotfiles-dmail-daemon-placement.md)
と ADR 0023。

### 12. dmail container nonroot uid 65532 vs host VM linux_user uid 1000 の write 衝突

Issue 0001 Phase 3 deploy verify (2026-05-06) で踏んだ permission
denied error。 distroless `:nonroot` image は uid 65532 固定で起動
する一方、 dotfiles の workspace VM startup-script は
`/var/lib/phonewave/{archive,outbox}` を `chown linux_user:linux_user`
(uid 1000-ish) で作成。 dmail-receiver container は uid 65532 で
書こうとして `permission denied: open /outbox/.tmp-...` で fail。

→ **fix**: `chmod 0777` (採用、 ADR 0023 Negative consequence 参照)。
   workspace VM が per-user + short-lived + tag:exe-workspace
   tailnet ACL 内 = trust boundary 狭いので acceptable。
   multi-tenant / 長寿命 化したら shared group + setgid 2775 へ
   refactor (= ADR 0023 の deferred refactor)。

---

## テスト戦略

### 既存（維持）

- ユニット: `just test`
- Lint: `just lint`
- シナリオ (runn): `just test-runn`
- スクリプト round-trip: `just test-scripts`

### 追加が必要

- **Phase 1**:
  - `internal/adapter/output/pubsub/` — Pub/Sub emulator を使った publish/subscribe
  - `internal/adapter/output/phonewave/` — 一時ディレクトリで atomic write 検証
  - `cmd/dmail-receiver/` — emulator + 一時 outbox で e2e
- **Phase 2**: Slash Command の HMAC 検証 + DispatchAgentTask UseCase のユニット
- **Phase 3**: Pub/Sub push subscription を `httptest.Server` で受けるテスト
- **Phase 4**: 承認フロー (Block Kit ボタン → D-Mail 発行) の e2e

### 触れないこと

- 既存の approve/deny テストはそのまま。新機能のために既存テストを書き換えない
- 既存 runn シナリオに `/agent` を混ぜない（新シナリオファイルを足す）
- 5本柱本体のテストは触らない（別 repo で管理）

---

## 関連リポジトリと変更が必要な場所

| リポジトリ | 変更箇所 | 内容 |
| --- | --- | --- |
| `hironow/runops-gateway` | このリポジトリ | コード本体 (cmd/dmail-receiver, cmd/dmail-emitter, adapter 群) |
| `hironow/dotfiles` | `tofu/exe/startup-script.tpl` | dmail-receiver / dmail-emitter systemd 追加 |
| `hironow/dotfiles` | `tofu/exe/iam.tf` | exe-workspace SA に Pub/Sub subscriber 権限 |
| `hironow/dotfiles` | `exe/coder/setup/phonewave-init.sh` | runops-gateway 用の outbox + SKILL.md を `phonewave init` 時に追加 |
| `hironow/runops-gateway` | `tofu/pubsub.tf` (新規) | Pub/Sub topology |
| `hironow/runops-gateway` | `docs/adr/0012-no-new-dmail-kinds.md` | ADR (決定 A) |
| `hironow/runops-gateway` | `docs/adr/0013-pubsub-bridge-for-outbox.md` | ADR (決定 B) |
| `hironow/runops-gateway` | `docs/adr/0014-slack-notification-centralized.md` | ADR (決定 C) |
| 5本柱 (sightjack/paintress/...) | **変更なし** | 5本柱への変更は禁止 |

`hironow/dotfiles` への変更は別 PR で先行マージしてから、
runops-gateway 側の Phase 1 実装に入る。

---

## デバッグの起点

何かおかしいときに最初に確認する場所:

1. **Slack 受信できているか**: Cloud Run のログで `/slack/interactive` or `/slack/command` への POST を確認
2. **HMAC 検証が通っているか**: 同じくログで `signature mismatch` を grep
3. **Pub/Sub publish が成功しているか**: GCP Console の Pub/Sub topic で publish 数 + ack 数を見る
4. **dmail-receiver が動いているか**: `journalctl -u dmail-receiver -f` (exe-coder VM 上)
5. **outbox に .md が現れているか**: `ls /var/lib/phonewave/runops-gateway/outbox/`
6. **phonewave が配送しているか**: `journalctl -u phonewave -f` で delivery log
7. **5本柱が受け取っているか**: 各ツールの `inbox/` を `ls`、または各ツールのログ
8. **逆流が動いているか (Phase 3)**: dmail-emitter のログ + Pub/Sub `dmail-outbound` 統計
9. **trace の連結**: Jaeger UI (`http://localhost:16686` via tunnel) で
   gateway → receiver → phonewave → 5本柱 のスパンが繋がるか確認

これらが全て OK なのに動かない場合、Slack App 側の Request URL 設定 (Slash Command と
Interactive 両方) を疑う。

---

## ローカル動作確認

### 既存パターン（維持）

詳細は [`docs/local-verification.md`](local-verification.md) を参照。

| パターン | 概要 |
|---|---|
| **A. 操作対象なし** | GCP・Slack 不要。`just test-runn` + `--dry-run` + curl で署名検証とペイロード構造を確認 |
| **B. 操作対象あり (CLI)** | `go run ./cmd/runops approve ... --no-slack` で実 GCP を操作 |
| **B. 操作対象あり (Slack E2E)** | `tailscale funnel 8080` でローカルサーバーを公開し、実 Slack ボタンから GCP 操作まで全パスを確認 |

### Phase 1 (新) で追加するパターン

| パターン | 概要 |
|---|---|
| **C. Slash Command の HMAC 検証** | curl で署名付き POST を `/slack/command` に送り、Block Kit JSON が返ることを確認 |
| **D. dispatch_approve thread reply** | 確認ボタンの value を含む POST を `/slack/interactive` に送り、stub log + thread reply を確認 |

### Phase 2 以降で完成したパターン (✅ 実装済、`docs/local-verification.md` 参照)

| パターン | 概要 |
|---|---|
| **E-1. Pub/Sub bridge の自動 integration test** | `just pubsub-up && just pubsub-init && just test-integration` で publisher / receiver / emitter / outbound subscription / HIGH severity approval の 6 round-trip を全部 emulator で検証 |
| **E-2. 手動 receiver smoke** | `cmd/dmail-receiver` を一時 outbox に向けて起動 + `scripts/smoke/dispatch.go` で 1 件 publish |
| **E-3. 手動 emitter smoke** | `cmd/dmail-emitter` を一時 archive に向けて起動 + heredoc で `.md` を投入 |
| **E-4. Phase 3 thread reply smoke** | gateway を `PUBSUB_DMAIL_OUTBOUND_SUB` 込みで起動 + mock Slack 受信 server で chat.postMessage を観察 |
| **E-5. OTel trace を Jaeger で確認** | `just trace-up` → 3 binary を `OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317` 込みで起動 → Slack POST or smoke スクリプトで dispatch を 1 件流す → `http://localhost:16686` で `runops-gateway` / `dmail-receiver` / `dmail-emitter` の 3 service が表示され、上記 inbound/outbound 2 trace tree がそれぞれ 1 つの trace_id で繋がっていることを確認 |

### 5本柱まで含めた完全 e2e (📝 future)

| パターン | 概要 |
|---|---|
| **F. phonewave 経由 e2e** | E-2 の outbox を含めて `phonewave init` し、paintress inbox まで届くかを確認 |
| **G. 5本柱の即時起動** | F の流れで paintress を `paintress run` 起動済みにして、specification を実際に処理するか確認 |
| **H. HIGH severity 4-eyes 完走** | amadeus が convergence を出す → gateway が approval ボタン → 別 user が Approve → ack が amadeus inbox に届く |

---

## 次にこのドキュメントを更新するタイミング

- **dmail-receiver / dmail-emitter** が `hironow/dotfiles` で systemd
  unit として deploy されたら、本番 Pub/Sub backlog が消化されることを
  確認 → ハマりどころ集 8-prepre (DLQ は consumer 必須) に「実際に DLQ
  に流れるパターンを観察した時の所感」を追記
- **Phase 3 outbound StreamingPull** を実運用化したら
  (`gh variable set CLOUD_RUN_MIN_INSTANCES 1`)、 cold start / 課金 /
  trace の繋がりの実体験を追記
- **5本柱本体** が D-Mail frontmatter の `traceparent` を読んで span を
  再開する対応を入れたら、ハマりどころ 7 の「5本柱との連結」を解消済に
  書き直す
- 5本柱までの完全 e2e (パターン F/G/H) を 1 度通したら、その手順を
  `docs/local-verification.md` に昇格させ、ここからは削る
- handler/receiver/emitter 内の細かい span (handleDispatchAction,
  receiver.OnMessage, atomic write 等) は既に PR #8 で追加済。新規 span
  を増やしたら本セクションでなく `docs/local-verification.md` の
  Pattern E-2/3/4 に追記
- **Cloud Trace の実 trace 検証** が完了したら (gcp.project_id 修正後の
  PR #22 deploy 直後の時点では Cloud Trace UI に反映前)、span tree の
  実スクショを `experiments/2026-05-05_otel-cloud-run-pubsub-jaeger.md`
  か本ファイルに 1 件添付
- リポジトリリネームの判断が出たら「全体ステータス」の冒頭に決定を書く

更新は破壊的でよい（過去の状態を残さない）。Git 履歴がそれを担う。

---

## 連絡先・参照

- 設計の意図: `docs/intent.md`
- 既存 ADR: `docs/adr/0001-0022` (0001-0019 は Phase 1〜4a、0020-0022 は OTel + CloudEvents 検討)
- ローカル検証手順: `docs/local-verification.md`
- Slack 設定: `docs/slack-setup.md`
- 残作業 (cross-repo 依存): `docs/issues/`
- 運用 runbook: `docs/runbooks/`
  - `dlq.md` — DLQ alert 発火時の triage 手順
- 調査ノート: `experiments/`
  - `2026-05-05_otel-cloud-run-pubsub-jaeger.md` — OTel ベスプラ調査 (ADR 0020 のインプット)
  - `2026-05-05_cloudevents-adoption.md` — CloudEvents 採用検討 (ADR 0022 のインプット)
  - `2026-05-05_pubsub-dlq-terminal-sink.md` — DLQ pull subscription + alert (`tofu/monitoring.tf` のインプット)
- 5本柱の README:
  - `/Users/nino/tap/sightjack/README.md`
  - `/Users/nino/tap/paintress/README.md`
  - `/Users/nino/tap/amadeus/README.md`
  - `/Users/nino/tap/dominator/README.md`
  - `/Users/nino/tap/phonewave/README.md`
- exe.hironow.dev 全体図: `/Users/nino/dotfiles/exe/docs/architecture.md`

困ったら `docs/intent.md` を読み直す。それでも解決しない場合は、
Phase 0 の動作確認手順（既存 README）まで戻る。

---

## Token broker (refs#0007) — 機能完成 (2026-05-08)

5 本柱 D-Mail Dispatcher とは独立した別機能 (refs#0007) として、4 caller
type (human-operator / gateway-service / workspace-daemon / ai-agent)
が `POST /broker/token` で短期 GitHub installation token を受け取る経路。
2026-05-08 に **PR #53-#82 の 28 PR 連続着地で機能完成** し、operator が
env を設定するだけで activate 可能な状態になった。

### 28 PR の進行 (2026-05-07 → 2026-05-08)

| Phase | PR | 内容 |
|---|---|---|
| 0 | #53 | release-gate enforcement (ADR 0031, 0033) |
| (fix) | #55 | release-gate rule self-fix (go-github invalid pattern) |
| (chore) | #54 | lint policy alignment (`just semgrep --severity ERROR`) |
| 1a | #56 | Domain types + port (grant matrix, audit_fingerprint) |
| 1b | #57 | Usecase orchestration (5-stage Mint pipeline) |
| 2a | #58 | In-memory token cache + singleflight |
| 2b-1 | #59 | GitHub broker orchestration (per-project repo binding) |
| 2c-1 | #60 | AI agent session domain + port |
| 3a | #61 | HTTP handler `POST /broker/token` |
| 2d-1 | #62 | IdentityClaims domain helper |
| 2d-2a | #63 | Delegated agent verifier (ai-agent) |
| 4-1 | #64 | ADR 0032 grant matrix pin (Accepted) |
| 2d-2b | #65 | JWKsVerifier RS256 (keyfunc/v3 + golang-jwt/v5) |
| 2d-2c | #66 | Gcloud identity verifier (human-operator) |
| 2d-2d | #67 | CloudRun IAM verifier (gateway-service) |
| 2d-2e | #68 | Workload identity verifier (workspace-daemon) |
| 2c-2-1 | #69 | In-memory agent session registry |
| 2b-2-1 | #70 | Ghinstallation prod minter |
| 3b-1 | #71 | Authenticator interface widen (project_id + tool) |
| 3b-2 | #72 | ChainAuthenticator (4 caller dispatch by header) |
| 3b-3a | #73 | BrokerConfig env var loader (5 required + 2 default + 2 optional) |
| (refactor) | #74 | github Minter + ctor export |
| 3b-3b-1 | #75 | composition.NewBrokerDependencies wiring helper |
| 3b-3b-2 | #76 | cmd/server mount (opt-in pattern, broker reachable) |
| 2b-2-2a | #77 | PrivateKeyFetcher port + File adapter |
| 2b-2-2b | #78 | Secret Manager fetcher + dev/prod selector |
| 2c-2-2-1 | #79 | Firestore agent session registry impl + emulator integ test |
| 2c-2-2-2 | #80 | Firestore selector + cmd/server lifecycle |
| 4-2 | #81 | IaC: Secret Manager secret + Cloud Run IAM binding |
| (docs) | #82 | env-var contract + 4-step rollout sequence |

### Production rollout (operator scope, 4 step)

```bash
# 1. Secret Manager + IAM binding を作成
cd tofu && tofu apply

# 2. GitHub App private key を out-of-band で upload (Terraform state 外)
gcloud secrets versions add github-app-private-key --data-file=/path/to/github-app.pem

# 3. Cloud Run service env vars (詳細は docs/runops-gateway-env-vars.md)
gcloud run services update runops-gateway --update-env-vars=...

# 4. Re-deploy。 構造化ログ "token broker registered (#0007)" 確認
```

ロールバック: `BROKER_AUDIENCE` を unset すると broker は disable され、既存
Slack / admin endpoint は影響を受けない (= opt-in pattern, Phase 3b-3b-2)。

### 残 work

- **Phase 3c** (full integration test, real GitHub App test secret): cdr workspace で operator が走らせる scope。 既存 PR #61 handler test + PR #75 composition test + PR #79 emulator integration test で coverage 済みで、 production block ではない。
- **Pub/Sub emulator topic init failure** (CI flaky): broker 進行中に観察された別 technical debt。 broker と独立、 別 chore PR で修正予定。

### 重要な architectural insight (token broker 進行で判明)

- **release-gate (ADR 0033) self-fix path bug**: Phase 0 で broken rule を merge した PR #53 直後、 base-ref-read 設計のため self-fix PR (#55) も同 broken rule で fail-closed。 GitHub UI 上 `--squash` merge で 1 度だけ bypass し正常状態へ復帰。 ADR 0034 (rule parse error → bootstrap exception) を残作業として記録。
- **paths.yaml glob 設計**: `agent_session_registry*` (prefix) は単一 canonical ファイル前提で、 `in_memory_agent_session_registry.go` 等の多 impl に対応せず → `*agent_session*` (substring) へ broaden (PR #69 内で同梱 fix)。 同様の prefix-only glob pattern bug が他 path に存在しないか今後 audit 推奨。
- **secure-by-default lib 採用効果**: `keyfunc/v3` + `golang-jwt/v5` で自前 RSA + JWT parse 約 150 行を 80 行に圧縮、 `alg=none` attack 防御 + kid rotation も lib 内蔵。 ghinstallation/v2 が transitive で go-github/v66 → /v84 への migrate を引いた (= secure lib のメンテ追従コストとのトレードオフ)。

### Follow-ups (2026-05-08、 broker 28 PR 着地後)

token broker 機能完成後の周辺整備として 4 PR + 1 fix-up 着地:

| PR | 内容 |
|---|---|
| #84 | **ADR 0034** (Proposed): release-gate rule parse error bootstrap exception。 Phase 0 の self-fix path bug (PR #55 の GitHub UI 一回限り bypass) を architectural fix として pin、 release-gate.yaml への workflow 変更は ADR Accepted 後に別 PR で着手 |
| #85 | paths.yaml chore: `docs/adr/0034-*` を auth_boundary glob に追加 (= Phase 0 / ADR 0033 で 0031/0032/0033 だけ enumerate していた漏れの follow-up)。 future enhancement として class-based pattern (`docs/adr/00[3-9][0-9]-release-gate*`) 化を deferred として note |
| #86 | **Pub/Sub emulator topic init failure 修復** (token broker 30 PR で 5+ PR 観察された persistent flaky)。 3 commit progression: (1) `\|\| true` 排除で fail-loud → (2) curl `--write-out %{http_code}` の retry-per-emit collapse → (3) `docker container healthy ≠ REST listener ready` race condition 吸収 (60s probe loop with curl exit-code semantics)。 PR 自身で両 trigger Pub/Sub pass 確認済み |
| (refs commit) | `tap/refs/docs/issues/0007-runops-gateway-github-app-secret-manager.md` Status 🟡 → 🟢 完了。 12 受入基準すべて [x] (Phase 3c integration test 1 件のみ admin scope 明記)。 28 PR table + production rollout 4-step を contained |

### Follow-ups で見えた追加 architectural insight

- **ADR-implementation 分離 pattern**: ADR 0034 を Proposed で pin、 implementation は別 PR (= Accepted 後)。 architectural decisions と code changes の cadence 分離で review window を確保。
- **Russian doll bug pattern**: Pub/Sub flake fix で `\|\| true` 排除 → curl write-out repetition bug 露出 → http_code collapse fix で false positive 修復 → 真の race condition 直視可能 → probe loop で吸収。 各 commit が前 fix の ulterior bug を露出させる sequence は fail-loud principle の威力 (= silent failure では observation 不可)。
- **`docker container healthy` の認識限界**: HEALTHCHECK は supervisor 単位、 multi-process container (Firebase emulator は Pub/Sub + Firestore + UI 同居) では per-service ready check (= REST endpoint actual reach) を独自実装が現実的。

### 関連 docs

- `docs/runops-gateway-env-vars.md` — Token broker 全 env var + production rollout sequence
- `docs/adr/0032-token-broker-caller-grant-matrix.md` — 4 caller × 5 tool grant matrix の Accepted ADR
- `docs/adr/0031-production-deploy-gate-on-develop.md` + `0033-release-gate-path-externalization.md` — release-gate workflow design
- `docs/adr/0034-release-gate-rule-parse-error-bootstrap-exception.md` — Phase 0 self-fix path bug の Proposed ADR (implementation 待ち)
- `tofu/secret_manager_github_app.tf` — Secret Manager secret + Cloud Run IAM binding
- `scripts/init-pubsub.sh` — fail-loud + retry + REST API readiness probe (Pub/Sub flake fix 完了)
