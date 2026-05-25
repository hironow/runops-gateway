package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestValidateProjectID_AcceptsValid(t *testing.T) {
	for _, id := range []string{
		"foo",
		"foo-bar",
		"foo_bar",
		"FooBar",
		"a1b2c3",
		"x",                     // 1 char OK
		strings.Repeat("a", 64), // boundary: exactly 64 chars
	} {
		if err := domain.ValidateProjectID(id); err != nil {
			t.Errorf("ValidateProjectID(%q) want nil, got %v", id, err)
		}
	}
}

func TestValidateProjectID_RejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"too long (65)":      strings.Repeat("a", 65),
		"slash":              "foo/bar",
		"dot":                "foo.bar",
		"space":              "foo bar",
		"colon":              "foo:bar",
		"unicode":            "ふー",
		"leading whitespace": " foo",
	}
	for name, id := range cases {
		err := domain.ValidateProjectID(id)
		if err == nil {
			t.Errorf("ValidateProjectID(%q) [%s] want error, got nil", id, name)
			continue
		}
		if !errors.Is(err, domain.ErrInvalidProjectID) {
			t.Errorf("ValidateProjectID(%q) [%s] want ErrInvalidProjectID, got %v", id, name, err)
		}
	}
}

func TestProjectStatusConstants(t *testing.T) {
	if got := string(domain.ProjectStatusActive); got != "active" {
		t.Errorf("ProjectStatusActive = %q, want \"active\"", got)
	}
	if got := string(domain.ProjectStatusArchived); got != "archived" {
		t.Errorf("ProjectStatusArchived = %q, want \"archived\"", got)
	}
}
