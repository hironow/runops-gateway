# 0009. カナリアリリース前の DB マイグレーション二重確認

**Date:** 2026-04-02
**Status:** Accepted

## Context

Cloud Build が送信する Slack メッセージには「DB Migration」と「10% Canary」の 2 つのボタンが
横並びで表示される。現在の実装では、DB マイグレーションを実施していない状態でカナリアボタンを
押しても何も確認されずに `ShiftTraffic` が実行される。

スキーマ変更を含むデプロイでこの順序が逆になると本番障害に直結するため、
ガードレールが必要である。

### 解決すべき問い

1. **「マイグレーション未実施」をどう検知するか**
2. **確認ダイアログをどのタイミングで出すか**
3. **マイグレーション完了後にどうボタンを更新するか**

---

## 検討した選択肢

### 案 A: 常にダイアログを表示（always-on confirm）

Slack Block Kit の `confirm` オブジェクトをカナリアボタンに付与する。
ボタンを押すたびに Slack クライアントが確認ダイアログを表示する。

```json
{
  "type": "button",
  "text": "10% Canary",
  "confirm": {
    "title": "続行しますか？",
    "text": "DBマイグレーションを実施済みですか？未実施の場合は先に実行してください。",
    "confirm": "はい、続行します",
    "deny": "キャンセル"
  }
}
```

**利点**: 実装が最小（gateway の変更ゼロ、Cloud Build の Block Kit のみ変更）  
**欠点**: マイグレーション不要なデプロイや 2 回目以降のカナリアステップでも毎回出る。フリクションが大きい。

### 案 B: `migration_done` フラグ + 条件付きダイアログ（採用）

`actionValue` に `migration_done bool` フィールドを追加する。

- Cloud Build が初期メッセージを送るとき: カナリアボタンの値に `migration_done: false`
- `migration_done: false` のボタンには Slack `confirm` を付与する
- マイグレーション完了後: gateway が `response_url` でメッセージを更新し、`migration_done: true` のカナリアボタン（confirm なし）に置き換える

マイグレーション完了後は確認なしでカナリアボタンを押せる。意図した操作フローが自然に強制される。

**利点**: 状態が正確に反映される。完了後はフリクションなし。  
**欠点**: 実装範囲が広い（`actionValue`、blockkit、notifier、usecase、cloudbuild.yaml すべてに変更が必要）。

---

## Decision

**案 B（`migration_done` フラグ＋条件付きダイアログ）を採用する。**

### 実装方針

#### 1. `internal/adapter/input/slack/handler.go` — `actionValue` の拡張

```go
type actionValue struct {
    ResourceType    string `json:"resource_type"`
    ResourceName    string `json:"resource_name"`
    Target          string `json:"target"`
    Action          string `json:"action"`
    IssuedAt        int64  `json:"issued_at"`
    MigrationDone   bool   `json:"migration_done"`   // 追加
    // マイグレーション完了後にカナリアボタンを再構築するために必要な情報
    NextServiceName string `json:"next_service_name"` // 追加（job actionのみ使用）
    NextRevision    string `json:"next_revision"`     // 追加（job actionのみ使用）
    NextAction      string `json:"next_action"`       // 追加（job actionのみ使用、例: "canary_10"）
}
```

job action に `next_*` フィールドを持たせることで、マイグレーション完了後に gateway が
カナリアボタンを自力で再構築できる。

#### 2. `internal/adapter/output/slack/blockkit.go` — 条件付き confirm

`BuildApprovalMessage` の approve ボタン生成ロジックで、
`ApproveValue` が `migration_done: false` かつ `resource_type: service` のときに
`confirm` オブジェクトを付与する。

実装上は `DeploymentPayload` に `RequireConfirm bool` フィールドを追加し、
Block Kit 生成時に判断する。これにより blockkit 層が `actionValue` の内部フォーマットを
知る必要がなくなる。

```go
type DeploymentPayload struct {
    // ... 既存フィールド ...
    RequireConfirm bool  // true のとき approve ボタンに confirm オブジェクトを付与
}
```

#### 3. `internal/usecase/runops.go` — `approveJob` でのカナリアボタン再投稿

`approveJob` がマイグレーション成功後に `OfferNextCanary`（新メソッド、または `OfferContinuation` の拡張）を呼び、
`response_url` を使ってメッセージを「✅ マイグレーション完了、カナリア実行可能」に更新する。
更新後のメッセージには `migration_done: true` のカナリアボタンを含む。

このとき UseCase は `req` の `NextServiceName / NextRevision / NextAction` フィールドを使って
新しい `ApprovalRequest` を組み立て、Notifier に渡す。

#### 4. `cloudbuild.yaml` — 初期メッセージの変更

カナリアボタンの action value に `migration_done: false` を追加。
job の action value に `next_service_name`, `next_revision`, `next_action` を追加。

```bash
SRV_ACTION=$(printf '{"resource_type":"service","resource_name":"%s","target":"%s",
  "action":"canary_10","issued_at":%s,"migration_done":false}' ...)

JOB_ACTION=$(printf '{"resource_type":"job","resource_name":"%s","action":"migrate_apply",
  "issued_at":%s,"next_service_name":"%s","next_revision":"%s","next_action":"canary_10"}' ...)
```

### メッセージ遷移フロー

```
Cloud Build 初期メッセージ
  [ 1. DB Migration ]  [ 2. 10% Canary (confirm付き) ]  [ Deny ]

  ↓ ユーザーが「DB Migration」を押す
  ↓ gateway が migration 実行

  ↓ 完了後、同じ response_url でメッセージを更新

更新後メッセージ
  ✅ DB Migration 完了 (by @user)
  [ 10% Canary (confirmなし) ]  [ Deny ]

  ↓ ユーザーが「10% Canary」を押す（確認なし）
  ↓ gateway が canary 実行

  → ADR 0008 の段階的昇格フローへ続く
```

カナリアボタンを先に押した場合（`migration_done: false`）:

```
Slack クライアントが confirm ダイアログを表示
  「DBマイグレーションを実施しましたか？」
  [ はい、続行します ]  [ キャンセル ]

  ↓ 「はい」を押した場合のみ gateway に POST が届く
  ↓ gateway は通常通り ShiftTraffic を実行
```

---

## Consequences

### Positive
- マイグレーション完了後はフリクションなく canary を実行できる
- マイグレーション未実施でも「はい」を押せば続行できる（スキーマ変更なしのデプロイに対応）
- Slack の UI が状態を正確に反映する（ボタンの有無・ダイアログの有無）
- 段階的カナリア（ADR 0008）とのメッセージ更新フローが統一される

### Negative
- `actionValue` に `next_*` フィールドが追加されて構造が複雑になる
- `approveJob` がカナリアの情報（service name / revision）を知る必要が生じ、ジョブとサービスが結合する
- スキーマ変更のないデプロイでも `migration_done: false` でメッセージが届くため、Cloud Build 側で制御が必要（将来的には `include_migration: bool` を substitution で指定する）

### Neutral
- `migration_done` フィールドは後方互換性がある（旧クライアントは無視する）
- `RequireConfirm` の判定は blockkit 層で完結するため、UseCase の変更は最小限

## 関連 ADR

- ADR 0008: Progressive Canary Rollout（`OfferContinuation` の設計を共有する）
