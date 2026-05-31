//go:build integration

package testutils

import (
	"context"
	"fmt"
	"time"

	gpubsub "cloud.google.com/go/pubsub/v2"
	pubsubpb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Topic / subscription names runops-gateway uses (mirror scripts/init-pubsub.sh).
const (
	TopicInbound     = "dmail-inbound"
	TopicInboundDLQ  = "dmail-inbound-dlq"
	TopicOutbound    = "dmail-outbound"
	TopicOutboundDLQ = "dmail-outbound-dlq"
	SubReceiver      = "dmail-receiver-sub" // -> dmail-inbound
	SubGateway       = "runops-gateway-sub" // -> dmail-outbound
)

func topicPath(project, name string) string {
	return fmt.Sprintf("projects/%s/topics/%s", project, name)
}

func subPath(project, name string) string {
	return fmt.Sprintf("projects/%s/subscriptions/%s", project, name)
}

// InitPubSub creates the topics + subscriptions runops-gateway uses,
// idempotently (AlreadyExists tolerated). This is the Go port of
// scripts/init-pubsub.sh: it runs inside TestMain so the integration tests
// never depend on an externally-initialized emulator.
func InitPubSub(ctx context.Context, projectID string) error {
	client, err := newReadyPubSubClient(ctx, projectID)
	if err != nil {
		return err
	}
	defer client.Close()

	for _, t := range []string{TopicInbound, TopicInboundDLQ, TopicOutbound, TopicOutboundDLQ} {
		if _, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
			Name: topicPath(projectID, t),
		}); err != nil && status.Code(err) != codes.AlreadyExists {
			return fmt.Errorf("testutils: create topic %s: %w", t, err)
		}
	}

	for _, s := range []struct{ name, topic string }{
		{SubReceiver, TopicInbound},
		{SubGateway, TopicOutbound},
	} {
		if _, err := client.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
			Name:               subPath(projectID, s.name),
			Topic:              topicPath(projectID, s.topic),
			AckDeadlineSeconds: 60,
		}); err != nil && status.Code(err) != codes.AlreadyExists {
			return fmt.Errorf("testutils: create subscription %s: %w", s.name, err)
		}
	}
	return nil
}

// newReadyPubSubClient opens a Pub/Sub client and waits for the emulator's
// 9399 listener to answer an admin RPC. The testcontainers wait only checks the
// 4400 hub (HTTP 200), which can precede the Pub/Sub listener being ready, so
// we probe with GetTopic: a NotFound reply means the listener is reachable (the
// topic simply does not exist yet). Up to 30 attempts at 2s = 60s, matching
// init-pubsub.sh's probe budget.
func newReadyPubSubClient(ctx context.Context, projectID string) (*gpubsub.Client, error) {
	var lastErr error
	for range 30 {
		client, err := gpubsub.NewClient(ctx, projectID)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}
		_, perr := client.TopicAdminClient.GetTopic(ctx, &pubsubpb.GetTopicRequest{
			Topic: topicPath(projectID, TopicInbound),
		})
		if perr == nil || status.Code(perr) == codes.NotFound {
			return client, nil // listener reachable
		}
		_ = client.Close()
		lastErr = perr
		time.Sleep(2 * time.Second)
	}
	return nil, fmt.Errorf("testutils: pubsub emulator not ready after 60s: %w", lastErr)
}
