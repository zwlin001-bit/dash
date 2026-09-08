package credentials_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	"dash/internal/credentials"
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
		t.Skipf("Skipping test: cannot connect to local MySQL on 33306: %v", err)
	}
	return d
}

func TestCredentialsStore(t *testing.T) {
	d := getTestDB(t)
	defer d.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	masterKey := make([]byte, 32)
	if _, err := rand.Read(masterKey); err != nil {
		t.Fatal(err)
	}

	store := credentials.NewStore(d, masterKey)

	akPayload := credentials.AliyunAKPayload{
		AccessKeyID:     "LTAI5t8abc12345678",
		AccessKeySecret: "secretKeySuperSensitive999",
	}
	rawJSON, _ := json.Marshal(akPayload)

	// 1. Create credential
	credName := "test-aliyun-" + ulid.New()
	summary, err := store.Create(ctx, credName, "aliyun_ak", rawJSON)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if summary.Fingerprint != "LTAI****5678" {
		t.Fatalf("unexpected fingerprint: got %s, want LTAI****5678", summary.Fingerprint)
	}

	// Verify database content: enc_payload is encrypted, not plaintext
	var encPayload []byte
	err = d.QueryRow(ctx, "SELECT enc_payload FROM credentials WHERE id = ?", summary.ID).Scan(&encPayload)
	if err != nil {
		t.Fatalf("failed to query raw row: %v", err)
	}
	if string(encPayload) == string(rawJSON) {
		t.Fatal("secret stored in plaintext in database!")
	}

	// 2. List credentials
	list, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	found := false
	for _, item := range list {
		if item.ID == summary.ID {
			found = true
			if item.Fingerprint != "LTAI****5678" {
				t.Fatalf("listed item has wrong fingerprint: %s", item.Fingerprint)
			}
		}
	}
	if !found {
		t.Fatalf("created credential %s not found in list", summary.ID)
	}

	// 3. GetDecrypted (internal only)
	c, decrypted, err := store.GetDecrypted(ctx, summary.ID)
	if err != nil {
		t.Fatalf("GetDecrypted failed: %v", err)
	}
	if c.Name != credName {
		t.Fatalf("expected name %s, got %s", credName, c.Name)
	}
	var decryptedAK credentials.AliyunAKPayload
	if err := json.Unmarshal(decrypted, &decryptedAK); err != nil {
		t.Fatalf("failed to unmarshal decrypted payload: %v", err)
	}
	if decryptedAK.AccessKeySecret != akPayload.AccessKeySecret {
		t.Fatalf("decrypted secret mismatch: got %s, want %s", decryptedAK.AccessKeySecret, akPayload.AccessKeySecret)
	}

	// 4. Delete protection when referenced by cloud_account
	accID := ulid.New()
	now := time.Now().UnixMilli()
	_, err = d.Exec(ctx, `INSERT INTO cloud_accounts (id, provider_code, name, credential_id, is_enabled, created_at_ms, updated_at_ms)
VALUES (?, 'aliyun', ?, ?, 1, ?, ?)`, accID, "acc-"+accID, summary.ID, now, now)
	if err != nil {
		t.Fatalf("failed to create dummy cloud account: %v", err)
	}

	// Trying to delete must fail with ErrInUse
	err = store.Delete(ctx, summary.ID)
	if err != credentials.ErrInUse {
		t.Fatalf("expected ErrInUse when deleting used credential, got %v", err)
	}

	// Remove referencing cloud_account
	_, err = d.Exec(ctx, "DELETE FROM cloud_accounts WHERE id = ?", accID)
	if err != nil {
		t.Fatalf("failed to delete test cloud account: %v", err)
	}

	// Now delete must succeed
	if err := store.Delete(ctx, summary.ID); err != nil {
		t.Fatalf("Delete failed after removing referencing account: %v", err)
	}

	// Verify deleted
	_, err = store.GetSummary(ctx, summary.ID)
	if err != credentials.ErrNotFound {
		t.Fatalf("expected ErrNotFound after deletion, got %v", err)
	}
}
