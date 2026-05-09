package domain

import "time"

// PendingApproval は admin endpoint (= POST /admin/projects /
// POST /admin/projects/{id}/archive) の HIGH severity mutation request が
// 4-eyes approval を待っている state を表す。 ADR 0039 §gate flow 参照。
//
// Lifecycle:
//
//  1. POST 受信 → severity 判定で HIGH → CreateIfNotExists で
//     IdempotencyKey + Op + BodyJSON + RequesterActorType をスナップショット保存。
//     Status は PendingStatusPendingApproval。
//  2. convergence D-Mail 発行 → Slack approve/deny button post (= 既存 ADR 0019 path)
//  3. approve click → ADR 0035 invariant validation → 元 op を registry に apply →
//     Transition(PendingStatusApprovedApplied, appliedAt=now)
//  4. denied / timeout → Transition(PendingStatusDenied / PendingStatusTimeout, nil)
//  5. client polling: GET /admin/projects/pending/{IdempotencyKey} で Status 取得
//
// Multi-instance contract (ADR 0026 Firestore production deploy carry):
// 同一 IdempotencyKey の並列 POST は CreateIfNotExists で重複検出され、
// 1 つだけが approval flow に進む (= idempotent retry safe)。
type PendingApproval struct {
	IdempotencyKey     string        `firestore:"idempotency_key"      json:"idempotency_key"`
	Op                 PendingOp     `firestore:"op"                   json:"op"`
	BodyJSON           []byte        `firestore:"body_json"            json:"body_json"`
	RequesterActorType string        `firestore:"requester_actor_type" json:"requester_actor_type"`
	CreatedAt          time.Time     `firestore:"created_at"           json:"created_at"`
	Status             PendingStatus `firestore:"status"               json:"status"`
	AppliedAt          *time.Time    `firestore:"applied_at,omitempty" json:"applied_at,omitempty"`
}

// PendingOp は admin endpoint mutation の operation 種別 (= add / archive)。
// ADR 0039 では LOW severity (= list / show) は approval gate に乗らないため
// PendingOp の対象外。 hard delete も別 endpoint (= 0024 軸 C) で扱うため対象外。
type PendingOp string

const (
	// PendingOpAdd = POST /admin/projects body POST (= project add、 HIGH severity)
	PendingOpAdd PendingOp = "add"
	// PendingOpArchive = POST /admin/projects/{id}/archive (= project archive、 HIGH severity)
	PendingOpArchive PendingOp = "archive"
)

// PendingStatus は PendingApproval の lifecycle state。
// 遷移可能 path:
//
//	pending_approval → approved_applied (= approve click + apply 成功)
//	pending_approval → denied            (= deny click)
//	pending_approval → timeout           (= 15 分経過、 ADR 0039 default)
//
// terminal state (= approved_applied / denied / timeout) からの再遷移は禁止。
// adapter 実装 (= §2-b) で transition validation を行う。
type PendingStatus string

const (
	// PendingStatusPendingApproval = 4-eyes approval 待ち (= initial state)
	PendingStatusPendingApproval PendingStatus = "pending_approval"
	// PendingStatusApprovedApplied = approve + apply 成功 (= terminal)
	PendingStatusApprovedApplied PendingStatus = "approved_applied"
	// PendingStatusDenied = deny click された (= terminal)
	PendingStatusDenied PendingStatus = "denied"
	// PendingStatusTimeout = 15 分経過で timeout (= terminal)
	PendingStatusTimeout PendingStatus = "timeout"
)

// IsTerminal は PendingStatus が terminal state (= 再遷移禁止) かを返す。
func (s PendingStatus) IsTerminal() bool {
	switch s {
	case PendingStatusApprovedApplied, PendingStatusDenied, PendingStatusTimeout:
		return true
	default:
		return false
	}
}
