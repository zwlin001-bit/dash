package dialect

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

type mockExecer struct {
	executedQueries []string
	callCount       int
	totalRows       int
}

func (m *mockExecer) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	m.executedQueries = append(m.executedQueries, query)
	m.callCount++
	return nil, nil
}

func TestFactory(t *testing.T) {
	dOra, err := Get("oracle")
	if err != nil || dOra.Name() != "oracle" {
		t.Fatalf("expected oracle dialect, got %v, err=%v", dOra, err)
	}

	dMy, err := Get("mysql")
	if err != nil || dMy.Name() != "mysql" {
		t.Fatalf("expected mysql dialect, got %v, err=%v", dMy, err)
	}

	_, err = Get("postgres")
	if err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}

func TestOracleRebind(t *testing.T) {
	d, _ := Get("oracle")

	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "SELECT * FROM nodes WHERE id = ?",
			expected: "SELECT * FROM nodes WHERE id = :1",
		},
		{
			input:    "SELECT * FROM nodes WHERE id = ? AND status = ?",
			expected: "SELECT * FROM nodes WHERE id = :1 AND status = :2",
		},
		{
			input:    "SELECT * FROM nodes WHERE name = 'hello?world' AND id = ?",
			expected: "SELECT * FROM nodes WHERE name = 'hello?world' AND id = :1",
		},
		{
			input:    "SELECT * FROM nodes WHERE name = 'it''s a ?' AND id = ?",
			expected: "SELECT * FROM nodes WHERE name = 'it''s a ?' AND id = :1",
		},
		{
			input:    "SELECT * FROM nodes -- what is ?\nWHERE id = ?",
			expected: "SELECT * FROM nodes -- what is ?\nWHERE id = :1",
		},
		{
			input:    "SELECT * FROM nodes /* block ? */ WHERE id = ?",
			expected: "SELECT * FROM nodes /* block ? */ WHERE id = :1",
		},
		{
			// Idempotency: already has :1 :2, no ?
			input:    "SELECT * FROM nodes WHERE id = :1 AND status = :2",
			expected: "SELECT * FROM nodes WHERE id = :1 AND status = :2",
		},
		{
			// Mixed: already has :1 :2, then ? appended
			input:    "SELECT * FROM nodes WHERE id = :1 OFFSET ? ROWS FETCH NEXT ? ROWS ONLY",
			expected: "SELECT * FROM nodes WHERE id = :1 OFFSET :2 ROWS FETCH NEXT :3 ROWS ONLY",
		},
	}

	for _, c := range cases {
		got := d.Rebind(c.input)
		if got != c.expected {
			t.Errorf("Oracle Rebind(%q)\n got:  %q\n want: %q", c.input, got, c.expected)
		}
	}
}

func TestMySQLRebind(t *testing.T) {
	d, _ := Get("mysql")
	q := "SELECT * FROM nodes WHERE id = ? AND status = ?"
	if got := d.Rebind(q); got != q {
		t.Errorf("MySQL Rebind should be identity, got: %q", got)
	}
}

func TestPaginate(t *testing.T) {
	dOra, _ := Get("oracle")
	qOra, argsOra := dOra.Paginate("SELECT * FROM t WHERE a = ?", 10, 20)
	if !strings.Contains(qOra, "OFFSET ? ROWS FETCH NEXT ? ROWS ONLY") {
		t.Fatalf("unexpected oracle paginate query: %s", qOra)
	}
	if len(argsOra) != 2 || argsOra[0] != 20 || argsOra[1] != 10 {
		t.Fatalf("unexpected oracle paginate args: %v", argsOra)
	}

	dMy, _ := Get("mysql")
	qMy, argsMy := dMy.Paginate("SELECT * FROM t WHERE a = ?", 10, 20)
	if !strings.Contains(qMy, "LIMIT ? OFFSET ?") {
		t.Fatalf("unexpected mysql paginate query: %s", qMy)
	}
	if len(argsMy) != 2 || argsMy[0] != 10 || argsMy[1] != 20 {
		t.Fatalf("unexpected mysql paginate args: %v", argsMy)
	}
}

func TestUpsertSQL(t *testing.T) {
	dOra, _ := Get("oracle")
	sqlOra := dOra.UpsertSQL("nodes", []string{"id"}, []string{"status", "updated_at_ms"})
	if !strings.Contains(sqlOra, "MERGE INTO nodes d") {
		t.Errorf("oracle upsert missing MERGE: %s", sqlOra)
	}
	if !strings.Contains(sqlOra, "USING (SELECT :1 id, :2 status, :3 updated_at_ms FROM dual) s") {
		t.Errorf("oracle upsert missing USING: %s", sqlOra)
	}
	if !strings.Contains(sqlOra, "WHEN MATCHED THEN UPDATE SET d.status = s.status, d.updated_at_ms = s.updated_at_ms") {
		t.Errorf("oracle upsert missing WHEN MATCHED: %s", sqlOra)
	}
	if !strings.Contains(sqlOra, "WHEN NOT MATCHED THEN INSERT (id, status, updated_at_ms) VALUES (s.id, s.status, s.updated_at_ms)") {
		t.Errorf("oracle upsert missing WHEN NOT MATCHED: %s", sqlOra)
	}

	dMy, _ := Get("mysql")
	sqlMy := dMy.UpsertSQL("nodes", []string{"id"}, []string{"status", "updated_at_ms"})
	if !strings.Contains(sqlMy, "INSERT INTO nodes (id, status, updated_at_ms) VALUES (?, ?, ?)") {
		t.Errorf("mysql upsert missing INSERT: %s", sqlMy)
	}
	if !strings.Contains(sqlMy, "ON DUPLICATE KEY UPDATE status = VALUES(status), updated_at_ms = VALUES(updated_at_ms)") {
		t.Errorf("mysql upsert missing ON DUPLICATE KEY: %s", sqlMy)
	}
}

func TestBatchInsertChunking(t *testing.T) {
	cols := []string{"id", "val"}
	totalRows := 2500
	rows := make([][]any, totalRows)
	for i := 0; i < totalRows; i++ {
		rows[i] = []any{i, i * 10}
	}

	ctx := context.Background()

	// Oracle test chunking
	dOra, _ := Get("oracle")
	mockOra := &mockExecer{}
	if err := dOra.BatchInsert(ctx, mockOra, "t", cols, rows); err != nil {
		t.Fatalf("oracle BatchInsert error: %v", err)
	}
	if mockOra.callCount != 3 { // 1000 + 1000 + 500 = 3 chunks
		t.Fatalf("expected 3 chunks for oracle, got %d", mockOra.callCount)
	}

	// MySQL test chunking
	dMy, _ := Get("mysql")
	mockMy := &mockExecer{}
	if err := dMy.BatchInsert(ctx, mockMy, "t", cols, rows); err != nil {
		t.Fatalf("mysql BatchInsert error: %v", err)
	}
	if mockMy.callCount != 3 { // 1000 + 1000 + 500 = 3 chunks
		t.Fatalf("expected 3 chunks for mysql, got %d", mockMy.callCount)
	}

	// Empty rows should return nil immediately without executing
	emptyMock := &mockExecer{}
	if err := dOra.BatchInsert(ctx, emptyMock, "t", cols, nil); err != nil {
		t.Fatalf("expected nil on empty rows, got %v", err)
	}
	if emptyMock.callCount != 0 {
		t.Fatalf("expected 0 calls for empty rows, got %d", emptyMock.callCount)
	}
}
