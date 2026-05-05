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

---

# 拡張意図: 5本柱 D-Mail Dispatcher 化

> このセクションは Phase 0（既存 ChatOps）の上に積み増す **Phase 1〜4 の拡張意図**
> を扱う。Phase 0 の意図は本ドキュメント前半（「概要」〜「ADR」）に記述済み。
> 当セクションを読む前に前半を一通り読むこと。

## なぜこのセクションが存在するのか

このリポジトリは現在「Slack ChatOps gateway for GCP operations」として動作している。
ここに「**5本柱 AI Agent への D-Mail dispatch**」という新しいドメインを足す判断をした。
本セクションはその判断の背景と境界を、コードを読んだだけでは復元できない粒度で記す。

将来このリポジトリを触る人（自分自身を含む）が、なぜこの設計を選んだかを
理解できるようにすることが目的である。

---

## 5本柱と D-Mail Protocol の前提

このセクションを読む前に、以下を **既に存在するもの** として理解する必要がある。
詳細は各リポジトリ (`/Users/nino/tap/{sightjack,paintress,amadeus,dominator,phonewave}`)
の README を参照。

### 5本柱（Steins;Gate / SIREN / Clair Obscur / PSYCHO-PASS モチーフ）

| 柱 | 役割 | 状態保管 | 待ち受け方式 |
|---|---|---|---|
| **sightjack** | Designer (Linear issue 分析・wave 生成) | `.siren/` | fsnotify on inbox + idle-timeout |
| **paintress** | Implementer (PR作成・review fix loop) | `.expedition/` | fsnotify on inbox + idle-timeout (default 30m) |
| **amadeus** | Verifier (post-merge divergence) | `.gate/` | fsnotify on inbox + auto-merge daemon |
| **dominator** | NFR Judge (k6 負荷試験) | `.pass/` | プラン承認後 `run --plan-id` 起動 |
| **phonewave** | Courier daemon (D-Mail 配送) | `.phonewave/` | fsnotify on **全 outbox**, SKILL.md ベース routing |

### D-Mail Protocol (schema v1)

- 物理形式: **Markdown ファイル + YAML frontmatter**
- 配置場所: 各ツールの `inbox/`, `outbox/`, `archive/` ディレクトリ
- ルーティング: 各ツールの `skills/dmail-{sendable,readable}/SKILL.md` で
  `produces` / `consumes` の `kind` を宣言、phonewave が `outbox/` を fsnotify 監視して
  対応する `inbox/` に atomic write (temp + rename) で配送
- 既存 kind:

  | kind | フロー | 説明 |
  |---|---|---|
  | `specification` | sightjack → paintress | issue 仕様（実装依頼） |
  | `report` | paintress → amadeus | 実装完了報告 |
  | `design-feedback` | amadeus → sightjack | 設計レベル是正フィードバック |
  | `implementation-feedback` | amadeus → paintress | 実装レベル是正フィードバック |
  | `convergence` | amadeus → sightjack | 世界線収束アラート |
  | `ci-result` | CI/CD → amadeus | CI/CD パイプライン結果 |

### 観測性の前提

5本柱は全て OpenTelemetry 計装済み（`OTEL_EXPORTER_OTLP_ENDPOINT` で有効化）。
runops-gateway も同じ Jaeger インスタンスにスパンを送る。

---

## 解こうとしている問題

5本柱が `phonewave run` 経由で常駐し、D-Mail を交換する構造はすでに動いている。
しかしこの構造には **「外部からの起点（最初の D-Mail）をどう投入するか」** という穴がある。

要件を分解すると以下:

1. Slack の `/agent <role> <task>` コマンド、または管理対象アプリの CI/CD（`ci-result`）
   が gateway 経由で 5本柱の世界に D-Mail を投入できる
2. 投入された D-Mail は phonewave の routing で適切な inbox に配送される
3. 結果（`report` 等）は逆向きに流れ、Slack thread に **runops-gateway 経由で** 通知される
4. 既存の ChatOps（Cloud Run カナリア・DB マイグレ）とは衝突せず共存する

これは性質として既存の runops-gateway が解いている問題と同型である。
入口（Slack/CLI）→ 認証認可 → **D-Mail 生成（GCP 操作の代わり）** → 結果通知 という
骨格が一致する。

---

## 設計上の最重要決定（A/B/C）

3 つの判断が他の全ての設計を駆動する。順に記す。

### 決定 A: 新しい D-Mail kind は追加しない

**判断**: 既存の kind (`specification` / `report` / `design-feedback` /
`implementation-feedback` / `convergence` / `ci-result`) のみを使う。

**理由**:

- 5本柱は SKILL.md で produces / consumes を厳密に宣言している。新しい kind を増やすと、
  receiving 側の SKILL.md と consume 実装を全て更新する必要があり、scope が爆発する
- 「Slack から paintress に dispatch」は意味的に **「人間が specification を書く」** と
  同型である。kind を増やすより、specification の payload に sender 情報を埋めるほうが
  既存設計と整合する
- 「CI/CD から amadeus に通知」は既存の `ci-result` kind がそのまま使える
- kind を増やす衝動を抑え、5本柱本体への変更を **ゼロ** に保つことを優先する

**含意**:

- runops-gateway は specification の **発行者（producer）として paintress 等の SKILL.md
  に追記される必要はない**。phonewave は producer の身元を SKILL.md で全 enumerate して
  いるわけではなく、outbox にファイルが現れた時点で kind を見て配送する。
- ただし phonewave の routing 表に「runops-gateway という新しい outbox 提供者」を
  認識させるため、**runops-gateway 用の SKILL.md は別途用意する**（後述の Phase 2）。

### 決定 B: outbox 書き込みは Pub/Sub 経由でインフラ吸収

**判断**: runops-gateway は Pub/Sub に publish するだけ。exe-coder VM 上の subscriber
daemon が Pub/Sub を pull して、phonewave の outbox に atomic write する。

**理由**:

- runops-gateway は Cloud Run で動く。**Cloud Run から exe-coder VM のローカル
  ファイルシステムに直接書く手段は基本的に存在しない**（NFS マウントは不可、SSH 越し
  のファイル書き込みは脆弱、tsnet 経由でも writable network file system はない）
- Pub/Sub は GCP マネージド。配送保証 (at-least-once)・重複排除 (message ID)・
  デッドレター・順序保証（ordering key 利用時）が標準装備
- runops-gateway 側は **「Pub/Sub に publish するだけ」** なので Cloud Run の
  ステートレス性と相性が良い
- exe-coder 側の subscriber daemon は **既存の phonewave と同じく fsnotify
  ベースの courier 思想**（push を受けて atomic write）。実装スタイルが揃う
- 障害ドメインが分離される: Pub/Sub に積まれている限り、Cloud Run がスケールイン
  しても exe-coder VM が preempt されても D-Mail は失われない

**含意**:

- 新規コンポーネント `dmail-receiver` を exe-coder VM 上に systemd サービスとして配置。
  実装は本リポジトリの `cmd/dmail-receiver/` に置く（runops-gateway の Pub/Sub 契約と
  同じ言語・同じテストハーネスで管理するのが整合的）
- 5本柱への変更はゼロ。phonewave から見ると「単に outbox に新しい .md が現れた」
  だけに見える
- 双方向 (Slack 通知側) も対称: exe-coder 上の **`dmail-emitter`** daemon が
  各ツールの `outbox/` (or `archive/`) を fsnotify で見て、対応する D-Mail を
  Pub/Sub に publish。runops-gateway が push subscription で受信して Slack 通知

### 決定 C: Slack 通知は runops-gateway に集約

**判断**: paintress の companion (`paintress-slack` 等) は使わない。
Slack 通知は全て runops-gateway 側で実装する。

**理由**:

- runops-gateway は既に Slack 受信側の HMAC 検証・response_url 制御・Block Kit
  テンプレート・compressButtonValue を持っている。**通知側を別実装にすると、
  Slack ペイロードの管轄が二重化する**
- paintress companion は Socket Mode (WebSocket) を使う設計で、Cloud Run の
  HTTP ベースモデルと相性が悪い（常時接続を保つ別 process が要る）
- 1 Slack App 1 Channel 構成で、ChatOps の通知も AgentOps の通知も同じ
  conversation flow に乗せたい（人間が見る側はチャンネルで一元化される）
- approval (人間承認) も同様に runops-gateway の既存 EnvAuthChecker + Block Kit
  ボタンを再利用する。companion の `--approve-cmd` プロトコルとは別の仕組みになる

**含意**:

- paintress 等は `--notify-cmd` / `--approve-cmd` を **空または stub** で起動する
  （通知は phonewave 経由で gateway に逆流する D-Mail に任せる）
- runops-gateway は dmail-emitter から受けた D-Mail を解釈して Slack に変換する
  ロジックを持つ。具体的には `report` kind を受けたら「✅ paintress 完了 (PR #123)」
  のように Slack thread に reply する
- HIGH severity の approval gate (paintress の docs/approval-contract.md) は、
  runops-gateway が D-Mail を受けた時点で Slack に承認ボタンを出し、人間が
  押したら逆向きの D-Mail (kind 検討中、おそらく既存の `convergence` 流用) を
  Pub/Sub 経由で受け取る

---

## アーキテクチャ全体像

```
[ Human / CI/CD ]                          [ 5本柱 (exe-coder VM) ]
        |                                          ^ |
        | Slack /agent or                          | |
        | CI webhook                               | |
        v                                          | v
+------------------+                       +-------+--------+
| runops-gateway   |  publish              | dmail-receiver |
| (Cloud Run)      | --------> Pub/Sub --> | (systemd)      |
|                  |  (inbound topic)      |                |
|  - HMAC verify   |                       | atomic write   |
|  - AuthZ         |                       | to outbox/     |
|  - kind選択       |                       +-------+--------+
|  - Slack notify  |                               |
+------------------+                               | phonewave
        ^                                          | routes
        |                                          v
        |                                  +----------------+
        |  push subscription               |   sightjack    |
        +-- (outbound topic) <--+          |   paintress    |
                                |          |   amadeus      |
                                |          |   dominator    |
                          +-----+-------+  +----------------+
                          | dmail-emitter|        |
                          | (systemd)    |<-------+
                          |              |  fsnotify
                          | scan all     |  outbox/archive
                          | tools' out   |
                          +--------------+
```

Legend / 凡例:
- runops-gateway: 既存の Slack ChatOps gateway (Cloud Run)
- Pub/Sub: GCP Pub/Sub (inbound topic と outbound topic の 2 本)
- dmail-receiver: Pub/Sub から phonewave outbox に書き出す bridge daemon
- dmail-emitter: 各ツールの outbox/archive を fsnotify で見て Pub/Sub に流す daemon
- phonewave: 既存の courier daemon (変更なし)
- 5本柱: sightjack / paintress / amadeus / dominator (変更なし)

---

## 設計判断の優先順位

迷ったときの判定基準を明示しておく。

### 1. 5本柱本体への変更ゼロを死守する

5本柱は既に独立した Go バイナリとして動いており、各々が独自のドメイン・状態を持つ。
runops-gateway 拡張で 5本柱に変更が必要になるなら、その変更が 5本柱の README に
書ける単一の機能改善であるかを問う。書けないなら、変更箇所は runops-gateway 側
（dmail-receiver / dmail-emitter）に寄せる。

### 2. 既存 D-Mail kind を流用する

決定 A で示した通り、新規 kind を追加しない。
新しい意味付けは payload (frontmatter の追加フィールド) で表現する。
具体的には:

- `metadata.source` フィールドに `runops-gateway-slack` / `runops-gateway-ci` 等を入れる
- `metadata.requester_id` に Slack user ID を入れる
- 新しい意味が必要になる前に「既存 kind の意味の拡張」で表現できないか考える

### 3. Pub/Sub message attribute に kind と target を埋める

publish 時の message attribute に最低限以下を入れる:

- `kind` (例: `specification`)
- `target_tool` (例: `paintress` / `*` で全 tool に配送)
- `dmail_schema_version` (`"1"`)
- `idempotency_key` (SHA-256, dedup 用)

これにより dmail-receiver 側で Pub/Sub のフィルター機能を使った
subscription 分割が将来できる。

### 4. Slack 3秒ルールを最優先する

Slack webhook 受信は 3秒以内に 200 を返さねばならない。
Pub/Sub publish は同期 RPC だが、Cloud Run と同じ asia-northeast1 から呼べば
通常 50-100ms で返る。**Pub/Sub publish の失敗時のフォールバック** はリトライではなく
Slack に「:x: D-Mail 投入失敗」を即時通知。リトライは receiver 側の dead letter で扱う。

### 5. runops-gateway の MemoryStore は best-effort と割り切る

既存の MemoryStore は instance-local。Cloud Run の autoscale 下では effective でない。
Agent dispatch の冪等性は **Pub/Sub message の `idempotency_key` 属性 + receiver 側の
ファイル名重複チェック** で担保する。runops-gateway 側は first line of defense にすぎない。

### 6. 観測性は OpenTelemetry に寄せる

5本柱は既に OTel 計装済み。Pub/Sub message attribute に traceparent (W3C Trace
Context) を埋めて、receiver / emitter / 5本柱の span を Jaeger 上で連結する。

---

## 命名と境界の議論（未決）

リポジトリ名を `runops-gateway` のまま維持するか、リネームするか。

### 維持派の論拠

- 既存のドキュメント・URL・gh variable・Slack App 設定が全て `runops` で統一されている
- リネームのコストは技術的というより認知的に大きい
- `runops` を「runtime ops」と再解釈すれば AgentOps も含意できる

### リネーム派の論拠

- Phase 2 以降で agent 機能が中核になると、`runops` の名前が縛りになる
- Steins;Gate モチーフ（5本柱）と命名思想を揃えるなら別名が候補（例: `divergence-gate` 等）
- 早めにやるほどコストが小さい

### 現時点の決定

**保留**。Phase 3 完了時点で再評価する。それまでは `runops-gateway` のまま。
ただし新規ドキュメント・新規 ADR では「ChatOps + AgentOps gateway」という
役割記述を意識的に使う。

---

## やらないこと（明示的なスコープ外）

### 1. 5本柱本体への機能追加

新しい D-Mail kind の追加、新しい subcommand の追加、companion binary の作成、
これらはやらない。5本柱の READMEに変更が要るような拡張は Out of Scope。

### 2. paintress companion (Slack/Telegram/Discord) の利用

決定 C の通り、Slack 通知は runops-gateway に集約する。
companion を使った構成は採用しない（重複と複雑化を避ける）。

### 3. Phonewave のバックエンド変更

Phonewave は **ファイル + fsnotify + atomic write** のままでよい。
当初は Postgres LISTEN/NOTIFY 化を検討したが、5本柱の前提と衝突するので破棄。
Pub/Sub は phonewave の **外側** で動かす（receiver / emitter が境界）。

### 4. Coder REST API の直接呼び出し

5本柱は既に exe-coder VM 上で systemd 化された daemon として常駐する想定なので、
Cloud Run から Coder workspace を毎回起動する必要はない。
将来「per-task workspace 起動」が必要になった場合、それは paintress 等の swarm mode
（worktree pool）で吸収するのが筋で、runops-gateway が Coder API を叩く layer は持たない。

### 5. マルチテナント化

このゲートウェイは個人運用前提。社内ツール・チーム利用には拡張しない。

### 6. SaaS 化

リポジトリは MIT で公開しているが、product 化はしない。

---

## 関連 ADR と外部ドキュメント

このリポジトリの `docs/adr/` には 0001-0011 の決定が既に記録されている。
5本柱統合に伴い以下の ADR を追加した（2026-05-05）:

- ADR 0012: 新しい D-Mail kind は追加しない（決定 A）
- ADR 0013: outbox 書き込みは Pub/Sub bridge を経由する（決定 B）
- ADR 0014: Slack 通知は runops-gateway に集約する（決定 C）
- ADR 0015: dmail-receiver / dmail-emitter は本リポジトリで管理する

外部ドキュメント参照:

- [`/Users/nino/tap/phonewave/README.md`](file:///Users/nino/tap/phonewave/README.md) — D-Mail Protocol 仕様
- [`/Users/nino/tap/sightjack/README.md`](file:///Users/nino/tap/sightjack/README.md) — Designer の振る舞い
- [`/Users/nino/tap/paintress/README.md`](file:///Users/nino/tap/paintress/README.md) — Implementer の振る舞い、approval-contract
- [`/Users/nino/tap/amadeus/README.md`](file:///Users/nino/tap/amadeus/README.md) — Verifier、PR convergence pipeline
- [`/Users/nino/tap/dominator/README.md`](file:///Users/nino/tap/dominator/README.md) — NFR Judge

---

## 最後に

このセクションを読んでいる将来の自分（または引き継ぎ先）へ:

ChatOps と AgentOps を一つの gateway に集約するという判断は、
表面的には便利さの問題に見えるが、本質は **「人間の指示と AI の指示を区別しない」**
という Phonewave の設計思想と整合させるためである。

Slack で `/canary frontend 30` と打つのと、`/agent paintress fix M-42` と打つのは、
入口・認証・非同期実行・通知の構造が完全に同型である。
両者を別の system にすることは、その同型性を捨てることになる。

そしてもう一つ大事なこと:

5本柱は既に動いている。runops-gateway 拡張で **5本柱を「直す」誘惑が必ず湧くが、
それは別のリポジトリでやるべき仕事**である。runops-gateway は 5本柱の世界の
**外周にある関税ゲート**であって、内政には介入しない。Pub/Sub bridge という
インフラ吸収の判断（決定 B）は、この関税ゲート性を物理的に強制するためでもある。

迷ったら、この同型性と関税ゲート性を維持する方を選ぶ。

— hironow
