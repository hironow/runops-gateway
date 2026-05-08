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

// MetadataKeyRequesterActorType is the canonical DMail.Metadata key for
// the actor type of the original requester (per ADR 0036 §Carry point 1).
// Value is one of the four CallerType enum strings; empty / absent is
// treated as CallerHumanOperator during the migration window (see ADR 0036).
const MetadataKeyRequesterActorType = "requester_actor_type"

// MetadataKeyInitiatingActorType carries the distal (initiating) actor
// when the proximate RequesterActorType is workspace-daemon (per ADR 0037
// §Axis 3). REQUIRED for HIGH severity Phase 4a approvals; optional
// otherwise. Value is one of the four CallerType enum strings.
const MetadataKeyInitiatingActorType = "initiating_actor_type"

// MetadataKeyRequesterActorSource carries the producer-side source
// declaration for RequesterActorType (per ADR 0037 §Axis 4). Producer-
// writable enum: { broker, env, unknown }. The gateway derives the
// internal classification (broker_verified / env_attested / unknown /
// spoofed_broker) from this value plus its own request context.
const MetadataKeyRequesterActorSource = "requester_actor_source"

// canonicalDMailKeys are the frontmatter keys ParseDMail extracts directly
// into the DMail struct (everything else lands in Metadata). Mirrors the
// fixed-order section in RenderMarkdown.
var canonicalDMailKeys = map[string]struct{}{
	"dmail-schema-version": {},
	"id":                   {},
	"kind":                 {},
	"target":               {},
	"source":               {},
	"idempotency_key":      {},
}

// ParseDMail parses the on-disk .md document produced by RenderMarkdown back
// into a DMail value. Frontmatter must be delimited by two `---` lines; the
// body is whatever follows the closing delimiter (leading blank line stripped).
// Unknown kinds are rejected so a corrupted file does not propagate as a
// supposedly valid kind elsewhere.
func ParseDMail(b []byte) (DMail, error) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return DMail{}, fmt.Errorf("dmail: missing opening --- frontmatter delimiter")
	}
	// Strip the opening delimiter.
	rest := strings.TrimPrefix(s, "---\n")
	rest = strings.TrimPrefix(rest, "---\r\n")
	closeIdx := strings.Index(rest, "\n---\n")
	if closeIdx < 0 {
		closeIdx = strings.Index(rest, "\n---\r\n")
	}
	if closeIdx < 0 {
		// Allow a trailing --- with no body.
		if strings.HasSuffix(rest, "\n---") {
			closeIdx = len(rest) - len("\n---")
		}
	}
	if closeIdx < 0 {
		return DMail{}, fmt.Errorf("dmail: missing closing --- frontmatter delimiter")
	}
	frontmatter := rest[:closeIdx]
	body := rest[closeIdx:]
	body = strings.TrimPrefix(body, "\n---\n")
	body = strings.TrimPrefix(body, "\n---\r\n")
	body = strings.TrimPrefix(body, "\n---")
	body = strings.TrimPrefix(body, "\n")

	m := DMail{Metadata: map[string]string{}}
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		val := strings.TrimSpace(line[colon+1:])
		// Strip optional quotes on the value (we render dmail-schema-version as a string).
		val = strings.Trim(val, "\"")
		if _, isCanonical := canonicalDMailKeys[key]; isCanonical {
			switch key {
			case "id":
				m.ID = val
			case "kind":
				k, err := ParseDMailKind(val)
				if err != nil {
					return DMail{}, fmt.Errorf("dmail: %w", err)
				}
				m.Kind = k
			case "target":
				m.Target = val
			case "source":
				m.Source = val
			case "idempotency_key":
				m.IdempotencyKey = val
			case "dmail-schema-version":
				// Only schema v1 is recognized; refuse anything else loudly.
				if val != "1" {
					return DMail{}, fmt.Errorf("dmail: unsupported schema version %q", val)
				}
			}
			continue
		}
		m.Metadata[key] = val
	}
	if m.Kind == "" {
		return DMail{}, fmt.Errorf("dmail: kind is required")
	}
	m.Body = body
	return m, nil
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
