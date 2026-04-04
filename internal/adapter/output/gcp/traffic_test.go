package gcp

import (
	"testing"
)

func TestSelectActiveRevision_PicksHighestTraffic(t *testing.T) {
	// given — rev-old has 10%, rev-current has 90%; new canary targets rev-new
	traffic := []trafficEntry{
		{revision: "rev-old", percent: 10},
		{revision: "rev-current", percent: 90},
	}

	// when
	got := selectActiveRevision(traffic, "rev-new")

	// then — must pick rev-current (90%), not rev-old (10%)
	if got != "rev-current" {
		t.Errorf("selectActiveRevision = %q, want rev-current (highest traffic)", got)
	}
}

func TestSelectActiveRevision_SkipsTargetRevision(t *testing.T) {
	// given — target revision has the highest traffic, should be skipped
	traffic := []trafficEntry{
		{revision: "rev-target", percent: 90},
		{revision: "rev-other", percent: 10},
	}

	// when
	got := selectActiveRevision(traffic, "rev-target")

	// then — must pick rev-other, not rev-target
	if got != "rev-other" {
		t.Errorf("selectActiveRevision = %q, want rev-other", got)
	}
}

func TestSelectActiveRevision_SingleRevision_ReturnsEmpty(t *testing.T) {
	// given — only the target revision exists
	traffic := []trafficEntry{
		{revision: "rev-target", percent: 100},
	}

	// when
	got := selectActiveRevision(traffic, "rev-target")

	// then — no other revision available
	if got != "" {
		t.Errorf("selectActiveRevision = %q, want empty string", got)
	}
}

func TestSelectActiveRevision_AllZeroPercent_ReturnsEmpty(t *testing.T) {
	// given — all other revisions have 0% traffic
	traffic := []trafficEntry{
		{revision: "rev-target", percent: 100},
		{revision: "rev-old", percent: 0},
	}

	// when
	got := selectActiveRevision(traffic, "rev-target")

	// then
	if got != "" {
		t.Errorf("selectActiveRevision = %q, want empty string (all others at 0%%)", got)
	}
}

func TestSelectActiveRevision_ThreeWaySplit_PicksHighest(t *testing.T) {
	// given — 3-way split: rev-A(60%), rev-B(30%), rev-C(10%)
	traffic := []trafficEntry{
		{revision: "rev-C", percent: 10},
		{revision: "rev-A", percent: 60},
		{revision: "rev-B", percent: 30},
	}

	// when — new canary for rev-D
	got := selectActiveRevision(traffic, "rev-D")

	// then — must pick rev-A (60%), the highest
	if got != "rev-A" {
		t.Errorf("selectActiveRevision = %q, want rev-A (highest at 60%%)", got)
	}
}

func TestSelectActiveRevision_EmptyTraffic_ReturnsEmpty(t *testing.T) {
	// given
	got := selectActiveRevision(nil, "rev-new")

	// then
	if got != "" {
		t.Errorf("selectActiveRevision = %q, want empty string", got)
	}
}

// --- isTrafficAlreadyMatching tests ---

func TestIsTrafficAlreadyMatching_ExactMatch(t *testing.T) {
	// given — current traffic matches desired state
	current := []trafficEntry{
		{revision: "rev-new", percent: 10},
		{revision: "rev-old", percent: 90},
	}

	// when
	got := isTrafficAlreadyMatching(current, "rev-new", 10)

	// then
	if !got {
		t.Error("expected match when current == desired")
	}
}

func TestIsTrafficAlreadyMatching_DifferentPercent(t *testing.T) {
	// given
	current := []trafficEntry{
		{revision: "rev-new", percent: 30},
		{revision: "rev-old", percent: 70},
	}

	// when
	got := isTrafficAlreadyMatching(current, "rev-new", 10)

	// then
	if got {
		t.Error("expected no match when percent differs")
	}
}

func TestIsTrafficAlreadyMatching_RevisionNotFound(t *testing.T) {
	// given
	current := []trafficEntry{
		{revision: "rev-old", percent: 100},
	}

	// when
	got := isTrafficAlreadyMatching(current, "rev-new", 10)

	// then
	if got {
		t.Error("expected no match when revision not in traffic")
	}
}

func TestIsTrafficAlreadyMatching_ZeroPercent_RevisionAbsent(t *testing.T) {
	// given — rollback: target is not in traffic (already at 0%)
	current := []trafficEntry{
		{revision: "rev-old", percent: 100},
	}

	// when
	got := isTrafficAlreadyMatching(current, "rev-new", 0)

	// then — revision absent with desired 0% means already rolled back
	if !got {
		t.Error("expected match when revision absent and desired percent is 0")
	}
}

func TestIsTrafficAlreadyMatching_ZeroPercent_RevisionPresent(t *testing.T) {
	// given — revision still in traffic with 0%
	current := []trafficEntry{
		{revision: "rev-new", percent: 0},
		{revision: "rev-old", percent: 100},
	}

	// when
	got := isTrafficAlreadyMatching(current, "rev-new", 0)

	// then
	if !got {
		t.Error("expected match when revision at 0% and desired is 0")
	}
}

// --- Idempotency scenario tests ---

func TestIdempotency_CanaryAlreadyAt30_SkipUpdate(t *testing.T) {
	// given — canary already at 30%, requesting 30% again
	current := []trafficEntry{
		{revision: "rev-new", percent: 30},
		{revision: "rev-old", percent: 70},
	}

	// then — isTrafficAlreadyMatching returns true → controller skips UpdateService
	if !isTrafficAlreadyMatching(current, "rev-new", 30) {
		t.Error("expected idempotent skip when canary already at desired percent")
	}
}

func TestIdempotency_100Percent_NoActiveRevisionNeeded(t *testing.T) {
	// given — revision at 100%, requesting 100% again
	current := []trafficEntry{
		{revision: "rev-new", percent: 100},
	}

	if !isTrafficAlreadyMatching(current, "rev-new", 100) {
		t.Error("expected idempotent skip at 100%")
	}
}

func TestIdempotency_RollbackAlreadyDone(t *testing.T) {
	// given — rollback already done: target not in traffic
	current := []trafficEntry{
		{revision: "rev-old", percent: 100},
	}

	if !isTrafficAlreadyMatching(current, "rev-new", 0) {
		t.Error("expected idempotent skip for already-rolled-back revision")
	}
}

func TestIdempotency_DifferentPercent_MustUpdate(t *testing.T) {
	// given — canary at 10%, requesting 30%
	current := []trafficEntry{
		{revision: "rev-new", percent: 10},
		{revision: "rev-old", percent: 90},
	}

	if isTrafficAlreadyMatching(current, "rev-new", 30) {
		t.Error("expected no skip when percent differs")
	}
}

// --- selectActiveRevision for WorkerPool scenarios ---

func TestSelectActiveRevision_WorkerPool_TargetIsLatest(t *testing.T) {
	// given — target revision IS the latest, simulating the LATEST bug scenario
	// WorkerPool with 2 instance splits: rev-v2(10%), rev-v1(90%)
	// If we used LATEST and rev-v2 is latest, both splits would point to rev-v2.
	// With selectActiveRevision, we correctly pick rev-v1.
	traffic := []trafficEntry{
		{revision: "pool-rev-v2", percent: 10},
		{revision: "pool-rev-v1", percent: 90},
	}

	got := selectActiveRevision(traffic, "pool-rev-v2")
	if got != "pool-rev-v1" {
		t.Errorf("selectActiveRevision = %q, want pool-rev-v1", got)
	}
}

func TestSelectActiveRevision_TiedPercent_PicksFirst(t *testing.T) {
	// given — two revisions with equal traffic
	traffic := []trafficEntry{
		{revision: "rev-A", percent: 50},
		{revision: "rev-B", percent: 50},
	}

	// when — either is acceptable; implementation picks the first one found (strict >)
	got := selectActiveRevision(traffic, "rev-C")
	if got != "rev-A" {
		t.Errorf("selectActiveRevision = %q, want rev-A (first with highest)", got)
	}
}

// --- Compensating rollback: selectActiveRevision during rollback ---

func TestSelectActiveRevision_RollbackScenario(t *testing.T) {
	// given — canary in progress: rev-old(90%), rev-new(10%)
	// Rollback means ShiftTraffic(rev-new, 0): need activeRevision = rev-old
	traffic := []trafficEntry{
		{revision: "rev-old", percent: 90},
		{revision: "rev-new", percent: 10},
	}

	got := selectActiveRevision(traffic, "rev-new")
	if got != "rev-old" {
		t.Errorf("rollback: selectActiveRevision = %q, want rev-old", got)
	}
}

func TestSelectActiveRevision_CompensatingRollback_AfterPartialShift(t *testing.T) {
	// given — svc-A was shifted to 10%, now compensating back to 0%
	// Current traffic on svc-A: rev-new(10%), rev-old(90%)
	traffic := []trafficEntry{
		{revision: "rev-new", percent: 10},
		{revision: "rev-old", percent: 90},
	}

	// Compensating: ShiftTraffic(svc-A, rev-new, 0) → activeRevision = rev-old
	got := selectActiveRevision(traffic, "rev-new")
	if got != "rev-old" {
		t.Errorf("compensating rollback: selectActiveRevision = %q, want rev-old", got)
	}
}
