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
