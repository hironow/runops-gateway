package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// SQLitePendingStore persists PendingApproval records in the SQLite state DB.
//
// It is intended for dev / test / operator local Mac use only. Production
// (Cloud Run, multi-instance) must use FirestorePendingStore (= ADR 0026).
//
// Multi-step state transitions (Transition) execute inside a SQL
// transaction so the terminal-state check and the UPDATE commit
// atomically against concurrent approval-ack handling.
type SQLitePendingStore struct {
	db *sql.DB
}

// NewSQLitePendingStore wires a SQLite DB into the PendingStore port.
func NewSQLitePendingStore(db *sql.DB) *SQLitePendingStore {
	return &SQLitePendingStore{db: db}
}

// CreateIfNotExists inserts a new PendingApproval. On UNIQUE constraint
// violation we read the existing record back so callers can present prior
// state to the operator (= idempotent retry semantics).
func (s *SQLitePendingStore) CreateIfNotExists(
	ctx context.Context,
	p domain.PendingApproval,
) (domain.PendingApproval, error) {
	if p.IdempotencyKey == "" {
		return domain.PendingApproval{}, errors.New("idempotency key required")
	}
	if p.Status == "" {
		p.Status = domain.PendingStatusPendingApproval
	}
	createdAt := p.CreatedAt
	if createdAt.IsZero() {
		createdAt = nowUTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pending_approvals (
			idempotency_key, op, body_json, requester_actor_type,
			created_at, status
		) VALUES (?, ?, ?, ?, ?, ?)
	`, p.IdempotencyKey, string(p.Op), p.BodyJSON, p.RequesterActorType,
		formatSQLiteDatetime(createdAt), string(p.Status))
	if err != nil {
		if isUniqueConstraintViolation(err) {
			existing, getErr := s.Get(ctx, p.IdempotencyKey)
			if getErr != nil {
				return domain.PendingApproval{},
					fmt.Errorf("sqlite create-if-not-exists fetch existing: %w", getErr)
			}
			return existing, port.ErrPendingAlreadyExists
		}
		return domain.PendingApproval{}, fmt.Errorf("insert pending approval: %w", err)
	}
	p.CreatedAt = createdAt
	return p, nil
}

// Get fetches a PendingApproval by IdempotencyKey. Missing rows return
// ErrPendingNotFound.
func (s *SQLitePendingStore) Get(
	ctx context.Context,
	idempotencyKey string,
) (domain.PendingApproval, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT idempotency_key, op, body_json, requester_actor_type,
			created_at, status, applied_at
		FROM pending_approvals WHERE idempotency_key = ?
	`, idempotencyKey)
	p, err := scanPendingApproval(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.PendingApproval{}, port.ErrPendingNotFound
	}
	if err != nil {
		return domain.PendingApproval{}, fmt.Errorf("scan pending approval: %w", err)
	}
	return p, nil
}

// Transition updates status (and AppliedAt for approved_applied) inside a
// transaction so the terminal-state check and UPDATE commit atomically.
// Validation rules mirror the Firestore adapter (ADR 0039 §lifecycle).
func (s *SQLitePendingStore) Transition(
	ctx context.Context,
	idempotencyKey string,
	newStatus domain.PendingStatus,
	appliedAt *time.Time,
) error {
	if newStatus == domain.PendingStatusApprovedApplied && appliedAt == nil {
		return errors.New("approved_applied transition requires non-nil appliedAt")
	}
	if newStatus != domain.PendingStatusApprovedApplied && appliedAt != nil {
		return errors.New("non-approved transition must not pass appliedAt")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var existingStatus string
	row := tx.QueryRowContext(ctx, `
		SELECT status FROM pending_approvals WHERE idempotency_key = ?
	`, idempotencyKey)
	if err := row.Scan(&existingStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return port.ErrPendingNotFound
		}
		return fmt.Errorf("tx select status: %w", err)
	}
	if domain.PendingStatus(existingStatus).IsTerminal() {
		return port.ErrPendingTerminalTransition
	}

	if appliedAt != nil {
		_, err = tx.ExecContext(ctx, `
			UPDATE pending_approvals
			SET status = ?, applied_at = ?
			WHERE idempotency_key = ?
		`, string(newStatus), formatSQLiteDatetime(*appliedAt), idempotencyKey)
	} else {
		_, err = tx.ExecContext(ctx, `
			UPDATE pending_approvals SET status = ? WHERE idempotency_key = ?
		`, string(newStatus), idempotencyKey)
	}
	if err != nil {
		return fmt.Errorf("tx update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("tx commit: %w", err)
	}
	return nil
}

// scanPendingApproval is shared by Get (sql.Row.Scan).
func scanPendingApproval(scan func(...any) error) (domain.PendingApproval, error) {
	var (
		p          domain.PendingApproval
		opStr      string
		createdStr string
		statusStr  string
		appliedStr sql.NullString
	)
	if err := scan(
		&p.IdempotencyKey, &opStr, &p.BodyJSON, &p.RequesterActorType,
		&createdStr, &statusStr, &appliedStr,
	); err != nil {
		return domain.PendingApproval{}, err
	}
	p.Op = domain.PendingOp(opStr)
	p.Status = domain.PendingStatus(statusStr)
	created, err := parseSQLiteDatetime(createdStr)
	if err != nil {
		return domain.PendingApproval{}, fmt.Errorf("parse created_at: %w", err)
	}
	p.CreatedAt = created
	if appliedStr.Valid {
		applied, err := parseSQLiteDatetime(appliedStr.String)
		if err != nil {
			return domain.PendingApproval{}, fmt.Errorf("parse applied_at: %w", err)
		}
		p.AppliedAt = &applied
	}
	return p, nil
}

// formatSQLiteDatetime returns the canonical "YYYY-MM-DD HH:MM:SS" format
// used by parseSQLiteDatetime; explicit formatting avoids fractional-
// second drift that some sqlite drivers introduce.
func formatSQLiteDatetime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}
