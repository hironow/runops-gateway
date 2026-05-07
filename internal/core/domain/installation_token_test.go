package domain_test

import (
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// AuditFingerprint is the ONLY token-derived value plan v8 §5.5 allows
// to be written to logs / OTel span attributes / D-Mail / Pub/Sub /
// archive. It is a 16-hex-char (=8 byte) prefix of sha256(token) so
// operators can correlate audit entries without ever writing the
// token itself.
func TestAuditFingerprint_DeterministicAndShape(t *testing.T) {
	token := "ghs_exampleshortlivedinstallationtoken"
	fp1 := domain.AuditFingerprint(token)
	fp2 := domain.AuditFingerprint(token)
	if fp1 != fp2 {
		t.Errorf("AuditFingerprint not deterministic: %q vs %q", fp1, fp2)
	}
	if len(fp1) != 16 {
		t.Errorf("AuditFingerprint length got %d, want 16", len(fp1))
	}
	if strings.Trim(fp1, "0123456789abcdef") != "" {
		t.Errorf("AuditFingerprint must be lowercase hex, got %q", fp1)
	}
}

// Distinct tokens must produce distinct fingerprints — collision risk
// at 8-byte truncation is negligible for the audit volume the broker
// expects, but identical fingerprints for different tokens would
// silently break audit correlation.
func TestAuditFingerprint_DistinctTokensDistinctFingerprints(t *testing.T) {
	a := domain.AuditFingerprint("ghs_alpha_token")
	b := domain.AuditFingerprint("ghs_beta_token")
	if a == b {
		t.Errorf("AuditFingerprint collision: %q == %q for distinct tokens", a, b)
	}
}

// Empty input still returns a fixed-shape fingerprint (sha256 of "")
// rather than panicking — the broker never calls this on empty input
// in practice but a defensive sentinel keeps the invariant clean.
func TestAuditFingerprint_EmptyInputDoesNotPanic(t *testing.T) {
	fp := domain.AuditFingerprint("")
	if len(fp) != 16 {
		t.Errorf("AuditFingerprint(\"\") length got %d, want 16", len(fp))
	}
}
