package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/auth"
	"github.com/hironow/runops-gateway/internal/core/domain/rpc"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Compile-time assertion: AdminTokensRegistry satisfies port.OperatorLookup.
var _ port.OperatorLookup = (*auth.AdminTokensRegistry)(nil)

// hashTokenForFixture is a test helper to produce the SHA256 hex digest of a
// token. Production callers MUST use the registry's Lookup which performs the
// hashing internally.
func hashTokenForFixture(t *testing.T, token string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func writeRegistryFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "admin-tokens.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestParseTokenRegistry_ValidYAML_BuildsLookupMap(t *testing.T) {
	// given
	aliceHash := hashTokenForFixture(t, "alice-secret")
	bobHash := hashTokenForFixture(t, "bob-secret")
	yaml := "tokens:\n" +
		"  - operator_id: U01234ABCD\n" +
		"    token_hash: " + aliceHash + "\n" +
		"    email: alice@example.com\n" +
		"  - operator_id: U05678EFGH\n" +
		"    token_hash: " + bobHash + "\n" +
		"    email: bob@example.com\n"
	path := writeRegistryFile(t, yaml)

	// when
	reg, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err != nil {
		t.Fatalf("LoadAdminTokensRegistry: %v", err)
	}
	if reg == nil {
		t.Fatalf("registry is nil")
	}
	op, ok := reg.Lookup("alice-secret")
	if !ok {
		t.Fatalf("alice token miss")
	}
	if op.OperatorID != "U01234ABCD" || op.Email != "alice@example.com" {
		t.Errorf("alice operator: got %+v", op)
	}
	if op.ActorType != rpc.ActorTypeHumanOperator {
		t.Errorf("alice ActorType: got %q", op.ActorType)
	}
	if _, ok := reg.Lookup("bob-secret"); !ok {
		t.Errorf("bob token miss")
	}
}

func TestTokenRegistry_LookupHit_ReturnsOperator(t *testing.T) {
	// given
	tokenHash := hashTokenForFixture(t, "secret-1")
	yaml := "tokens:\n" +
		"  - operator_id: U1\n" +
		"    token_hash: " + tokenHash + "\n" +
		"    email: a@b.c\n"
	path := writeRegistryFile(t, yaml)
	reg, _ := auth.LoadAdminTokensRegistry(path)

	// when
	op, ok := reg.Lookup("secret-1")

	// then
	if !ok {
		t.Fatalf("expected hit")
	}
	if op.OperatorID != "U1" {
		t.Errorf("OperatorID: got %q", op.OperatorID)
	}
}

func TestTokenRegistry_LookupMiss_ReturnsZero(t *testing.T) {
	// given
	hash := hashTokenForFixture(t, "valid")
	yaml := "tokens:\n" +
		"  - operator_id: U1\n" +
		"    token_hash: " + hash + "\n" +
		"    email: a@b.c\n"
	path := writeRegistryFile(t, yaml)
	reg, _ := auth.LoadAdminTokensRegistry(path)

	// when
	op, ok := reg.Lookup("wrong-token")

	// then
	if ok {
		t.Errorf("expected miss")
	}
	if !op.IsZero() {
		t.Errorf("miss must return zero Operator, got %+v", op)
	}
}

func TestParseTokenRegistry_DuplicateOperatorID_Rejected(t *testing.T) {
	// given
	hash1 := hashTokenForFixture(t, "tok1")
	hash2 := hashTokenForFixture(t, "tok2")
	yaml := "tokens:\n" +
		"  - operator_id: U_DUP\n" +
		"    token_hash: " + hash1 + "\n" +
		"    email: a@b.c\n" +
		"  - operator_id: U_DUP\n" +
		"    token_hash: " + hash2 + "\n" +
		"    email: c@d.e\n"
	path := writeRegistryFile(t, yaml)

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for duplicate operator_id")
	}
	if !strings.Contains(err.Error(), "operator_id") {
		t.Errorf("error message should mention operator_id: %v", err)
	}
}

func TestParseTokenRegistry_DuplicateTokenHash_Rejected(t *testing.T) {
	// given - same hash for two operators is also a misconfiguration
	dupHash := hashTokenForFixture(t, "shared-token")
	yaml := "tokens:\n" +
		"  - operator_id: U_A\n" +
		"    token_hash: " + dupHash + "\n" +
		"    email: a@b.c\n" +
		"  - operator_id: U_B\n" +
		"    token_hash: " + dupHash + "\n" +
		"    email: b@c.d\n"
	path := writeRegistryFile(t, yaml)

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for duplicate token_hash")
	}
	if !strings.Contains(err.Error(), "token_hash") {
		t.Errorf("error message should mention token_hash: %v", err)
	}
}

func TestParseTokenRegistry_InvalidYAML_ReturnsError(t *testing.T) {
	// given
	path := writeRegistryFile(t, "tokens: [\n  not-yaml")

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for invalid YAML")
	}
}

func TestParseTokenRegistry_EmptyTokensList_Rejected(t *testing.T) {
	// given
	path := writeRegistryFile(t, "tokens: []\n")

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for empty tokens list")
	}
}

func TestParseTokenRegistry_RejectsMissingFile(t *testing.T) {
	// given
	dir := t.TempDir()
	path := filepath.Join(dir, "no-such-file.yaml")

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
}

func TestParseTokenRegistry_RejectsMalformedTokenHash(t *testing.T) {
	// given - non-hex token_hash should be rejected at load time
	yaml := "tokens:\n" +
		"  - operator_id: U1\n" +
		"    token_hash: not-a-hex-string-zzz\n" +
		"    email: a@b.c\n"
	path := writeRegistryFile(t, yaml)

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for non-hex token_hash")
	}
}

func TestParseTokenRegistry_RejectsEmptyOperatorID(t *testing.T) {
	// given
	hash := hashTokenForFixture(t, "tok")
	yaml := "tokens:\n" +
		"  - operator_id: ''\n" +
		"    token_hash: " + hash + "\n" +
		"    email: a@b.c\n"
	path := writeRegistryFile(t, yaml)

	// when
	_, err := auth.LoadAdminTokensRegistry(path)

	// then
	if err == nil {
		t.Fatalf("expected error for empty operator_id")
	}
}
