package control_test

import (
	"context"
	"testing"
	"time"

	"dash/internal/control"
)

func TestULID(t *testing.T) {
	id1 := control.NewULID()
	id2 := control.NewULID()

	if len(id1) != 26 {
		t.Fatalf("expected ULID length 26, got %d (%s)", len(id1), id1)
	}
	if len(id2) != 26 {
		t.Fatalf("expected ULID length 26, got %d (%s)", len(id2), id2)
	}
	if id1 == id2 {
		t.Fatalf("expected unique ULIDs, got duplicate %s", id1)
	}
	if id1 >= id2 {
		t.Fatalf("expected monotonic ULID order: id1=%s, id2=%s", id1, id2)
	}
}

func TestDBConnectivity(t *testing.T) {
	db := getTestDB(t)
	defer db.Close()

	cleanTables(t, db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int
	err := db.QueryRow(ctx, "SELECT COUNT(*) FROM nodes").Scan(&count)
	if err != nil {
		t.Fatalf("query nodes failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 nodes, got %d", count)
	}
}
