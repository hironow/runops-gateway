# Issue 0020: Phase 2a — Pub/Sub publish 経路の確立

> **完了 (2026-05-05, ✅ `feat/long-running-dispatch` ブランチ)**

## Goal

Phase 1 で stub に留めていた `port.Dispatcher` の本実装として、Cloud Pub/Sub
publisher を導入し、Slack `/agent` dispatch を `dmail-inbound` topic に
publish する経路を確立する。

ADR 0013 (Pub/Sub bridge) の **publish 側のみ** を本 issue でカバー。
**dmail-receiver (Phase 2b)** と **dmail-emitter (Phase 2c)** は別 issue。

## 設計

### 追加した型

- `domain.DMailKind` enum (6 種、ADR 0012 に従い拡張禁止)
- `domain.ParseDMailKind` 厳格パーサ
- `domain.DMail` (ID / Kind / Target / Source / IdempotencyKey / Body / Metadata)
- `domain.DMail.RenderMarkdown` で frontmatter + body の .md を deterministic に出力
- `domain.DMail.OperationKey` 文字列 (in-process dedup 用)
- `port.DMailPublisher` interface (`PublishDMail(ctx, m) (id, error)`)

### 追加した adapter

- `internal/adapter/output/pubsub/Publisher`
  - cloud.google.com/go/pubsub/v2 を使用 (v1 は deprecated)
  - `EnableMessageOrdering = true`、ordering key は `DMail.Target` (target_tool 単位で serialize)
  - publishFunc を内部に持ち、unit test では fake を inject
  - 統合テストは Pub/Sub emulator (PUBSUB_EMULATOR_HOST) で実行
- `internal/adapter/output/dispatcher/PubsubDispatcher`
  - `port.Dispatcher` 実装、内部で `port.DMailPublisher` を呼ぶ
  - DispatchRequest → DMail 変換: kind=specification / target=role /
    source="runops-gateway-slack" / Metadata.requester_id

### cmd/server feature flag

```
DISPATCHER_BACKEND=stub   (default、Phase 1 互換)
DISPATCHER_BACKEND=pubsub (Phase 2a、PUBSUB_PROJECT_ID + PUBSUB_DMAIL_INBOUND_TOPIC 必須)
```

不正な値や必須 env 不足は起動時エラー (loadConfig で早期 return)。

### Pub/Sub message 仕様 (ADR 0013 準拠)

| Attribute | 値 |
|---|---|
| `kind` | "specification" 等 (DMailKind 文字列) |
| `target_tool` | "paintress" / "sightjack" / "amadeus" / "dominator" |
| `source` | "runops-gateway-slack" |
| `dmail_schema_version` | "1" |
| `idempotency_key` | DispatchRequest.IdempotencyKey |
| その他 (forwarded) | `requester_id`, `traceparent` 等 (Metadata から自動転送) |

| Field | 値 |
|---|---|
| `data` | DMail.RenderMarkdown() のバイト列 (frontmatter + body) |
| `OrderingKey` | DMail.Target (target_tool ごとに serialize) |

## TDD カバレッジ

- domain: kind enum / parser / RenderMarkdown / OperationKey 5 件
- pubsub publisher: ADR attributes / data round-trip / ordering key / error
  propagation / zero-value rejection 5 件
- dispatcher.PubsubDispatcher: translation / zero-role rejection / publish
  error propagation 3 件

統合テスト (emulator 使用) は本 PR では未実装。docs/local-verification.md
に手動手順を記載予定 (Phase 2b で receiver と一緒に検証する想定)。

## 残課題 (Phase 2 の他 issue へ)

- **Phase 2b (Issue 0021 予定)**: exe-coder VM 上の `dmail-receiver` daemon
  (Pub/Sub StreamingPull → phonewave outbox に atomic write)
- **Phase 2c (Issue 0022 予定)**: exe-coder VM 上の `dmail-emitter` daemon
  (5本柱 archive を fsnotify 監視 → Pub/Sub `dmail-outbound` publish)
- **Infra (別 PR)**: tofu で Pub/Sub topic / subscription / SA 権限を作成、
  exe-coder VM の workload identity SA に subscriber 権限付与
- **本番化**: Cloud Run env vars の `DISPATCHER_BACKEND=pubsub` 切替は
  Phase 2b/2c が動いてから

## 関連

- ADR 0012: 新しい D-Mail kind は追加しない (specification を流用)
- ADR 0013: Pub/Sub bridge (本 issue で publish 側を Accepted に昇格)
- ADR 0015: dmail-receiver / dmail-emitter は本リポで管理 (依然 Proposed)
