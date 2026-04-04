# Issue 0009: cmd/server - HTTP サーバーエントリーポイント

## Goal

依存解決（Wiring）を行い、HTTP サーバーを起動する `cmd/server/main.go` を実装する。

## 実装内容

- 環境変数の読み込みとバリデーション（起動時チェック）
- 各アダプターのインスタンス化と UseCase への注入
- `POST /slack/interactive` エンドポイントの登録
- `GET /healthz` ヘルスチェックエンドポイントの登録
- `PORT` 環境変数（デフォルト: `8080`）でのリッスン
- Graceful shutdown（`os.Signal` + `http.Server.Shutdown`）

## 必要な環境変数

| 変数名 | 必須 | 説明 |
|---|---|---|
| `SLACK_SIGNING_SECRET` | ✅ | Slack 署名検証用シークレット |
| `ALLOWED_SLACK_USERS` | — | 許可ユーザー ID（カンマ区切り） |
| `BUTTON_EXPIRY_SECONDS` | 任意 | ボタン有効期限（デフォルト 7200） |
| `PORT` | 任意 | リッスンポート（デフォルト 8080） |

> GCP プロジェクト ID とリージョンはサーバーの環境変数ではなく、Slack ボタン値から取得される（クロスプロジェクト対応）。

## Definition of Done (DoD)

- [ ] 必須環境変数が欠落している場合に起動時エラーを出して `exit 1` で終了するテスト
- [ ] `GET /healthz` が `200 OK` と `{"status":"ok"}` を返すテスト
- [ ] `SIGTERM` シグナル受信でサーバーが graceful shutdown するテスト（または手動確認）
- [ ] 全依存が正しく Wiring されること（統合テストで `/healthz` が疎通すること）

## 非機能要件

- **可用性**: Cloud Run のヘルスチェックが `/healthz` で通ること（`liveness probe` 相当）
- **セキュリティ**: `SLACK_SIGNING_SECRET` が起動ログに出力されないこと
- **可観測性**: 起動時に使用中の設定値（機密情報以外）が structured log に出力されること
- **信頼性**: graceful shutdown により、処理中のリクエストが完了してから終了すること
- **CLI モード独立性**: `cmd/server` は HTTP サーバー専用であり、CLI とは独立していること
