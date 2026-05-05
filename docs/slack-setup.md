# Slack App Setup

runops-gateway が使用する Slack App の作成・設定手順。

## 必要な Slack 機能

| 機能 | 用途 |
|---|---|
| Incoming Webhooks | `notify-slack.sh` からの Block Kit メッセージ送信 |
| Slash Commands | `/runops` での dispatch リクエスト発行 (Phase 1 以降) |
| Interactivity | ボタンクリックを runops-gateway の HTTP エンドポイントへ転送 |
| Signing Secret | リクエストの正当性検証（HMAC SHA-256） |
| Bot Token | `chat.postMessage` フォールバック (ADR 0017) と HIGH severity 4-eyes 承認 (ADR 0019) で必要 |

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
https://<CLOUD_RUN_URL>/slack/interactive
```

Cloud Run URL は以下で取得できる:

```bash
GATEWAY_PROJECT=your-gcp-project-id
gcloud run services describe runops-gateway \
  --project=${GATEWAY_PROJECT} \
  --region=asia-northeast1 \
  --format="value(status.url)"
```

**Save Changes** をクリック。

---

### 4. Slash Command の登録

Phase 1 以降、`/runops` で dispatch リクエストを発行する。

```
App Home > Features > Slash Commands → Create New Command
```

下記のとおり登録する (本リポ実装は `/slack/command` ルートで受け付ける):

| 項目 | 値 |
|---|---|
| **Command** | `/runops` |
| **Request URL** | `https://<CLOUD_RUN_URL>/slack/command` |
| **Short Description** | `exec runops` |
| **Usage Hint** | `[request]` |
| **Escape channels, users, and links sent to your app** | ✅ チェックする |

`Escape ...` を ON にすると、本文中の `<@U1234>` `<#C1234>` 等が
`<@U1234\|user>` `<#C1234\|general>` に展開されてサーバーに届く。
runops-gateway 側のパースが想定する形式なので **必ず ON**。

Preview of Autocomplete Entry が以下のようになっていれば OK:

```
RunOps Gateway
  /runops [request]    exec runops
```

**Save** をクリックして登録。

---

### 5. Bot Token Scopes の設定

ADR 0017 の `FallbackNotifier` (response_url が 30 分超 / 5 回上限を超え
たときの fallback) と ADR 0019 の HIGH severity 4-eyes 承認 prompt は
`chat.postMessage` を呼ぶため Bot Token が必要。

```
App Home > Features > OAuth & Permissions > Scopes > Bot Token Scopes
```

最低限以下を追加:

| Scope | 用途 |
|---|---|
| `commands` | Slash Commands `/runops` (上で登録済) |
| `chat:write` | `chat.postMessage` で thread reply / approval prompt を送信 |
| `chat:write.public` | bot が join していない public channel にも投稿 (任意、運用で必要なら) |

スコープを変更したらワークスペース再インストールが要求されるので
従う。インストール後 **Bot User OAuth Token** (`xoxb-...`) を控える
(後段の Secret Manager 登録で使う)。

---

### 6. Signing Secret の取得

```
App Home > Settings > Basic Information > App Credentials > Signing Secret
```

**Show** をクリックして値をコピーする。

---

### 7. シークレットの登録（GCP Secret Manager）

tofu apply 後に placeholder を実際の値で上書きする。

> **注意**: シークレットの値をコマンドライン引数に直接書かないこと。
> シェルヒストリーやプロセス一覧（`ps`）から漏洩するリスクがある。

```bash
# Signing Secret（プロンプトから入力、エコーバックなし）
read -rs SIGNING_SECRET && printf '%s' "$SIGNING_SECRET" | \
  gcloud secrets versions add slack-signing-secret \
    --project=${GATEWAY_PROJECT} \
    --data-file=-

# Incoming Webhook URL（プロンプトから入力、エコーバックなし）
read -rs WEBHOOK_URL && printf '%s' "$WEBHOOK_URL" | \
  gcloud secrets versions add slack-webhook-url \
    --project=${GATEWAY_PROJECT} \
    --data-file=-

# Bot Token (xoxb-...) — ADR 0017 / 0019 で必須
read -rs BOT_TOKEN && printf '%s' "$BOT_TOKEN" | \
  gcloud secrets versions add slack-bot-token \
    --project=${GATEWAY_PROJECT} \
    --data-file=-
```

登録後、Cloud Run の env が secret 参照に切り替わっていれば自動で
最新版を読み直す (tofu/main.tf で `version = "latest"` を指定)。
切替直後の revision には rollout が必要:

```bash
gcloud run services update runops-gateway \
  --project=${GATEWAY_PROJECT} \
  --region=asia-northeast1 \
  --update-secrets=SLACK_SIGNING_SECRET=slack-signing-secret:latest,SLACK_BOT_TOKEN=slack-bot-token:latest
```

---

### 8. ワークスペースへのインストール

```
App Home > Settings > Install App → Install to Workspace
```

権限を確認して **許可する** をクリック。
(Bot Token Scopes を後から増やした場合も再インストール必要)

---

## 構成図

```
+--------------------------------+      +----------------------------------+
|  Slack Workspace               |      |  GATEWAY_PROJECT                 |
|                                |      |                                  |
|  notify-slack.sh               |      |  Secret Manager                  |
|  POST Block Kit message ------+----->|  - slack-signing-secret          |
|  via Incoming Webhook URL      |      |    (HMAC verification)           |
|                                |      |  - slack-webhook-url             |
|  User types /runops <request> -+----->|    (Incoming Webhook URL)        |
|  POST /slack/command           |      |  - slack-bot-token  (xoxb-...)   |
|                                |      |    (chat.postMessage fallback)   |
|  User clicks button -----------+----->|                                  |
|  POST /slack/interactive       |      |  runops-gateway (Cloud Run)      |
|  (with response_url)           |      |  1. Verify Signature             |
|                                |      |  2. AuthZ (user allow-list)      |
|  Slack receives reply <--------+------|  3. dispatch / GCP op            |
|    primary:  response_url      |      |  4. notify primary path          |
|    fallback: chat.postMessage  |<-----|     -> (FallbackNotifier on      |
|                                |      |        timeout / 5-call limit)   |
+--------------------------------+      +----------------------------------+
```

Legend / 凡例:

- Incoming Webhook URL: Slack App の通知送信先 URL (hooks.slack.com)
- /slack/command: runops-gateway の Slash Command 受信エンドポイント (Phase 1 以降)
- /slack/interactive: runops-gateway のインタラクション受信エンドポイント
- response_url: ボタン付きメッセージの更新用 URL (Slack がペイロードに含めて送信、30 分有効 / 5 回上限)
- chat.postMessage: Bot Token を使った直接 API。response_url が切れた後の fallback (ADR 0017) と HIGH severity 4-eyes 承認 prompt (ADR 0019) で使う
- Signing Secret: リクエストの正当性を検証するための共有秘密鍵
- Bot Token: chat.postMessage を呼ぶための OAuth トークン (xoxb-...)

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
