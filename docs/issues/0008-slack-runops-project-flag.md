# 0008 Slack `/runops` command に `--project=<id>` flag

**Target repo:** `hironow/runops-gateway`
**Priority:** P1 (multiplex Phase α blocker)
**Depends on:** 0009 (project registry SoT)
**Blocks:** Phase α 完成
**Cross-ref:** [tap/refs/docs/issues/0003](../../../../tap/refs/docs/issues/0003-runops-gateway-slack-command-project-flag.md)
**Status:** 📝 未着手

## 概要

`/runops paintress --project=foo "<request>"` 構文サポート。
Block Kit Approve UI で project 名を表示、interactive callback で project 引き継ぎ、
Pub/Sub publish 時に `project_id` attribute 付与。

## 動機

multiplex で「どの project への dispatch か」を operator が明示できる UI が必要。
Slack channel 単位で project を固定する案もあるが、operator が同じ Slack channel から
複数 project に dispatch する scenario も現実的。

## 受入基準

- [ ] `/runops <tool> --project=<id> "<request>"` をパース
- [ ] `--project` 未指定時は **gateway DB の default project** を使用 (or エラー — 0009 との依存で挙動決定)
- [ ] `<id>` が registry に未登録なら **Slack ephemeral message でエラー応答** (DM kick は不要)
- [ ] Block Kit Approve UI に "Project: foo" を表示
- [ ] `interactive_handler` で `value` または `private_metadata` 経由で `project_id` を引き継ぎ
- [ ] DispatchAgentTask usecase が `project_id` を Pub/Sub message attribute に乗せる
- [ ] approval (4-eyes) flow でも `project_id` が一貫して引き継がれる
- [ ] integration test: 2 project 分 dispatch して別 outbox に届くこと (0006 と組み合わせ)

## 実装ヒント

- 既存 `slash command` parser に flag-style argument を加える (`shell-words` lib で split 推奨)
- Block Kit `private_metadata` に project_id を JSON で persist
- ADR 草案: `docs/adr/00NN-project-id-flag.md`

## 関連

- 0009 (registry SoT)
- 0006 / 0007 (Pub/Sub 両端)
