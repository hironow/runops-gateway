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
