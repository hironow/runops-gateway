// Package usecase implements the core application use cases for runops-gateway.
// It depends only on the port interfaces and domain types; no infrastructure
// packages are imported here.
package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// RunOpsService implements port.RunOpsUseCase.
type RunOpsService struct {
	gcp      port.GCPController
	notifier port.Notifier
	auth     port.AuthChecker
	store    port.StateStore
}

// NewRunOpsService constructs a RunOpsService with the required secondary ports.
func NewRunOpsService(gcp port.GCPController, notifier port.Notifier, auth port.AuthChecker, store port.StateStore) *RunOpsService {
	return &RunOpsService{gcp: gcp, notifier: notifier, auth: auth, store: store}
}

// ApproveAction executes the approved operation described by req.
func (s *RunOpsService) ApproveAction(ctx context.Context, req domain.ApprovalRequest) error {
	target := port.NotifyTarget{
		ResponseURL: req.ResponseURL,
		Mode:        modeFrom(req.Source),
	}

	key := port.OperationKey(req)
	if !s.store.TryLock(key) {
		_ = s.notifier.SendEphemeral(ctx, target, req.ApproverID, "⚠️ この操作は既に実行中です。")
		return fmt.Errorf("usecase: operation already in progress: %s", key)
	}
	defer s.store.Release(key)

	if !s.auth.IsAuthorized(req.ApproverID) {
		if err := s.notifier.SendEphemeral(ctx, target, req.ApproverID, "権限がありません。承認操作は許可されたユーザーのみ実行できます。"); err != nil {
			slog.Error("SendEphemeral failed", "err", err)
		}
		return fmt.Errorf("usecase: unauthorized user: %s", req.ApproverID)
	}

	if s.auth.IsExpired(req.IssuedAt) {
		if err := s.notifier.SendEphemeral(ctx, target, req.ApproverID, "このリクエストは期限切れです。再度操作を実行してください。"); err != nil {
			slog.Error("SendEphemeral failed", "err", err)
		}
		return fmt.Errorf("usecase: request expired (issued_at=%d)", req.IssuedAt)
	}

	switch req.ResourceType {
	case domain.ResourceTypeService:
		return s.approveService(ctx, req, target)
	case domain.ResourceTypeJob:
		return s.approveJob(ctx, req, target)
	case domain.ResourceTypeWorkerPool:
		return s.approveWorkerPool(ctx, req, target)
	default:
		return fmt.Errorf("unsupported resource type: %s", req.ResourceType)
	}
}

// DenyAction notifies the relevant parties that the operation was denied.
func (s *RunOpsService) DenyAction(ctx context.Context, req domain.ApprovalRequest) error {
	target := port.NotifyTarget{
		ResponseURL: req.ResponseURL,
		Mode:        modeFrom(req.Source),
	}

	blocks := completionBlock(fmt.Sprintf(":x: 操作が拒否されました。リソース: *%s*", req.ResourceName))
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		return fmt.Errorf("usecase: deny notification failed: %w", err)
	}
	return nil
}

// approveService handles traffic shifting for Cloud Run services.
func (s *RunOpsService) approveService(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	if err := s.notifier.UpdateMessage(ctx, target, "⏳ トラフィック切り替え中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	act, err := domain.ParseAction(req.Action)
	if err != nil {
		_ = s.notifier.UpdateMessage(ctx, target, "❌ 無効なアクション: "+req.Action)
		return err
	}
	percent := act.Percent
	if percent == 0 {
		percent = 10 // default for canary
	}
	if err := s.gcp.ShiftTraffic(ctx, req.ResourceName, req.Target, percent); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("エラーが発生しました: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	blocks := completionBlock(fmt.Sprintf("✅ トラフィック切り替え完了。サービス: *%s* → %d%%", req.ResourceName, percent))
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		slog.Error("ReplaceMessage failed", "err", err)
	}
	return nil
}

// approveJob handles DB backup and Cloud Run job execution.
func (s *RunOpsService) approveJob(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	if err := s.notifier.UpdateMessage(ctx, target, "📦 DBバックアップを取得中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.TriggerBackup(ctx, req.ResourceName); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("バックアップエラー: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	if err := s.notifier.UpdateMessage(ctx, target, "✅ バックアップ完了。マイグレーション実行中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.ExecuteJob(ctx, req.ResourceName, []string{"--mode=apply"}); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("マイグレーションエラー: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	blocks := completionBlock(fmt.Sprintf("✅ マイグレーション完了。ジョブ: *%s*", req.ResourceName))
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		slog.Error("ReplaceMessage failed", "err", err)
	}
	return nil
}

// approveWorkerPool handles instance allocation shifting for worker pools.
func (s *RunOpsService) approveWorkerPool(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	if err := s.notifier.UpdateMessage(ctx, target, "⏳ インスタンス割り当て切り替え中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	act, err := domain.ParseAction(req.Action)
	if err != nil {
		_ = s.notifier.UpdateMessage(ctx, target, "❌ 無効なアクション: "+req.Action)
		return err
	}
	percent := act.Percent
	if percent == 0 {
		percent = 10 // default for canary
	}
	if err := s.gcp.UpdateWorkerPool(ctx, req.ResourceName, req.Target, percent); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("エラーが発生しました: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	blocks := completionBlock(fmt.Sprintf("✅ インスタンス割り当て切り替え完了。プール: *%s* → %d%%", req.ResourceName, percent))
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		slog.Error("ReplaceMessage failed", "err", err)
	}
	return nil
}

// modeFrom converts a source identifier to a notification mode string.
func modeFrom(source string) string {
	if source == "cli" {
		return "stdout"
	}
	return "slack"
}

// completionBlock builds a Slack block payload for operation completion messages.
func completionBlock(summary string) map[string]any {
	return map[string]any{
		"replace_original": true,
		"blocks": []map[string]any{
			{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": summary}},
		},
	}
}
