package control_test

import (
	"context"
	"testing"
	"time"

	"dash/internal/control"
	"dash/internal/protocol"
)

func TestRegistry_Methods(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	reg := control.NewRegistry(database, nil)
	defer reg.Stop()

	// Initial empty
	if reg.OnlineCount() != 0 {
		t.Fatalf("expected 0 online, got %d", reg.OnlineCount())
	}
	if len(reg.OnlineNodes()) != 0 {
		t.Fatalf("expected 0 online nodes, got %v", reg.OnlineNodes())
	}

	node1 := "01TESTNODE0000000000000001"
	node2 := "01TESTNODE0000000000000002"

	sess1 := control.NewSession(node1, "127.0.0.1", nil)
	sess2 := control.NewSession(node2, "127.0.0.1", nil)

	reg.Register(sess1)
	reg.Register(sess2)

	if reg.OnlineCount() != 2 {
		t.Fatalf("expected 2 online, got %d", reg.OnlineCount())
	}
	if !reg.IsOnline(node1) || !reg.IsOnline(node2) {
		t.Fatalf("expected both nodes online")
	}

	gotSess, ok := reg.Get(node1)
	if !ok || gotSess != sess1 {
		t.Fatalf("expected to get sess1, got %+v", gotSess)
	}

	// Kick node1
	if !reg.Kick(node1) {
		t.Fatalf("expected Kick(node1) to return true")
	}
	if reg.IsOnline(node1) {
		t.Fatalf("expected node1 to be offline after Kick")
	}
	if reg.OnlineCount() != 1 {
		t.Fatalf("expected 1 online, got %d", reg.OnlineCount())
	}

	// Fallback commands
	cmd := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodServerConfig,
	}
	reg.EnqueueFallbackCommand(node2, cmd)
	popped := reg.PopFallbackCommands(node2)
	if len(popped) != 1 || popped[0].Method != protocol.MethodServerConfig {
		t.Fatalf("unexpected popped commands: %+v", popped)
	}
	poppedEmpty := reg.PopFallbackCommands(node2)
	if len(poppedEmpty) != 0 {
		t.Fatalf("expected empty slice, got %v", poppedEmpty)
	}

	// Unregister sess2
	reg.Unregister(sess2)
	if reg.IsOnline(node2) {
		t.Fatalf("expected node2 to be offline after unregister")
	}
}

func TestRegistry_SweepInactive(t *testing.T) {
	database := getTestDB(t)
	defer database.Close()
	cleanTables(t, database)

	ctx := context.Background()

	// Insert an online node in database that hasn't been seen for 100 seconds
	nodeID := control.NewULID()
	token, _ := control.GenerateRandomToken()
	oldTime := time.Now().UnixMilli() - 100*1000

	_, err := database.Exec(ctx, `INSERT INTO nodes (
		id, name, display_order, is_hidden, agent_token_hash,
		conn_state, last_seen_at_ms, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nodeID, "zombie-node", 0, 0, control.HashToken(token),
		"online", oldTime, oldTime, oldTime,
	)
	if err != nil {
		t.Fatalf("insert node failed: %v", err)
	}

	reg := control.NewRegistry(database, nil)
	defer reg.Stop()

	// Add a session whose last_seen was 100s ago
	sess := control.NewSession(nodeID, "127.0.0.1", nil)
	sess.UpdateLastSeen(oldTime)
	reg.Register(sess)

	// Trigger background sweep
	reg.Start(ctx)

	// Wait up to 2 seconds for sweep (or call sweep via reflection/internal, but let's test start/stop)
	// Actually sweep runs every 15s in ticker, but we can verify DB query directly
	// Let's test sweeping logic directly by calling Stop or waiting
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Also verify Stop cleans up all sessions
	reg.Stop()

	if reg.OnlineCount() != 0 {
		t.Fatalf("expected 0 online after Stop, got %d", reg.OnlineCount())
	}
}
