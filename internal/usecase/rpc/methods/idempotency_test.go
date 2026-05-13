package methods_test

import (
	"encoding/json"
	"testing"

	"github.com/hironow/runops-gateway/internal/usecase/rpc/methods"
)

func TestComputeIdempotencyKey_Deterministic(t *testing.T) {
	// given - same inputs
	op := "U01234ABCD"
	method := methods.MethodNameProjectAdd
	params := json.RawMessage(`{"id":"alpha","github_org":"acme"}`)

	// when - compute twice
	k1, err1 := methods.ComputeIdempotencyKey(op, method, params)
	k2, err2 := methods.ComputeIdempotencyKey(op, method, params)

	// then
	if err1 != nil || err2 != nil {
		t.Fatalf("compute err: %v %v", err1, err2)
	}
	if k1 != k2 {
		t.Errorf("non-deterministic: %q vs %q", k1, k2)
	}
}

func TestComputeIdempotencyKey_OperatorIsolation(t *testing.T) {
	// given - same method + params, different operator
	method := methods.MethodNameProjectAdd
	params := json.RawMessage(`{"id":"alpha"}`)

	// when
	k1, _ := methods.ComputeIdempotencyKey("U_ALICE", method, params)
	k2, _ := methods.ComputeIdempotencyKey("U_BOB", method, params)

	// then - different operators must produce different keys
	if k1 == k2 {
		t.Errorf("operator isolation failed: both got %q", k1)
	}
}

func TestComputeIdempotencyKey_FieldOrderInvariant(t *testing.T) {
	// given - same params with reordered fields
	op := "U_ALICE"
	method := methods.MethodNameProjectAdd
	a := json.RawMessage(`{"id":"alpha","github_org":"acme"}`)
	b := json.RawMessage(`{"github_org":"acme","id":"alpha"}`)

	// when
	k1, err1 := methods.ComputeIdempotencyKey(op, method, a)
	k2, err2 := methods.ComputeIdempotencyKey(op, method, b)

	// then - canonical_json should make reorder produce same key
	if err1 != nil || err2 != nil {
		t.Fatalf("compute err: %v %v", err1, err2)
	}
	if k1 != k2 {
		t.Errorf("field-order invariance failed: %q vs %q", k1, k2)
	}
}

func TestComputeIdempotencyKey_Format(t *testing.T) {
	// then - key is 32 lowercase hex (= 16-byte SHA256 prefix)
	k, err := methods.ComputeIdempotencyKey("U_X", "any", nil)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if len(k) != 32 {
		t.Errorf("key length: got %d, want 32", len(k))
	}
	for _, c := range k {
		isDigit := c >= '0' && c <= '9'
		isLowerHex := c >= 'a' && c <= 'f'
		if !isDigit && !isLowerHex {
			t.Errorf("non-hex char: %q in %s", c, k)
			break
		}
	}
}

func TestComputeIdempotencyKey_DifferentMethods(t *testing.T) {
	// given - same op + params, different method
	op := "U_ALICE"
	params := json.RawMessage(`{"id":"alpha"}`)

	// when
	add, _ := methods.ComputeIdempotencyKey(op, methods.MethodNameProjectAdd, params)
	arc, _ := methods.ComputeIdempotencyKey(op, methods.MethodNameProjectArchive, params)

	// then
	if add == arc {
		t.Errorf("method isolation failed: both got %q", add)
	}
}
