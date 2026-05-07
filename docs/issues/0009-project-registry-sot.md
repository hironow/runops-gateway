# 0009 project registry の SoT を gateway DB で持つ

**Target repo:** `hironow/runops-gateway` (本リポ SQLite state store)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** —
**Blocks:** 0008 (Slack flag)、0010 (GitHub App)
**Cross-ref:** [tap/refs/docs/issues/0004](../../../../tap/refs/docs/issues/0004-runops-gateway-project-registry.md)
**Status:** 🟡 着地 (port + SQLite adapter + CLI + ADR 0025、Firestore は #0011 で追加、HTTP admin endpoint は #0012 で追加)

## 概要

`project_id` の正規 SoT を gateway 側 SQLite (or 既存の state store) に持つ。
1 project = 1 row、attribute は (project_id, github_org, github_repo, workspace_path,
slack_default_channel?, github_app_installation_id, created_at, status)。

## 動機

[refs/docs/multiplex-discussion.md §"論点 1: 「project」という単位の SoT は何か?"](https://github.com/hironow/tap/blob/main/refs/docs/multiplex-discussion.md) で 4 候補:

1. (github_org, github_repo) tuple
2. workspace VM 内ディレクトリ名
3. **gateway 側 SQLite に project registry を持つ ← 本 issue 推奨**
4. Slack channel ID

候補 3 を選ぶ理由: Slack thread / 4-eyes / GitHub App の 3 軸を gateway が握るため、
gateway に SoT を置く方が制御しやすい。

## 受入基準

- [x] gateway 内 SQLite に `projects` table を新設 (modernc.org/sqlite + WAL + busy_timeout)
- [x] schema: `(id PK, github_org, github_repo, workspace_path, slack_default_channel,
      github_app_installation_id, status, created_at, archived_at)`
- [x] 管理 CLI: `runops project add|list|show|archive` (`cmd/runops` に subcommand)
- [x] integration test: project add → list → show → archive の lifecycle (build tag `integration`)
- [x] port/adapter pattern: SQLite (本 PR) + Firestore (#0011) を切替可能な interface 設計
- [x] env factory fail-closed: `RUNOPS_PROJECT_REGISTRY` 必須、 `RUNOPS_ENV=development` のみ sqlite default 許容
- [x] ADR 0025 に採用根拠を記録

### defer 項目 (本 issue scope 外、別 issue で実装)

- [ ] **#0010** add 時に GitHub App installation ID 検証 (installation が当該 repo を含むか確認)
- [ ] **#0008** dispatch 経路で `project_id` を validate (registry に存在 + status=active)
- [ ] **#0011** Firestore adapter 実装 (production deploy 用) + ADR 0026
- [ ] **#0012** HTTP admin endpoint (production の registry 操作経路、 CLI 経路と分離)
- [ ] migration: 既存 default project の seed 経路 (production cutover 時に必要、 #0011 と同時)

## 実装結果 (2026-05-07)

- domain layer: `internal/core/domain/project.go` (Project struct + ID validation regex)
- port layer: `internal/core/port/port.go` (ProjectRegistry interface + ProjectListFilter)
- state layer: `internal/adapter/output/state/sqlite.go` (substrate), `sqlite_project_registry.go`
  (impl), `registry_factory.go` (env-driven adapter selection)、 migrations を `embed.FS` で
  bundle
- cli layer: `internal/adapter/input/cli/project.go` (add/list/show/archive subcommand tree)
- composition root: `cmd/runops/main.go` (env-driven optional wiring)

## 関連

- 0008 (Slack flag、registry を validate に使う) — 本 PR の port を経由
- 0010 (GitHub App、registry の installation_id を使う) — 本 PR の field を消費
- 0011 [新規] (Firestore adapter)
- 0012 [新規] (HTTP admin endpoint、 production CLI 経路の正規 API)
- refs 0013 (project lifecycle CLI、 中期)
- ADR 0025 (本 PR で起票)
