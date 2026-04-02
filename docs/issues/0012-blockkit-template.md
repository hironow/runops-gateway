# Issue 0012: Slack Block Kit テンプレート

## Goal

承認・拒否ボタン付きの Slack メッセージテンプレートを定義する。
環境（本番 / ステージング / 開発）が一目でわかる指差し確認用画像を含む。

## テンプレート仕様

### 表示項目

| フィールド | 内容 |
|---|---|
| タイトル | 操作の種別（例: `🚀 カナリアデプロイ承認リクエスト`） |
| 環境 | `production` / `staging` / `development`（画像で視覚的に強調） |
| リソース種別 | `Cloud Run Service` / `Cloud Run Job` / `Cloud Run Worker Pool` |
| リソース名 | 例: `frontend-service` |
| 対象リビジョン | 例: `frontend-service-00042-abc`（Job の場合は N/A） |
| アクション | 例: `10% Canary` / `DB Migration Apply` |
| 実行者（ビルド） | Cloud Build トリガー元（GitHub SHA / ブランチ） |
| 発行日時 | ISO 8601 形式 |
| 有効期限 | 発行から 2時間後 |

### 指差し確認用画像

操作ミスを防ぐため、環境ごとに異なる色・アイコンの画像を使用する。

| 環境 | 画像 URL（仮） | 視覚的特徴 |
|---|---|---|
| production | `https://placehold.co/75x75/FF0000/FFFFFF?text=PROD` | 赤背景・白文字 `PROD` |
| staging | `https://placehold.co/75x75/FFA500/FFFFFF?text=STG` | オレンジ背景・白文字 `STG` |
| development | `https://placehold.co/75x75/008000/FFFFFF?text=DEV` | 緑背景・白文字 `DEV` |

> 本番実装では Google Cloud Storage に配置したオリジナル画像に差し替える。

### Block Kit JSON テンプレート（production / canary リリースの例）

```json
{
  "blocks": [
    {
      "type": "header",
      "text": {
        "type": "plain_text",
        "text": "🚀 デプロイ承認リクエスト",
        "emoji": true
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "新しいデプロイの承認が必要です。\n内容を確認し、Approve または Deny を選択してください。"
      }
    },
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "*環境:*\n`production`\n*リソース種別:*\nCloud Run Service\n*リソース名:*\n`frontend-service`\n*対象リビジョン:*\n`frontend-service-00042-abc`\n*アクション:*\n10% Canary リリース\n*ビルド:*\n`main @ a1b2c3d`\n*発行日時:*\n2026-04-02T10:00:00+09:00\n*有効期限:*\n2026-04-02T12:00:00+09:00 まで"
      },
      "accessory": {
        "type": "image",
        "image_url": "https://placehold.co/75x75/FF0000/FFFFFF?text=PROD",
        "alt_text": "PROD environment indicator"
      }
    },
    {
      "type": "divider"
    },
    {
      "type": "actions",
      "elements": [
        {
          "type": "button",
          "text": {
            "type": "plain_text",
            "emoji": true,
            "text": "✅ Approve"
          },
          "style": "primary",
          "action_id": "approve",
          "value": "{\"resource_type\":\"service\",\"resource_name\":\"frontend-service\",\"target\":\"frontend-service-00042-abc\",\"action\":\"canary_10\",\"issued_at\":1711990000}"
        },
        {
          "type": "button",
          "text": {
            "type": "plain_text",
            "emoji": true,
            "text": "🚫 Deny"
          },
          "style": "danger",
          "action_id": "deny",
          "value": "{\"resource_type\":\"service\",\"resource_name\":\"frontend-service\",\"target\":\"frontend-service-00042-abc\",\"action\":\"canary_10\",\"issued_at\":1711990000}"
        }
      ]
    }
  ]
}
```

### 完了後のメッセージ（ボタン消去版）

```json
{
  "replace_original": true,
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "✅ *承認済み・実行完了*\n`frontend-service` の 10% Canary リリースが完了しました。\n承認者: <@U0123ABCD>"
      },
      "accessory": {
        "type": "image",
        "image_url": "https://placehold.co/75x75/FF0000/FFFFFF?text=PROD",
        "alt_text": "PROD environment indicator"
      }
    }
  ]
}
```

### 拒否後のメッセージ

```json
{
  "replace_original": true,
  "blocks": [
    {
      "type": "section",
      "text": {
        "type": "mrkdwn",
        "text": "🚫 *拒否済み*\n`frontend-service` の 10% Canary リリースは拒否されました。\n拒否者: <@U0123ABCD>"
      }
    }
  ]
}
```

## Go 実装（`internal/adapter/output/slack/blockkit.go`）

Block Kit JSON は Go のテンプレートまたは構造体で生成する。

```go
type DeploymentPayload struct {
    Environment  string // "production", "staging", "development"
    ResourceType string
    ResourceName string
    Target       string
    Action       string
    BuildInfo    string // "main @ a1b2c3d"
    IssuedAt     time.Time
    ActionValue  string // JSON 文字列（ボタンの value）
}

func BuildApprovalMessage(p DeploymentPayload) map[string]interface{}
func BuildCompletionMessage(approverID, summary string, env string) map[string]interface{}
func BuildDenialMessage(denierID, summary string) map[string]interface{}
func EnvironmentImageURL(env string) string
```

## Definition of Done (DoD)

- [ ] `BuildApprovalMessage` が production / staging / development で異なる画像 URL を返すテスト
- [ ] `BuildApprovalMessage` の `value` フィールドに正しい JSON が含まれるテスト
- [ ] `BuildCompletionMessage` にボタン（actions ブロック）が含まれないテスト（二重実行防止）
- [ ] Block Kit JSON が Slack Block Kit Builder でバリデーションを通ること（手動確認）
- [ ] 各環境の画像が実際に表示される URL であること（手動確認）

## 非機能要件

- **UX（最重要）**: 環境（production/staging）が一目で判別できること。色盲に配慮した色使い（赤＋テキストラベル）にすること
- **セキュリティ**: `value` フィールドの JSON に機密情報（シークレット等）が含まれないこと
- **保守性**: 画像 URL は定数または設定ファイルで一元管理すること（ハードコード禁止）
