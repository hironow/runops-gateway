package domain_test

import (
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestDMailKindConstants(t *testing.T) {
	cases := []struct {
		kind domain.DMailKind
		want string
	}{
		{domain.DMailKindSpecification, "specification"},
		{domain.DMailKindReport, "report"},
		{domain.DMailKindDesignFeedback, "design-feedback"},
		{domain.DMailKindImplementationFeedback, "implementation-feedback"},
		{domain.DMailKindConvergence, "convergence"},
		{domain.DMailKindCIResult, "ci-result"},
	}
	for _, tc := range cases {
		if string(tc.kind) != tc.want {
			t.Errorf("kind=%q want %q", tc.kind, tc.want)
		}
	}
}

func TestParseDMailKind_AcceptsKnown(t *testing.T) {
	for _, name := range []string{
		"specification", "report", "design-feedback",
		"implementation-feedback", "convergence", "ci-result",
	} {
		k, err := domain.ParseDMailKind(name)
		if err != nil {
			t.Errorf("ParseDMailKind(%q) returned error: %v", name, err)
		}
		if string(k) != name {
			t.Errorf("ParseDMailKind(%q) = %q", name, k)
		}
	}
}

func TestParseDMailKind_RejectsUnknown(t *testing.T) {
	for _, name := range []string{"", "SPECIFICATION", "feedback", "unknown"} {
		if _, err := domain.ParseDMailKind(name); err == nil {
			t.Errorf("ParseDMailKind(%q) expected error, got nil", name)
		}
	}
}

func TestDMail_FrontmatterContainsCanonicalFields(t *testing.T) {
	m := domain.DMail{
		ID:             "01HZW0K0AB12CD34EF56GH78JK",
		Kind:           domain.DMailKindSpecification,
		Target:         "paintress",
		Source:         "runops-gateway-slack",
		IdempotencyKey: "ab12cd34",
		Body:           "Fix M-42 in the auth module.",
		Metadata: map[string]string{
			"requester_id": "U0123ABCD",
		},
	}

	doc := m.RenderMarkdown()

	// Frontmatter must include the schema version, kind, target, idempotency_key,
	// and source so phonewave routes correctly and ADR 0012 sender info is
	// preserved without introducing a new kind.
	for _, want := range []string{
		"dmail-schema-version: \"1\"",
		"kind: specification",
		"target: paintress",
		"source: runops-gateway-slack",
		"idempotency_key: ab12cd34",
		"requester_id: U0123ABCD",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("RenderMarkdown missing %q; got:\n%s", want, doc)
		}
	}
	// Body must appear after the closing ---.
	if !strings.Contains(doc, "Fix M-42 in the auth module.") {
		t.Errorf("RenderMarkdown body missing; got:\n%s", doc)
	}
}

func TestDMail_OperationKey_StableAndUnique(t *testing.T) {
	a := domain.DMail{
		ID:             "01HZW0K0AB12CD34EF56GH78JK",
		Kind:           domain.DMailKindSpecification,
		Target:         "paintress",
		IdempotencyKey: "ab12cd34",
	}
	if a.OperationKey() == "" {
		t.Error("OperationKey should not be empty")
	}
	first := a.OperationKey()
	second := a.OperationKey()
	if first != second {
		t.Errorf("OperationKey must be stable across calls; first=%q second=%q", first, second)
	}

	b := a
	b.IdempotencyKey = "different"
	if a.OperationKey() == b.OperationKey() {
		t.Error("OperationKey must differ when IdempotencyKey differs")
	}

	c := a
	c.Target = "sightjack"
	if a.OperationKey() == c.OperationKey() {
		t.Error("OperationKey must differ when Target differs")
	}
}
