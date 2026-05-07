# 0012 HTTP admin endpoint for project registry (production CLI 経路の正規 API)

**Target repo:** `hironow/runops-gateway` (本リポ `cmd/server/`)
**Priority:** P2 (Phase α 完了後、 production operations 改善)
**Depends on:** 0009 (port), 0011 (Firestore adapter)
**Blocks:** —
**Status:** 📝 未着手

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

- [ ] gateway server (Cloud Run main) に admin route を追加
  - POST `/admin/projects` — body は Project struct JSON
  - GET `/admin/projects` — query: `?status=active|archived|all`
  - GET `/admin/projects/{id}` — show
  - POST `/admin/projects/{id}/archive` — archive (idempotent)
- [ ] auth: gateway 既存 IAM Identity-Aware Proxy or admin-only auth header
      pattern を踏襲 (operator が認可された identity でのみ叩ける)
- [ ] 内部実装は `port.ProjectRegistry` を直接呼ぶ (SQLite or Firestore は
      composition root の env で決まる)
- [ ] 4-eyes 不要 (registry mutation は high-severity でない判定)
- [ ] `cmd/runops` CLI に `--remote=<gateway-url>` flag を追加検討 (本 issue
      scope に含めるかは別途、 stub で良い)
- [ ] integration test: 同じ Project が CLI / HTTP どちらからでも書ける /
      読める equivalence test (Firestore emulator + http test client)

## 実装ヒント

- 既存 `cmd/server/` の HTTP routing pattern を踏襲
- error response: 既存 gateway の JSON error envelope に整合
- ADR は本 issue 完了時に新規起票 (ADR 0027 想定)

## 関連

- 0009 (port + CLI、 同 port を共有、 CLI は dev local 限定で確定)
- 0011 (Firestore adapter、 本 issue の主な実装対象)
- ADR 0025 (本 issue を「production CLI 経路の正規 API」と位置付ける根拠)
