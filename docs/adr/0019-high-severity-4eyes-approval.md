# 0019. HIGH severity D-Mail には Slack 4-eyes approval を要求する

**Date:** 2026-05-05
**Status:** Accepted

## Context

Phase 3 (ADR 0018) で 5本柱 → Pub/Sub → gateway → Slack thread reply の
復路が動くようになった。`amadeus` の `convergence` D-Mail などはアラート
レベルの強弱があり、HIGH severity (`metadata.severity=high`) は **人間に
即時アクションを要求** する性質を持つ。

paintress 自身も `docs/approval-contract.md` で「HIGH severity は人間
承認を経てから後続処理を進める」契約を定めている (3-way: Slack /
Telegram / Discord)。Phase 1 で runops-gateway は既に dispatch_approve
で `requester == approver` の guard を入れている (Codex round 4 F-7) が、
HIGH severity の convergence は **真の 4-eyes** を要求する: 元 dispatch
を出した本人ではなく **別の operator が承認** する仕組みが必要。

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | HIGH severity でも普段の thread reply のみ。承認は外部 (Linear / GitHub) で実施 |
| B | Slack thread reply に Block Kit Approve/Deny ボタンを付け、別 user の click のみ accept |
| C | Telegram bot 等の外部 channel に escalation (paintress companion 流用) |

### 案 A の問題点

- Phase 4 の goal は「production-ready な agent ops」。HIGH severity を
  alert message のまま放置すると、operator が見逃した時のリスクが大きい
- Slack 側で完結しないと UX が悪い (channel 切替が摩擦)

### 案 C の問題点

- ADR 0014 で **Slack に集約** することを既に決めている
- paintress companion (Socket Mode) は Cloud Run と相性が悪い (ADR 0014)
- 通知 channel を増やすと運用コストが増える

## Decision

**HIGH severity convergence D-Mail を gateway が受信したとき、Slack thread
に通常 reply + Block Kit Approve/Deny ボタンを post する。**
承認者は **元 dispatch の requester とは別の Slack user** でなければならない。

### 採用するアーキテクチャ

```
+--------------+   convergence (severity=high)   +--------------------+
| 5本柱        | ------------------------------> | dmail-outbound     |
| (amadeus 等) |                                  | topic              |
+--------------+                                  +--------+-----------+
                                                            |
                                                            v
+----------------------------------------+
| runops-gateway (Cloud Run)             |
|                                         |
| OutboundReceiver                       |
|    ↓                                    |
| DispatchResultHandler                  |
|   - kindEmoji + 通常 thread reply      |
|   - severity=high なら approval ボタン  |  <-- 本 ADR で追加
|    ↓                                    |
| FallbackNotifier (chat.postMessage)    |
+----------------------------------------+
                  |
                  | Slack thread に Approve/Deny ボタン表示
                  v
+----------------------------------------+
| Slack channel (元 thread)               |
|   * Approve clicked by user_B          |
|   * (user_A == requester は reject)    |
+----------------------------------------+
                  |
                  | /slack/interactive
                  v
+----------------------------------------+
| InteractiveHandler                     |
|   action_id: approval_approve / _deny  |
|   guards:                              |
|     1. clicker != original requester   |
|     2. ConsumedTokenStore (one-time)   |
|     3. allowlist (EnvAuthChecker)      |
|    ↓ (approve only)                     |
| Pub/Sub publish (kind=convergence,     |
|   target=元 source, source=runops-     |
|   gateway-slack, metadata.parent       |
|   _idempotency_key=元 D-Mail)          |
+----------------------------------------+
                  |
                  v dmail-inbound へ戻る
                  v 5本柱 (例: amadeus) が ack を受け取り、後続処理
```

### 採用する規約

| 項目 | 値 |
|---|---|
| 検出条件 | `mail.Kind == convergence` AND `mail.Metadata["severity"] == "high"` |
| ボタン action_id | `approval_approve` / `approval_deny` |
| 承認者 guard | `clicker_user_id != metadata.requester_id` (4-eyes); 等しければ reject |
| 認可 | 既存の EnvAuthChecker で承認許可 ID list を引き続き利用 |
| dedup | ConsumedTokenStore に `approval/<parent_idempotency_key>` を mark |
| 承認後 | gateway が新しい convergence kind D-Mail を **dmail-inbound に publish** (元 source = target、自身を source として) |
| 拒否時 | thread に "🚫 Denied by <clicker>" を post、Pub/Sub 経路には何も流さない |

### `metadata.severity` の扱い

D-Mail Protocol schema v1 で `severity` は frontmatter の任意フィールドとして
許容済み (amadeus README より、low/medium/high の 3 値)。`domain.DMail.Metadata`
は既に開いており、新しい canonical field として追加する必要なし。
`severity` 取得は `mail.Metadata["severity"]` で行う。

### Slack ボタン value

既存 `dispatchActionValue` は specification 経路用の payload なので、
**新しい `approvalActionValue`** を別途定義する:

```go
type approvalActionValue struct {
    ParentIdempotencyKey string `json:"parent_idempotency_key"`
    OriginalRequesterID  string `json:"original_requester_id"` // for 4-eyes guard
    Source               string `json:"source"`                // 元 D-Mail の source (例 "amadeus")
    Target               string `json:"target"`                // 元 D-Mail の target (例 "sightjack")
    BodyDigest           string `json:"body_digest"`           // 改ざん検知 (本文の SHA-256 prefix)
    IssuedAt             int64  `json:"issued_at"`
}
```

decode は parseDispatchActionValue と同じ `decodeButtonValue` ヘルパー再利用。

### 承認後の publish 経路

承認後 gateway は新しい convergence D-Mail を **dmail-inbound topic** に
publish する (target は元 D-Mail の source、source は `runops-gateway-slack`)。
これにより phonewave の routing が dmail-receiver 経由で paintress / amadeus
等の inbox にメッセージを届ける。

ただし **dmail-inbound への publish は PubsubDispatcher を流用** (新 publisher
は作らない)。新しい port `ApprovalPublisher` (or `port.DMailPublisher` 直接利用)
で済む。

### Cloud Run min-instances との関係

ADR 0018 で min-instances=1 を要求済み。本 ADR では追加要件なし
(ConsumedTokenStore も in-process で OK、4-eyes は同一 instance 内で十分
確実に判定できる)。

## Consequences

### Positive

- HIGH severity の convergence が必ず人間承認を通る
- 4-eyes (clicker != requester) の強制で、誤操作 / 自演承認を防げる
- Slack 上で完結 (channel 切替不要)
- 既存 ConsumedTokenStore / EnvAuthChecker / FallbackNotifier の再利用
- approvalActionValue は dispatchActionValue と分離されているので、
  Phase 1 経路への影響なし

### Negative

- HIGH severity の本数が増えると、未承認の thread が channel に溜まる
  (運用上 escalation policy が必要、Phase 5 以降の課題)
- 同一 channel に複数の HIGH severity が同時に出ると承認順序が曖昧
- approval click の reject 通知が thread に積み上がるので、後で見ると
  ノイズになる (将来 ephemeral 化を検討)

### Neutral

- approvalActionValue は dispatchActionValue と並走する。将来 Phase 5+ で
  両者を統合する余地はあるが、Phase 4a 段階では分離が安全

## 関連 ADR

- ADR 0014: Slack 通知集約 (本 ADR の前提)
- ADR 0017: Bot Token + chat.postMessage (本 ADR の reply 経路)
- ADR 0018: pull subscription (本 ADR の上流経路)
- Codex round 4 F-7: clicker hijack guard (本 ADR の 4-eyes guard の Phase 1 版)

## 参照

- `/Users/nino/tap/amadeus/README.md` の severity routing
- `/Users/nino/tap/paintress/docs/approval-contract.md`
- `/Users/nino/dotfiles/exe/` (将来 production deploy 時の SA / IAM 参考)
