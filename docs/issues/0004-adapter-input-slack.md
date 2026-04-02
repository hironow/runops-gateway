# Issue 0004: Adapter - Input - Slack HTTP Handler

## Goal

Slack Interactive Payload を受け取り、署名検証・ペイロードパースを行い、
`RunOpsUseCase` へ委譲する Driving Adapter を実装する。

## エンドポイント

`POST /slack/interactive`

## 処理フロー

1. `io.ReadAll` でリクエストボディ読み込み（検証前に消費しない）
2. `X-Slack-Signature` / `X-Slack-Request-Timestamp` で HMAC SHA-256 署名検証
3. `x-www-form-urlencoded` の `payload` フィールドから JSON をデコード
4. `domain.ApprovalRequest` へのマッピング（`action_id` で approve/deny を判定）
5. Goroutine で `useCase.ApproveAction` または `DenyAction` を非同期実行
6. 即座に `200 OK` を返す（Slack 3秒ルール回避）

## Slack Payload の `value` JSON スキーマ

```json
{
  "resource_type": "service",
  "resource_name": "frontend-service",
  "target": "frontend-service-v001",
  "action": "canary_10",
  "issued_at": 1711900000
}
```

## Definition of Done (DoD)

- [ ] 不正な署名のリクエストは `401 Unauthorized` を返すテスト
- [ ] 有効な署名のリクエストは 3秒以内（測定不要、ノンブロッキングの確認）に `200 OK` を返すテスト
- [ ] 署名検証のユニットテストが存在する（正常・異常各 2 ケース以上）
- [ ] ペイロードパース（`value` の JSON デコード）のユニットテストが存在する
- [ ] `action_id` が "approve" / "deny" 以外の場合のハンドリングテストが存在する
- [ ] `Actions` が空の場合の安全な処理テストが存在する

## 非機能要件

- **セキュリティ**: 署名検証をスキップするコードパスが存在しないこと
- **セキュリティ**: `SLACK_SIGNING_SECRET` がログに出力されないこと
- **可用性**: Slack から予期しないペイロード形式が来た場合でもパニックしないこと
- **CLI モード独立性**: このアダプターは完全に Slack 専用であり、CLI モードとは独立していること（ADR 0007）
