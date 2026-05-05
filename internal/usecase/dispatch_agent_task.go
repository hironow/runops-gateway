package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// DispatchService implements the dispatch_agent_task use case.
//
// Phase 1 keeps the dispatch path symmetric with RunOpsService: TryLock + auth +
// notify, then call the Dispatcher port (StubDispatcher in Phase 1, swapped to
// PubsubDispatcher in Phase 2). The lock is released after dispatch so a
// failing dispatch can be retried by the operator without admin reset.
type DispatchService struct {
	dispatcher port.Dispatcher
	notifier   port.Notifier
	auth       port.AuthChecker
	store      port.StateStore
}

// NewDispatchService wires the dispatch use case with the required ports.
func NewDispatchService(d port.Dispatcher, n port.Notifier, a port.AuthChecker, s port.StateStore) *DispatchService {
	return &DispatchService{dispatcher: d, notifier: n, auth: a, store: s}
}

// DispatchAgentTask executes the Slack /agent (or CLI runops dispatch) flow:
//  1. dedup via StateStore (best-effort)
//  2. authorize requester
//  3. hand off to Dispatcher
//  4. notify the requester via the supplied NotifyTarget
//
// The dispatch itself is fire-and-forget from the agent's perspective; the
// notifier reply is just an acknowledgement that the request was accepted.
func (s *DispatchService) DispatchAgentTask(ctx context.Context, req domain.DispatchRequest, target port.NotifyTarget) error {
	key := req.OperationKey()
	if !s.store.TryLock(key) {
		_ = s.notifier.SendEphemeral(ctx, target, req.RequesterID, "⚠️ この dispatch は既に処理中です。")
		return fmt.Errorf("usecase: dispatch already in progress: %s", key)
	}
	defer s.store.Release(key)

	if !s.auth.IsAuthorized(req.RequesterID) {
		if err := s.notifier.SendEphemeral(ctx, target, req.RequesterID,
			"権限がありません。dispatch は許可されたユーザーのみ実行できます。"); err != nil {
			slog.Error("SendEphemeral failed", "err", err)
		}
		return fmt.Errorf("usecase: unauthorized requester: %s", req.RequesterID)
	}

	if err := s.dispatcher.Dispatch(ctx, req); err != nil {
		if nerr := s.notifier.UpdateMessage(ctx, target,
			fmt.Sprintf("❌ dispatch 失敗: %v", err)); nerr != nil {
			slog.Error("UpdateMessage failed", "err", nerr)
		}
		return fmt.Errorf("usecase: dispatcher returned error: %w", err)
	}

	ack := fmt.Sprintf(":eyes: %s に dispatch を受け付けました (id=%s)", req.Role, req.IdempotencyKey)
	if err := s.notifier.UpdateMessage(ctx, target, ack); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}
	return nil
}
