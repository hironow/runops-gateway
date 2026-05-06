# 0007 dmail-emitter が emit 時に project_id attribute を付与

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/dmail-emitter/`)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** refs 0006 ([exe] project ディレクトリ命名規則)
**Blocks:** 0010 (multiplex 動作確認、refs 0010)
**Cross-ref:** [tap/refs/docs/issues/0002](../../../../tap/refs/docs/issues/0002-runops-gateway-dmail-emitter-project-id-attribute.md)
**Status:** 📝 未着手

## 概要

`cmd/dmail-emitter/main.go` は `PHONEWAVE_ARCHIVE_DIRS` colon 区切りで複数 watch 対応済 ✅。
ただし fsnotify で検出した archive file から **どの project の D-Mail か** を逆引きして
Pub/Sub publish 時に `project_id` attribute に乗せる経路が未実装。

## 動機

multiplex で 1 VM 内 N project の archive を 1 emitter が watch する以上、
Slack thread reply 突合に project_id が必須 (refs 0003 / 0011 と連動)。

## 受入基準

- [ ] env var `PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT` で `project_id → archive path` の map を受け付ける
- [ ] fsnotify event の path から **どの project に属するか** を判定 (map の prefix match)
- [ ] Pub/Sub publish 時に message attribute `project_id` を付与
- [ ] backward-compat: 単一 archive dir 環境では project_id 未設定で publish (現状動作維持)
- [ ] 不明 path (map のどれにも match しない) は **エラーログ + skip** (DLQ 送りは過剰、emitter は read-only)
- [ ] integration test: 2 project archive を分離 watch、各 publish に project_id 付与確認

## 実装ヒント

- 現状 `phonewaveinput.NewWatcher(emitter, dirs ...string)` を拡張: `NewWatcherWithProjectMap(emitter, map[string]string)`
- 逆引き: `for projectID, archiveDir := range map { if strings.HasPrefix(eventPath, archiveDir) { ... } }`
- prefix match は filepath.Clean を介して trailing slash 揺れを正規化
- D-Mail v1.1 の redundant carry 原則に従い、frontmatter `metadata.project_id` も読めるなら attribute と cross-check 推奨
  ([tap/refs/docs/dmail-metadata-v1-1.md](https://github.com/hironow/tap/blob/main/refs/docs/dmail-metadata-v1-1.md))

## 関連

- 0006 (sister: receiver 側)
- refs 0006 (exe 側 ~/projects/<id>/ 命名規則 → archive path map の元)
