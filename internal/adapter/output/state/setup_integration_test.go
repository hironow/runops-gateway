//go:build integration

package state_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	testutils "github.com/hironow/runops-gateway/tests/utils"
)

// TestMain starts a self-contained firebase emulator via testcontainers and
// injects FIRESTORE_EMULATOR_HOST / GOOGLE_CLOUD_PROJECT before running the
// Firestore integration tests. Per the 2026-05-31 absolute rule there is no
// t.Skip fallback: the tests depend ONLY on testcontainers, never on a
// locally-running emulator or external FIRESTORE_EMULATOR_HOST.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	em, err := testutils.RunFirebaseEmulator(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: start firebase emulator: %v\n", err)
		os.Exit(1)
	}

	os.Setenv("FIRESTORE_EMULATOR_HOST", em.FirestoreHost)
	os.Setenv("GOOGLE_CLOUD_PROJECT", testutils.FirebaseProjectID)

	// Gate on the 8080 listener being reachable (4400 hub HTTP 200 != 8080
	// listen ready). Fail loud rather than racing the listener per-test.
	if err := testutils.FirestoreReady(ctx, testutils.FirebaseProjectID); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: firestore not ready: %v\n", err)
		_ = em.Terminate(context.Background())
		os.Exit(1)
	}

	code := m.Run()

	if err := em.Terminate(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: terminate emulator: %v\n", err)
	}
	os.Exit(code)
}
