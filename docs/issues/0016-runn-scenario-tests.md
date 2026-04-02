# Issue 0016: runn シナリオテスト

## Goal

`tests/runn/` に runn を使った API シナリオテストを追加する。
実際の HTTP サーバーを `httptest.NewServer` で起動し、Slack の署名付きリクエストを送信して
エンドポイントの E2E 動作を確認する。

## テストシナリオ

### `tests/runn/approve_canary.yaml`

Agent がカナリアデプロイを承認するシナリオ:

1. Slack 署名付き POST `/slack/interactive` (action_id=approve, resource_type=service)
2. 200 OK が返ること
3. レスポンスボディが空であること（非同期処理のため）

### `tests/runn/deny_operation.yaml`

Agent が操作を拒否するシナリオ:

1. Slack 署名付き POST `/slack/interactive` (action_id=deny)
2. 200 OK が返ること

### `tests/runn/invalid_signature.yaml`

不正署名リクエストのシナリオ:

1. 署名なし POST `/slack/interactive`
2. 401 Unauthorized が返ること

### `tests/runn/healthz.yaml`

ヘルスチェックシナリオ:

1. GET `/healthz`
2. 200 OK、`{"status":"ok"}` が返ること

## justfile への追加

```
# Run scenario tests (requires server to be running)
test-runn:
    runn run tests/runn/*.yaml
```

## Definition of Done (DoD)

- [ ] 4 シナリオファイルが存在する（`.yaml` 拡張子）
- [ ] `runn run tests/runn/healthz.yaml` が通る（サーバー起動なしで確認可能な範囲）
- [ ] シナリオは Agent 視点の記述になっている（「POST する」ではなく「Agent が承認リクエストを送る」）

## 非機能要件

- ファイル拡張子は必ず `.yaml`（`.yml` 禁止）
- モック禁止（E2E テストのため実サーバーを使う）
