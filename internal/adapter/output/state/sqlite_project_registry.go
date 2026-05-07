package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hironow/runops-gateway/internal/core/domain"
	"github.com/hironow/runops-gateway/internal/core/port"
)

// SQLiteProjectRegistry persists projects in the SQLite state DB.
//
// It is intended for dev / test / operator local Mac use only. Production
// (Cloud Run, multi-instance) must use the Firestore adapter shipped in
// issue #0011; see ADR 0026.
type SQLiteProjectRegistry struct {
	db *sql.DB
}

// NewSQLiteProjectRegistry wires a SQLite DB into the ProjectRegistry port.
func NewSQLiteProjectRegistry(db *sql.DB) *SQLiteProjectRegistry {
	return &SQLiteProjectRegistry{db: db}
}

// Add inserts a project. Returns ErrInvalidProjectID for malformed ids and
// ErrProjectAlreadyExists when the id collides with an existing row.
func (r *SQLiteProjectRegistry) Add(ctx context.Context, p domain.Project) error {
	if err := domain.ValidateProjectID(p.ID); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO projects (
			id, github_org, github_repo, workspace_path,
			slack_default_channel, github_app_installation_id
		) VALUES (?, ?, ?, ?, ?, ?)
	`, p.ID, p.GitHubOrg, p.GitHubRepo, p.WorkspacePath,
		p.SlackDefaultChannel, p.GitHubAppInstallationID)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return domain.ErrProjectAlreadyExists
		}
		return fmt.Errorf("insert project: %w", err)
	}
	return nil
}

// Get returns a single project by id, or ErrProjectNotFound.
func (r *SQLiteProjectRegistry) Get(ctx context.Context, id string) (domain.Project, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, github_org, github_repo, workspace_path,
			slack_default_channel, github_app_installation_id,
			status, created_at, archived_at
		FROM projects WHERE id = ?
	`, id)
	p, err := scanProject(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Project{}, domain.ErrProjectNotFound
	}
	if err != nil {
		return domain.Project{}, fmt.Errorf("scan project: %w", err)
	}
	return p, nil
}

// List returns all projects, optionally filtered by status.
func (r *SQLiteProjectRegistry) List(ctx context.Context, filter port.ProjectListFilter) ([]domain.Project, error) {
	var (
		rows *sql.Rows
		err  error
	)
	const baseSelect = `
		SELECT id, github_org, github_repo, workspace_path,
			slack_default_channel, github_app_installation_id,
			status, created_at, archived_at
		FROM projects`
	if filter.Status == "" {
		rows, err = r.db.QueryContext(ctx, baseSelect+" ORDER BY id")
	} else {
		rows, err = r.db.QueryContext(ctx, baseSelect+" WHERE status = ? ORDER BY id", string(filter.Status))
	}
	if err != nil {
		return nil, fmt.Errorf("query projects: %w", err)
	}
	defer rows.Close()

	var out []domain.Project
	for rows.Next() {
		p, err := scanProject(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}
	return out, nil
}

// Archive marks a project as archived. Idempotent: archiving an
// already-archived project succeeds without error.
func (r *SQLiteProjectRegistry) Archive(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `
		UPDATE projects
		SET status = ?, archived_at = COALESCE(archived_at, datetime('now'))
		WHERE id = ?
	`, string(domain.ProjectStatusArchived), id)
	if err != nil {
		return fmt.Errorf("archive project: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return domain.ErrProjectNotFound
	}
	return nil
}

// scanProject is shared by Get (sql.Row.Scan) and List (sql.Rows.Scan).
func scanProject(scan func(...any) error) (domain.Project, error) {
	var (
		p           domain.Project
		statusStr   string
		createdStr  string
		archivedStr sql.NullString
	)
	if err := scan(
		&p.ID, &p.GitHubOrg, &p.GitHubRepo, &p.WorkspacePath,
		&p.SlackDefaultChannel, &p.GitHubAppInstallationID,
		&statusStr, &createdStr, &archivedStr,
	); err != nil {
		return domain.Project{}, err
	}
	p.Status = domain.ProjectStatus(statusStr)
	created, err := parseSQLiteDatetime(createdStr)
	if err != nil {
		return domain.Project{}, fmt.Errorf("parse created_at: %w", err)
	}
	p.CreatedAt = created
	if archivedStr.Valid {
		archived, err := parseSQLiteDatetime(archivedStr.String)
		if err != nil {
			return domain.Project{}, fmt.Errorf("parse archived_at: %w", err)
		}
		p.ArchivedAt = &archived
	}
	return p, nil
}

// parseSQLiteDatetime parses the format produced by SQLite's
// datetime('now'): "YYYY-MM-DD HH:MM:SS" in UTC.
func parseSQLiteDatetime(s string) (time.Time, error) {
	return time.Parse("2006-01-02 15:04:05", s)
}

// isUniqueConstraintViolation matches modernc.org/sqlite's PRIMARY KEY
// collision message ("constraint failed: UNIQUE constraint failed: ...").
func isUniqueConstraintViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
