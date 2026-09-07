package db

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}
	// Test Open nil config
	_, err := Open(nil)
	if err == nil {
		t.Fatal("expected error on nil config")
	}

	// Test unsupported driver
	cfg.Driver = "unsupported_driver"
	_, err = Open(cfg)
	if err == nil {
		t.Fatal("expected error on unsupported driver")
	}
}

func TestBatchInsertValidation(t *testing.T) {
	d, err := New(&sql.DB{}, "mysql")
	if err != nil {
		t.Fatalf("failed to create DB: %v", err)
	}

	ctx := context.Background()

	// Empty rows should return nil immediately
	if err := d.BatchInsert(ctx, "t", []string{"id"}, nil); err != nil {
		t.Fatalf("expected nil for empty rows, got %v", err)
	}

	// Empty cols with rows should return error
	if err := d.BatchInsert(ctx, "t", nil, [][]any{{1}}); err == nil {
		t.Fatal("expected error for empty cols with rows")
	}

	// Column count mismatch
	rows := [][]any{
		{"a", 1},
		{"b"}, // mismatch!
	}
	if err := d.BatchInsert(ctx, "t", []string{"col1", "col2"}, rows); err == nil {
		t.Fatal("expected error for row length mismatch")
	}
}

func TestErrNotFound(t *testing.T) {
	err := ErrNotFound
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected ErrNotFound to match sql.ErrNoRows")
	}
}
