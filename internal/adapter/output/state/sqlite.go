package state

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// migrationFiles bundles the .sql files applied at startup. Filenames are
// sorted lexicographically and used as the id stored in _migrations.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// OpenSQLite opens (and migrates) the SQLite state database at dbPath.
//
// PRAGMAs follow the canonical tap 5 ツール pattern (S0037 substrate lock):
// busy_timeout=5s, journal_mode=WAL, synchronous=NORMAL, foreign_keys=ON.
//
// The function is idempotent: re-opening an already-migrated DB is a no-op
// for the migration runner because applied ids are recorded in _migrations.
func OpenSQLite(ctx context.Context, dbPath string) (*sql.DB, error) {
	dsn := buildSQLiteDSN(dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := applyMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	return db, nil
}

func buildSQLiteDSN(dbPath string) string {
	q := url.Values{}
	for _, pragma := range []string{
		"busy_timeout(5000)",
		"journal_mode(WAL)",
		"synchronous(NORMAL)",
		"foreign_keys(ON)",
	} {
		q.Add("_pragma", pragma)
	}
	return fmt.Sprintf("file:%s?%s", dbPath, q.Encode())
}

func applyMigrations(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}

	for _, name := range names {
		if applied[name] {
			continue
		}
		if err := applySingleMigration(ctx, db, name); err != nil {
			return err
		}
	}
	return nil
}

func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _migrations (
			id         TEXT PRIMARY KEY NOT NULL,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("ensure _migrations table: %w", err)
	}
	return nil
}

func loadAppliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM _migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan migration id: %w", err)
		}
		applied[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migrations: %w", err)
	}
	return applied, nil
}

func applySingleMigration(ctx context.Context, db *sql.DB, name string) error {
	body, err := migrationFiles.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("read migration %s: %w", name, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", name, err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback is best-effort on commit success
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("exec %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO _migrations(id) VALUES (?)`, name); err != nil {
		return fmt.Errorf("record %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", name, err)
	}
	return nil
}
