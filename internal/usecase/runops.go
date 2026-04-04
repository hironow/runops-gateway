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

func targetFrom(req domain.ApprovalRequest) port.NotifyTarget {
	mode := port.ModeSlack
	if req.Source == "cli" {
		mode = port.ModeStdout
	}
	return port.NotifyTarget{ResponseURL: req.ResponseURL, Mode: mode}
}

// ApproveAction executes the approved operation described by req.
func (s *RunOpsService) ApproveAction(ctx context.Context, req domain.ApprovalRequest) error {
	target := targetFrom(req)

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
	target := targetFrom(req)
	if err := s.notifier.ReplaceMessage(ctx, target, fmt.Sprintf(":x: 操作が拒否されました。リソース: *%s*", req.ResourceNames)); err != nil {
		return fmt.Errorf("usecase: deny notification failed: %w", err)
	}
	return nil
}

// shiftFn is a function that shifts traffic/instances for a single resource.
type shiftFn func(ctx context.Context, project, location, name, target string, percent int32) error

// approveShift is the shared logic for approveService and approveWorkerPool.
// It parses the action, iterates over CSV resources, applies the shift function,
// handles compensating rollback on failure, and offers continuation buttons.
func (s *RunOpsService) approveShift(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget, shift shiftFn, progressMsg, summaryFmt string) error {
	if err := s.notifier.UpdateMessage(ctx, target, progressMsg); err != nil {
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
	done := make([]shifted, 0, len(names))

	for i, name := range names {
		rev := csvAt(targets, i)
		if err := shift(ctx, req.Project, req.Location, name, rev, percent); err != nil {
			rollbackMsg := s.compensateRollback(ctx, done, func(d shifted) error {
				return shift(ctx, d.project, d.location, d.name, d.target, 0)
			})
			s.offerRetry(ctx, target, req, fmt.Sprintf("❌ エラーが発生しました: %v\n%s", err, rollbackMsg))
			return err
		}
		done = append(done, shifted{req.Project, req.Location, name, rev})
	}

	summary := fmt.Sprintf(summaryFmt, req.ResourceNames, percent)
	var nextReq, stopReq *domain.ApprovalRequest
	if act.Name != "rollback" {
		nextPercent := domain.NextCanaryPercent(percent)
		if nextPercent > 0 {
			nextReq = cloneRequest(req, fmt.Sprintf("canary_%d", nextPercent))
			stopReq = cloneRequest(req, "rollback")
		}
	}
	if err := s.notifier.OfferContinuation(ctx, target, summary, nextReq, stopReq); err != nil {
		slog.Error("OfferContinuation failed", "err", err)
	}
	return nil
}

func (s *RunOpsService) approveService(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	return s.approveShift(ctx, req, target, s.gcp.ShiftTraffic,
		"⏳ トラフィック切り替え中...",
		"✅ トラフィック切り替え完了。サービス: *%s* → %d%%")
}

func (s *RunOpsService) approveWorkerPool(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	return s.approveShift(ctx, req, target, s.gcp.UpdateWorkerPool,
		"⏳ インスタンス割り当て切り替え中...",
		"✅ インスタンス割り当て切り替え完了。プール: *%s* → %d%%")
}

// approveJob handles DB backup and Cloud Run job execution.
func (s *RunOpsService) approveJob(ctx context.Context, req domain.ApprovalRequest, target port.NotifyTarget) error {
	if err := s.notifier.UpdateMessage(ctx, target, "📦 DBバックアップを取得中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.TriggerBackup(ctx, req.Project, req.ResourceNames); err != nil {
		s.offerRetry(ctx, target, req, fmt.Sprintf("❌ バックアップエラー: %v", err))
		return err
	}

	if err := s.notifier.UpdateMessage(ctx, target, "✅ バックアップ完了。マイグレーション実行中..."); err != nil {
		slog.Error("UpdateMessage failed", "err", err)
	}

	if err := s.gcp.ExecuteJob(ctx, req.Project, req.Location, req.ResourceNames, []string{"--mode=apply"}); err != nil {
		s.offerRetry(ctx, target, req, fmt.Sprintf("❌ マイグレーションエラー: %v", err))
		return err
	}

	summary := fmt.Sprintf("✅ マイグレーション完了。ジョブ: *%s*", req.ResourceNames)

	if req.NextServiceNames != "" {
		nextReq := cloneRequest(req, req.NextAction)
		nextReq.ResourceType = domain.ResourceTypeService
		nextReq.ResourceNames = req.NextServiceNames
		nextReq.Targets = req.NextRevisions
		nextReq.MigrationDone = true

		denyReq := cloneRequest(req, req.NextAction)
		denyReq.ResourceType = domain.ResourceTypeService
		denyReq.ResourceNames = req.NextServiceNames
		denyReq.Targets = req.NextRevisions

		if err := s.notifier.OfferContinuation(ctx, target, summary, nextReq, denyReq); err != nil {
			slog.Error("OfferContinuation failed", "err", err)
		}
		return nil
	}

	if err := s.notifier.ReplaceMessage(ctx, target, summary); err != nil {
		slog.Error("ReplaceMessage failed", "err", err)
	}
	return nil
}

// --- helpers ---

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

func csvAt(ss []string, i int) string {
	if i < len(ss) {
		return ss[i]
	}
	return ""
}

type shifted struct{ project, location, name, target string }

func (s *RunOpsService) compensateRollback(ctx context.Context, done []shifted, rollbackFn func(shifted) error) string {
	if len(done) == 0 {
		return "ロールバック不要（変更なし）"
	}
	var failed []string
	for _, d := range done {
		if err := rollbackFn(d); err != nil {
			slog.Error("compensating rollback failed", "resource", d.name, "err", err)
			failed = append(failed, d.name)
		}
	}
	if len(failed) > 0 {
		return fmt.Sprintf("⚠️ 一部ロールバック失敗（手動確認が必要）: %s", strings.Join(failed, ", "))
	}
	return "ロールバック済み"
}

// cloneRequest creates a copy of req with a new action and fresh IssuedAt.
func cloneRequest(req domain.ApprovalRequest, action string) *domain.ApprovalRequest {
	r := req
	r.Action = action
	r.IssuedAt = time.Now().Unix()
	return &r
}

func (s *RunOpsService) offerRetry(ctx context.Context, target port.NotifyTarget, req domain.ApprovalRequest, errMsg string) {
	retryReq := cloneRequest(req, req.Action)
	if err := s.notifier.OfferContinuation(ctx, target, errMsg, retryReq, nil); err != nil {
		slog.Error("OfferContinuation (retry) failed", "err", err)
	}
}
