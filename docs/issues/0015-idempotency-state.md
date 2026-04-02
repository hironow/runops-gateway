# Issue 0015: 二重実行防止 — インメモリ状態管理

## Goal

同一の `ApprovalRequest`（同一 `ResourceName` + `Action` + `IssuedAt`）が
2 回 `ApproveAction` を受けた場合に 2 回目をスキップする。

永続化（Firestore/Redis）は今後の拡張とし、まずはインメモリ実装で防止する。

## 変更内容

### port に StateStore を追加 (`internal/core/port/port.go`)

```go
// StateStore tracks in-flight and completed operations to prevent double execution.
type StateStore interface {
    // TryLock attempts to claim the operation key. Returns true if claimed (first call).
    // Returns false if already claimed (duplicate).
    TryLock(key string) bool
    // Release removes the lock for the given key.
    Release(key string)
}

// OperationKey returns a canonical string key for an ApprovalRequest.
func OperationKey(req domain.ApprovalRequest) string {
    return fmt.Sprintf("%s/%s/%s/%d", req.ResourceType, req.ResourceName, req.Action, req.IssuedAt)
}
```

### インメモリ実装 (`internal/adapter/output/state/memory.go`)

```go
package state

import "sync"

// MemoryStore is a thread-safe in-memory StateStore.
type MemoryStore struct {
    mu   sync.Mutex
    keys map[string]struct{}
}

func NewMemoryStore() *MemoryStore
func (s *MemoryStore) TryLock(key string) bool
func (s *MemoryStore) Release(key string)
```

### UseCase に StateStore を注入 (`internal/usecase/runops.go`)

`ApproveAction` の先頭で `TryLock` し、`false` なら ephemeral 通知して return nil。
処理完了（またはエラー）後に `Release`。

## Definition of Done (DoD)

- [ ] `MemoryStore.TryLock` の並行安全テスト（goroutine 10本で同時呼び出し → 1本のみ true）
- [ ] UseCase テストに重複実行時の skip テストが追加される
- [ ] `just test -race` が通る（data race なし）
- [ ] `cmd/server/main.go` で `state.NewMemoryStore()` を UseCase に渡す

## 非機能要件

- `sync.Mutex` のみ使用（外部依存なし）
- `Release` は `defer` で確実に呼ばれること
