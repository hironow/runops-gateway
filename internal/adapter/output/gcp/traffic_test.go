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
