package domain_test

import (
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

func TestParseDMail_RoundTrip(t *testing.T) {
	original := domain.DMail{
		ID:             "01HZW0K0AB12CD34EF56GH78JK",
		Kind:           domain.DMailKindReport,
		Target:         "amadeus",
		Source:         "paintress",
		IdempotencyKey: "abcd",
		Body:           "PR #42 merged successfully.\n",
		Metadata: map[string]string{
			"requester_id": "U0999",
		},
	}
	doc := original.RenderMarkdown()

	parsed, err := domain.ParseDMail([]byte(doc))
	if err != nil {
		t.Fatalf("ParseDMail: %v", err)
	}
	if parsed.ID != original.ID || parsed.Kind != original.Kind ||
		parsed.Target != original.Target || parsed.Source != original.Source ||
		parsed.IdempotencyKey != original.IdempotencyKey {
		t.Errorf("canonical fields drifted: got %+v want %+v", parsed, original)
	}
	if !strings.Contains(parsed.Body, "PR #42 merged successfully.") {
		t.Errorf("body lost: %q", parsed.Body)
	}
	if parsed.Metadata["requester_id"] != "U0999" {
		t.Errorf("metadata lost: %v", parsed.Metadata)
	}
}

func TestParseDMail_RejectsMissingFrontmatter(t *testing.T) {
	if _, err := domain.ParseDMail([]byte("no frontmatter at all\n")); err == nil {
		t.Error("expected error when frontmatter is missing")
	}
}

func TestParseDMail_RejectsUnclosedFrontmatter(t *testing.T) {
	bad := "---\nkind: report\ntarget: amadeus\n\nbody body body\n"
	if _, err := domain.ParseDMail([]byte(bad)); err == nil {
		t.Error("expected error when closing --- is missing")
	}
}

func TestParseDMail_RejectsUnknownKind(t *testing.T) {
	bad := "---\ndmail-schema-version: \"1\"\nkind: bogus\ntarget: amadeus\n---\n\nbody\n"
	if _, err := domain.ParseDMail([]byte(bad)); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestParseDMail_PreservesUnknownMetadataKeys(t *testing.T) {
	doc := "---\n" +
		"dmail-schema-version: \"1\"\n" +
		"id: x1\n" +
		"kind: specification\n" +
		"target: paintress\n" +
		"slack_thread_ts: 1700000000.000050\n" +
		"trace_id: deadbeef\n" +
		"---\n\nplease fix\n"
	parsed, err := domain.ParseDMail([]byte(doc))
	if err != nil {
		t.Fatalf("ParseDMail: %v", err)
	}
	if parsed.Metadata["slack_thread_ts"] != "1700000000.000050" {
		t.Errorf("missing slack_thread_ts: %v", parsed.Metadata)
	}
	if parsed.Metadata["trace_id"] != "deadbeef" {
		t.Errorf("missing trace_id: %v", parsed.Metadata)
	}
}
