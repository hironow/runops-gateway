// Package port — admin endpoint approval gate pending store secondary port.
//
// ADR 0039 §async two-phase + Firestore pending state で導入された port。
// admin endpoint (= POST /admin/projects / POST /admin/projects/{id}/archive)
// が HIGH severity と判定したとき、 mutation を pending state に保存して
// 4-eyes approval flow に乗せる。 Approval flow 完了後に Get / Transition で
// state を読み出して mutation を apply、 もしくは denied / timeout で reject。
//
// Multi-instance contract (ADR 0026 production deploy carry):
// 同一 IdempotencyKey の並列 POST は CreateIfNotExists で重複検出され、
// 1 つだけが approval flow を生成する。 Firestore transaction で実装する。
// 既存 in-process state での実装は dev / unit test 用に限定。
package port

import (
	"context"
	"errors"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// ErrPendingAlreadyExists は CreateIfNotExists で同 IdempotencyKey の
// PendingApproval が既に存在するときに返る。 caller は idempotent retry
// として既存 state を再利用する想定 (= 同 body の重複 POST 安全)。
var ErrPendingAlreadyExists = errors.New("pending approval already exists")

// ErrPendingNotFound は Get / Transition で対象 IdempotencyKey が
// 見つからないときに返る。 GC 後 (= 7 日経過) のアクセスもこれを返す。
var ErrPendingNotFound = errors.New("pending approval not found")

// ErrPendingTerminalTransition は terminal state (= approved_applied /
// denied / timeout) からの再遷移試行に対して返る。
var ErrPendingTerminalTransition = errors.New("pending approval already in terminal state")

// PendingStore は admin endpoint approval gate の pending state を永続化する port。
// Firestore production adapter (= §2-b) と SQLite dev adapter (= §2-c) の
// dual strategy を ADR 0025 carry で採用する。
type PendingStore interface {
	// CreateIfNotExists は新 PendingApproval を作成する。 既に同
	// IdempotencyKey が存在すれば既存 record を返し ErrPendingAlreadyExists
	// を返す (= caller は idempotent retry として既存 state を再利用)。
	CreateIfNotExists(ctx context.Context, p domain.PendingApproval) (domain.PendingApproval, error)

	// Get は IdempotencyKey で PendingApproval を取得する。
	// 存在しなければ ErrPendingNotFound を返す。
	Get(ctx context.Context, idempotencyKey string) (domain.PendingApproval, error)

	// Transition は status を遷移させる。 terminal state からの遷移は
	// ErrPendingTerminalTransition を返す。 newStatus が
	// PendingStatusApprovedApplied のとき appliedAt は non-nil 必須、
	// それ以外の terminal status (= denied / timeout) では nil。
	// 対象が存在しなければ ErrPendingNotFound を返す。
	Transition(ctx context.Context, idempotencyKey string, newStatus domain.PendingStatus, appliedAt *time.Time) error
}
