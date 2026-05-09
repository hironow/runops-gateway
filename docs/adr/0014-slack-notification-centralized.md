# 0014. Slack 通知は runops-gateway に集約する

**Date:** 2026-05-05
**Status:** Accepted

## Context

paintress には `paintress-slack`, `paintress-tg`, `paintress-discord` という
**companion binary** が同梱されており、`--notify-cmd` / `--approve-cmd` を介して
Slack / Telegram / Discord に通知・承認を送る仕組みが既に存在する
（[`/Users/nino/tap/paintress/README.md`](file:///Users/nino/tap/paintress/README.md)
の「Companion Binaries」章）。

5本柱拡張（[`docs/intent.md`](../intent.md) の「拡張意図: 5本柱 D-Mail Dispatcher 化」章）
では、Slack 双方向通信（受信 + 通知）が必要になる。

通知側の実装手段として、companion を使うか runops-gateway 側で集約するかの判断が必要。

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | paintress-slack 等の companion を 5本柱の `--notify-cmd` で起動し、Slack 通知を担当させる |
| B | runops-gateway が dmail-emitter 経由で 5本柱の D-Mail を受信し、Slack に変換して通知 |

### 案 A の問題点

- **管轄の二重化**: runops-gateway は既に Slack 受信側（HMAC 検証・response_url 制御・
  Block Kit テンプレート・compressButtonValue）を持っている。通知側を別実装にすると
  Slack ペイロードの管轄が分散する
- **常時接続モデルとの相性**: paintress companion は **Socket Mode (WebSocket)**
  ベースで、Cloud Run の HTTP リクエスト駆動モデルと相性が悪い。常時接続を保つ
  別 process を exe-coder VM に立てる必要がある
- **承認フローの分断**: 既存 ChatOps の承認は runops-gateway の EnvAuthChecker +
  Block Kit ボタンで実装されている。HIGH severity 承認 (paintress の
  approval-contract) を companion 側で実装すると、認可ロジックが 2 箇所に分かれる
- **Slack App / Channel の一元化**: 1 Slack App / 1 Channel でやりたい運用に対し、
  companion を使うと Bot Token と Signing Secret が別管理になりやすい

## Decision

**Slack 通知（双方向）は runops-gateway に集約する。
paintress companion は Phase 1〜4 の範囲では使わない。**

### 通知経路

```
[ 5本柱 (paintress 等) ]
    +-- archive/ に D-Mail を書き込む
            |
            v
[ dmail-emitter (exe-coder VM) ]
    +-- fsnotify で archive/ を監視
    +-- Pub/Sub dmail-outbound topic に publish
            |
            v
[ runops-gateway (Cloud Run) ]
    +-- /pubsub/dmail-outbound エンドポイントで受信 (push subscription)
    +-- D-Mail kind に応じた Slack 変換ロジック
    +-- Slack thread に reply (response_url or chat.postMessage)
```

Legend / 凡例:

- archive/: phonewave が delivery 完了後に保管する永続ディレクトリ
- dmail-emitter: archive を監視して Pub/Sub に流す daemon (本リポジトリで管理)
- response_url: 30 分以内なら使用、超えるなら chat.postMessage

### 通知種別と Slack 変換

| D-Mail kind | Slack 変換 |
|---|---|
| `report` (paintress → amadeus) | thread reply: "✅ paintress 完了 (PR <link>)" |
| `design-feedback` (amadeus → sightjack) | thread reply: "🎨 設計フィードバック: <summary>" |
| `implementation-feedback` (amadeus → paintress) | thread reply: "🔧 実装フィードバック: <summary>" |
| `convergence` (amadeus → sightjack) | thread reply + HIGH severity なら承認ボタン |
| `ci-result` (CI/CD → amadeus) | thread reply: "🚦 CI: <status>" |

### approval gate

paintress の docs/approval-contract.md にある HIGH severity 承認は:

1. paintress が HIGH severity D-Mail を archive に出す
2. dmail-emitter → Pub/Sub → runops-gateway
3. runops-gateway が Slack に **既存の Block Kit 承認ボタン** を出す
4. 人間がクリック → runops-gateway が `convergence` kind の D-Mail を発行
   （ADR 0012 の規約に従い payload で意味を識別）
5. dmail-receiver 経由で paintress inbox に届く

これにより認可ロジックが既存 EnvAuthChecker + Block Kit に統一される。

### paintress 側の起動オプション

paintress を exe-coder VM 上で systemd 起動する際は:

```bash
paintress --notify-cmd "" --approve-cmd "" /path/to/repo
```

通知も承認も runops-gateway 経由で D-Mail として処理されるため、
companion は呼ばれない。

## Consequences

### Positive

- Slack ペイロード管轄の単一情報源化（Block Kit / response_url / HMAC が gateway に集中）
- Cloud Run のステートレス HTTP モデルと自然に整合（Socket Mode の常時接続 process が不要）
- 承認ロジックが既存 EnvAuthChecker + Block Kit に統一される
- Slack App と Channel が 1 つで済む

### Negative

- runops-gateway 側で D-Mail kind ごとの Slack 変換ロジックを実装する必要がある
  （companion はこれを各 platform 別に既に持っているため、再実装になる）
- Telegram / Discord 対応が将来必要になった場合、本 ADR の決定を超えて再評価が必要
  （現時点では Slack のみで運用する想定）

### Neutral

- paintress companion 自体は削除しない。将来「runops-gateway を経由しない局所運用」が
  必要になった場合や、Slack 以外の platform を試したい場合のために残しておく
- companion を使わない決定は **本 ADR の scope 内**（Phase 1〜4）に限定。長期的に
  Slack 以外への展開が必要になれば再評価する

## 関連 ADR

- ADR 0006: CLI 操作時の Slack メッセージ同期（既存の `chat.update` 規約）
- ADR 0007: CLI モードの Slack 独立性（`--no-slack` フラグ）
- ADR 0012: 新しい D-Mail kind は追加しない（決定 A、本 ADR と対）
- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する（決定 B、本 ADR と対）

## 参照

- [`docs/intent.md`](../intent.md) — 「拡張意図: 5本柱 D-Mail Dispatcher 化」章
- [`docs/handover.md`](../handover.md) — Phase 3 / Phase 4 の通知・承認設計
- `/Users/nino/tap/paintress/README.md` — Companion Binaries 章
- `/Users/nino/tap/paintress/docs/approval-contract.md` — Three-way approval contract
