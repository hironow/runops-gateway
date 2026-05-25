# 0012. 新しい D-Mail kind は追加しない

**Date:** 2026-05-05
**Status:** Proposed (Phase 2 以降で再評価)

> **Draft notice (2026-05-05)**: 本 ADR は D-Mail Protocol を runops-gateway に
> 統合する判断であり、Phase 2 以降の議論である。Phase 1（Issue 0018: シンプル経路）
> では D-Mail には触れない。Phase 1 完了後、Phase 2 着手時に本 ADR を Accepted に
> 昇格するか再評価する。

## Context

runops-gateway を 5本柱（sightjack / paintress / amadeus / dominator / phonewave）の
D-Mail Dispatcher として拡張する判断（[`docs/intent.md`](../intent.md) の
「拡張意図: 5本柱 D-Mail Dispatcher 化」章）に伴い、
**Slack `/agent` コマンド** や **管理対象アプリの CI/CD** から D-Mail を投入する経路を
新設する必要がある。

このとき「Slack 起点の dispatch」「CI 起点の通知」を表現する新しい D-Mail kind
（例: `slack-dispatch`, `ci-trigger` 等）を追加するか否かの判断が必要になった。

### D-Mail Protocol schema v1 の現状

phonewave / sightjack / paintress / amadeus / dominator が宣言する produces / consumes
は SKILL.md（Agent Skills v1 形式）で表現されており、現行 6 種:

| kind | フロー | 説明 |
|---|---|---|
| `specification` | sightjack → paintress | issue 仕様（実装依頼） |
| `report` | paintress → amadeus | 実装完了報告 |
| `design-feedback` | amadeus → sightjack | 設計レベル是正フィードバック |
| `implementation-feedback` | amadeus → paintress | 実装レベル是正フィードバック |
| `convergence` | amadeus → sightjack | 世界線収束アラート |
| `ci-result` | CI/CD → amadeus | CI/CD パイプライン結果 |

### 検討した選択肢

| 案 | 内容 | 5本柱への変更 |
|----|------|---|
| A | 新規 kind を追加（例: `slack-dispatch`, `verification-feedback` 等） | **必須**: receiver 側 SKILL.md 更新 + consume 実装追加 |
| B | 既存 kind を流用、payload (frontmatter) に sender 情報を埋める | **不要** |

### 案 A の問題点

- **5本柱本体への変更が必須化**: 新 kind を receive する全ツールの SKILL.md と
  consume 実装を同期更新する必要がある。これは 5本柱を別リポジトリで独立進化
  させる方針と矛盾する
- **scope が爆発**: 「Slack 起点」「CI 起点」「将来追加される起点」ごとに kind を
  増やすと、phonewave のルーティング表が肥大化する
- **意味の重複**: 「Slack から paintress に dispatch」は意味的に
  「人間が specification を書く」と同型である。新 kind を作る理由が薄い

## Decision

**新しい D-Mail kind を runops-gateway 拡張で追加しない。**
既存 6 種の kind を流用し、**送信者・由来情報は payload (frontmatter) の追加
フィールドで表現する**。

### 識別フィールド規約

D-Mail frontmatter に以下を追記する（既存 5本柱は未知フィールドを無視するため互換）:

```yaml
metadata:
  dmail-schema-version: "1"
  source: "runops-gateway-slack"      # or "runops-gateway-ci" など
  requester_id: "U0123ABCD"           # Slack user ID / CI commit author
  parent_idempotency_key: ""          # 親 D-Mail がある場合（reply chain）
  slack_thread_ts: ""                 # Slack thread 連結用（任意）
```

### 既存 kind と新しい意味の対応

| 新しい意味 | 流用する kind |
|---|---|
| Slack `/agent paintress fix M-42` | `specification` (target=paintress) |
| Slack `/agent sightjack scan` | `specification` (target=sightjack) |
| 管理対象アプリの CI 完了通知 | `ci-result` (既存のまま) |
| 人間の HIGH severity 承認応答 | `convergence` 流用 (Phase 4 で再評価) |

## Consequences

### Positive

- 5本柱本体への変更がゼロになる。runops-gateway 単独で Phase 1〜3 が完結する
- phonewave のルーティング表が増えない（kind の cardinality が固定）
- 「runops-gateway は外周のゲートであって内政には介入しない」という
  intent.md の関税ゲート性が物理的に強制される

### Negative

- payload フィールドへの依存が増える: receiver 側で `metadata.source` を見て
  分岐したくなる場合があるが、5本柱本体ではこれを避け、必要なら **gateway 側で
  payload を加工してから配送** するルールにする
- 将来「どうしても新 kind が必要」な場面が来た場合、本 ADR を superseded する別
  ADR を起こすコストがある

### Neutral

- payload フィールドの schema 拡張（後方互換あり）は schema v1 内で吸収可能。
  v2 を切る判断とは独立

## 関連 ADR

- ADR 0013: outbox 書き込みは Pub/Sub bridge 経由（決定 B、本 ADR と対）
- ADR 0014: Slack 通知は runops-gateway に集約（決定 C、本 ADR と対）

## 参照

- [`docs/intent.md`](../intent.md) — 「拡張意図: 5本柱 D-Mail Dispatcher 化」章
- `tap/phonewave/README.md` — D-Mail Protocol 仕様
