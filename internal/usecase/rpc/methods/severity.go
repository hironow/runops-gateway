package methods

import "sort"

// Severity classifies a JSON-RPC method by its impact on system state,
// per ADR 0040 §method 命名規約 + §approval gate integration.
//
//   - SeverityLow methods are read-only and do not require approval.
//     They execute synchronously and return a result envelope.
//   - SeverityHigh methods mutate persistent state and require 4-eyes
//     approval (= ADR 0035 carry: effective_requester_id != approver_id).
//     They create a PendingApproval and return `{idempotency_key,
//     status: "pending_approval"}` so the client can poll via
//     `runops.admin.project.pending.get`.
type Severity string

const (
	// SeverityLow = read-only method, no approval gate.
	SeverityLow Severity = "LOW"
	// SeverityHigh = mutation method, requires 4-eyes approval flow.
	SeverityHigh Severity = "HIGH"
)

// severityByMethod is the canonical method → severity map. Adding a new
// method MUST add an entry here, or Classify will report `ok=false` and
// downstream gates (= mutation check, readiness signal) will not see it.
//
// The entries are sourced from ADR 0040 §method 命名規約.
var severityByMethod = map[string]Severity{
	MethodNameProjectGet:     SeverityLow,
	MethodNameProjectList:    SeverityLow,
	MethodNamePendingGet:     SeverityLow,
	MethodNameProjectAdd:     SeverityHigh,
	MethodNameProjectArchive: SeverityHigh,
}

// Classify returns the severity of the given method name. ok==false
// when the method is not registered (= unknown to the classifier, the
// dispatcher will already have returned -32601 in that case, but this
// helper still reports "unknown" so callers can fail-safe).
func Classify(methodName string) (Severity, bool) {
	sev, ok := severityByMethod[methodName]
	if !ok {
		return "", false
	}
	return sev, true
}

// ListBySeverity returns the registered method names with the given
// severity, sorted lexically for stable enumeration (= readiness signal,
// startup log, doc generation).
func ListBySeverity(sev Severity) []string {
	out := make([]string, 0, len(severityByMethod))
	for name, s := range severityByMethod {
		if s == sev {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
