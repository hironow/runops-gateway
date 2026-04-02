// Package usecase implements the core application use cases for runops-gateway.
// It depends only on the port interfaces and domain types; no infrastructure
// packages are imported here.
package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

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

	blocks := completionBlock(fmt.Sprintf(":x: 操作が拒否されました。リソース: *%s*", req.ResourceNames))
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		return fmt.Errorf("usecase: deny notification failed: %w", err)
	}
	return nil
}

// approveService handles traffic shifting for Cloud Run services.
// ResourceNames/Targets may be comma-separated lists; all resources are shifted
// atomically with compensating rollback (to 0%) if any individual shift fails.
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
	if act.Name == "rollback" {
		percent = 0
	} else if percent == 0 {
		percent = 10
	}

	names := splitCSV(req.ResourceNames)
	targets := splitCSV(req.Targets)
	type shifted struct{ name, target string }
	done := make([]shifted, 0, len(names))

	for i, name := range names {
		rev := csvAt(targets, i)
		if err := s.gcp.ShiftTraffic(ctx, name, rev, percent); err != nil {
			// Compensating rollback: restore all already-shifted resources to 0%.
			for _, d := range done {
				if rerr := s.gcp.ShiftTraffic(ctx, d.name, d.target, 0); rerr != nil {
					slog.Error("compensating rollback failed", "resource", d.name, "err", rerr)
				}
			}
			if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("エラーが発生しました: %v ロールバック済み", err)); uerr != nil {
				slog.Error("UpdateMessage failed", "err", uerr)
			}
			return err
		}
		done = append(done, shifted{name, rev})
	}

	summary := fmt.Sprintf("✅ トラフィック切り替え完了。サービス: *%s* → %d%%", req.ResourceNames, percent)
	var nextReq *domain.ApprovalRequest
	var stopReq *domain.ApprovalRequest
	if act.Name != "rollback" {
		nextPercent := domain.NextCanaryPercent(percent)
		if nextPercent > 0 {
			nextReq = &domain.ApprovalRequest{
				ResourceType:  req.ResourceType,
				ResourceNames: req.ResourceNames,
				Targets:       req.Targets,
				Action:        fmt.Sprintf("canary_%d", nextPercent),
				Source:        req.Source,
				IssuedAt:      time.Now().Unix(),
				ResponseURL:   req.ResponseURL,
			}
			stopReq = &domain.ApprovalRequest{
				ResourceType:  req.ResourceType,
				ResourceNames: req.ResourceNames,
				Targets:       req.Targets,
				Action:        "rollback",
				Source:        req.Source,
				IssuedAt:      time.Now().Unix(),
				ResponseURL:   req.ResponseURL,
			}
		}
	}
	if err := s.notifier.OfferContinuation(ctx, target, summary, nextReq, stopReq); err != nil {
		slog.Error("OfferContinuation failed", "err", err)
	}
	return nil
}

// approveJob handles DB backup and Cloud Run job execution.
func (s *RunOpsService) approveJob(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	if err := s.notifier.UpdateMessage(ctx, target, "📦 DBバックアップを取得中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.TriggerBackup(ctx, req.ResourceNames); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("バックアップエラー: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	if err := s.notifier.UpdateMessage(ctx, target, "✅ バックアップ完了。マイグレーション実行中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.ExecuteJob(ctx, req.ResourceNames, []string{"--mode=apply"}); err != nil {
		if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("マイグレーションエラー: %v", err)); uerr != nil {
			slog.Error("UpdateMessage failed", "err", uerr)
		}
		return err
	}

	summary := fmt.Sprintf("✅ マイグレーション完了。ジョブ: *%s*", req.ResourceNames)

	// If next_* fields are set, offer the canary button with migration_done=true.
	if req.NextServiceNames != "" {
		nextReq := &domain.ApprovalRequest{
			ResourceType:  domain.ResourceTypeService,
			ResourceNames: req.NextServiceNames,
			Targets:       req.NextRevisions,
			Action:        req.NextAction,
			Source:        req.Source,
			IssuedAt:      time.Now().Unix(),
			ResponseURL:   req.ResponseURL,
			MigrationDone: true,
		}
		// Deny button for the canary step (migration_done=true, no confirm needed)
		denyReq := &domain.ApprovalRequest{
			ResourceType:  domain.ResourceTypeService,
			ResourceNames: req.NextServiceNames,
			Targets:       req.NextRevisions,
			Action:        req.NextAction,
			Source:        req.Source,
			IssuedAt:      time.Now().Unix(),
			ResponseURL:   req.ResponseURL,
		}
		if err := s.notifier.OfferContinuation(ctx, target, summary, nextReq, denyReq); err != nil {
			slog.Error("OfferContinuation failed", "err", err)
		}
		return nil
	}

	blocks := completionBlock(summary)
	if err := s.notifier.ReplaceMessage(ctx, target, blocks); err != nil {
		slog.Error("ReplaceMessage failed", "err", err)
	}
	return nil
}

// approveWorkerPool handles instance allocation shifting for worker pools.
// Applies the same all-or-nothing CSV iteration and compensating rollback as approveService.
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
	if act.Name == "rollback" {
		percent = 0
	} else if percent == 0 {
		percent = 10
	}

	names := splitCSV(req.ResourceNames)
	targets := splitCSV(req.Targets)
	type shifted struct{ name, target string }
	done := make([]shifted, 0, len(names))

	for i, name := range names {
		rev := csvAt(targets, i)
		if err := s.gcp.UpdateWorkerPool(ctx, name, rev, percent); err != nil {
			for _, d := range done {
				if rerr := s.gcp.UpdateWorkerPool(ctx, d.name, d.target, 0); rerr != nil {
					slog.Error("compensating rollback failed", "resource", d.name, "err", rerr)
				}
			}
			if uerr := s.notifier.UpdateMessage(ctx, target, fmt.Sprintf("エラーが発生しました: %v ロールバック済み", err)); uerr != nil {
				slog.Error("UpdateMessage failed", "err", uerr)
			}
			return err
		}
		done = append(done, shifted{name, rev})
	}

	summary := fmt.Sprintf("✅ インスタンス割り当て切り替え完了。プール: *%s* → %d%%", req.ResourceNames, percent)
	var nextReq *domain.ApprovalRequest
	var stopReq *domain.ApprovalRequest
	if act.Name != "rollback" {
		nextPercent := domain.NextCanaryPercent(percent)
		if nextPercent > 0 {
			nextReq = &domain.ApprovalRequest{
				ResourceType:  req.ResourceType,
				ResourceNames: req.ResourceNames,
				Targets:       req.Targets,
				Action:        fmt.Sprintf("canary_%d", nextPercent),
				Source:        req.Source,
				IssuedAt:      time.Now().Unix(),
				ResponseURL:   req.ResponseURL,
			}
			stopReq = &domain.ApprovalRequest{
				ResourceType:  req.ResourceType,
				ResourceNames: req.ResourceNames,
				Targets:       req.Targets,
				Action:        "rollback",
				Source:        req.Source,
				IssuedAt:      time.Now().Unix(),
				ResponseURL:   req.ResponseURL,
			}
		}
	}
	if err := s.notifier.OfferContinuation(ctx, target, summary, nextReq, stopReq); err != nil {
		slog.Error("OfferContinuation failed", "err", err)
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

// splitCSV splits a comma-separated string into trimmed, non-empty elements.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			result = append(result, t)
		}
	}
	return result
}

// csvAt returns the element at index i in a slice, or "" if out of bounds.
func csvAt(ss []string, i int) string {
	if i < len(ss) {
		return ss[i]
	}
	return ""
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
