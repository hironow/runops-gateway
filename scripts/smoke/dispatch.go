package main

// smoke-test publisher: publishes one DMail through the production
// PubsubDispatcher path against the local Firebase emulator.
//
// Run:
//   PUBSUB_EMULATOR_HOST=localhost:9399 \
//   PUBSUB_PROJECT_ID=runops-local \
//   go run /tmp/runops-smoke-pub.go --target paintress --text "hello from smoke test"

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/dispatcher"
	pubsubadapter "github.com/hironow/runops-gateway/internal/adapter/output/pubsub"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

func main() {
	target := flag.String("target", "paintress", "target agent role")
	text := flag.String("text", "smoke-test body", "dispatch text")
	flag.Parse()

	ctx := context.Background()
	pub, err := pubsubadapter.NewPublisher(ctx, "runops-local", "dmail-inbound")
	if err != nil {
		slog.Error("create publisher", "err", err)
		os.Exit(1)
	}
	defer pub.Close()

	d := dispatcher.NewPubsubDispatcher(pub)
	stamp := time.Now().Format("150405")
	req := domain.DispatchRequest{
		Role:           domain.AgentRole(*target),
		Text:           *text,
		RequesterID:    "U_SMOKE",
		IdempotencyKey: "smoke-" + stamp,
		IssuedAt:       time.Now().Unix(),
	}
	if err := d.Dispatch(ctx, req); err != nil {
		slog.Error("dispatch", "err", err)
		os.Exit(1)
	}
	fmt.Printf("published: target=%s text=%q idempotency_key=%s\n", *target, *text, req.IdempotencyKey)
}
