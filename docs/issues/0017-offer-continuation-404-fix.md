# Issue 0017: OfferContinuation の 404 を抑止する

> **解決済み (2026-05-05, ✅ Phase 2a `feat/long-running-dispatch` ブランチ)**:
> ADR 0017 (Bot Token + chat.postMessage fallback) を採用し、`FallbackNotifier`
> decorator として実装した。`ResponseURLNotifier` を wrap して、primary が
> response_url 30分/5回制限の 404 を返した時に chat.postMessage に自動切替する。
> NotifyTarget に ChannelID + ThreadTS を追加し、InteractiveHandler が Slack
> payload から populate する。`SLACK_BOT_TOKEN` 未設定時は Phase 0 互換 (起動時
> WARN ログ)。Issue 0017 計画案 (a) を選択、案 (b)(c) は scope 外。
>
> 関連 commits: `1d387b6 docs(adr): record ADR 0017` /
> `5a41762 test(slack): RED tests for FallbackNotifier` /
> `86ea6d2 fix(slack): chat.postMessage fallback for response_url limits` /
> `b964620 feat(server): wire FallbackNotifier with SLACK_BOT_TOKEN`
>
> 残課題: tofu に `slack-bot-token` Secret Manager リソース追加 (本 PR 外、
> infra フェーズで実施)。

## Goal

`internal/usecase/runops.go` の `approveShift` が成功した後、
`offerOrFallback` 経由で `Notifier.OfferContinuation` を呼ぶ際に
**Slack response_url から 404 が返るケース** を再現・抑止する。

カナリアトラフィックシフト自体は成功するが、次のステップボタンが出ない事象が
intermittent に発生している（Phase 0 既知課題、`docs/handover.md` 参照）。

## Background

調査済みの事実 (2026-05-05):

- `internal/adapter/output/slack/notifier.go:132` の post 層は 404 を error 化済み
  （`TestPost_404_ReturnsErrorWithBody` でカバー）
- handler.go 側は `responseURLTimeout = 25 * time.Minute` でタイムアウト設定
- 不明なのは「**なぜ Slack が 404 を返すのか**」のシーケンストリガー

## 仮説（優先度順）

### 仮説 1: response_url の 30 分有効期限超過 (最有力)

Slack response_url は **発行から 30 分** で失効する。
`approveShift` は以下のシーケンスで動く:

```
T+0    UpdateMessage("⏳ トラフィック切り替え中...")
T+0    shift(name1, rev1, percent)            # GCP LRO 1
T+L1   shift(name2, rev2, percent)            # GCP LRO 2
...
T+Ln   OfferContinuation(summary, nextReq, stopReq)
```

マルチリソース (ADR 0010) で N サービスを逐次処理すると、
LRO 待機の累計が容易に 25-30 分を超える。
その結果、最後の OfferContinuation が response_url を expire させた状態で叩く。

### 仮説 2: response_url の 5 回使用制限超過

Slack response_url は **同一 URL を 5 回まで** 使える。
`offerOrFallback` のフォールバックチェーンは以下の通り消費する:

```
1. UpdateMessage (progress)
2. OfferContinuation (success path)  ← 失敗時
3. UpdateMessage (fallback)           ← さらに失敗時
4. UpdateMessage (timeout fallback)   ← handler.go の notifyIfTimeout 経路
```

連続失敗 + retry で 5 回を超えると 6 回目が 404。

### 仮説 3: Block Kit ペイロード構造不正による silent failure

`BuildProgressMessage` が生成する Block Kit が稀に Slack の検証を通らず、
200 を返しつつ実際は配信されない (`TestPost_200InvalidBlocks_NoErrorButLogsWarning`
の silent failure と類似)。
404 ではなく invalid_blocks の症状が混在している可能性もある。

## TDD Red 設計

別ブランチ (`fix/offer-continuation-404`) で以下のテストを追加する。
全て `internal/usecase/runops_test.go` に置く（usecase 層の振る舞いとして検証）。

### Test 1: `TestApproveShift_OfferContinuationFailsAfter30Min` (仮説 1)

```go
func TestApproveShift_OfferContinuationFailsAfter30Min(t *testing.T) {
    // given
    callTimes := []time.Time{}
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        callTimes = append(callTimes, time.Now())
        // 最初の call から 30 分以上経過していたら 404 (expired_url)
        if len(callTimes) > 0 && time.Since(callTimes[0]) > 30*time.Minute {
            w.WriteHeader(http.StatusNotFound)
            w.Write([]byte("expired_url"))
            return
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer srv.Close()

    // 仮想時計で「shift 内部で 31 分経過」をシミュレートする stub gcp
    slowGCP := &slowShiftStub{delay: 31 * time.Minute}

    svc := NewRunOpsService(slowGCP, NewResponseURLNotifier(), allowAllAuth, NewMemoryStore())
    target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

    // when
    err := svc.ApproveAction(context.Background(), reqWith3Resources(), target)

    // then
    // 現状: OfferContinuation 失敗 → fallback UpdateMessage も失敗 → error chain 出力
    // 期待: chat.postMessage への自動 fallback で error を抑制 (Phase 1 fix で実装)
    if err != nil {
        t.Errorf("expected fallback to recover, got: %v", err)
    }
}
```

注意: 仮想時計の実装は `slowShiftStub` を `time.Sleep` 同期版にする必要があるため、
`internal/usecase/runops.go` の shift 呼び出しに **時計の抽象化** が要る。
これは Tidy First の structural change として別コミットで先行する。

### Test 2: `TestApproveShift_OfferContinuationFailsAfter5Calls` (仮説 2)

```go
func TestApproveShift_OfferContinuationFailsAfter5Calls(t *testing.T) {
    // given
    var count int
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        count++
        if count > 5 {
            w.WriteHeader(http.StatusNotFound)
            w.Write([]byte("rate_limited"))
            return
        }
        w.WriteHeader(http.StatusOK)
    }))
    defer srv.Close()

    // 失敗を誘発する gcp stub (途中で error)
    failingGCP := &failingShiftStub{failAt: 2}  // 2 番目の resource で失敗
    svc := NewRunOpsService(failingGCP, NewResponseURLNotifier(), allowAllAuth, NewMemoryStore())
    target := port.NotifyTarget{CallbackURL: srv.URL, Mode: port.ModeSlack}

    // when — フォールバックチェーンが 5 回制限を超える
    err := svc.ApproveAction(context.Background(), reqWith3Resources(), target)

    // then
    // 現状: 6 回目で 404 → silent (slog.Error にログするだけ)
    // 期待: 5 回目以降を検知して chat.postMessage に切り替え
    assertNoSilentFailureInLogs(t)
}
```

## 修正方向（優先度順）

### (a) chat.postMessage への自動 fallback (推奨)

response_url の制約 (30 分 / 5 回) を回避する根本解。

- runops-gateway の Notifier に `chatPostMessageFallback` フィールドを追加
- 既存の `slack-webhook-url` Secret を使うのではなく、Bot Token + chat.postMessage API を使う
- response_url での失敗検出時に thread_ts を保持して chat.postMessage で reply

ただしこれは ADR 0006 (CLI 操作時の Slack メッセージ同期) の `chat.update` 対応と
**統合実装** したほうが効率的。両方とも Bot Token + Slack Web API への依存を持ち込む
判断なので、別 ADR を起票する。

### (b) operator visible な expiry warning の挿入

UpdateMessage の段階で「⏳ 残り X 分」を Slack に表示する。
30 分超過しそうなら早めに `chat.postMessage` への切替を Slack 側にも見せる。

### (c) response_url 使用回数のメトリクス化

OpenTelemetry counter で `slack_response_url_calls_total{status}` を計装。
30 分 / 5 回の境界に近づいたら警告ログ。
原因切り分けが現状ログだけでは難しいことの根本対策。

## 別ブランチでの作業手順

1. ブランチ `fix/offer-continuation-404` を main から派生
2. **Tidy First / Structural**: `internal/usecase/runops.go` の `shift` 呼び出しに
   `Clock interface` を導入（`refactor(usecase): inject clock for shift sequence`）
3. **Tidy First / Structural**: テストヘルパー (`slowShiftStub`, `failingShiftStub`,
   `assertNoSilentFailureInLogs`) を `internal/usecase/testhelpers_test.go` に追加
4. **Behavioral / Test**: Test 1 と Test 2 を Red で追加 (`test(usecase): reproduce
   OfferContinuation 404 from response_url limits`)
5. **Behavioral / Fix**: 修正方向 (a)(b)(c) のうち (a) を実装し Green
   (`fix(slack): fallback to chat.postMessage when response_url returns 404`)
6. **Behavioral / Observability**: (c) のメトリクス追加
   (`feat(slack): add response_url call counter`)
7. (b) は (a) で 404 を防げれば優先度を下げて別 issue に切る

## 完了条件

- Test 1, Test 2 が green
- 既存の `just test` / `just lint` が全 pass
- `OfferContinuation` の error path で silent failure が起きないことを runn シナリオで確認
- ADR を新規起票 (chat.postMessage 採用判断、response_url との併用方針)

## 関連

- ADR 0002: Slack 3秒ルールの回避（response_url の制約源泉）
- ADR 0006: CLI 操作時の Slack メッセージ同期（chat.update 対応、本 issue と統合候補）
- ADR 0008: Progressive Canary Rollout（OfferContinuation の存在理由）
- ADR 0010: Multi-Resource Deployment（30 分超過の典型シナリオ）
- `docs/handover.md`: Phase 0 既知課題 1
- `internal/adapter/output/slack/notifier.go:132` (post 層の 404 ハンドリング)
- `internal/usecase/runops.go` の `approveShift`, `offerOrFallback`
