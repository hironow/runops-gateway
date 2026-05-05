package domain

import (
	"fmt"
	"sort"
	"strings"
)

// DMailKind enumerates the six message kinds defined by the D-Mail Protocol
// schema v1 (see /Users/nino/tap/phonewave/README.md). Phase 1+ runops-gateway
// reuses these existing kinds — ADR 0012 forbids introducing new ones —
// and identifies the sender via the Source / Metadata fields instead.
type DMailKind string

const (
	DMailKindSpecification          DMailKind = "specification"
	DMailKindReport                 DMailKind = "report"
	DMailKindDesignFeedback         DMailKind = "design-feedback"
	DMailKindImplementationFeedback DMailKind = "implementation-feedback"
	DMailKindConvergence            DMailKind = "convergence"
	DMailKindCIResult               DMailKind = "ci-result"
)

// ParseDMailKind returns the canonical DMailKind for s or an error if s is not
// one of the six lowercase kind names. Strict to avoid silent misrouting; ADR
// 0012 keeps this enum closed.
func ParseDMailKind(s string) (DMailKind, error) {
	switch DMailKind(s) {
	case DMailKindSpecification, DMailKindReport, DMailKindDesignFeedback,
		DMailKindImplementationFeedback, DMailKindConvergence, DMailKindCIResult:
		return DMailKind(s), nil
	default:
		return "", fmt.Errorf("unknown D-Mail kind: %q", s)
	}
}

// DMail is the canonical D-Mail Protocol v1 message handed to publishers.
// Phase 2a uses it as the value type for the Pub/Sub publisher; Phase 2b/2c
// re-uses it on the receiver / emitter sides to read and reconstruct the
// .md file written into a phonewave outbox.
type DMail struct {
	// ID is a globally unique identifier for the message (typically ULID/UUID).
	// Used as the Pub/Sub message attribute and the on-disk filename stem.
	ID string
	// Kind is one of the six DMailKind values.
	Kind DMailKind
	// Target is the destination tool name ("paintress" / "sightjack" / "amadeus" /
	// "dominator" / "*" for broadcast). Phonewave uses this to choose the inbox.
	Target string
	// Source identifies the producer ("runops-gateway-slack" / "runops-gateway-ci" /
	// "<tool>"). Required by ADR 0012 in lieu of new kinds.
	Source string
	// IdempotencyKey deduplicates retries across the publish path; receivers
	// SHOULD honor it.
	IdempotencyKey string
	// Body is the message body (Markdown). Rendered after the YAML frontmatter
	// in the on-disk .md file.
	Body string
	// Metadata is an open-ended set of additional frontmatter fields (e.g.
	// requester_id, slack_thread_ts, parent_idempotency_key). Keys with a
	// dash in them (e.g. dmail-schema-version) are reserved by the protocol
	// and must not be set here.
	Metadata map[string]string
}

// OperationKey returns a stable deduplication key for the message. Used by
// in-process StateStore (Phase 1) and by Pub/Sub message attributes (Phase 2+).
func (m DMail) OperationKey() string {
	return fmt.Sprintf("dmail/%s/%s/%s", m.Kind, m.Target, m.IdempotencyKey)
}

// RenderMarkdown produces the on-disk .md document. Schema v1 frontmatter goes
// between two `---` lines; the canonical fields appear in a fixed order so
// snapshot tests stay deterministic, and the user-supplied Metadata follows
// in alphabetical order so two equal DMails always serialize identically.
func (m DMail) RenderMarkdown() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("dmail-schema-version: \"1\"\n")
	if m.ID != "" {
		fmt.Fprintf(&b, "id: %s\n", m.ID)
	}
	fmt.Fprintf(&b, "kind: %s\n", m.Kind)
	if m.Target != "" {
		fmt.Fprintf(&b, "target: %s\n", m.Target)
	}
	if m.Source != "" {
		fmt.Fprintf(&b, "source: %s\n", m.Source)
	}
	if m.IdempotencyKey != "" {
		fmt.Fprintf(&b, "idempotency_key: %s\n", m.IdempotencyKey)
	}
	if len(m.Metadata) > 0 {
		keys := make([]string, 0, len(m.Metadata))
		for k := range m.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s: %s\n", k, m.Metadata[k])
		}
	}
	b.WriteString("---\n\n")
	b.WriteString(m.Body)
	if !strings.HasSuffix(m.Body, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}
