package admin_approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// Orchestrator coordinates the admin-mutation approval flow:
// pickup → validate → apply → transition. It depends on the same
// PendingStore + ProjectRegistry ports that the §B-5.2 mutation methods
// produced; no adapter imports.
//
// Concurrency: Orchestrator is goroutine-safe as long as the underlying
// store + registry are. The 4-eyes + apply + transition steps are NOT
// wrapped in a single transaction (= different backends), so a stuck
// caller could observe pending_approval briefly before approved_applied
// becomes visible. Idempotency is provided by IdempotencyKey + terminal
// state guard (= duplicate clicks see ErrAlreadyTerminal).
type Orchestrator struct {
	store    port.PendingStore
	registry port.ProjectRegistry
	// now is injectable so tests can pin appliedAt; production uses time.Now.
	now func() time.Time
}

// NewOrchestrator wires the orchestrator with its required ports.
// Panics on nil dependencies — those are programmer errors that should
// surface at startup, not under load.
func NewOrchestrator(store port.PendingStore, registry port.ProjectRegistry) *Orchestrator {
	if store == nil || registry == nil {
		panic("admin_approval.NewOrchestrator: store and registry must not be nil")
	}
	return &Orchestrator{
		store:    store,
		registry: registry,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// OnApprovalAck handles the "approve" click on an admin pending. Steps:
//
//  1. Get the pending by idempotency key.
//  2. If already terminal → return ErrAlreadyTerminal (idempotent for
//     duplicate Slack clicks).
//  3. Validate ADR 0035 4-eyes: approver != requester + approver is
//     human-operator.
//  4. Fail closed if EffectiveRequesterID is empty (= legacy migration).
//  5. Decode BodyJSON and apply the op (Add / Archive) to ProjectRegistry.
//  6. Transition pending to approved_applied with applied_at = now().
//
// Errors from validation halt the flow BEFORE touching the registry.
// Errors from registry.Add / Archive leave the pending in
// pending_approval so the operator can retry.
func (o *Orchestrator) OnApprovalAck(
	ctx context.Context,
	idempotencyKey, approverID string,
	approverType domain.CallerType,
) error {
	pa, err := o.fetchActive(ctx, idempotencyKey)
	if err != nil {
		return err
	}

	if err := validate4Eyes(pa, approverID, approverType); err != nil {
		slog.WarnContext(ctx, "admin_approval: validation failed",
			"idempotency_key", idempotencyKey, "error", err.Error())
		return err
	}

	if err := o.apply(ctx, pa); err != nil {
		// pending stays in pending_approval; operator may retry.
		return fmt.Errorf("admin_approval: apply %s: %w", pa.Op, err)
	}

	now := o.now()
	if err := o.store.Transition(ctx, idempotencyKey, domain.PendingStatusApprovedApplied, &now); err != nil {
		// The mutation was applied but the transition failed; this is
		// a partial-success window. Caller should alert; the next
		// approval-ack retry will see ErrAlreadyTerminal because the
		// registry already has the project.
		return fmt.Errorf("admin_approval: transition: %w", err)
	}
	slog.InfoContext(ctx, "admin_approval: approved + applied",
		"idempotency_key", idempotencyKey,
		"op", string(pa.Op),
		"requester", pa.EffectiveRequesterID,
		"approver", approverID)
	return nil
}

// OnApprovalDeny transitions the pending to `denied` without touching
// the registry. Validation rules:
//   - Pending must exist and be non-terminal.
//   - Approver actor type must be human-operator (= AI agent cannot deny
//     admin mutations, mirroring the approve gate).
//
// Self-approval is allowed (= a requester may rescind their own pending).
func (o *Orchestrator) OnApprovalDeny(
	ctx context.Context,
	idempotencyKey, approverID string,
	approverType domain.CallerType,
) error {
	pa, err := o.fetchActive(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if approverType != domain.CallerHumanOperator {
		return ErrApproverActorNotHuman
	}
	if err := o.store.Transition(ctx, idempotencyKey, domain.PendingStatusDenied, nil); err != nil {
		return fmt.Errorf("admin_approval: transition deny: %w", err)
	}
	slog.InfoContext(ctx, "admin_approval: denied",
		"idempotency_key", idempotencyKey,
		"approver", approverID,
		"requester", pa.EffectiveRequesterID,
		"op", string(pa.Op))
	return nil
}

// OnApprovalTimeout transitions the pending to `timeout` (= 15 min
// expiry per ADR 0039 default). Called by the background timeout
// scheduler (= future work, §B-5.5 production-decision); for now this
// surface is exposed for tests and manual triage.
func (o *Orchestrator) OnApprovalTimeout(ctx context.Context, idempotencyKey string) error {
	pa, err := o.fetchActive(ctx, idempotencyKey)
	if err != nil {
		return err
	}
	if err := o.store.Transition(ctx, idempotencyKey, domain.PendingStatusTimeout, nil); err != nil {
		return fmt.Errorf("admin_approval: transition timeout: %w", err)
	}
	slog.InfoContext(ctx, "admin_approval: timed out",
		"idempotency_key", idempotencyKey,
		"requester", pa.EffectiveRequesterID,
		"op", string(pa.Op))
	return nil
}

// fetchActive returns the pending record, mapping ErrPendingNotFound to
// the package-level ErrPendingMissing and treating terminal records as
// ErrAlreadyTerminal.
func (o *Orchestrator) fetchActive(ctx context.Context, key string) (domain.PendingApproval, error) {
	pa, err := o.store.Get(ctx, key)
	if err != nil {
		if errors.Is(err, port.ErrPendingNotFound) {
			return domain.PendingApproval{}, ErrPendingMissing
		}
		return domain.PendingApproval{}, fmt.Errorf("admin_approval: fetch pending: %w", err)
	}
	if pa.Status.IsTerminal() {
		return pa, ErrAlreadyTerminal
	}
	return pa, nil
}

// validate4Eyes enforces ADR 0035: approver must be human + must differ
// from the requester. Returns nil on success.
func validate4Eyes(pa domain.PendingApproval, approverID string, approverType domain.CallerType) error {
	if pa.EffectiveRequesterID == "" {
		return ErrUnresolvedRequester
	}
	if approverType != domain.CallerHumanOperator {
		return ErrApproverActorNotHuman
	}
	if pa.EffectiveRequesterID == approverID {
		return ErrSelfApproval
	}
	return nil
}

// apply dispatches the pending op to the registry. BodyJSON is decoded
// to the expected param shape per Op type. Domain validation (= id
// rules, etc.) happens inside the registry adapter; this layer trusts
// the body because §B-5.2 mutation methods already validated it before
// creating the pending.
func (o *Orchestrator) apply(ctx context.Context, pa domain.PendingApproval) error {
	switch pa.Op {
	case domain.PendingOpAdd:
		return o.applyAdd(ctx, pa)
	case domain.PendingOpArchive:
		return o.applyArchive(ctx, pa)
	default:
		return fmt.Errorf("unknown pending op: %q", pa.Op)
	}
}

// addBody mirrors methods.projectAddParams (= the wire shape of
// project.add). Duplicated here to keep admin_approval free of usecase/rpc
// imports, preserving the layer boundary.
type addBody struct {
	ID                      string `json:"id"`
	GitHubOrg               string `json:"github_org"`
	GitHubRepo              string `json:"github_repo"`
	WorkspacePath           string `json:"workspace_path"`
	SlackDefaultChannel     string `json:"slack_default_channel,omitempty"`
	GitHubAppInstallationID int64  `json:"github_app_installation_id,omitempty"`
}

func (o *Orchestrator) applyAdd(ctx context.Context, pa domain.PendingApproval) error {
	var b addBody
	if err := json.Unmarshal(pa.BodyJSON, &b); err != nil {
		return fmt.Errorf("decode add body: %w", err)
	}
	proj := domain.Project{
		ID:                      b.ID,
		GitHubOrg:               b.GitHubOrg,
		GitHubRepo:              b.GitHubRepo,
		WorkspacePath:           b.WorkspacePath,
		SlackDefaultChannel:     b.SlackDefaultChannel,
		GitHubAppInstallationID: b.GitHubAppInstallationID,
		Status:                  domain.ProjectStatusActive,
		CreatedAt:               o.now(),
	}
	return o.registry.Add(ctx, proj)
}

type archiveBody struct {
	ID string `json:"id"`
}

func (o *Orchestrator) applyArchive(ctx context.Context, pa domain.PendingApproval) error {
	var b archiveBody
	if err := json.Unmarshal(pa.BodyJSON, &b); err != nil {
		return fmt.Errorf("decode archive body: %w", err)
	}
	return o.registry.Archive(ctx, b.ID)
}
