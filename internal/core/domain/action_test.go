package domain_test

import (
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestParseAction_Canary10(t *testing.T) {
	// given / when
	got, err := domain.ParseAction("canary_10")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "canary" {
		t.Errorf("expected Name %q, got %q", "canary", got.Name)
	}
	if got.Percent != 10 {
		t.Errorf("expected Percent 10, got %d", got.Percent)
	}
}

func TestParseAction_Canary50(t *testing.T) {
	// given / when
	got, err := domain.ParseAction("canary_50")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "canary" {
		t.Errorf("expected Name %q, got %q", "canary", got.Name)
	}
	if got.Percent != 50 {
		t.Errorf("expected Percent 50, got %d", got.Percent)
	}
}

func TestParseAction_MigrateApply(t *testing.T) {
	// given / when
	got, err := domain.ParseAction("migrate_apply")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "migrate_apply" {
		t.Errorf("expected Name %q, got %q", "migrate_apply", got.Name)
	}
	if got.Percent != 0 {
		t.Errorf("expected Percent 0, got %d", got.Percent)
	}
}

func TestParseAction_Rollback(t *testing.T) {
	// given / when
	got, err := domain.ParseAction("rollback")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "rollback" {
		t.Errorf("expected Name %q, got %q", "rollback", got.Name)
	}
	if got.Percent != 0 {
		t.Errorf("expected Percent 0, got %d", got.Percent)
	}
}

func TestParseAction_Empty(t *testing.T) {
	// given / when
	_, err := domain.ParseAction("")

	// then
	if err == nil {
		t.Fatal("expected error for empty string, got nil")
	}
}

func TestParseAction_NegativePercent(t *testing.T) {
	// given / when
	_, err := domain.ParseAction("canary_-1")

	// then
	if err == nil {
		t.Fatal("expected error for negative percent, got nil")
	}
}

func TestParseAction_Over100(t *testing.T) {
	// given / when
	_, err := domain.ParseAction("canary_101")

	// then
	if err == nil {
		t.Fatal("expected error for percent > 100, got nil")
	}
}

func TestParseAction_NonNumericSuffix(t *testing.T) {
	// given / when
	got, err := domain.ParseAction("foo_bar")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "foo_bar" {
		t.Errorf("expected Name %q, got %q", "foo_bar", got.Name)
	}
	if got.Percent != 0 {
		t.Errorf("expected Percent 0, got %d", got.Percent)
	}
}

func TestParseAction_BoundaryPercent0(t *testing.T) {
	// given / when — percent=0 is within valid range
	got, err := domain.ParseAction("canary_0")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "canary" {
		t.Errorf("expected Name %q, got %q", "canary", got.Name)
	}
	if got.Percent != 0 {
		t.Errorf("expected Percent 0, got %d", got.Percent)
	}
}

func TestParseAction_BoundaryPercent100(t *testing.T) {
	// given / when — percent=100 is the maximum valid value
	got, err := domain.ParseAction("canary_100")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "canary" {
		t.Errorf("expected Name %q, got %q", "canary", got.Name)
	}
	if got.Percent != 100 {
		t.Errorf("expected Percent 100, got %d", got.Percent)
	}
}

func TestParseAction_MultipleUnderscores_NumericSuffix(t *testing.T) {
	// given — "foo_bar_10": SplitN(...,2) → ["foo","bar_10"]; "bar_10" is not int → whole string is name
	got, err := domain.ParseAction("foo_bar_10")

	// then
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Name != "foo_bar_10" {
		t.Errorf("expected Name %q, got %q", "foo_bar_10", got.Name)
	}
	if got.Percent != 0 {
		t.Errorf("expected Percent 0, got %d", got.Percent)
	}
}

func TestParseAction_Percent101(t *testing.T) {
	// given — 101 is strictly over the maximum
	_, err := domain.ParseAction("canary_101")

	// then
	if err == nil {
		t.Fatal("expected error for percent=101, got nil")
	}
}

func TestNextCanaryPercent_10Returns30(t *testing.T) {
	// given / when
	got := domain.NextCanaryPercent(10)

	// then
	if got != 30 {
		t.Errorf("NextCanaryPercent(10) = %d, want 30", got)
	}
}

func TestNextCanaryPercent_30Returns50(t *testing.T) {
	if got := domain.NextCanaryPercent(30); got != 50 {
		t.Errorf("NextCanaryPercent(30) = %d, want 50", got)
	}
}

func TestNextCanaryPercent_50Returns80(t *testing.T) {
	if got := domain.NextCanaryPercent(50); got != 80 {
		t.Errorf("NextCanaryPercent(50) = %d, want 80", got)
	}
}

func TestNextCanaryPercent_80Returns100(t *testing.T) {
	if got := domain.NextCanaryPercent(80); got != 100 {
		t.Errorf("NextCanaryPercent(80) = %d, want 100", got)
	}
}

func TestNextCanaryPercent_100Returns0(t *testing.T) {
	// 100% is the final step; no next step
	if got := domain.NextCanaryPercent(100); got != 0 {
		t.Errorf("NextCanaryPercent(100) = %d, want 0", got)
	}
}

func TestNextCanaryPercent_InvalidReturns0(t *testing.T) {
	// values not in CanarySteps return 0
	for _, v := range []int32{0, 15, 25, 99} {
		if got := domain.NextCanaryPercent(v); got != 0 {
			t.Errorf("NextCanaryPercent(%d) = %d, want 0", v, got)
		}
	}
}

func TestCanarySteps_HasExpectedValues(t *testing.T) {
	expected := []int32{10, 30, 50, 80, 100}
	if len(domain.CanarySteps) != len(expected) {
		t.Fatalf("CanarySteps length = %d, want %d", len(domain.CanarySteps), len(expected))
	}
	for i, v := range expected {
		if domain.CanarySteps[i] != v {
			t.Errorf("CanarySteps[%d] = %d, want %d", i, domain.CanarySteps[i], v)
		}
	}
}
