# handover — 5本柱 D-Mail Dispatcher 拡張の実装状況と引き継ぎ

## このドキュメントの位置づけ

`docs/intent.md` が「なぜ・何を」を扱うのに対し、本ドキュメントは
「どこまで実装済みで、何が残っていて、どこに罠があるか」を扱う。

新しい session を開始するとき、または将来このリポジトリに戻ってくるとき、
最初に読むべきページとして書く。日付ベースで上書き更新する想定。

最終更新: 2026-05-05

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

> **方針変更 (2026-05-05)**: D-Mail / Pub/Sub bridge は粒度が大きいため、
> Phase 1 を **「シンプル経路」(Slack 内で完結する stub dispatch)** に差し替えた。
> 旧 Phase 1〜4 (D-Mail 系) は Phase 2 以降に押し下げ、関連 ADR (0012/0013/0015) は
> **Proposed (draft)** ステータスに降格。Phase 1 完了後に再評価する。

| Phase | 状態 | 内容 |
| --- | --- | --- |
| Phase 0 | ✅ 完了 | 既存 ChatOps（Cloud Run カナリア・DB マイグレ） |
| **Phase 1 (新)** | ✅ 完了 (2026-05-05, develop merged) | **シンプル経路**: `/slack/command` → **Block Kit 確認 → Approve クリック (`/slack/interactive`)** → DispatchAgentTask → thread reply。Codex Review round 2/3/4 の致命指摘 **5 件** はすべて修正済み |
| **Phase 2a** | 🟡 実装中 (`feat/long-running-dispatch`) | Issue 0017 chat.postMessage fallback (ADR 0017) ✅ + PubsubDispatcher publish 経路 ✅ + `DISPATCHER_BACKEND=stub\|pubsub` 切替 ✅ |
| Phase 2b | 📝 draft | exe-coder VM 上の `dmail-receiver` daemon (Pub/Sub pull → phonewave outbox atomic write) |
| Phase 2c | 📝 draft | exe-coder VM 上の `dmail-emitter` daemon (5本柱 archive → Pub/Sub `dmail-outbound` publish) |
| Phase 3 | 📝 draft | dmail-outbound subscription + InteractiveHandler への結果 thread reply (Slack 集約) |
| Phase 4 | 📝 draft | HIGH severity approval gate + 本番化 (4-eyes、preempt 解除) |

「設計済 / 未着手」は intent.md と本ドキュメントに方針が書かれているが
コードに手がついていない状態。
「draft」は Phase 1 完了後に再評価する想定で、現時点では深掘りしない。

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

詳細は **[`docs/issues/0018-simple-message-path.md`](issues/0018-simple-message-path.md)**
を参照（フロー図・コード差分・TDD 計画・ローカル動作確認手順を含む）。

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

Pub/Sub message の attribute に `traceparent` を入れて、receiver/emitter で復元する。
5本柱の OTel スパンと連結するには、receiver が phonewave outbox に書く際に
traceparent を D-Mail frontmatter にも書き込む必要がある（5本柱が読んで span に紐付ける）。
ただし 5本柱側でこの propagation が未実装なら、Phase 1 では gateway 側のスパンだけで止まる。

### 8. Slack response_url と chat.postMessage の使い分け

- `/agent paintress ...` の即時返信は response_url 一択
- 30分超えたら chat.postMessage 経由
- 失敗時の通知は両方試す（fallback）

### 9. Phase 1 を終わらせずに Phase 2 に入らない

Slack 連携 (Phase 2) を先に作りたくなるが、Pub/Sub topology が動いていない状態で
Slack 側を書くと、ローカルでテストできずデバッグが破綻する。
Phase 1 の `gcloud pubsub topics publish dmail-inbound --message=...` で 5本柱まで
届くことを CLI で確認してから Phase 2 に移る。

### 10. 5本柱への変更を絶対に入れない

intent.md の優先順位 1 を破ると、自分で書いた roadmap を自分で壊すことになる。
5本柱の挙動が気に食わなくても、対応は **runops-gateway 側のラッパー** で行う。
5本柱の修正 PR は別の repo に切る。

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

### Phase 2 以降で追加するパターン (draft)

| パターン | 概要 |
|---|---|
| **E. Pub/Sub bridge の e2e (emulator)** | `gcloud beta emulators pubsub` + `dmail-receiver` をローカル起動し、`/tmp/test-outbox` に書き込まれることを確認 |
| **F. phonewave 経由 e2e** | E の outbox を含めて `phonewave init` し、paintress inbox まで届くかを確認 |
| **G. 5本柱の即時起動** | E/F の流れで paintress を `paintress run` 起動済みにして、specification を実際に処理するか確認 |

---

## 次にこのドキュメントを更新するタイミング

- Phase 1 が完了したら「Phase 1: ✅ 完了」に更新し、ハマりどころ集を実体験ベースで書き直す
- ADR が起票されたら本ドキュメントの「関連 ADR」リンクを足す
- リポジトリリネームの判断が出たら「全体ステータス」の冒頭に決定を書く

更新は破壊的でよい（過去の状態を残さない）。Git 履歴がそれを担う。

---

## 連絡先・参照

- 設計の意図: `docs/intent.md`
- 既存 ADR: `docs/adr/0001-0011`
- 5本柱の README:
  - `/Users/nino/tap/sightjack/README.md`
  - `/Users/nino/tap/paintress/README.md`
  - `/Users/nino/tap/amadeus/README.md`
  - `/Users/nino/tap/dominator/README.md`
  - `/Users/nino/tap/phonewave/README.md`
- exe.hironow.dev 全体図: hironow/dotfiles の `exe/docs/architecture.md`

困ったら `docs/intent.md` を読み直す。それでも解決しない場合は、
Phase 0 の動作確認手順（既存 README）まで戻る。
