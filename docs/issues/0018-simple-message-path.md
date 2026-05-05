# Issue 0018: シンプルなメッセージ経路を確立する (Phase 1)

> **実装完了 (2026-05-05, ✅ 当初計画通りに着地)**:
> 一度は「Slash Command が直接 dispatch する 1 ステップ」に簡略化した版で
> merge しかけたが、Codex Review round 3 で **F-5 (確認ステップ削除) が致命**と
> 指摘されたため、本 Issue の TDD 計画通りの 2 ステップ構成に戻した。
>
> 最終的な経路:
>
> 1. `/agent <role> <text>` (Slash Command) → `POST /slack/command`
> 2. CommandHandler が Block Kit ephemeral 確認を **同期で返す** (Approve / Deny ボタン)
> 3. Approve クリック → `POST /slack/interactive` → `dispatch_approve`
> 4. InteractiveHandler が dispatchActionValue を decode → DispatchAgentTask 実行
> 5. StubDispatcher が role / text_len / text_sha256 / requester / idempotency_key /
>    issued_at をログ出力 (text 生値は出さない、F-4 fix)
> 6. Notifier が "✅ ... に dispatch を受け付けました" を response_url に返す
>
> Phase 4 で扱う **HIGH severity 4-eyes 承認** はこの確認ステップを基盤に上乗せする
> 形になる (今は requester == approver でよい)。
>
> 詳細は本ブランチのコミット (特に `feat(slack): add dispatch confirmation
> building blocks` / `fix(slack): require Block Kit confirmation before
> dispatch (F-5)`) を参照。

## Goal

D-Mail / Pub/Sub bridge / 5本柱統合に着手する前に、
**「Slack で受け、認可し、確認し、応答する」最小の循環** を runops-gateway 単体で完結させる。

これにより:

1. Slash Command の HMAC 検証経路が動くこと
2. Block Kit による「依頼内容の確認 → 承認」の対話が動くこと
3. Slack thread に gateway 側から reply できること
4. 既存の `EnvAuthChecker` / `MemoryStore` / response_url 制御が新しい入口でも再利用できること

を確認する。**実処理 (5本柱への投入) はすべて stub** とする。
ログ出力 + Slack thread への ack のみで完結する。

## Why first

Phase 2 以降の Pub/Sub bridge / dmail-receiver / dmail-emitter は **粒度が大きい**。
これを最初に組むと、Slack 経路のバグなのか Pub/Sub のバグなのか receiver のバグなのか
切り分けに時間が取られる。

シンプル経路を先に通すことで:

- Slash Command Request URL の Slack App 設定がワーク
- HMAC + auth + Block Kit の組み合わせが新エンドポイントで動く
- thread_ts の伝搬と response_url の使い分けの実装パターンが先に整う

これらは Phase 2 以降の **「Slack 通知集約」(ADR 0014) の前提** なので、
ここで先に確立しておくと Phase 2 の難所が減る。

## Scope

### In Scope

- Slack Slash Command `/agent <role> <text>` 受信エンドポイント `/slack/command`
- HMAC 検証 (既存 `internal/adapter/input/slack/verify.go` を流用)
- 認可チェック (既存 `EnvAuthChecker` 流用)
- Block Kit での確認メッセージ (「:speech_balloon: 依頼を受け付けました。実行しますか?」 + Approve/Deny)
- 承認時の thread reply (「:eyes: dispatch 受付 (id=<uuid>)」)
- in-process なリクエスト ID 採番 (UUID v4)
- 既存 `MemoryStore` での簡易冪等性

### Out of Scope (Phase 2 以降)

- Pub/Sub publish
- dmail-receiver / dmail-emitter
- 5本柱への実投入 (D-Mail outbox 書き込み)
- chat.postMessage への fallback (Issue 0017 と統合)
- 新 D-Mail kind の追加 (ADR 0012 で禁止)

## 設計

### エンドポイント

```
POST /slack/command
  - Slack Slash Command の Request URL
  - 既存の /slack/interactive とは別経路
  - HMAC は同じ signing secret を使用
```

### コマンド仕様

```
/agent <role> <free text>

例:
  /agent paintress fix M-42
  /agent sightjack scan ENG project
  /agent amadeus check --base main
```

`<role>` は `paintress|sightjack|amadeus|dominator` のいずれか。
それ以外は ephemeral message で reject。

### フロー

```
[ Human ] -- /agent paintress fix M-42 --> [ Slack ]
                                              |
                                              | webhook
                                              v
                              +-- POST /slack/command -----+
                              | runops-gateway             |
                              |                            |
                              | 1. HMAC verify             |
                              | 2. parse role + text       |
                              | 3. auth check              |
                              | 4. Block Kit confirmation  |
                              |    + Approve/Deny buttons  |
                              | 5. response_url で表示    |
                              +----------------------------+
                                              |
                              user clicks Approve button
                                              |
                              +-- POST /slack/interactive -+
                              | (既存ハンドラ)             |
                              |                            |
                              | 6. parse action_id=        |
                              |    "dispatch_approve"      |
                              | 7. auth + dedup check      |
                              | 8. log event (stub処理)    |
                              | 9. thread reply (response_url) |
                              |    ":eyes: 受付 id=<uuid>" |
                              +----------------------------+
```

Legend / 凡例:
- thread reply: Slack の元メッセージに応答 (response_url で `replace_original=false`)
- stub処理: ログ出力のみ。実際の dispatch は Phase 2 以降

### 追加するコード

```
internal/
├── core/
│   ├── domain/
│   │   └── dispatch_request.go    [新規] DispatchRequest (role, text, requesterID, idempotencyKey)
│   └── port/
│       └── dispatcher.go          [新規] Dispatcher IF (Stub 実装で満たす)
├── usecase/
│   └── dispatch_agent_task.go     [新規] DispatchAgentTask UseCase
└── adapter/
    ├── input/
    │   └── slack/
    │       ├── command.go         [新規] /slack/command handler
    │       └── command_test.go    [新規]
    └── output/
        └── dispatcher/
            └── stub.go            [新規] StubDispatcher (slog 出力のみ)
```

### ボタン value の payload (既存規約に従う)

`internal/adapter/output/slack/blockkit.go` の `compressButtonValue` を流用。
Phase 0 の `actionValue` 構造体を拡張せず、新しい `dispatchValue` を独立に持つ:

```go
type dispatchValue struct {
    Role            string `json:"role"`
    Text            string `json:"text"`
    RequesterID     string `json:"requester_id"`
    IdempotencyKey  string `json:"idempotency_key"`
    IssuedAt        int64  `json:"issued_at"`
}
```

action_id は `dispatch_approve` / `dispatch_deny` で識別。
`/slack/interactive` の既存ハンドラで分岐を追加する (`switch` の case を増やす)。

## TDD 計画

### Red (順番)

1. `TestDispatchCommand_RejectsInvalidSignature` (HMAC 検証)
2. `TestDispatchCommand_ParsesRoleAndText` (パース)
3. `TestDispatchCommand_RejectsUnknownRole`
4. `TestDispatchCommand_RejectsUnauthorizedUser`
5. `TestDispatchCommand_BuildsConfirmationBlockKit` (Block Kit 構造)
6. `TestApproveDispatchAction_LogsAndReplies` (承認時 stub 処理 + thread reply)
7. `TestDenyDispatchAction_RepliesEphemeral` (拒否時)
8. `TestDispatchAction_DedupsByIdempotencyKey` (二重実行防止)

### Structural (Tidy First)

- 既存 `internal/adapter/input/slack/handler.go` の `ServeHTTP` から
  Slash Command 用の処理を分岐するため、handler を 2 種類にする
  (`InteractiveHandler` と `CommandHandler`) **構造変更を先にコミット**

### Behavioral

- Stub Dispatcher 実装 (5本柱への投入はせず slog のみ)
- DispatchAgentTask UseCase 実装
- `/slack/interactive` 既存ハンドラに `dispatch_approve` / `dispatch_deny` 分岐追加

## ローカル動作確認

```bash
# 1. signing secret は既存 test-secret を使う
SLACK_SIGNING_SECRET=test-secret PORT=8080 go run ./cmd/server

# 2. Slash Command を curl で再現 (署名計算)
ts=$(date +%s)
sig_basestring="v0:${ts}:command=%2Fagent&text=paintress+fix+M-42&user_id=Utest"
sig="v0=$(echo -n "$sig_basestring" | openssl dgst -sha256 -hmac test-secret | awk '{print $2}')"
curl -X POST http://localhost:8080/slack/command \
  -H "X-Slack-Request-Timestamp: ${ts}" \
  -H "X-Slack-Signature: ${sig}" \
  -d "command=%2Fagent&text=paintress+fix+M-42&user_id=Utest"

# 3. 200 OK + Block Kit JSON が返る (確認ボタン付き)
# 4. response_url が実際の Slack なら (ngrok / tailscale funnel) 確認画面が出る
```

## 完了条件

- 上記 Red テスト 8 本が green
- 既存の `just test` / `just lint` / `just test-runn` が全 pass
- 新規 runn シナリオ `tests/runn/dispatch_command.yaml` で Slash Command 経路を E2E 確認
- ローカルで `/agent paintress test` を Slack から打って ack が返ることを確認
- ログに `dispatched stub` (role, text, requester) が記録される

## 完了後の次ステップ

Phase 2 (Pub/Sub bridge) に進む際は:

- StubDispatcher を `PubsubDispatcher` に差し替え
- DispatchAgentTask UseCase の Dispatcher port 注入を切り替え
- ADR 0013 / 0015 を Accepted に昇格
- Issue 0019 (新規) で Phase 2 の TDD 計画を起票

つまり **Phase 1 のコードは破棄せず Phase 2 に綺麗に積み上がる構造** にする。

## 関連

- ADR 0002: Slack 3秒ルールの回避
- ADR 0005: Ports and Adapters パターンの採用
- ADR 0014: Slack 通知は runops-gateway に集約 (Phase 1 でも有効)
- ADR 0012/0013/0015: D-Mail / Pub/Sub bridge 関連 (**Proposed のまま、Phase 2 で再評価**)
- `docs/handover.md` Phase 1 (シンプル経路) セクション
- 5本柱 README: `/Users/nino/tap/{sightjack,paintress,amadeus,dominator,phonewave}/README.md`
  (Phase 2 以降で参照、Phase 1 では関与しない)
