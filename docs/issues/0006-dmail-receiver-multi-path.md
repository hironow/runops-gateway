# 0006 dmail-receiver の multi-project path 対応

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/dmail-receiver/`)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** —
**Blocks:** 0010 (multiplex 動作確認)、0007 (sister: emitter)
**Cross-ref:** [tap/refs/docs/issues/0001](../../../../tap/refs/docs/issues/0001-runops-gateway-dmail-receiver-multi-path.md) (cross-repo dispatch 親)
**Status:** 📝 未着手

## 概要

`cmd/dmail-receiver/main.go` は現状 `PHONEWAVE_OUTBOX_DIR` 単一 path のみ対応。
multiplex (1 VM = N project, hironow 決定 2026-05-06 選択肢 B) の必要条件として
**Pub/Sub message attribute `project_id` を見て write 先 outbox を選択** できるよう拡張。

## 動機

[refs/docs/multiplex-discussion.md](https://github.com/hironow/tap/blob/main/refs/docs/multiplex-discussion.md) §"Level C — 異 project 同 VM" で
**dmail-receiver の単一 path 設計が multiplex の致命的制約** と特定。

## 受入基準

- [ ] env var `PHONEWAVE_OUTBOX_DIRS_BY_PROJECT` で `project_id → outbox path` の map を受け付ける
      (例: `foo:/home/coder/projects/foo/.phonewave/outbox,bar:/home/coder/projects/bar/.phonewave/outbox`)
- [ ] Pub/Sub message attribute から `project_id` を読み、対応する outbox に atomic write
- [ ] `project_id` 不明 (map に無い OR attribute なし) は **エラーログ + DLQ 送り** で hard fail (silently drop しない)
- [ ] backward-compat: `PHONEWAVE_OUTBOX_DIR` 単一 env も継続サポート (deprecated、map 未設定時のみ使用)
- [ ] 既存の atomic write / idempotency 保証は維持
- [ ] integration test: 2 project 分の outbox に正しく振り分けられること

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
