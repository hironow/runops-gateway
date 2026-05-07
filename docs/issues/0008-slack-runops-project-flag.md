# 0008 Slack `/runops` command に `--project=<id>` flag

**Target repo:** `hironow/runops-gateway`
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** 0009 (project registry SoT)
**Blocks:** Phase α 完成
**Cross-ref:** [tap/refs/docs/issues/0003](../../../../tap/refs/docs/issues/0003-runops-gateway-slack-command-project-flag.md)、 ADR 0027 (本 PR で起票、 dispatch path metadata carry standard)
**Status:** 🟡 着地 (parser + registry validate + button value carry + Block Kit + Pub/Sub attribute + ADR 0027、 4-eyes approval carry は scope 外で defer)

## 概要

`/runops paintress --project=foo "<request>"` 構文サポート。
Block Kit Approve UI で project 名を表示、interactive callback で project 引き継ぎ、
Pub/Sub publish 時に `project_id` attribute 付与。

## 動機

multiplex で「どの project への dispatch か」を operator が明示できる UI が必要。
Slack channel 単位で project を固定する案もあるが、operator が同じ Slack channel から
複数 project に dispatch する scenario も現実的。

## 受入基準

- [x] `/agent <role> --project=<id> "<request>"` をパース (reject-first parser、 14 ケース unit test)
- [x] `--project` 未指定時は project_id 空のまま carry (default project 解決は #0013 lifecycle CLI で defer)
- [x] `<id>` が registry に未登録なら **Slack ephemeral message でエラー応答**
- [x] archived project_id も同様に reject (registry status check)
- [x] 重複 `--project=foo --project=bar` は parser-level で reject
- [x] registry が wire されていない deployment で `--project` 指定 → fail-closed ephemeral error
- [x] Block Kit Approve UI に "Project: foo" を表示 (project_id 空の場合は行不在 = backward compat)
- [x] `interactive_handler` で button value 経由で `project_id` を引き継ぎ
- [x] DispatchAgentTask 経路 (PubsubDispatcher) が `project_id` を Pub/Sub message attribute に carry
- [x] cmd/server に ProjectRegistry を opt-in 配線 (env-driven、 cleanup defer)
- [x] handler unit test 6 ケース (happy / unknown / 未指定 / duplicate / archived / disabled)
- [x] ADR 0027 起票

### defer 項目 (本 issue scope 外)

- [ ] **approval (4-eyes) flow への project_id carry** → 別 issue (将来別 ADR で契約定義、 ADR 0027 §Out-of-scope に記載)
- [ ] **#0006** dmail-receiver multi-project path (workspace VM env routing)
- [ ] **#0007** dmail-emitter が D-Mail frontmatter に project_id 書込
- [ ] **#0013** default project 解決 (operator UX 改善、 lifecycle CLI で扱う)
- [ ] integration test 2 project full flow → #0006/#0007 完了後に gateway-driven E2E として実装 (refs #0019)

## 実装ヒント

- 既存 `slash command` parser に flag-style argument を加える (`shell-words` lib で split 推奨)
- Block Kit `private_metadata` に project_id を JSON で persist
- ADR 草案: `docs/adr/00NN-project-id-flag.md`

## 関連

- 0009 (registry SoT)
- 0006 / 0007 (Pub/Sub 両端)
