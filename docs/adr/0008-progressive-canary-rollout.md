# 0008. Progressive Canary Rollout — ステップ昇格とロールバックボタン

**Date:** 2026-04-02
**Status:** Accepted

## Context

現在の実装では `canary_10` が成功すると Slack のメッセージがボタンなしの完了表示に置き換わる。
その後の操作（30% → 50% → 80% → 100% への昇格、または途中停止）は手段がない。

解決すべき設計上の問いは以下の3点。

1. **カナリア昇格ステップをどう定義するか**
2. **「次のステップ」ボタンと「停止（ロールバック）」ボタンをどう表示するか**
3. **ロールバックとは何を意味するか** — 「前リビジョンへ戻す」なのか「現リビジョンを 0% にする」なのか

## Decision

### 1. カナリア昇格ステップの定義

`internal/core/domain` にステップ列を定数として定義する。

```
10% → 30% → 50% → 80% → 100%
```

`domain.NextCanaryPercent(current int32) int32` がステップ列から次の値を返す。
`100` の次は `0`（昇格完了、次ステップなし）。
ステップ列に存在しない値（例: `15`）に対しても `0` を返す。

ステップ列は `domain.CanarySteps` としてエクスポートし、テスト・UI から参照できるようにする。

### 2. 完了後のメッセージ設計（`OfferContinuation`）

`port.Notifier` に新メソッドを追加する。

```go
// OfferContinuation replaces the message with a completion summary and,
// if nextReq is non-nil, buttons to advance or stop the rollout.
OfferContinuation(ctx context.Context, target NotifyTarget,
    summary string, nextReq *domain.ApprovalRequest) error
```

- `nextReq == nil`（100% 到達またはカナリア以外）: 従来の完了メッセージ（ボタンなし）
- `nextReq != nil`: 次ステップの「Approve（N%へ昇格）」ボタン＋「Stop（ロールバック）」ボタンを表示

メッセージには現在の状態も明示する。例:

```
✅ 10% Canary 完了 — frontend-service
現在: v2 = 10%, v1 = 90%

[ ✅ 30% に昇格 ]  [ 🛑 停止・ロールバック ]
```

`IssuedAt` は `OfferContinuation` 呼び出し時に `time.Now().Unix()` を設定し、
各ステップに独立した 2 時間の有効期限を与える。

### 3. ロールバックの定義と実装

**「停止・ロールバック」ボタンを押したときの意味**: 現リビジョンのトラフィックを 0% にする。
Cloud Run は自動的に残りのトラフィックを前のリビジョンへ振り向ける。

つまり `ShiftTraffic(ctx, serviceName, revision, 0)` を実行する。

この設計を選んだ理由:

- **前リビジョン名をボタン値に持たせる必要がない** — Cloud Run はトラフィック割り当てを持つ全リビジョンを管理しており、指定リビジョンを 0% にすれば残余トラフィックが自動分配される
- **Cloud Build 時点の情報のみで完結する** — 実行時に「前リビジョン名は何か」をクエリする必要がなく、gateway の複雑度が上がらない
- **冪等** — 同じ操作を 2 回行っても状態が変わらない

ロールバック用のアクション名は `"rollback"` とし、`ParseAction("rollback")` は `Action{Name:"rollback", Percent:0}` を返す（既存の挙動）。

UseCase の `approveService` は `act.Name == "rollback"` の場合に `percent = 0` で `ShiftTraffic` を呼ぶ。

ロールバックが完了した場合は `OfferContinuation(ctx, target, summary, nil)` を呼んでボタンなし完了メッセージを表示する。

### 4. Worker Pool への適用

`approveWorkerPool` も同じ `domain.NextCanaryPercent` ロジックと `OfferContinuation` を使う。

### 5. Deny ボタンとの関係

Deny は「操作の拒否（実行しない）」。Stop は「実行中のロールアウトを停止して戻す」。
両者は異なる。ステップ昇格メッセージでは **Deny ボタンを出さず** に Stop ボタンのみ表示する。
- Deny: 承認前に拒否。useCase.DenyAction を呼ぶ
- Stop: 承認済みの次ステップを「ロールバックとして承認」。useCase.ApproveAction(canary_0/rollback) を呼ぶ

これにより DenyAction は「初回承認リクエストの拒否」にのみ使われるようになり、責務が明確になる。

## Consequences

### Positive
- カナリア昇格フローが Slack で完結する（Cloud Build の再トリガー不要）
- 各ステップで停止判断できるため、問題発生時の影響範囲を最小化できる
- `domain.NextCanaryPercent` がドメイン知識として明示され、テスト可能になる
- `port.Notifier` を拡張するだけで Slack・stdout 両方に対応できる

### Negative
- `port.Notifier` インターフェースが拡張されるため、全モック実装に `OfferContinuation` の追加が必要
- ロールバックが「percent=0 の ShiftTraffic」であることを開発者が知る必要がある（明示的なコメントで補う）

### Neutral
- カナリアステップ列（10/30/50/80/100）は `domain.CanarySteps` としてエクスポートするが、現時点では変更不可（定数）。可変にするには設定ファイル対応が必要
- `IssuedAt` を各ステップで更新することで、ステップ間の操作に 2 時間以上かかっても期限切れにならない
