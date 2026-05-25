# 0007 dmail-emitter が emit 時に project_id attribute を付与

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/dmail-emitter/`)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** refs 0006 ([exe] project ディレクトリ命名規則)
**Blocks:** 0010 (multiplex 動作確認、refs 0010)
**Cross-ref:** [tap/refs/docs/issues/0002](../../../../tap/refs/docs/issues/0002-runops-gateway-dmail-emitter-project-id-attribute.md)、 ADR 0029 (本 PR で起票、 emitter project_id resolution decision)
**Status:** 🟡 着地 (ArchiveRouter + multi-mode env + frontmatter override + peer-mode guard + ADR 0029、 single-mode 100% backward compat 維持)

## 概要

`cmd/dmail-emitter/main.go` は `PHONEWAVE_ARCHIVE_DIRS` colon 区切りで複数 watch 対応済 ✅。
ただし fsnotify で検出した archive file から **どの project の D-Mail か** を逆引きして
Pub/Sub publish 時に `project_id` attribute に乗せる経路が未実装。

## 動機

multiplex で 1 VM 内 N project の archive を 1 emitter が watch する以上、
Slack thread reply 突合に project_id が必須 (refs 0003 / 0011 と連動)。

## 受入基準

- [x] env var `PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT` で `project_id → archive path` の map を受け付ける (phonewave.ParseDirsByProject、 #0006/#0007 共通)
- [x] fsnotify event の path から **どの project に属するか** を判定 (MultiArchiveRouter の prefix match、 nested dir reject)
- [x] Pub/Sub publish 時に message attribute `project_id` を付与 (DMail.Metadata に set、 既存 publisher の attrs merge 経路を流用)
- [x] backward-compat: 単一 archive dir 環境では project_id 未設定で publish (SingleArchiveRouter で 100% byte-identical)
- [x] 不明 path (map のどれにも match しない) は **warn-log + skip** (DLQ 送りは過剰、 emitter は read-only)
- [x] frontmatter project_id != path 由来 → path-derived 優先 + warn-log (operator 信頼)
- [x] PHONEWAVE_PEER_RECEIVER_MODE env で boot 時 mode mismatch fail-fast (codex review v1 #3 反映)
- [x] PHONEWAVE_ARCHIVE_DIRS の filepath.SplitList 化 (Windows path 対応、 codex review v1 #2 反映)
- [x] integration test: 2 ケース multi-mode (publish + skip) emulator 経由
- [x] ADR 0029 起票

### defer 項目 (本 issue scope 外)

- [ ] **5 ツール (paintress / sightjack / amadeus / dominator / phonewave)** が D-Mail emit 時に frontmatter project_id を書込 (本 PR は読込専用)
- [ ] **exe 側 #0010** workspace VM systemd で multi-project env 配信 (PHONEWAVE_ARCHIVE_DIRS_BY_PROJECT + PHONEWAVE_PEER_RECEIVER_MODE)
- [ ] **#0012** HTTP admin endpoint
- [ ] default project 解決 (path 不明時 fallback) → ADR 0029 で意図的 reject

## 実装ヒント

- 現状 `phonewaveinput.NewWatcher(emitter, dirs ...string)` を拡張: `NewWatcherWithProjectMap(emitter, map[string]string)`
- 逆引き: `for projectID, archiveDir := range map { if strings.HasPrefix(eventPath, archiveDir) { ... } }`
- prefix match は filepath.Clean を介して trailing slash 揺れを正規化
- D-Mail v1.1 の redundant carry 原則に従い、frontmatter `metadata.project_id` も読めるなら attribute と cross-check 推奨
  ([tap/refs/docs/dmail-metadata-v1-1.md](https://github.com/hironow/tap/blob/main/refs/docs/dmail-metadata-v1-1.md))

## 関連

- 0006 (sister: receiver 側)
- refs 0006 (exe 側 ~/projects/<id>/ 命名規則 → archive path map の元)
