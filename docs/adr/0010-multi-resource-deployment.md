# 0010. Multi-Resource Deployment — All-or-Nothing と冪等性

**Date:** 2026-04-02
**Status:** Accepted
**Supersedes:** 初版 ADR 0010（PostNewMessage + NextServiceNames 案）

## Context

一回のデプロイで複数の Cloud Run Service / Worker Pool を同時にリリースするケースがある。
DB Migration は 1 回だけだが、その後の Canary は複数リソースに適用する必要がある。

### 解決すべき問い

1. **All-or-Nothing 保証**: 3 サービスの 10% Canary で 2 番目が失敗したとき、1 番目をどう戻すか
2. **冪等性**: 同じ承認ボタンを再押しした場合・一部障害後の再試行が安全か
3. **疎結合**: gateway が「何サービスあるか」をコンパイル時に知らなくてよいか
4. **CLI との対称性**: Slack ボタンとコマンドラインで同じ論理単位を扱えるか

---

## 初版案の問題点（再掲）

### `PostNewMessage` 案の設計上の問題

初版では migration 完了後に gateway が `PostNewMessage` でサービスごとに N 通の新規メッセージを投稿する案を提案した。

```
問題: port.Notifier に Slack の "replace_original: false" という実装詳細が漏れる
問題: CLI モードには「新規メッセージ投稿」という概念がない
問題: gateway が orchestrator になる（responder であるべき）
```

### N 通の独立メッセージ案の問題

Cloud Build が N 通の独立したメッセージを送る場合、gateway は各クリックで 1 リソースしか見えない。
1 番目のサービスが成功し 2 番目が失敗しても gateway には補償する手段がない（部分状態が発生する）。

---

## Decision

**1 deployment = 1 Slack message = 1 ApprovalRequest にリソース名を CSV で束ねる。**

```
action value (canary ボタン):
{
  "resource_type":  "service",
  "resource_names": "frontend-service,backend-service,admin-service",
  "targets":        "frontend-v2,backend-v3,admin-v4",
  "action":         "canary_10",
  "issued_at":      1700000000
}
```

UseCase はリストを展開して **全リソースを逐次実行**。1 つでも失敗したら
**補償ロールバック（ShiftTraffic → 0%）** を先行成功分に適用して返す。

```
approveService:
  for each (name, rev):
    ShiftTraffic(name, rev, 10%)   ← 失敗したら break
    shifted = append(shifted, ...)

  if err != nil:
    for each done in shifted:
      ShiftTraffic(done.name, done.rev, 0)  ← 補償
    return err

  OfferContinuation(nextReq{resource_names=same CSV})
```

冪等性は 2 層で保証する:

- `StateStore.TryLock`: 同一 `(resource_names, action, issued_at)` の二重実行をブロック
- `ShiftTraffic` 自体の冪等性: 同じ % に再設定しても Cloud Run の状態は変わらない

---

## 実装方針

### 1. `domain.ApprovalRequest` — 単複フィールドのリネーム

```go
// 変更前
ResourceName string  // 単一
Target       string  // 単一

// 変更後
ResourceNames string  // CSV: "frontend-service,backend-service"
Targets       string  // CSV: "frontend-v2,backend-v2" (順序対応)
```

```go
// 変更前 (ADR 0009 の migration → canary 再投稿フィールド)
NextServiceName string
NextRevision    string

// 変更後
NextServiceNames string  // CSV
NextRevisions    string  // CSV
```

`NextAction` は全サービス共通のため変更なし。

### 2. `port.OperationKey` — ResourceNames を使う

```go
func OperationKey(req domain.ApprovalRequest) string {
    return fmt.Sprintf("%s/%s/%s/%d",
        req.ResourceType, req.ResourceNames, req.Action, req.IssuedAt)
}
```

### 3. `handler.go` — 後方互換フォールバック

```go
// actionValue に resource_names / targets (複数形) を追加
// 旧フォーマット (resource_name / target 単数形) もフォールバックとして保持
type actionValue struct {
    ResourceName  string `json:"resource_name"`   // legacy
    ResourceNames string `json:"resource_names"`  // new
    Target        string `json:"target"`           // legacy
    Targets       string `json:"targets"`          // new
    ...
}

// マッピング時:
if av.ResourceNames != "" {
    req.ResourceNames = av.ResourceNames
    req.Targets       = av.Targets
} else {
    req.ResourceNames = av.ResourceName   // legacy fallback
    req.Targets       = av.Target
}
```

### 4. `usecase/runops.go` — 補償ロールバック付き複数実行

```go
type shifted struct{ name, target string }

names   := splitCSV(req.ResourceNames)
targets := splitCSV(req.Targets)
done    := make([]shifted, 0, len(names))

for i, name := range names {
    rev := safeGet(targets, i)
    if err := s.gcp.ShiftTraffic(ctx, name, rev, percent); err != nil {
        for _, d := range done {
            _ = s.gcp.ShiftTraffic(ctx, d.name, d.target, 0) // 補償
        }
        return err
    }
    done = append(done, shifted{name, rev})
}
```

### 5. `cli/approve.go` — CSV 引数をそのまま ResourceNames へ

```
# 単一リソース (従来通り)
runops approve service frontend-service --action canary_10 --target v2

# 複数リソース (新規)
runops approve service "frontend-service,backend-service" \
  --action canary_10 --target "frontend-v2,backend-v2"
```

CLI の引数 `<resource-name>` は CSV を受け付ける。`ResourceNames = args[1]` のまま。

### 6. `cloudbuild.yaml` — _SERVICE_NAMES (CSV) でデプロイループ

```bash
substitutions:
  _SERVICE_NAMES: "frontend-service,backend-service"

# deploy-service ステップをループに変更
IFS=',' read -ra SERVICES <<< "${_SERVICE_NAMES}"
REVISIONS=""
for SVC in "${SERVICES[@]}"; do
    REV=$(gcloud run deploy "${SVC}" ... --no-traffic \
        --format="value(status.latestCreatedRevisionName)")
    REVISIONS="${REVISIONS:+${REVISIONS},}${REV}"
done
echo "${REVISIONS}" > /workspace/revisions.txt

# action value:
SRV_ACTION=$(printf '{"resource_type":"service","resource_names":"%s","targets":"%s","action":"canary_10","issued_at":%s,"migration_done":false}' \
  "${_SERVICE_NAMES}" "${REVISIONS}" "${TIMESTAMP}")
JOB_ACTION=$(printf '{"resource_type":"job","resource_names":"%s","targets":"","action":"migrate_apply","issued_at":%s,"next_service_names":"%s","next_revisions":"%s","next_action":"canary_10"}' \
  "${_MIGRATION_JOB_NAME}" "${TIMESTAMP}" "${_SERVICE_NAMES}" "${REVISIONS}")
```

---

## Consequences

### Positive

- **All-or-Nothing 保証**: 補償ロールバックで部分状態が発生しない
- **冪等性**: `StateStore.TryLock` + `ShiftTraffic` の冪等性で安全に再試行可能
- **疎結合**: `port.Notifier` に変更なし。`PostNewMessage` 不要
- **CLI との対称性**: CSV 引数で CLI も同一の論理単位を扱える
- **スケール**: サービス数が増えても gateway / port の変更は不要

### Negative

- `ApprovalRequest.ResourceName → ResourceNames` のリネームは既存コード全体に影響する
- 補償ロールバックはベストエフォート: ロールバック自体が失敗するケースをアラートで検知すべき
- 逐次実行のため N サービスの Canary 完了時間は N 倍になる（並列化は将来 issue）

### Neutral

- 旧フォーマット (`resource_name` 単数形) は handler のフォールバックで引き続き動作する
- Worker Pool も同じパターンで対応（`approveWorkerPool` も同様に複数対応）
- `blockkit.DeploymentPayload.ResourceName` は初期メッセージ表示用に単一のまま維持

## 関連 ADR

- ADR 0008: Progressive Canary Rollout (`OfferContinuation` の nextReq に ResourceNames を引き継ぐ)
- ADR 0009: Migration Double Confirmation (`NextServiceNames` CSV で置き換え)
