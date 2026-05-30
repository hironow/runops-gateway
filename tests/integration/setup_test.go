//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	testutils "github.com/hironow/runops-gateway/tests/utils"
)

// TestMain starts a self-contained firebase emulator via testcontainers,
// injects the dynamic host:port into PUBSUB_EMULATOR_HOST / FIRESTORE_EMULATOR_HOST,
// initializes the Pub/Sub topology, runs the tests, and terminates the container.
//
// Per the 2026-05-31 absolute rule, the integration tests depend ONLY on
// testcontainers and never on a locally-running emulator, external
// PUBSUB_EMULATOR_HOST, or docker-compose. There is no t.Skip fallback: if the
// Docker daemon is unavailable the container start fails loudly here.
func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	em, err := testutils.RunFirebaseEmulator(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: start firebase emulator: %v\n", err)
		os.Exit(1)
	}

	os.Setenv("PUBSUB_EMULATOR_HOST", em.PubSubHost)
	os.Setenv("FIRESTORE_EMULATOR_HOST", em.FirestoreHost)

	if err := testutils.InitPubSub(ctx, testutils.FirebaseProjectID); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: init pubsub: %v\n", err)
		_ = em.Terminate(context.Background())
		os.Exit(1)
	}

	code := m.Run()

	if err := em.Terminate(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: terminate emulator: %v\n", err)
	}
	os.Exit(code)
}
