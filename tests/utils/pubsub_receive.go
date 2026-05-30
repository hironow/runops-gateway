//go:build integration

package testutils

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"
)

// ReceiveOne pulls a single message from subID or returns an error if nothing
// arrives within deadline. Acks the message so subsequent tests start clean.
// Moved here from the per-file integration test helpers so every test shares
// one implementation.
func ReceiveOne(ctx context.Context, t *testing.T, client *gpubsub.Client, subID string, deadline time.Duration) (*gpubsub.Message, error) {
	t.Helper()
	sub := client.Subscriber(subID)
	pullCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	var (
		mu  sync.Mutex
		got *gpubsub.Message
	)
	err := sub.Receive(pullCtx, func(_ context.Context, m *gpubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		if got == nil {
			got = m
			m.Ack()
			cancel() // stop after the first message
			return
		}
		m.Nack()
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	if got == nil {
		return nil, errors.New("no message received within deadline")
	}
	return got, nil
}
