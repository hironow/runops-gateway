package methods

// Exported JSON-RPC method names registered by this package, per
// ADR 0040 §method 命名規約 (= `runops.<service>.<resource>.<verb>`).
//
// Method handlers reference these constants instead of duplicating
// literal strings, so a typo or rename is caught at compile time.
// External packages (e.g. wiring layer, readiness signal) also depend
// on these to enumerate the registered admin methods without coupling
// to the concrete Method types.
const (
	// MethodNameProjectGet (LOW severity) returns a single Project by id.
	MethodNameProjectGet = "runops.admin.project.get"
	// MethodNameProjectList (LOW severity) returns Projects filtered by status.
	MethodNameProjectList = "runops.admin.project.list"
	// MethodNamePendingGet (LOW severity) returns the approval-flow state
	// for a given idempotency_key (= mutation request snapshot, body
	// excluded per pending_get.go redactPendingApproval).
	MethodNamePendingGet = "runops.admin.project.pending.get"

	// MethodNameProjectAdd (HIGH severity) creates a Project via the
	// 4-eyes approval flow. Registered by §B-5.2 (= mutation methods).
	MethodNameProjectAdd = "runops.admin.project.add"
	// MethodNameProjectArchive (HIGH severity) archives a Project via the
	// 4-eyes approval flow. Registered by §B-5.2.
	MethodNameProjectArchive = "runops.admin.project.archive"
)
