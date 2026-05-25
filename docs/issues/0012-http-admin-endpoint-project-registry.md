# 0012 HTTP admin endpoint for project registry (production CLI 経路の正規 API)

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/server/`)
**Priority:** P2 (Phase α 完了後、 production operations 改善)
**Depends on:** 0009 (port), 0011 (Firestore adapter)
**Blocks:** —
**Status:** 🟡 着地 (Bearer auth + 4 endpoint + opt-in 配線 + ADR 0030 + Test job で gate)
**Cross-ref:** ADR 0030 (本 PR で起票、 HTTP admin endpoint authentication strategy)

## 概要

production の project registry を operator が安全に操作できる
**HTTP admin endpoint** を gateway に追加。 #0009 の `runops project ...`
CLI は **operator local Mac dev only** で固定し、 production 操作は
HTTP endpoint 経由に一本化する (ADR 0025 の決定)。

## 動機

ADR 0025 で「production registry mutation flows through gateway HTTP admin
endpoint」と明記済。 Cloud Run には `kubectl exec` 相当が無いため、
operator が production registry を触る経路が現状無い。

## 受入基準

- [x] gateway server (Cloud Run main) に admin route を追加
    - POST `/admin/projects` — body は Project struct JSON
    - GET `/admin/projects` — query: `?status=active|archived|all`
    - GET `/admin/projects/{id}` — show
    - POST `/admin/projects/{id}/archive` — archive (idempotent)
- [x] auth: **Bearer token (env-driven、 opt-in、 constant-time 比較)** — 既存 admin auth 前例なしのため新規 strategy を ADR 0030 で確立
- [x] 内部実装は `port.ProjectRegistry` を直接呼ぶ (SQLite or Firestore は composition root の env で決まる)
- [x] 4-eyes 不要 (registry mutation は high-severity でない判定)
- [x] **fail-closed**: env (RUNOPS_ADMIN_TOKEN) + registry が両方揃ったときのみ admin handler を mux に register、 attack surface ゼロ
- [x] **token 漏洩防止**: log / response / OTel span / Handler.String() 全箇所で token 不出力、 captureSlogBuffer test で証明
- [x] **token 正規化**: 12 ケース unit test (Bearer/bearer/BEARER + prefix only / leading whitespace / trailing newline / token internal whitespace 等) で boundary 全 cover
- [x] handler unit test 19 ケース + lifecycle test 1 ケース (SQLite t.TempDir 経由 round-trip) — build tag なしで既存 Test job が gate
- [x] ADR 0030 起票

### defer 項目 (本 issue scope 外)

- [ ] **`cmd/runops` CLI に `--remote=<gateway-url>` flag** — 別 issue (client-side companion)
- [ ] IAP 統合 / SSO / identity-bound audit log — 将来 ADR で扱う
- [ ] rate limit / brute-force 対策 — Cloud Armor / Cloud Run concurrency cap で対応する想定、 application 層では実装せず
- [ ] pagination — Phase α は project 数 small、 必要時 ?cursor= で拡張

## 実装ヒント

- 既存 `cmd/server/` の HTTP routing pattern を踏襲
- error response: 既存 gateway の JSON error envelope に整合
- ADR は本 issue 完了時に新規起票 (ADR 0027 想定)

## 関連

- 0009 (port + CLI、 同 port を共有、 CLI は dev local 限定で確定)
- 0011 (Firestore adapter、 本 issue の主な実装対象)
- ADR 0025 (本 issue を「production CLI 経路の正規 API」と位置付ける根拠)
