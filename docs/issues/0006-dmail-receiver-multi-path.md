# 0006 dmail-receiver の multi-project path 対応

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/dmail-receiver/`)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** —
**Blocks:** 0010 (multiplex 動作確認)、0007 (sister: emitter)
**Cross-ref:** [tap/refs/docs/issues/0001](../../../../tap/refs/docs/issues/0001-runops-gateway-dmail-receiver-multi-path.md) (cross-repo dispatch 親)、 ADR 0028 (本 PR で起票、 multi-mode routing decision)
**Status:** 🟡 着地 (OutboxRouter + multi-mode env + ParseOutboxDirsByProject + CI pubsub-integration job + ADR 0028、 single-mode 100% backward compat 維持)

## 概要

`cmd/dmail-receiver/main.go` は現状 `PHONEWAVE_OUTBOX_DIR` 単一 path のみ対応。
multiplex (1 VM = N project, hironow 決定 2026-05-06 選択肢 B) の必要条件として
**Pub/Sub message attribute `project_id` を見て write 先 outbox を選択** できるよう拡張。

## 動機

[refs/docs/multiplex-discussion.md](https://github.com/hironow/tap/blob/main/refs/docs/multiplex-discussion.md) §"Level C — 異 project 同 VM" で
**dmail-receiver の単一 path 設計が multiplex の致命的制約** と特定。

## 受入基準

- [x] env var `PHONEWAVE_OUTBOX_DIRS_BY_PROJECT` で `project_id → outbox path` の map を受け付ける
      (`foo:/abs/foo,bar:/abs/bar` 形式、 ParseOutboxDirsByProject で fail-loud parse)
- [x] Pub/Sub message attribute から `project_id` を読み (multi-mode 限定)、 対応する outbox に atomic write
- [x] `project_id` 不明 / 未登録 / 不正 format (multi-mode) → ErrProjectNotRouted + nack で DLQ 行き (max_delivery_attempts=5)
- [x] backward-compat: `PHONEWAVE_OUTBOX_DIR` 単一 env が継続サポート (single-mode、 SingleOutboxRouter で project_id 完全 ignore = 100% byte-identical)
- [x] 両 env 同時設定時は multi-mode 優先 + single env を deprecated warn-log
- [x] 既存の atomic write / idempotency 保証は維持 (phonewave.OutboxWriter 変更なし)
- [x] integration test: 2 project 分の outbox に正しく振り分け + cross-leak 不在 + DLQ 行き + single-mode 互換 (3 ケース、 emulator 経由)
- [x] CI pubsub-integration job 追加 (firestore-integration pattern 踏襲)
- [x] ADR 0028 起票

### defer 項目 (本 issue scope 外)

- [ ] **#0007** dmail-emitter 側 (publish に project_id attribute + D-Mail frontmatter 書込)
- [ ] **exe 側 #0010** workspace VM systemd で multi-project env 配信
- [ ] **exe 側 #0006** workspace VM `~/projects/<id>/` 命名規則
- [ ] **#0012** HTTP admin endpoint
- [ ] default project 解決 (multi-mode で project_id 不在時 fallback) → ADR 0028 で意図的 reject

## 実装ヒント

- 既存 `OutboxWriter` を `OutboxWriterRegistry` に拡張、`Get(projectID) OutboxWriter` で fetch
- `PHONEWAVE_OUTBOX_DIRS_BY_PROJECT` のパースは `key:value,key:value` shape (env var 制約上 JSON より読みやすい)
- DLQ への送りは既存 Pub/Sub DLQ 設定 (Phase 4b) を流用
- 親 issue: D-Mail metadata v1.1 で `project_id` を redundant に message attribute + frontmatter 両方に carry する設計
  ([tap/refs/docs/dmail-metadata-v1-1.md](https://github.com/hironow/tap/blob/main/refs/docs/dmail-metadata-v1-1.md))

## 関連

- 0007 (sister: dmail-emitter 側、attribute publish)
- refs 0010 (exe-coder multi-project env、本 issue の上位 deploy 経路)
- refs 0014 軸 2 (5 ツール parser が未知 metadata key を ignore する regression test、既に 4 ツール landed)
