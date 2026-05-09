package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"sigs.k8s.io/yaml"

	domainrpc "github.com/hironow/runops-gateway/internal/core/domain/rpc"
)

// AdminTokensRegistry maps SHA256 token hashes to operator identities.
//
// Built from a YAML file (= path comes from RUNOPS_ADMIN_TOKENS_REGISTRY_FILE
// per ADR 0040 §identity contract). The file format:
//
//	tokens:
//	  - operator_id: U01234ABCD       # Slack user_id (= 4-eyes namespace 一致)
//	    token_hash: <sha256-hex>      # token は record しない、 hash のみ
//	    email: alice@example.com      # log/audit 用
//
// Lookup hashes the submitted bearer token (= SHA256 → 64-char lowercase hex)
// and resolves it via O(1) map lookup. Strict constant-time scanning is NOT
// performed; map non-constant-time variability is bounded by network jitter
// (per ADR 0040 §identity contract — "network latency dominant").
//
// The registry is immutable after Load; runtime rotation requires server
// restart (= scope-out for §B-3, see ADR 0040 §future).
type AdminTokensRegistry struct {
	byHash map[string]domainrpc.Operator
}

// tokensFile mirrors the on-disk YAML layout. JSON tags work via
// sigs.k8s.io/yaml, which goes YAML → JSON internally.
type tokensFile struct {
	Tokens []tokenEntry `json:"tokens"`
}

type tokenEntry struct {
	OperatorID string `json:"operator_id"`
	TokenHash  string `json:"token_hash"`
	Email      string `json:"email"`
}

// LoadAdminTokensRegistry reads and parses the YAML file at path. Returns an
// error for any validation failure (= caller is expected to treat this as
// startup-fatal per ADR 0040 §identity contract fail-closed posture).
//
// Validation rules:
//   - file must exist and be readable
//   - YAML must parse cleanly
//   - tokens list must be non-empty
//   - every entry must have non-empty operator_id and a 64-char lowercase
//     hex token_hash
//   - operator_id must be unique
//   - token_hash must be unique (= collisions indicate misconfiguration:
//     two operators using the same secret defeats the 4-eyes invariant)
func LoadAdminTokensRegistry(path string) (*AdminTokensRegistry, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path comes from operator-controlled env var
	if err != nil {
		return nil, fmt.Errorf("admin token registry: read %q: %w", path, err)
	}
	var f tokensFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("admin token registry: parse %q: %w", path, err)
	}
	if len(f.Tokens) == 0 {
		return nil, fmt.Errorf("admin token registry: %q has no tokens (empty registry rejected)", path)
	}

	byHash := make(map[string]domainrpc.Operator, len(f.Tokens))
	seenOperatorID := make(map[string]struct{}, len(f.Tokens))
	for i, e := range f.Tokens {
		if e.OperatorID == "" {
			return nil, fmt.Errorf("admin token registry: tokens[%d] empty operator_id", i)
		}
		if !isLowerHex64(e.TokenHash) {
			return nil, fmt.Errorf("admin token registry: tokens[%d] token_hash must be 64-char lowercase hex (sha256)", i)
		}
		if _, dup := seenOperatorID[e.OperatorID]; dup {
			return nil, fmt.Errorf("admin token registry: duplicate operator_id %q", e.OperatorID)
		}
		if _, dup := byHash[e.TokenHash]; dup {
			return nil, fmt.Errorf("admin token registry: duplicate token_hash for operator %q", e.OperatorID)
		}
		op, err := domainrpc.NewOperator(e.OperatorID, e.Email)
		if err != nil {
			return nil, fmt.Errorf("admin token registry: tokens[%d]: %w", i, err)
		}
		seenOperatorID[e.OperatorID] = struct{}{}
		byHash[e.TokenHash] = op
	}
	return &AdminTokensRegistry{byHash: byHash}, nil
}

// Lookup hashes the submitted token and returns the matching Operator.
// Returns the zero Operator and false on miss.
//
// The argument is the raw bearer token AFTER strict header parsing (= the
// caller must apply ADR 0030 §4 rules: no TrimSpace, single-space separator,
// control-char reject). This method does NOT re-validate framing; it expects
// a clean opaque token.
func (r *AdminTokensRegistry) Lookup(submittedToken string) (domainrpc.Operator, bool) {
	if r == nil {
		return domainrpc.Operator{}, false
	}
	sum := sha256.Sum256([]byte(submittedToken))
	op, ok := r.byHash[hex.EncodeToString(sum[:])]
	if !ok {
		return domainrpc.Operator{}, false
	}
	return op, true
}

// Size returns the number of registered operators. Useful for startup logs
// without leaking individual operator identities.
func (r *AdminTokensRegistry) Size() int {
	if r == nil {
		return 0
	}
	return len(r.byHash)
}

// isLowerHex64 reports whether s is exactly 64 lowercase hex characters
// (= the canonical form of a SHA256 digest).
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
