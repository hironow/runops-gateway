# runops-gateway: 設計意図

## 概要

`runops-gateway` は、CI/CDパイプライン（Cloud Build / GitHub Actions）と GCP 本番環境の間に位置し、
Slack からのインタラクティブな操作（ChatOps）を安全に GCP リソースへ仲介する API ゲートウェイ（ミドルウェア）である。

カナリアリリースのトラフィック操作や DB マイグレーションの実行といったクリティカルな状態変更を、
適切な認可・監査・UX を伴って実行することが目的となる。

---

## 解決する課題

### 背景

GitHub Actions から Cloud Build をトリガーするだけで即終了する設計（Fire-and-Forget）において、
デプロイされたリビジョンへのトラフィック切り替えやマイグレーション実行を人間が制御できる仕組みが必要。

具体的には以下の問題を解決する:

- **デプロイとトラフィック切り替えの分離**: 新リビジョンをトラフィック 0% でデプロイし、Slack ボタンで段階的にカナリア昇格
- **マイグレーションのタイミング制御**: DB スキーマ変更をコードデプロイのライフサイクルから切り離し、人間が明示的にトリガー
- **操作の可視化と認可**: 誰が、いつ、何をしたかを Slack チャンネルの履歴として残す

---

## アーキテクチャ

### 全体フロー

2 つの独立したパイプラインが存在する。

#### (A) runops-gateway 自体のデプロイ（GitHub Actions）

```
[ Developer ]
      |
      | git push (main branch)
      v
+----------------------------------------------+
| GitHub Actions (.github/workflows/cd.yaml)   |
|                                              |
|  check-changes: paths-filter (tofu/**)       |
|                                              |
|  infra job (tofu/** 変更時のみ):             |
|    - tofu init (GCS remote state)            |
|    - tofu apply (WIF / IAM / Cloud Run 等)   |
|                                              |
|  deploy job (常時):                          |
|    - docker build & push (Artifact Registry) |
|    - deploy-cloudrun@v2 (traffic 100%)       |
+----------------------------------------------+
      |
      | 認証: Workload Identity Federation (keyless)
      | SA: chatops-github-deployer@...
      v
+------------------------------+
| runops-gateway (Cloud Run)   |
| asia-northeast1              |
+------------------------------+

Legend:
- GitHub Actions: runops-gateway 自体の CI/CD
- Workload Identity Federation: キーレス OIDC 認証 (SA キー不要)
- Artifact Registry: asia-northeast1-docker.pkg.dev/{PROJECT}/runops
- tofu state: GCS バケット (gen-ai-hironow-tofu-state)
```

#### (B) 管理対象アプリのデプロイ → Slack ChatOps フロー

```
[ 管理対象アプリの CI/CD (Cloud Build 等) ]
      |
      |  a. Build & Push Container Images
      |  b. Cloud Run Service: Deploy (Traffic 0%)
      |  c. Cloud Run Jobs: Update Template
      |  d. notify-slack.sh -> POST Block Kit UI
      v
+---------------------------------------------+
| Slack (Workspace)                           |
|                                             |
|  [ 1. DBマイグレーション実行 ]              |
|  [ 2. 10% Canary リリース    ]              |
+---------------------------------------------+
                            |
                            | Click Button
                            v
+---------------------------------------------+
| runops-gateway (Go on Cloud Run)            |
|                                             |
|  1. Verify Signature (Slack 3sec rule)      |
|  2. AuthZ (User + Expiry check)             |
|  3. Router (Service / Job / Worker Pool)    |
|  4. Update Slack message (processing...)    |
|  5. LRO Polling & Error Handling            |
+---------------------------------------------+
                    |                    |
         Traffic Shift          Backup & Migrate
                    v                    v
  +--------------------+   +------------------------+
  | Cloud Run Service  |   | Cloud SQL              |
  |  V1: 90% (Prev)    |   | 1. On-Demand Backup    |
  |  V2: 10% (Canary)  |   +------------------------+
  +--------------------+              |
                                      v
                            +------------------------+
                            | Cloud Run Jobs         |
                            | - Apply Schema         |
                            +------------------------+

Legend:
- runops-gateway: ゲートウェイ（本リポジトリ）
- Cloud Build: 管理対象アプリ側の CI/CD (例)
- notify-slack.sh: scripts/ 以下のシェルスクリプト
- Cloud Run Service: HTTP トラフィックを処理するサービス
- Cloud Run Jobs: バッチ処理（マイグレーション等）
- Cloud SQL: リレーショナルデータベース
```

### 設計原則

**関心の分離 (Separation of Concerns)**

- 管理対象アプリの CI/CD（Cloud Build 等）はビルド・デプロイ（トラフィック 0%）と Slack 通知のみを担当
- runops-gateway がすべての「状態変更」を担うハブとなる
- Slack は入力インターフェースの 1 つに過ぎない（CLI も等価に扱う）
- runops-gateway 自体のデプロイは GitHub Actions が担当し、インフラは OpenTofu で管理する

**Ports and Adapters パターン（Hexagonal Architecture）**

コアロジック（UseCase）を外部インターフェースから完全に分離する。

```
Driving Adapters (Input)     Core Domain         Driven Adapters (Output)
+--------------------+    +-----------+    +----------------------+
|  Slack HTTP Handler| -> |           | -> | GCP Cloud Run API    |
|  CLI (Cobra)       | -> | UseCase   | -> | GCP Cloud SQL API    |
+--------------------+    |           | -> | Slack Notification   |
                          +-----------+    +----------------------+

Legend:
- Driving Adapters: 入力アダプター（Slack, CLI）
- Core Domain: ビジネスロジック（認可・オーケストレーション）
- Driven Adapters: 出力アダプター（GCP API, Slack API）
```

これにより、Slack と CLI の両方から `ApprovalRequest` という単一の構造体として透過的にコアへ渡される。

---

## 対応リソース種別

| リソース種別 | 操作概念 | 使用 API |
|---|---|---|
| Cloud Run Service | トラフィック割合の分割（カナリア） | `UpdateService` |
| Cloud Run Worker Pool | インスタンス割り当ての分割 | `UpdateWorkerPool` |
| Cloud Run Jobs | ジョブの即時実行 | `RunJob`（引数オーバーライド可） |

### 後方互換性の注意

- **Service / Worker Pool**: カナリア中は新旧バージョンが同時稼働するため、DB スキーマ・API レスポンス・メッセージフォーマットの後方互換性が必須
- **Jobs**: 1 回の実行は単一コンテナで完結するため並行稼働リスクは低いが、書き込みデータに対する互換性は担保が必要

---

## セキュリティ要件

### リクエスト検証

- `X-Slack-Signature` ヘッダーを使った HMAC SHA-256 署名検証（必須）
- `SLACK_SIGNING_SECRET` は GCP Secret Manager から実行時に読み込む

### 認可

- ペイロード内の `user.id` を許可リスト（環境変数または Slack ユーザーグループ）と照合
- 未認可ユーザーへは元のメッセージを変更せず `ephemeral` メッセージで通知
- ボタンに UNIXタイムスタンプを埋め込み、発行から **2時間（7200秒）** 経過後は無効化

### 最小権限の原則（IAM）

- runops-gateway のサービスアカウント: `run.developer` + `cloudsql.admin`（バックアップ用）
- マイグレーション Job のサービスアカウント: DB クライアント権限のみ
- アプリケーション用 Cloud Run のサービスアカウント: DB クライアント権限のみ

---

## マイグレーションワークフロー

DB マイグレーション実行時は以下の順で処理する（runops-gateway がオーケストレーション）:

1. **Slack 通知**: 「📦 DB バックアップを取得中...」
2. **Cloud SQL バックアップ**: LRO で完了を待機
3. **Slack 通知**: 「✅ バックアップ完了。マイグレーションを実行します...」
4. **Cloud Run Jobs 実行**: `--mode=apply` 引数オーバーライドで実行、LRO で完了待機
5. **Slack 通知**: 完了またはエラーを通知し、ボタンを消去

バックアップ処理を Job コンテナ内ではなく runops-gateway 側で行う理由: Job コンテナへの過剰な権限付与（`cloudsql.admin`）を回避するため。

---

## 非同期処理と UX

### Slack 3秒ルール

Slack の Interactive Payload は 3秒以内に HTTP 200 OK を返さないとエラー表示される。
そのため、リクエスト受信後に Goroutine で非同期処理へ逃がし、即座に 200 OK を返す。

### LRO 進捗のフィードバック

- 処理中は `response_url` を使って元のメッセージを「⏳ 処理中...」に上書き
- 完了時は `replace_original: true` でボタンを消去した完了メッセージに置き換え（二重実行防止）

### CPU スロットリング設定

Cloud Run のデフォルト設定では、レスポンス返却直後に CPU がスロットリングされ Goroutine が凍結される。
runops-gateway は必ず `run.googleapis.com/cpu-throttling = false`（CPU Always Allocated）で稼働させる。

---

## CLI サポート

CLI からも Slack と同等の操作が可能。`internal/adapter/input/cli` に Cobra を用いたコマンドを実装する。

```
# 承認操作
runops approve service frontend-service \
  --project=your-project --location=asia-northeast1 --action=canary_10

# 拒否操作
runops deny service frontend-service \
  --project=your-project --location=asia-northeast1
```

CLI から実行した場合も、Slack チャンネルの該当メッセージ（ボタン）を `chat.update` API で無効化し、
「[CLI 経由] 承認済み」に更新することで Slack 上の状態と整合性を保つ。

---

## ディレクトリ構成

```
runops-gateway/
├── cmd/
│   ├── server/           # HTTP サーバーエントリーポイント
│   └── runops/           # CLI エントリーポイント
├── internal/
│   ├── core/
│   │   ├── domain/       # エンティティ（Action, ResourceType 等）
│   │   └── port/         # Primary / Secondary インターフェース定義
│   ├── usecase/          # コアビジネスロジック（Approve / Deny）
│   └── adapter/
│       ├── input/
│       │   ├── slack/    # HTTP Handler, 署名検証, parseActionValue (gz: 展開)
│       │   └── cli/      # Cobra コマンド実装
│       └── output/
│           ├── gcp/      # Cloud Run / Cloud SQL API クライアント
│           ├── slack/    # Slack メッセージ更新ロジック + compressButtonValue
│           ├── auth/     # EnvAuthChecker (allowlist + 有効期限)
│           └── state/    # MemoryStore (TryLock/Release)
├── scripts/
│   └── notify-slack.sh   # 管理対象アプリの CI/CD から呼ばれる Slack 通知スクリプト
├── tofu/             # インフラ定義（Cloud Run, IAM, WIF, AR 等）
├── .github/
│   └── workflows/
│       ├── ci.yaml   # テスト（全ブランチ）
│       └── cd.yaml   # ビルド・インフラ適用・デプロイ（main push）
├── go.mod
└── Dockerfile

Legend:
- cmd/: エントリーポイント群
- internal/core/: ドメインとポート定義（外部依存なし）
- internal/usecase/: ビジネスロジック
- internal/adapter/: 入出力アダプター
- scripts/: CI/CD から呼ばれるシェルスクリプト
- tofu/: IaC（Infrastructure as Code）
```

---

---

## ボタン値の圧縮・制限管理

### Slack Block Kit フィールド長制限

Block Kit の各フィールドには Slack 側の文字数制限がある。

| フィールド | 上限 |
|---|---|
| header block `plain_text` | 150 文字 |
| section `mrkdwn` text | 3,000 文字 |
| button `value` | **2,000 文字** |
| button `text.text` | 75 文字 |

`safeTrunc(s, max)` で表示フィールドを rune 単位で切り詰める。
ボタン値は JSON → gzip → base64url 圧縮で制限内に収める。

### ボタン値の常時 gzip 圧縮（ADR 0011）

`ApprovalRequest` を JSON シリアライズした後、**常に** gzip + base64url 圧縮して `gz:` プレフィックスを付与する。

```
encode: JSON → gzip → base64url (RawURLEncoding) → "gz:" + ...
decode: "gz:" 検出 → base64url decode → gunzip → JSON parse
```

常時圧縮にすることで `parseActionValue` のデコードパスが全てのボタンクリックで実行され、
ラウンドトリップのバグを早期に検出できる。

圧縮後も 2,000 文字を超える稀なケースでは `buttonValueError` が検知し、
ボタンを壊す代わりに専用エラーメッセージを Slack に投稿する。

### scripts/notify-slack.sh

Cloud Build の Slack 通知ロジックを `scripts/notify-slack.sh` に外部化している。

- CI/CD パイプライン (`cloudbuild.yaml`) から呼ばれる
- `--dry-run` フラグで JSON ペイロードを標準出力に出力（テスト用）
- `compress_gz()` 関数が Go の `compressButtonValue` と同一アルゴリズムを実装
- Go テスト (`notify_script_test.go`) で bash→Go のラウンドトリップを保証

---

## Architecture Decision Records (ADR)

詳細は `docs/adr/` を参照。

| No | タイトル | 決定 |
|---|---|---|
| 0001 | CI/CD と状態変更ロジックの分離 | CI/CD はデプロイと Slack 通知のみ。状態変更は gateway に集約 |
| 0002 | Slack 3秒ルールの回避 | Goroutine による非同期処理 + `response_url` での逐次更新 |
| 0003 | CPU スロットリング無効化 | `cpu-throttling = false` 必須（Goroutine 凍結防止） |
| 0004 | マイグレーション前バックアップの分離 | バックアップは gateway 側でトリガー（最小権限の原則） |
| 0005 | Ports and Adapters パターンの採用 | Slack / CLI を対等な Driving Adapter として扱う |
| 0006 | CLI 操作時の Slack メッセージ同期 | CLI 実行時も `chat.update` で Slack 上のボタンを無効化 |
| 0007 | CLI モードの Slack 独立性 | `--no-slack` で Slack なし緊急操作を可能にする |
| 0008 | Progressive Canary Rollout | `OfferContinuation` で段階的カナリア昇格ボタンを動的生成 |
| 0009 | Canary/Migration ダブル確認 | DB マイグレーション後にカナリアボタンを再提示 |
| 0010 | Multi-Resource Deployment | CSV バンドル + 補償ロールバックで複数リソースの All-or-Nothing を保証 |
| 0011 | ボタン値の常時 gzip 圧縮 | 無条件圧縮でデコードパスを常時テスト・2,000 文字制限を透過的に回避 |
