package crypto

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestCryptoRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}

	mgr, err := NewManagerWithKey(key)
	if err != nil {
		t.Fatalf("NewManagerWithKey failed: %v", err)
	}

	plain := []byte("secret-bot-token-123456:ABC-DEF-GHI")
	ciphertext, nonce, keyID, err := mgr.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	if keyID != DefaultKeyID {
		t.Errorf("expected keyID %s, got %s", DefaultKeyID, keyID)
	}
	if len(nonce) != 24 { // 12 bytes = 24 hex chars
		t.Errorf("expected 24 hex char nonce, got %d chars: %s", len(nonce), nonce)
	}

	decrypted, err := mgr.Decrypt(ciphertext, nonce, keyID)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}

	if !bytes.Equal(decrypted, plain) {
		t.Fatalf("decrypted %s does not match plaintext %s", string(decrypted), string(plain))
	}
}

func TestCryptoTamperedFails(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mgr, _ := NewManagerWithKey(key)

	plain := []byte("hello world")
	ciphertext, nonce, keyID, _ := mgr.Encrypt(plain)

	// Tamper with ciphertext
	ciphertext[0] ^= 0xff

	_, err := mgr.Decrypt(ciphertext, nonce, keyID)
	if err == nil {
		t.Fatal("expected decryption error on tampered ciphertext, got nil")
	}
}

func TestLoadMasterKeyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Test 64 hex characters (like setup.sh produces)
	hexPath := filepath.Join(tmpDir, "hex.key")
	rawKey := make([]byte, 32)
	_, _ = rand.Read(rawKey)
	hexStr := hex.EncodeToString(rawKey) + "\n"
	if err := os.WriteFile(hexPath, []byte(hexStr), 0600); err != nil {
		t.Fatal(err)
	}

	mgr1, err := NewManager(hexPath)
	if err != nil {
		t.Fatalf("NewManager with hex key failed: %v", err)
	}

	// 2. Test 32 raw bytes
	rawPath := filepath.Join(tmpDir, "raw.key")
	if err := os.WriteFile(rawPath, rawKey, 0600); err != nil {
		t.Fatal(err)
	}

	mgr2, err := NewManager(rawPath)
	if err != nil {
		t.Fatalf("NewManager with raw key failed: %v", err)
	}

	// Both should decrypt the same ciphertext
	plain := []byte("cross-manager-test")
	c1, n1, k1, err := mgr1.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	d2, err := mgr2.Decrypt(c1, n1, k1)
	if err != nil {
		t.Fatalf("mgr2 failed to decrypt mgr1 ciphertext: %v", err)
	}
	if !bytes.Equal(d2, plain) {
		t.Fatalf("mismatch: %s != %s", string(d2), string(plain))
	}

	// 3. Test missing file
	missingPath := filepath.Join(tmpDir, "nonexistent.key")
	_, err = NewManager(missingPath)
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

func TestMask(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"1", "••••"},
		{"1234", "••••"},
		{"12345", "••••2345"},
		{"my-very-long-secret-key-9999", "••••9999"},
	}

	for _, c := range cases {
		got := Mask(c.input)
		if got != c.expected {
			t.Errorf("Mask(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}
