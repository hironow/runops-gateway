# 0013. outbox 書き込みは Pub/Sub bridge を経由する

**Date:** 2026-05-05
**Status:** Accepted (Phase 2a 着手で publish 経路を実装、2026-05-05)

> **2026-05-05 update**: Phase 2a (`feat/long-running-dispatch`) で
> publish 側 (`internal/adapter/output/pubsub` + `PubsubDispatcher`) が実装され
> 動作確認済み。Pub/Sub message attribute (kind / target_tool / source /
> dmail_schema_version / idempotency_key / forwarded metadata) を本 ADR 通りに
> 付与している。dmail-receiver (Phase 2b) と dmail-emitter (Phase 2c) は依然
> として未実装。

## Context

runops-gateway は Cloud Run 上で動作する。一方、5本柱と phonewave は
**exe-coder VM**（hironow/dotfiles で管理される常駐 VM、Cloud SQL Postgres と
Tailscale ACL を持つ）の上で systemd サービスとして稼働する想定である。

新しい D-Mail を 5本柱の世界に投入するには **phonewave が監視している outbox
ディレクトリ**（exe-coder VM のローカル FS）に `.md` ファイルを atomic write
（temp + rename）する必要がある。

### 物理的制約

| 経路候補 | 評価 |
|---|---|
| Cloud Run から NFS / GCS Fuse で直接マウント | Cloud Run はステートレス・読み取り専用 FS。書き込み可能 mount はサポート外 |
| SSH 越しのファイル書き込み (rsync 等) | キー管理が脆弱、atomic write の保証が難しい、レイテンシ大 |
| tsnet 経由で exe-coder の HTTP receiver を叩く | 自前 receiver の HTTP API 設計・認証・冪等性を全て自前実装することになる |
| Cloud Pub/Sub bridge | GCP マネージドで at-least-once 配送・DLQ・順序保証・重複排除 ID を標準装備 |

### 検討した選択肢

| 案 | 内容 |
|----|------|
| A | tsnet 経由で exe-coder 上の自前 HTTP receiver にリクエスト |
| B | Cloud Pub/Sub bridge（gateway publish → exe-coder の subscriber daemon が outbox に書く） |

### 案 A の問題点

- HTTP receiver の認証・冪等性・リトライを全て自前実装する必要がある
- exe-coder VM の preempt 中はリクエストが失われる（自前バッファリングが必要）
- Cloud Run の outbound IP は dynamic で、tsnet の起動コスト（cold start で +2-3s）が
  Slack 3 秒ルールに圧迫を与える

## Decision

**runops-gateway は Cloud Pub/Sub にメッセージを publish するだけ。**
**exe-coder VM 上の `dmail-receiver` daemon が Pub/Sub を pull して、
phonewave outbox に atomic write (temp + rename) する。**

### Topology

```
+--------------------+         +------------------+          +-------------------+
|  runops-gateway    |  pub    |  Pub/Sub topic   |  pull    |  dmail-receiver   |
|  (Cloud Run)       | ------> |  dmail-inbound   | -------> |  (exe-coder VM)   |
+--------------------+         +------------------+          +-------------------+
                                       |                              |
                                       v                              v
                                  Dead Letter Topic           atomic write to
                                  dmail-inbound-dlq           phonewave outbox
```

Legend / 凡例:
- pub: Cloud Pub/Sub publish (synchronous RPC, 50-100ms in asia-northeast1)
- pull: Cloud Pub/Sub StreamingPull subscriber (gRPC long-lived)
- DLQ: Dead Letter Queue (max delivery attempts 5)

### 双方向対称性

逆向き（5本柱から Slack 通知）も同じ思想で対称的に設計する:

- exe-coder VM 上の **`dmail-emitter`** daemon が各ツールの `archive/` を
  fsnotify で監視し、検出した D-Mail を Pub/Sub `dmail-outbound` topic に publish
- runops-gateway は push subscription で受信し、Slack thread に reply

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

### dmail-receiver の責務

1. Pub/Sub `dmail-inbound` から StreamingPull で message 受信
2. attribute から `idempotency_key` を取り出し、recently-seen set でメモリ dedup（TTL 1h）
3. ファイル名に idempotency_key を含めて outbox に **atomic write** (`.tmp-<key>` → rename)
4. 既存ファイルがあれば skip（disk dedup）
5. ack（再配送防止）

### dmail-receiver / dmail-emitter のソース管理

両 daemon は **runops-gateway リポジトリ内 `cmd/dmail-receiver/`, `cmd/dmail-emitter/`** に置く。
理由:

- Pub/Sub message 仕様（attribute schema）は runops-gateway 側の publisher と
  exe-coder 側の subscriber で **同期した型定義** が必要。同一リポジトリで
  Go の package import を共有するのが最も整合的
- テストハーネス（Pub/Sub emulator + 一時 outbox ディレクトリ）を共有できる
- 「5本柱本体を変更しない」という ADR 0012 の前提と整合（外周は外周で管理）

ADR 0015（dmail-receiver / dmail-emitter の責務範囲）で詳細を定める。

## Consequences

### Positive

- Cloud Run のステートレス性を保ちながら、exe-coder VM の preempt にも耐える
  （Pub/Sub に message が積まれている限り喪失しない）
- 障害ドメインが分離される: gateway / Pub/Sub / receiver / phonewave / 5本柱が
  それぞれ独立して落ちて復旧できる
- at-least-once 配送が標準装備で、DLQ で観測も容易
- 5本柱本体への変更ゼロ（receiver が atomic write するだけで phonewave から見ると
  「outbox に新しいファイルが現れた」だけに見える）

### Negative

- Pub/Sub の追加コスト（asia-northeast1: $40/TiB、1 message <= 100KB の前提で
  数万 messages/月までは無視できるレベル）
- レイテンシが増える: gateway から receive までに publish (50-100ms) + pull
  (sub-second) で **数百 ms** が乗る。Slack 3 秒ルールには十分余裕があるが、
  CLI 経由の即時実行型ワークロードでは体感差がある
- 運用要素が 2 つ増える（Pub/Sub topic + receiver daemon）

### Neutral

- Pub/Sub は schema v1 を attribute に明示するため、将来 v2 を切る場合も
  attribute によるバージョン分岐で受信側を移行しやすい

## 関連 ADR

- ADR 0012: 新しい D-Mail kind は追加しない（決定 A、本 ADR と対）
- ADR 0014: Slack 通知は runops-gateway に集約（決定 C、本 ADR と対）
- ADR 0015 (予定): dmail-receiver / dmail-emitter を本リポジトリで管理する責務範囲

## 参照

- [`docs/intent.md`](../intent.md) — 「拡張意図: 5本柱 D-Mail Dispatcher 化」章
- [`docs/handover.md`](../handover.md) — Phase 1 実装計画
- `/Users/nino/tap/phonewave/README.md` — phonewave の outbox 監視仕様
