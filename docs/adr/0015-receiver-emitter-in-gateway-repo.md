# 0015. dmail-receiver / dmail-emitter は本リポジトリで管理する

**Date:** 2026-05-05
**Status:** Proposed (Phase 2 以降で再評価)

> **Draft notice (2026-05-05)**: 本 ADR は ADR 0013 の Pub/Sub bridge を構成する
> 2 daemon の責務範囲を定める判断であり、Phase 2 以降の議論である。Phase 1
> （Issue 0018: シンプル経路）では receiver / emitter は実装しない。Phase 1 完了後、
> Phase 2 着手時に本 ADR を Accepted に昇格するか再評価する。

## Context

ADR 0013（outbox 書き込みは Pub/Sub bridge を経由する）の決定により、
2 つの新しい daemon が必要になった:

- **dmail-receiver**: exe-coder VM 上で Pub/Sub `dmail-inbound` を pull し、
  phonewave outbox に atomic write する
- **dmail-emitter**: exe-coder VM 上で 5本柱の `archive/` を fsnotify 監視し、
  Pub/Sub `dmail-outbound` topic に publish する

これらの daemon は **runops-gateway リポジトリ** で管理するか、
別の専用リポジトリ (例: `dmail-bridge`) に切り出すか、
あるいは `hironow/dotfiles` の exe-coder 設定の一部として
shell script + cron で書くか、判断が必要。

### 関連する制約

- 5本柱本体は変更しない（ADR 0012）
- 実体は exe-coder VM 上で systemd 起動される（intent.md / handover.md 参照）
- Pub/Sub message の attribute schema (`kind`, `target_tool`, `source`,
  `dmail_schema_version`, `idempotency_key`, `traceparent`) は **gateway 側 publisher と
  exe-coder 側 subscriber で同期した型定義** が必要

### 検討した選択肢

| 案 | 配置 | 評価 |
|----|------|------|
| A | `runops-gateway` リポジトリ内 `cmd/dmail-receiver/`, `cmd/dmail-emitter/` | 型同期と統合テストが容易 |
| B | 専用リポジトリ `hironow/dmail-bridge` | 関心の分離が明確だが、Pub/Sub schema を 2 リポジトリで同期する必要 |
| C | `hironow/dotfiles` の exe-coder セットアップに shell + cron で内蔵 | atomic write / fsnotify / Pub/Sub gRPC を shell で書くのは実用的でない |

### 案 B の問題点

- Pub/Sub message の attribute schema が runops-gateway と dmail-bridge の
  両方で定義される。バージョン乖離が発生しうる
- runops-gateway 側の Pub/Sub publisher のテストハーネス（emulator + 一時ファイル）
  と receiver 側のテストハーネスが重複する
- リポジトリ数が増える管理コスト

### 案 C の問題点

- Pub/Sub gRPC streaming pull、fsnotify、atomic write (temp + rename) を
  shell で実装するのは脆弱
- 5本柱・runops-gateway と同じく Go で書くほうが、テスト・観測性
  （OpenTelemetry 計装）の流儀が揃う

## Decision

**dmail-receiver と dmail-emitter は runops-gateway リポジトリ内に
`cmd/dmail-receiver/` と `cmd/dmail-emitter/` として配置する。**

### 配置と責務

```
runops-gateway/
├── cmd/
│   ├── server/                  # 既存: Slack Webhook 受信
│   ├── runops/                  # 既存: CLI
│   ├── dmail-receiver/          # 新規: Pub/Sub → phonewave outbox
│   │   └── main.go
│   └── dmail-emitter/           # 新規: phonewave archive → Pub/Sub
│       └── main.go
├── internal/
│   ├── adapter/
│   │   └── output/
│   │       ├── pubsub/          # 新規: Pub/Sub publisher / subscriber 共通実装
│   │       │   ├── publisher.go
│   │       │   └── subscriber.go
│   │       └── phonewave/       # 新規: outbox/archive ファイル操作
│   │           ├── writer.go    # atomic write (temp + rename)
│   │           └── watcher.go   # fsnotify wrapper
│   └── core/
│       └── domain/
│           └── dmail.go         # 新規: DMail (kind, target, payload, attributes)
```

Legend / 凡例:
- `cmd/dmail-receiver/`: exe-coder VM 上で動く receiver の entry point
- `cmd/dmail-emitter/`: exe-coder VM 上で動く emitter の entry point
- `internal/adapter/output/pubsub/`: Pub/Sub クライアントの共通実装（gateway も使う）
- `internal/adapter/output/phonewave/`: ファイル I/O 層（receiver / emitter で共有）
- `internal/core/domain/dmail.go`: D-Mail 型定義（gateway も receiver も emitter も使う）

### ビルド成果物

- `runops-gateway` (既存): Cloud Run 上の Slack ChatOps + AgentOps gateway
- `runops` (既存): Cobra CLI
- `dmail-receiver` (新規): exe-coder VM 上で systemd 起動される daemon
- `dmail-emitter` (新規): exe-coder VM 上で systemd 起動される daemon

### デプロイ方式

- `runops-gateway` / `runops`: GitHub Actions (`cd.yaml`) で Cloud Run に自動デプロイ（既存）
- `dmail-receiver` / `dmail-emitter`: GitHub Release のバイナリを `hironow/dotfiles` の
  exe-coder VM startup-script で `curl` 取得し、systemd 配置（hironow/dotfiles 側で実装）

### Single Source of Truth の保ち方

- D-Mail attribute schema は `internal/core/domain/dmail.go` の Go struct で表現
- gateway 側 publisher は struct を marshal して publish
- receiver / emitter は同じ struct で unmarshal
- attribute key の typo / 漏れを **コンパイル時に検出** できる

### CI ビルド

`.github/workflows/cd.yaml` のビルドステップに追加:

```yaml
# 既存
go build -o runops-gateway ./cmd/server
go build -o runops ./cmd/runops

# 新規
go build -o dmail-receiver ./cmd/dmail-receiver
go build -o dmail-emitter ./cmd/dmail-emitter
```

GitHub Release で `dmail-receiver-linux-amd64`, `dmail-emitter-linux-amd64` を
配布し、exe-coder VM の startup-script で取得する。

## Consequences

### Positive

- D-Mail attribute schema が 1 箇所（Go struct）に集約され、バージョン乖離を防げる
- Pub/Sub emulator + 一時ディレクトリでの統合テストが 1 リポジトリ内で完結する
- runops-gateway 拡張の全体像（gateway + 2 daemon）が 1 リポジトリで把握できる
- OpenTelemetry 計装、ログフォーマット、エラーハンドリング規約が gateway と統一される

### Negative

- リポジトリのスコープが「Slack ChatOps gateway」を超えて「ChatOps + AgentOps + bridge」に
  拡大する。intent.md で議論した「リポ名リネーム」判断 (Phase 3 完了時の再評価) の
  圧力が増す
- exe-coder VM 上で動く daemon が runops-gateway リポジトリの GitHub Release に
  依存することになる。デプロイ経路が 2 段階（リポジトリ → Release → VM startup）になる

### Neutral

- `cmd/dmail-receiver/` と `cmd/dmail-emitter/` は **Cloud Run には配置しない**。
  runops-gateway (cmd/server) のみが Cloud Run に行く。Dockerfile も既存のまま維持

## 関連 ADR

- ADR 0005: Ports and Adapters パターンの採用（本 ADR は internal/adapter 配下の
  新しい driven adapter として位置付け）
- ADR 0012: 新しい D-Mail kind は追加しない（D-Mail 型定義の安定性を保証）
- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する（本 ADR の前提）
- ADR 0014: Slack 通知は runops-gateway に集約する（本 ADR の Pub/Sub 対称性の根拠）

## 参照

- [`docs/intent.md`](../intent.md) — 「拡張意図: 5本柱 D-Mail Dispatcher 化」章
- [`docs/handover.md`](../handover.md) — Phase 1 実装計画（追加するコード一覧）
