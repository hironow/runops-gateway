//go:build integration

package testutils

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreReady blocks until the emulator's 8080 Firestore listener answers a
// real RPC. testcontainers' wait only checks the 4400 hub (HTTP 200), which can
// precede the 8080 listener binding (see RunFirebaseEmulator's note). Because
// firestore.NewClient is lazy (it does not dial on construction), the only way
// to prove the listener is reachable is to issue a real Get: a NotFound reply
// means reachable (the probe doc simply does not exist), while
// Unavailable/connection-refused means "not ready yet, retry". Up to 30
// attempts at 2s = 60s, mirroring newReadyPubSubClient's budget.
func FirestoreReady(ctx context.Context, projectID string) error {
	var lastErr error
	for range 30 {
		client, err := firestore.NewClient(ctx, projectID)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		_, gerr := client.Collection("_readiness").Doc("probe").Get(ctx)
		_ = client.Close()
		if gerr == nil || status.Code(gerr) == codes.NotFound {
			return nil // 8080 listener reachable
		}
		lastErr = gerr
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("testutils: firestore emulator not ready after 60s: %w", lastErr)
}
