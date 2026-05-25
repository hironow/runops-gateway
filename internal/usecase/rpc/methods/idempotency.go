package methods

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// idempotencyKeyHexLen is the number of hex characters used for the
// idempotency key (= 16-byte SHA256 prefix = 128 bits, collision-safe
// for the expected pending-approval population).
const idempotencyKeyHexLen = 32

// ComputeIdempotencyKey derives a deterministic 32-character lowercase
// hex key from the requester identity, JSON-RPC method name, and params.
//
// Two design invariants:
//
//   - **operator isolation**: same (method, params) submitted by different
//     operators produce different keys. This prevents one operator's
//     pending request from being mistaken as a retry of another's.
//
//   - **field-order invariance**: same logical params with reordered JSON
//     fields produce the same key. We canonicalize via json.Marshal of a
//     decoded map[string]any (= sorted keys by Go's encoder).
//
// Returns an error only when params is non-empty but invalid JSON
// (= the dispatcher will already have rejected before reaching here,
// so this is a defensive guard).
func ComputeIdempotencyKey(effectiveRequesterID, method string, params json.RawMessage) (string, error) {
	canonical, err := canonicalizeParams(params)
	if err != nil {
		return "", fmt.Errorf("canonicalize params: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(effectiveRequesterID))
	h.Write([]byte{0}) // separator so "a" + "bc" != "ab" + "c"
	h.Write([]byte(method))
	h.Write([]byte{0})
	h.Write(canonical)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:idempotencyKeyHexLen/2]), nil
}

// canonicalizeParams produces a stable serialization of the params object
// where map key order is normalized (= json.Marshal of map[string]any
// emits keys sorted lexicographically per encoding/json spec).
//
// Empty / null params canonicalize to "null".
func canonicalizeParams(params json.RawMessage) ([]byte, error) {
	if len(params) == 0 {
		return []byte("null"), nil
	}
	var v any
	if err := json.Unmarshal(params, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
