# Issue 0003: UseCase - Approve / Deny

## Goal

`ApproveAction` / `DenyAction` のコアオーケストレーションロジックを TDD で実装する。
GCP や Slack の具体実装には依存せず、Port インターフェースのみに依存する。

## Approve フロー（resource_type 別）

### service

1. 認可チェック（失敗時: ephemeral 通知して終了）
2. 有効期限チェック（失敗時: ephemeral 通知して終了）
3. `Notifier.UpdateMessage` で「⏳ トラフィック切り替え中...」通知
4. `GCPController.ShiftTraffic` でトラフィック切り替え（LRO 完了待ち）
5. `Notifier.ReplaceMessage` で「✅ 完了」通知（ボタン消去）

### job（マイグレーション）

1. 認可チェック / 有効期限チェック
2. `Notifier.UpdateMessage` で「📦 DB バックアップを取得中...」通知
3. `GCPController.TriggerBackup` で Cloud SQL バックアップ（LRO 完了待ち）
4. `Notifier.UpdateMessage` で「✅ バックアップ完了。マイグレーション実行中...」通知
5. `GCPController.ExecuteJob` で Job 実行（LRO 完了待ち）
6. `Notifier.ReplaceMessage` で「✅ マイグレーション完了」通知（ボタン消去）

### worker-pool

1. 認可チェック / 有効期限チェック
2. `Notifier.UpdateMessage` で「⏳ インスタンス割り当て切り替え中...」通知
3. （ShiftTraffic と同等の処理、Worker Pool 用 API に委譲）
4. 完了通知・ボタン消去

## Deny フロー

1. `Notifier.ReplaceMessage` で「🚫 拒否済み（by <ApproverID>）」通知（ボタン消去）

## 認可・有効期限チェック失敗時

- `Notifier.SendEphemeral` でエラーを本人のみに通知
- 元のメッセージ（ボタン）は保持する
- エラーを返さない（Slack への 200 OK 相当）

## Definition of Done (DoD)

- [ ] service / job / worker-pool の Approve フロー各テストが存在する（Red → Green → Refactor）
- [ ] Deny フローのテストが存在する
- [ ] 認可失敗・有効期限切れのテストケースが存在する（各 2 ケース以上）
- [ ] 未知の `resource_type` の場合にエラーを返すテストが存在する
- [ ] GCP API 失敗（各ステップ）時にエラー通知が送信されるテストが存在する
- [ ] Slack ダウン想定（Notifier がエラーを返す）でも処理が継続することを確認するテスト

## 非機能要件

- **テスタビリティ**: モック実装のみで全テストが通ること（実 GCP / 実 Slack 不要）
- **冪等性**: 同一の `ApprovalRequest` が 2 回実行されても安全に処理されること（エラーまたは no-op）
- **CLI モード対応**: `req.ResponseURL` が空でも `StdoutNotifier` を通じて全フローが完結すること（ADR 0007）
- **可観測性**: 各ステップの開始・完了・エラーが structured log として出力されること
