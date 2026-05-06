# 0009 project registry の SoT を gateway DB で持つ

**Target repo:** `hironow/runops-gateway` (本リポ SQLite state store)
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** —
**Blocks:** 0008 (Slack flag)、0010 (GitHub App)
**Cross-ref:** [tap/refs/docs/issues/0004](../../../../tap/refs/docs/issues/0004-runops-gateway-project-registry.md)
**Status:** 📝 未着手

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

- [ ] gateway 内 SQLite (既存 state DB) に `projects` table を新設
- [ ] schema: `(id PRIMARY KEY, github_org, github_repo, workspace_path, slack_default_channel,
      github_app_installation_id, created_at, status)`
- [ ] 管理 CLI: `runops project add|list|show|archive` (`cmd/runops` に subcommand)
- [ ] add 時に GitHub App installation ID 検証 (installation が当該 repo を含むか確認)
- [ ] dispatch 経路で `project_id` を validate (registry に存在 + status=active)
- [ ] migration: 既存 default project (= 1 VM = 1 project 期の暗黙 project) を seed する経路を提供
- [ ] integration test: project add → list → dispatch → archive の lifecycle

## 実装ヒント

- 既存 `internal/adapter/output/state` の SQLite 拡張
- 内部に `port.ProjectRegistry` interface を立てて usecase から呼ぶ (clean architecture)
- runops-gateway の `cmd/runops` 既存 subcommand を踏襲

## 関連

- 0008 (Slack flag、registry を validate に使う)
- 0010 (GitHub App、registry の installation_id を使う)
- refs 0013 (project lifecycle、registry の lifecycle 操作 — 中期)
