package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dash/internal/db"
	"dash/internal/ulid"
)

func getTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("Skipping DB test: cannot connect to test MySQL: %v", err)
	}
	return d
}

func setupInventoryServices(t *testing.T) (*db.DB, *NodeService, *GroupService, *TagService, *FactsService, *BillingService, *EnrollService) {
	d := getTestDB(t)
	tags := NewTagService(d)
	facts := NewFactsService(d)
	billing := NewBillingService(d)
	groups := NewGroupService(d)
	enroll := NewEnrollService(d)
	nodes := NewNodeService(d, tags, facts, billing)
	return d, nodes, groups, tags, facts, billing, enroll
}

// TestGroupsCRUD satisfies Acceptance Criterion 1: 分组完整 CRUD 都能跑通。
func TestGroupsCRUD(t *testing.T) {
	d, _, groups, _, _, _, _ := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()
	suffix := ulid.New()[:6]
	name := "group-" + suffix

	// 1. Create Group
	g, err := groups.CreateGroup(ctx, CreateGroupParams{
		Name:         name,
		DisplayOrder: 10,
	}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create group: %v", err)
	}
	if g.Name != name || g.DisplayOrder != 10 {
		t.Fatalf("unexpected group: %+v", g)
	}

	// Duplicate name -> conflict
	_, err = groups.CreateGroup(ctx, CreateGroupParams{
		Name: name,
	}, "user", "admin-1", "127.0.0.1")
	if err == nil || err.Error() != "duplicate_name" {
		t.Fatalf("expected duplicate_name error, got %v", err)
	}

	// 2. Get Group
	fetched, err := groups.GetGroup(ctx, g.ID)
	if err != nil {
		t.Fatalf("failed to get group: %v", err)
	}
	if fetched.ID != g.ID || fetched.Name != name {
		t.Fatalf("fetched mismatch: %+v", fetched)
	}

	// 3. Update Group
	newName := name + "-renamed"
	newOrder := 20
	updated, err := groups.UpdateGroup(ctx, g.ID, UpdateGroupParams{
		Name:         &newName,
		DisplayOrder: &newOrder,
	}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to update group: %v", err)
	}
	if updated.Name != newName || updated.DisplayOrder != 20 {
		t.Fatalf("unexpected updated group: %+v", updated)
	}

	// 4. List Groups
	list, err := groups.ListGroups(ctx, 1, 50)
	if err != nil {
		t.Fatalf("failed to list groups: %v", err)
	}
	if list.Total <= 0 {
		t.Fatalf("expected total > 0, got %d", list.Total)
	}

	// 5. Delete Group
	err = groups.DeleteGroup(ctx, g.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to delete group: %v", err)
	}

	// Verify not found
	_, err = groups.GetGroup(ctx, g.ID)
	if err == nil {
		t.Fatal("expected ErrNotFound after delete")
	}

	// 6. Verify audit_log
	var auditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE target_id = ? AND action IN ('group.create', 'group.update', 'group.delete')", g.ID).Scan(&auditCount)
	if err != nil || auditCount != 3 {
		t.Fatalf("expected 3 audit_log rows for group, got %d, err=%v", auditCount, err)
	}
}

// TestTagsCRUDAndNodeTagging satisfies Acceptance Criteria 1, 2, 4:
// - 标签完整 CRUD 跑通
// - 一个节点打多个标签、按标签反查节点，两个方向都对
// - 删除一个仍被节点使用的标签，关联行被正确清理，不留孤儿
func TestTagsCRUDAndNodeTagging(t *testing.T) {
	d, nodes, _, tags, _, _, _ := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()
	suffix := ulid.New()[:6]

	// 1. Create Tags
	tag1Name := "tag-hk-" + suffix
	color1 := "#3b82f6"
	t1, err := tags.CreateTag(ctx, CreateTagParams{Name: tag1Name, Color: &color1}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create tag1: %v", err)
	}

	tag2Name := "tag-proxy-" + suffix
	color2 := "#10b981"
	t2, err := tags.CreateTag(ctx, CreateTagParams{Name: tag2Name, Color: &color2}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create tag2: %v", err)
	}

	// Duplicate tag name check
	_, err = tags.CreateTag(ctx, CreateTagParams{Name: tag1Name}, "user", "admin-1", "127.0.0.1")
	if err == nil || err.Error() != "duplicate_name" {
		t.Fatalf("expected duplicate_name error, got: %v", err)
	}

	// 2. Create a node to attach tags to
	nodeName := "node-tagged-" + suffix
	node, err := nodes.CreateNode(ctx, CreateNodeParams{Name: nodeName}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	defer func() {
		_ = nodes.DeleteNode(ctx, node.ID, "user", "admin-1", "127.0.0.1")
	}()

	// 3. Attach multiple tags to the node (Criterion 2: 一个节点打多个标签)
	err = tags.AttachTag(ctx, node.ID, t1.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to attach tag1: %v", err)
	}
	err = tags.AttachTag(ctx, node.ID, t2.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to attach tag2: %v", err)
	}

	// 4. Direction 1: Node -> Tags (Query tags of node)
	attachedTags, err := tags.GetTagsByNodeID(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get tags for node: %v", err)
	}
	if len(attachedTags) != 2 {
		t.Fatalf("expected 2 tags for node, got %d", len(attachedTags))
	}

	// Verify inside GetNode as well
	nodeWithTags, err := nodes.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get node: %v", err)
	}
	if len(nodeWithTags.Tags) != 2 {
		t.Fatalf("expected 2 tags in node details, got %d", len(nodeWithTags.Tags))
	}

	// 5. Direction 2: Tag -> Nodes (Criterion 2: 按标签反查节点，两个方向都对)
	nodeIDsT1, err := tags.GetNodeIDsByTagID(ctx, t1.ID)
	if err != nil {
		t.Fatalf("failed to get node IDs for tag1: %v", err)
	}
	found := false
	for _, nid := range nodeIDsT1 {
		if nid == node.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("node %s not found in tag1 reverse query result: %v", node.ID, nodeIDsT1)
	}

	// Also verify ListNodes with tag_id filter
	listByTag, err := nodes.ListNodes(ctx, ListNodesFilter{TagID: t1.ID})
	if err != nil {
		t.Fatalf("failed to list nodes by tag_id: %v", err)
	}
	foundInList := false
	for _, item := range listByTag.Items.([]*Node) {
		if item.ID == node.ID {
			foundInList = true
			break
		}
	}
	if !foundInList {
		t.Fatalf("node %s not found in ListNodes with tag_id filter", node.ID)
	}

	// 6. Test Replace tags (e.g. replace with only tag2)
	err = tags.ReplaceNodeTags(ctx, node.ID, []string{t2.ID}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to replace tags: %v", err)
	}
	attachedTags, _ = tags.GetTagsByNodeID(ctx, node.ID)
	if len(attachedTags) != 1 || attachedTags[0].ID != t2.ID {
		t.Fatalf("expected 1 tag (t2) after replace, got %v", attachedTags)
	}

	// Attach t1 back for Criterion 4 test
	_ = tags.AttachTag(ctx, node.ID, t1.ID, "user", "admin-1", "127.0.0.1")

	// 7. Criterion 4: 删除一个仍被节点使用的标签，关联行被正确清理，不留孤儿
	err = tags.DeleteTag(ctx, t1.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to delete tag1: %v", err)
	}

	// Verify no orphan rows in node_tags for tag1
	var orphanCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM node_tags WHERE tag_id = ?", t1.ID).Scan(&orphanCount)
	if err != nil {
		t.Fatalf("failed to count orphan node_tags: %v", err)
	}
	if orphanCount != 0 {
		t.Fatalf("expected 0 orphan rows in node_tags for deleted tag, got %d", orphanCount)
	}

	// Verify node still exists and now only has t2
	nodeAfterTagDelete, err := nodes.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("node should still exist: %v", err)
	}
	if len(nodeAfterTagDelete.Tags) != 1 || nodeAfterTagDelete.Tags[0].ID != t2.ID {
		t.Fatalf("expected node to have only tag t2 remaining, got %v", nodeAfterTagDelete.Tags)
	}

	// Clean up tag2
	_ = tags.DeleteTag(ctx, t2.ID, "user", "admin-1", "127.0.0.1")
}

// TestNodeFactsAndBilling satisfies Acceptance Criterion 1: Facts 和 Billing 增删改查。
func TestNodeFactsAndBilling(t *testing.T) {
	d, nodes, _, _, facts, billing, _ := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()
	suffix := ulid.New()[:6]

	node, err := nodes.CreateNode(ctx, CreateNodeParams{Name: "node-spec-" + suffix}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}
	defer func() {
		_ = nodes.DeleteNode(ctx, node.ID, "user", "admin-1", "127.0.0.1")
	}()

	// 1. Facts: Save & Get
	arch := "amd64"
	osName := "Debian"
	osVer := "12"
	cores := 4
	memTotal := int64(8589934592)
	factsHash := "hash123"

	f := &NodeFacts{
		NodeID:    node.ID,
		Arch:      &arch,
		OSName:    &osName,
		OSVersion: &osVer,
		CPUCores:  &cores,
		MemTotal:  &memTotal,
		FactsHash: &factsHash,
	}

	err = facts.SaveFacts(ctx, f, "agent", node.ID, "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to save facts: %v", err)
	}

	savedFacts, err := facts.GetFacts(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get facts: %v", err)
	}
	if savedFacts.Arch == nil || *savedFacts.Arch != arch || savedFacts.CPUCores == nil || *savedFacts.CPUCores != 4 {
		t.Fatalf("facts mismatch: %+v", savedFacts)
	}

	// 2. Billing: Save & Get
	currency := "USD"
	price := 5.00
	cycleDays := 30
	isAutoRenew := true
	trafficLimit := int64(1000000000000)
	trafficKind := "sum"

	b, err := billing.SaveBilling(ctx, node.ID, UpdateBillingParams{
		Currency:         &currency,
		Price:            &price,
		CycleDays:        &cycleDays,
		IsAutoRenew:      &isAutoRenew,
		TrafficLimit:     &trafficLimit,
		TrafficLimitKind: &trafficKind,
	}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to save billing: %v", err)
	}
	if b.Currency == nil || *b.Currency != "USD" || b.Price == nil || *b.Price != 5.00 || !b.IsAutoRenew {
		t.Fatalf("billing mismatch: %+v", b)
	}

	savedBilling, err := billing.GetBilling(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get billing: %v", err)
	}
	if savedBilling.Price == nil || *savedBilling.Price != 5.00 {
		t.Fatalf("fetched billing mismatch: %+v", savedBilling)
	}

	// 3. Node details include facts and billing
	detailedNode, err := nodes.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("failed to get detailed node: %v", err)
	}
	if detailedNode.Facts == nil || detailedNode.Facts.Arch == nil || *detailedNode.Facts.Arch != "amd64" {
		t.Fatalf("detailed node missing facts: %+v", detailedNode.Facts)
	}
	if detailedNode.Billing == nil || detailedNode.Billing.Price == nil || *detailedNode.Billing.Price != 5.00 {
		t.Fatalf("detailed node missing billing: %+v", detailedNode.Billing)
	}

	// 4. Delete Billing
	err = billing.DeleteBilling(ctx, node.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to delete billing: %v", err)
	}
	_, err = billing.GetBilling(ctx, node.ID)
	if err == nil {
		t.Fatal("expected ErrNotFound after deleting billing")
	}
}

// TestNodeCascadeDelete satisfies Acceptance Criterion 3:
// 删除节点后，node_tags / node_facts / node_billing 里的关联行都被清掉
func TestNodeCascadeDelete(t *testing.T) {
	d, nodes, _, tags, facts, billing, _ := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()
	suffix := ulid.New()[:6]

	// Create node
	node, err := nodes.CreateNode(ctx, CreateNodeParams{Name: "node-del-" + suffix}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create node: %v", err)
	}

	// Attach tag
	t1, err := tags.CreateTag(ctx, CreateTagParams{Name: "tag-for-del-" + suffix}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create tag: %v", err)
	}
	defer func() {
		_ = tags.DeleteTag(ctx, t1.ID, "user", "admin-1", "127.0.0.1")
	}()
	_ = tags.AttachTag(ctx, node.ID, t1.ID, "user", "admin-1", "127.0.0.1")

	// Set facts
	arch := "arm64"
	_ = facts.SaveFacts(ctx, &NodeFacts{NodeID: node.ID, Arch: &arch}, "agent", node.ID, "127.0.0.1")

	// Set billing
	curr := "CNY"
	price := 35.5
	_, _ = billing.SaveBilling(ctx, node.ID, UpdateBillingParams{Currency: &curr, Price: &price}, "user", "admin-1", "127.0.0.1")

	// Verify that associations exist before delete
	var countTags, countFacts, countBilling int
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_tags WHERE node_id = ?", node.ID).Scan(&countTags)
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_facts WHERE node_id = ?", node.ID).Scan(&countFacts)
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_billing WHERE node_id = ?", node.ID).Scan(&countBilling)
	if countTags != 1 || countFacts != 1 || countBilling != 1 {
		t.Fatalf("pre-delete verification failed: tags=%d, facts=%d, billing=%d", countTags, countFacts, countBilling)
	}

	// Execute DeleteNode
	err = nodes.DeleteNode(ctx, node.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("delete node failed: %v", err)
	}

	// Criterion 3 verification: node_tags / node_facts / node_billing are completely cleaned up
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_tags WHERE node_id = ?", node.ID).Scan(&countTags)
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_facts WHERE node_id = ?", node.ID).Scan(&countFacts)
	_ = d.QueryRow(ctx, "SELECT count(1) FROM node_billing WHERE node_id = ?", node.ID).Scan(&countBilling)
	var countNodes int
	_ = d.QueryRow(ctx, "SELECT count(1) FROM nodes WHERE id = ?", node.ID).Scan(&countNodes)

	if countTags != 0 {
		t.Fatalf("expected 0 rows in node_tags after delete, got %d", countTags)
	}
	if countFacts != 0 {
		t.Fatalf("expected 0 rows in node_facts after delete, got %d", countFacts)
	}
	if countBilling != 0 {
		t.Fatalf("expected 0 rows in node_billing after delete, got %d", countBilling)
	}
	if countNodes != 0 {
		t.Fatalf("expected 0 rows in nodes after delete, got %d", countNodes)
	}

	// Verify audit_log
	var delAuditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE target_id = ? AND action = 'node.delete'", node.ID).Scan(&delAuditCount)
	if err != nil || delAuditCount == 0 {
		t.Fatalf("expected audit_log for node.delete, count=%d, err=%v", delAuditCount, err)
	}
}

// TestDeleteGroupUnsetsNodeGroupID verifies that deleting a group unsets node_group_id without deleting nodes.
func TestDeleteGroupUnsetsNodeGroupID(t *testing.T) {
	d, nodes, groups, _, _, _, _ := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()
	suffix := ulid.New()[:6]

	// 1. Create group
	g, err := groups.CreateGroup(ctx, CreateGroupParams{Name: "grp-cascade-" + suffix}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create group: %v", err)
	}

	// 2. Create node in that group
	node, err := nodes.CreateNode(ctx, CreateNodeParams{
		Name:        "node-in-grp-" + suffix,
		NodeGroupID: &g.ID,
	}, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create node in group: %v", err)
	}
	defer func() {
		_ = nodes.DeleteNode(ctx, node.ID, "user", "admin-1", "127.0.0.1")
	}()

	if node.NodeGroupID == nil || *node.NodeGroupID != g.ID {
		t.Fatalf("expected node_group_id %s, got %v", g.ID, node.NodeGroupID)
	}

	// 3. Delete group
	err = groups.DeleteGroup(ctx, g.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to delete group: %v", err)
	}

	// 4. Verify node STILL EXISTS and node_group_id is now NULL
	refetchedNode, err := nodes.GetNode(ctx, node.ID)
	if err != nil {
		t.Fatalf("node should not be deleted: %v", err)
	}
	if refetchedNode.NodeGroupID != nil {
		t.Fatalf("expected node_group_id to be nil, got: %s", *refetchedNode.NodeGroupID)
	}
}

// TestEnrollTokens verifies token generation, listing, and deletion.
func TestEnrollTokens(t *testing.T) {
	d, _, _, _, _, _, enroll := setupInventoryServices(t)
	defer d.Close()

	ctx := context.Background()

	// 1. Create token
	name := "preset-node-name"
	hours := 24
	resp, err := enroll.CreateToken(ctx, CreateEnrollTokenParams{
		PresetName:     &name,
		ExpiresInHours: &hours,
	}, "dash.example.com", "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to create enroll token: %v", err)
	}

	if resp.ID == "" || resp.Token == "" {
		t.Fatalf("expected non-empty id and token, got: %+v", resp)
	}
	expectedCmdPrefix := "curl -fsSL https://"
	if len(resp.InstallCmd) == 0 || resp.InstallCmd[:len(expectedCmdPrefix)] != expectedCmdPrefix {
		t.Fatalf("install command malformed: %s", resp.InstallCmd)
	}

	// 2. List tokens
	list, err := enroll.ListTokens(ctx, 1, 50)
	if err != nil {
		t.Fatalf("failed to list enroll tokens: %v", err)
	}
	found := false
	for _, it := range list.Items.([]*EnrollToken) {
		if it.ID == resp.ID {
			found = true
			if it.IsUsed || it.IsExpired {
				t.Fatalf("new token should not be used or expired: %+v", it)
			}
			break
		}
	}
	if !found {
		t.Fatalf("token %s not found in list", resp.ID)
	}

	// 3. Delete token
	err = enroll.DeleteToken(ctx, resp.ID, "user", "admin-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("failed to delete enroll token: %v", err)
	}

	// Verify deleted
	var count int
	_ = d.QueryRow(ctx, "SELECT count(1) FROM enroll_tokens WHERE id = ?", resp.ID).Scan(&count)
	if count != 0 {
		t.Fatalf("expected 0 tokens remaining for id %s, got %d", resp.ID, count)
	}

	// Verify audit_log
	var auditCount int
	err = d.QueryRow(ctx, "SELECT count(1) FROM audit_log WHERE target_id = ? AND action IN ('enroll_token.create', 'enroll_token.delete')", resp.ID).Scan(&auditCount)
	if err != nil || auditCount != 2 {
		t.Fatalf("expected 2 audit entries for enroll token, got %d, err=%v", auditCount, err)
	}
}

// TestHTTPModuleEndpoints verifies full HTTP routing and envelope responses.
func TestHTTPModuleEndpoints(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	mod := NewModule()
	mux := http.NewServeMux()
	mod.Init(d)
	mod.RegisterRoutes(mux)

	suffix := ulid.New()[:6]

	// 1. POST /api/v1/node-groups
	grpPayload := map[string]any{"name": "http-grp-" + suffix, "display_order": 1}
	grpBody, _ := json.Marshal(grpPayload)
	req, _ := http.NewRequest("POST", "/api/v1/node-groups", bytes.NewReader(grpBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 creating group, got %d, body: %s", rec.Code, rec.Body.String())
	}
	var createdGrp NodeGroup
	_ = json.Unmarshal(rec.Body.Bytes(), &createdGrp)

	// 2. POST /api/v1/nodes
	nodePayload := map[string]any{
		"name":          "http-node-" + suffix,
		"node_group_id": createdGrp.ID,
		"display_order": 5,
		"note":          "created via HTTP",
	}
	nodeBody, _ := json.Marshal(nodePayload)
	req2, _ := http.NewRequest("POST", "/api/v1/nodes", bytes.NewReader(nodeBody))
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 creating node, got %d, body: %s", rec2.Code, rec2.Body.String())
	}
	var createdNode Node
	_ = json.Unmarshal(rec2.Body.Bytes(), &createdNode)

	// 3. GET /api/v1/nodes/{id}
	req3, _ := http.NewRequest("GET", "/api/v1/nodes/"+createdNode.ID, nil)
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusOK {
		t.Fatalf("expected 200 getting node, got %d, body: %s", rec3.Code, rec3.Body.String())
	}

	// 4. PATCH /api/v1/nodes/{id}
	newNote := "updated note"
	patchPayload := map[string]any{"note": newNote}
	patchBody, _ := json.Marshal(patchPayload)
	req4, _ := http.NewRequest("PATCH", "/api/v1/nodes/"+createdNode.ID, bytes.NewReader(patchBody))
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, req4)

	if rec4.Code != http.StatusOK {
		t.Fatalf("expected 200 updating node, got %d, body: %s", rec4.Code, rec4.Body.String())
	}

	// 5. POST /api/v1/nodes/{id}/token:revoke
	req5, _ := http.NewRequest("POST", "/api/v1/nodes/"+createdNode.ID+"/token:revoke", nil)
	rec5 := httptest.NewRecorder()
	mux.ServeHTTP(rec5, req5)

	if rec5.Code != http.StatusOK {
		t.Fatalf("expected 200 revoking token, got %d, body: %s", rec5.Code, rec5.Body.String())
	}

	// 6. DELETE /api/v1/nodes/{id}
	req6, _ := http.NewRequest("DELETE", "/api/v1/nodes/"+createdNode.ID, nil)
	rec6 := httptest.NewRecorder()
	mux.ServeHTTP(rec6, req6)

	if rec6.Code != http.StatusOK {
		t.Fatalf("expected 200 deleting node, got %d, body: %s", rec6.Code, rec6.Body.String())
	}

	// Clean up group
	reqDelGrp, _ := http.NewRequest("DELETE", "/api/v1/node-groups/"+createdGrp.ID, nil)
	recDelGrp := httptest.NewRecorder()
	mux.ServeHTTP(recDelGrp, reqDelGrp)
}
