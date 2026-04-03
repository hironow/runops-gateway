# Slack App Setup

runops-gateway が使用する Slack App の作成・設定手順。

## 必要な Slack 機能

| 機能 | 用途 |
|---|---|
| Incoming Webhooks | `notify-slack.sh` からの Block Kit メッセージ送信 |
| Interactivity | ボタンクリックを runops-gateway の HTTP エンドポイントへ転送 |
| Signing Secret | リクエストの正当性検証（HMAC SHA-256） |

> Bot Token は不要。メッセージ更新は Slack が払い出す `response_url` のみで行う。

---

## 手順

### 1. Slack App の作成

1. <https://api.slack.com/apps> を開き **Create New App** → **From scratch**
2. App Name（例: `RunOps Gateway`）とインストール先ワークスペースを選択して作成

---

### 2. Incoming Webhooks の有効化

```
App Home > Features > Incoming Webhooks → ON
```

**Add New Webhook to Workspace** をクリックし、通知先チャンネルを選択。

生成された Webhook URL（`https://hooks.slack.com/services/...`）をコピーしておく。

---

### 3. Interactivity の設定

```
App Home > Features > Interactivity & Shortcuts → ON
```

**Request URL** に runops-gateway のエンドポイントを設定する:

```
https://<CLOUD_RUN_URL>/slack/actions
```

Cloud Run URL は以下で取得できる:

```bash
gcloud run services describe runops-gateway \
  --project=GATEWAY_PROJECT \
  --region=asia-northeast1 \
  --format="value(status.url)"
```

**Save Changes** をクリック。

---

### 4. Signing Secret の取得

```
App Home > Settings > Basic Information > App Credentials > Signing Secret
```

**Show** をクリックして値をコピーする。

---

### 5. シークレットの登録（GCP Secret Manager）

tofu apply 後に placeholder を実際の値で上書きする:

```bash
# Signing Secret
echo -n "YOUR_SIGNING_SECRET" | \
  gcloud secrets versions add slack-signing-secret \
    --project=GATEWAY_PROJECT \
    --data-file=-

# Incoming Webhook URL
echo -n "https://hooks.slack.com/services/YOUR/WEBHOOK/URL" | \
  gcloud secrets versions add slack-webhook-url \
    --project=GATEWAY_PROJECT \
    --data-file=-
```

登録後、Cloud Run を再起動してシークレットを反映させる:

```bash
gcloud run services update runops-gateway \
  --project=GATEWAY_PROJECT \
  --region=asia-northeast1 \
  --update-secrets=SLACK_SIGNING_SECRET=slack-signing-secret:latest
```

---

### 6. ワークスペースへのインストール

```
App Home > Settings > Install App → Install to Workspace
```

権限を確認して **許可する** をクリック。

---

## 構成図

```
+-----------------------------+       +-------------------------------+
|  Slack Workspace            |       |  GATEWAY_PROJECT              |
|                             |       |                               |
|  notify-slack.sh            |       |  Secret Manager               |
|  POST Block Kit message     |       |  - slack-signing-secret       |
|  via Incoming Webhook URL --+-------+-> (HMAC verification)         |
|                             |       |  - slack-webhook-url          |
|  User clicks button         |       |    (Incoming Webhook URL)     |
|  POST /slack/actions     ---+-------+->                             |
|  (with response_url)        |       |  runops-gateway (Cloud Run)   |
|                             |       |  1. Verify Signature          |
|  Slack receives update   <--+-------+--  2. AuthZ (user allow-list) |
|  via response_url           |       |  3. GCP operation             |
|                             |       |  4. POST result to response_url|
+-----------------------------+       +-------------------------------+
```

Legend:

- Incoming Webhook URL: Slack App の通知送信先 URL（hooks.slack.com）
- response_url: ボタン付きメッセージの更新用 URL（Slack がペイロードに含めて送信）
- Signing Secret: リクエストの正当性を検証するための共有秘密鍵
- /slack/actions: runops-gateway のインタラクション受信エンドポイント

---

## 許可ユーザーの設定

runops-gateway はボタンをクリックした Slack ユーザーの ID を環境変数 `ALLOWED_SLACK_USERS` と照合する。

Slack ユーザー ID の確認方法:

```
Slack > プロフィール > 「...」メニュー > メンバー ID をコピー
```

GitHub リポジトリ変数に登録する（複数の場合はカンマ区切り）:

```bash
gh variable set ALLOWED_SLACK_USERS \
  --repo hironow/runops-gateway \
  --body "UXXXXXXXXX,UYYYYYYYYY"
```
