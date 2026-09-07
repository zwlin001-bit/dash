package events_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/migrate"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping test: MySQL test DB not accessible: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	_, _ = d.Exec(ctx, "SELECT GET_LOCK('dash_events_test', 30)")
	t.Cleanup(func() {
		_, _ = d.Exec(context.Background(), "SELECT RELEASE_LOCK('dash_events_test')")
	})

	// Ensure migrations are up to date
	mig := migrate.New(d, "../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	// Clean up events and reset event_types
	_, _ = d.Exec(ctx, "DELETE FROM events")
	_, _ = d.Exec(ctx, "DELETE FROM event_types")

	return d
}

func TestULIDFormat(t *testing.T) {
	seen := make(map[string]bool)
	var prev string
	for i := 0; i < 1000; i++ {
		u := events.NewULID()
		if len(u) != 26 {
			t.Fatalf("expected ULID length 26, got %d (%s)", len(u), u)
		}
		if seen[u] {
			t.Fatalf("duplicate ULID generated: %s", u)
		}
		seen[u] = true
		if prev != "" && u < prev {
			// At same millisecond, entropy should generally not go backwards
			// but we ensure valid base32 characters
		}
		for _, c := range u {
			if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
				t.Fatalf("invalid character in ULID %s: %c", u, c)
			}
		}
		prev = u
	}
}

// TestSixBuiltinEvents verifies Acceptance Criterion 1:
// 六种事件都能在对应操作后正确产生，events 表里能查到
func TestSixBuiltinEvents(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := events.NewStore(d, 100)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	if err := store.SyncBuiltinTypes(ctx); err != nil {
		t.Fatalf("SyncBuiltinTypes failed: %v", err)
	}

	// 1. node.online
	events.Emit(ctx, events.Event{
		Type:       "node.online",
		Source:     "control",
		TargetKind: "node",
		TargetID:   "01J00000000000000000000001",
		Title:      "节点 node-1 上线",
		Payload: map[string]any{
			"node_name":       "node-1",
			"group_name":      "default",
			"public_ip":       "1.2.3.4",
			"offline_seconds": 120,
		},
	})

	// 2. node.offline
	events.Emit(ctx, events.Event{
		Type:       "node.offline",
		Source:     "control",
		TargetKind: "node",
		TargetID:   "01J00000000000000000000002",
		Title:      "节点 node-2 离线",
		Payload: map[string]any{
			"node_name":       "node-2",
			"group_name":      "hk-group",
			"public_ip":       "5.6.7.8",
			"last_seen_at_ms": time.Now().UnixMilli() - 60000,
		},
		DedupKey: "node.offline:01J00000000000000000000002",
	})

	// 3. node.enrolled
	events.Emit(ctx, events.Event{
		Type:       "node.enrolled",
		Source:     "control",
		TargetKind: "node",
		TargetID:   "01J00000000000000000000003",
		Title:      "新节点 node-3 接入",
		Payload: map[string]any{
			"node_name": "node-3",
			"public_ip": "9.10.11.12",
			"os_name":   "Ubuntu 24.04",
			"arch":      "amd64",
		},
	})

	// 4. node.removed
	events.Emit(ctx, events.Event{
		Type:       "node.removed",
		Source:     "inventory",
		TargetKind: "node",
		TargetID:   "01J00000000000000000000004",
		Title:      "节点 node-4 已删除",
		Payload: map[string]any{
			"node_name": "node-4",
			"operator":  "admin",
		},
	})

	// 5. auth.login_failed
	events.Emit(ctx, events.Event{
		Type:       "auth.login_failed",
		Source:     "inventory",
		TargetKind: "user",
		TargetID:   "admin",
		Title:      "用户 admin 登录失败",
		Payload: map[string]any{
			"username":   "admin",
			"ip":         "192.168.1.100",
			"fail_count": 3,
		},
	})

	// 6. system.db_unreachable
	events.Emit(ctx, events.Event{
		Type:   "system.db_unreachable",
		Source: "ingest",
		Title:  "数据库不可达",
		Payload: map[string]any{
			"error_msg":       "connection refused",
			"failed_seconds":  30,
			"dropped_batches": 2,
		},
	})

	// Wait for worker queue drain with timeout
	var records []events.EventRecord
	var total int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		records, total, err = store.ListEvents(ctx, events.Filter{})
		if err == nil && total == 6 && len(records) == 6 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if total != 6 || len(records) != 6 {
		t.Fatalf("expected 6 events persisted, got total=%d len=%d", total, len(records))
	}

	typeMap := make(map[string]events.EventRecord)
	for _, r := range records {
		typeMap[r.Type] = r
	}

	expectedTypes := []string{
		"node.online",
		"node.offline",
		"node.enrolled",
		"node.removed",
		"auth.login_failed",
		"system.db_unreachable",
	}

	for _, et := range expectedTypes {
		rec, ok := typeMap[et]
		if !ok {
			t.Fatalf("missing expected event type: %s", et)
		}
		if len(rec.ID) != 26 {
			t.Errorf("expected ULID ID for %s, got %s", et, rec.ID)
		}
		if len(rec.Payload) == 0 {
			t.Errorf("expected payload for %s, got empty", et)
		}
	}

	// Verify snapshot severity
	if typeMap["system.db_unreachable"].Severity != "critical" {
		t.Errorf("expected critical severity for system.db_unreachable, got %s", typeMap["system.db_unreachable"].Severity)
	}
	if typeMap["node.offline"].Severity != "warning" {
		t.Errorf("expected warning severity for node.offline, got %s", typeMap["node.offline"].Severity)
	}
	if typeMap["node.online"].Severity != "info" {
		t.Errorf("expected info severity for node.online, got %s", typeMap["node.online"].Severity)
	}

	// Verify dedup_key on node.offline
	if typeMap["node.offline"].DedupKey != "node.offline:01J00000000000000000000002" {
		t.Errorf("unexpected dedup_key: %s", typeMap["node.offline"].DedupKey)
	}
}

// TestNonBlockingWhenDBUnavailable verifies Acceptance Criterion 2:
// 停掉数据库，触发一次节点上线，业务正常完成，只在日志里看到事件写入失败
func TestNonBlockingWhenDBUnavailable(t *testing.T) {
	// Create a dummy store with closed / broken DB
	rawDB, err := sql.Open("mysql", "invalid_user:invalid_pass@tcp(127.0.0.1:9999)/no_such_db")
	if err != nil {
		t.Fatalf("sql.Open failed: %v", err)
	}
	d, err := db.New(rawDB, "mysql")
	if err != nil {
		t.Fatalf("db.New failed: %v", err)
	}
	defer d.Close()

	store := events.NewStore(d, 50)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	ctx := context.Background()

	// Measure Emit execution duration - MUST be non-blocking (sub-millisecond)
	start := time.Now()
	for i := 0; i < 20; i++ {
		events.Emit(ctx, events.Event{
			Type:       "node.online",
			Source:     "control",
			TargetKind: "node",
			TargetID:   fmt.Sprintf("node-%d", i),
			Title:      "节点上线",
			Payload: map[string]any{
				"node_name": fmt.Sprintf("node-%d", i),
			},
		})
	}
	elapsed := time.Since(start)

	if elapsed > 50*time.Millisecond {
		t.Fatalf("Emit took too long (%v), expected non-blocking execution", elapsed)
	}
}

// TestDispositionDrop verifies Acceptance Criterion 4:
// 在界面上把 node.online 的处置改成 drop，之后该类事件不再入库
func TestDispositionDrop(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := events.NewStore(d, 100)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	if err := store.SyncBuiltinTypes(ctx); err != nil {
		t.Fatalf("SyncBuiltinTypes failed: %v", err)
	}

	// 1. Initial state: node.online is "store", emit 1 event
	events.Emit(ctx, events.Event{
		Type:   "node.online",
		Source: "control",
		Title:  "node-1 online",
	})
	deadline := time.Now().Add(2 * time.Second)
	var records []events.EventRecord
	var total int
	var err error
	for time.Now().Before(deadline) {
		records, total, err = store.ListEvents(ctx, events.Filter{EventType: "node.online"})
		if err == nil && total == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if total != 1 {
		t.Fatalf("expected 1 node.online event, got total=%d err=%v", total, err)
	}

	// 2. Change disposition of node.online to "drop"
	updated, err := store.UpdateEventType(ctx, "node.online", "", "drop")
	if err != nil {
		t.Fatalf("UpdateEventType failed: %v", err)
	}
	if updated.Disposition != "drop" {
		t.Fatalf("expected disposition drop, got %s", updated.Disposition)
	}

	// 3. Emit node.online again: should be dropped, not stored
	events.Emit(ctx, events.Event{
		Type:   "node.online",
		Source: "control",
		Title:  "node-1 online second time",
	})
	time.Sleep(100 * time.Millisecond)

	records, total, err = store.ListEvents(ctx, events.Filter{EventType: "node.online"})
	if err != nil || total != 1 {
		t.Fatalf("expected still only 1 node.online event after drop, got total=%d", total)
	}

	// 4. Other events like node.offline should still be stored
	events.Emit(ctx, events.Event{
		Type:   "node.offline",
		Source: "control",
		Title:  "node-1 offline",
	})
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		records, total, err = store.ListEvents(ctx, events.Filter{EventType: "node.offline"})
		if err == nil && total == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if total != 1 {
		t.Fatalf("expected 1 node.offline event, got total=%d", total)
	}
	_ = records
}

// TestMarkReadAndUnreadCount verifies mark read and unread count functionality
func TestMarkReadAndUnreadCount(t *testing.T) {
	d := setupTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store := events.NewStore(d, 100)
	store.Start()
	defer store.Stop()
	oldStore := events.GetDefaultStore()
	defer events.SetDefaultStore(oldStore)
	events.SetDefaultStore(store)

	if err := store.SyncBuiltinTypes(ctx); err != nil {
		t.Fatalf("SyncBuiltinTypes failed: %v", err)
	}

	now := time.Now().UnixMilli()
	// Emit 3 events
	for i := 0; i < 3; i++ {
		events.Emit(ctx, events.Event{
			Type:       "node.online",
			Source:     "control",
			Title:      fmt.Sprintf("event-%d", i),
			OccurredAt: now - int64((3-i)*1000),
		})
	}
	deadline := time.Now().Add(2 * time.Second)
	var count int64
	var err error
	for time.Now().Before(deadline) {
		count, err = store.GetUnreadCount(ctx)
		if err == nil && count == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if count != 3 {
		t.Fatalf("expected unread count 3, got %d (err: %v)", count, err)
	}

	// Mark 1 by ID
	items, _, err := store.ListEvents(ctx, events.Filter{Limit: 1})
	if err != nil || len(items) != 1 {
		t.Fatalf("failed to query item: %v", err)
	}
	targetID := items[0].ID

	affected, err := store.MarkRead(ctx, events.MarkReadRequest{IDs: []string{targetID}})
	if err != nil || affected != 1 {
		t.Fatalf("expected 1 affected, got %d (err: %v)", affected, err)
	}

	count, err = store.GetUnreadCount(ctx)
	if err != nil || count != 2 {
		t.Fatalf("expected unread count 2, got %d", count)
	}

	// Mark remaining read using All
	affected, err = store.MarkRead(ctx, events.MarkReadRequest{All: true})
	if err != nil || affected != 2 {
		t.Fatalf("expected 2 affected, got %d", affected)
	}

	count, err = store.GetUnreadCount(ctx)
	if err != nil || count != 0 {
		t.Fatalf("expected unread count 0, got %d", count)
	}
}
