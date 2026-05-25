# 0010 GitHub App + Secret Manager 統合

**Target repo:** `hironow/runops-gateway`
**Priority:** P2 (Phase β auth foundation)
**Depends on:** 0009 (project registry に installation_id を持つ)
**Blocks:** refs 0008 (5 ツール側 GH_TOKEN 経路)、refs 0011 (AI agent identity)、refs 0012 (broker daemon)
**Cross-ref:** [tap/refs/docs/issues/0007](../../../../tap/refs/docs/issues/0007-runops-gateway-github-app-secret-manager.md)
**Status:** 📝 未着手

## 概要

GitHub App を hironow org に install、private key を Secret Manager に格納。
gateway は project ごとの installation_id を registry から引き、必要に応じて
installation token を fetch して経路に流す。

## 動機

[refs/docs/github-integration.md](https://github.com/hironow/tap/blob/main/refs/docs/github-integration.md) §"戦略 1: GitHub App + Secret Manager + per-invocation token fetch"
を採用 (hironow 決定 2026-05-06)。

戦略 1 の理由:

- scope 細やか (project 単位 install)
- audit 強い (GitHub App 単位)
- token rotation 自動 (installation token は 1 時間)
- 3 経路 (operator local / long-lived / Slack) 統一可能

## 受入基準

- [ ] GitHub App `runops-hironow` (仮) を hironow org に作成
- [ ] private key を Secret Manager に格納 (例: `github-app-private-key`、project ごと if scope 分離)
- [ ] gateway 内に `port.GitHubTokenBroker` interface
- [ ] `adapter/output/github` に installation token fetch 実装
      (`go-github` lib + `bradleyfalzon/ghinstallation` 等)
- [ ] Cloud Run SA に `roles/secretmanager.secretAccessor` 付与 (tofu)
- [ ] gateway 内 token cache (project_id + 50 min TTL) で Secret Manager API 連発防止
- [ ] integration test: GitHub App → installation token 取得 → octokit で repo read 1 回成功

## 実装ヒント

- ライブラリ候補: `github.com/bradleyfalzon/ghinstallation/v2`
- Cache 戦略: `golang.org/x/sync/singleflight` で同 project 並列要求の dedup
- ADR 草案: `docs/adr/00NN-github-app-auth.md`
- 5 ツール側は gh CLI 経由で `GH_TOKEN` env を auto-pickup する設計が既に確認済
  ([tap/refs/docs/audit-multiplex-readiness.md](https://github.com/hironow/tap/blob/main/refs/docs/audit-multiplex-readiness.md) §軸 4)
  本 issue は token を **gateway 側で fetch して producer (operator/Slack ハンドラ/AI agent dispatch) へ流す経路**を作る

## 関連

- 0009 (registry に installation_id)
- refs 0008 (5 ツールへの token 流入、documentation-only として完了済)
- refs 0011 (AI agent identity)
- refs 0012 (broker daemon、中期)
