package state_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hironow/runops-gateway/internal/adapter/output/state"
)

func TestOpenSQLite_AppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	db, err := state.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite first call: %v", err)
	}
	defer db.Close()

	// _migrations table populated; projects table exists
	row := db.QueryRow(`SELECT COUNT(*) FROM _migrations`)
	var count int
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan _migrations count: %v", err)
	}
	if count == 0 {
		t.Errorf("_migrations should have at least 1 entry, got 0")
	}

	row = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='projects'`)
	var name string
	if err := row.Scan(&name); err != nil {
		t.Errorf("projects table missing: %v", err)
	}
}

func TestOpenSQLite_IsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	// First open: applies migrations.
	db1, err := state.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	row := db1.QueryRow(`SELECT COUNT(*) FROM _migrations`)
	var firstCount int
	if err := row.Scan(&firstCount); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	db1.Close()

	// Second open: must NOT re-apply (count stable).
	db2, err := state.OpenSQLite(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer db2.Close()
	row = db2.QueryRow(`SELECT COUNT(*) FROM _migrations`)
	var secondCount int
	if err := row.Scan(&secondCount); err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if firstCount != secondCount {
		t.Errorf("migrations re-applied: first=%d second=%d", firstCount, secondCount)
	}
}
