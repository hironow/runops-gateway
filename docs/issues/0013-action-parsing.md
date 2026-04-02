# Issue 0013: Action 文字列のパース改善

## Goal

`ApproveAction` で `"canary_10"` のような文字列から percent を取り出す処理が
`usecase/runops.go` に直書きされており脆弱。`domain.Action` を構造体化して安全にパースする。

## 変更内容

### domain に Action 型を追加 (`internal/core/domain/domain.go`)

```go
// Action represents a parsed operation to perform on a resource.
type Action struct {
    Name    string // "canary", "migrate_apply", "rollback", etc.
    Percent int32  // traffic/instance percent (0 = not applicable)
}

// ParseAction parses an action string like "canary_10" or "migrate_apply".
// Returns error if the format is invalid.
func ParseAction(s string) (Action, error)
```

パース仕様:
- `"canary_10"` → `Action{Name:"canary", Percent:10}`
- `"canary_50"` → `Action{Name:"canary", Percent:50}`
- `"migrate_apply"` → `Action{Name:"migrate_apply", Percent:0}`
- `"rollback"` → `Action{Name:"rollback", Percent:0}`
- `""` → error
- `"canary_abc"` → error (percent must be integer)
- `"canary_-1"` → error (percent must be 0-100)
- `"canary_101"` → error

### UseCase で ParseAction を使うよう修正 (`internal/usecase/runops.go`)

`ShiftTraffic` 呼び出し前に `domain.ParseAction(req.Action)` を実行し、
`action.Percent` を渡す。

## Definition of Done (DoD)

- [ ] `ParseAction` のユニットテストが存在する（正常系 3件・異常系 4件以上）
- [ ] UseCase の既存テストが引き続き通る（`req.Action = "canary_10"` 形式のまま）
- [ ] `just test && just lint` が通る

## 非機能要件

- `domain` パッケージに `strconv` のみ追加（外部依存なし）
