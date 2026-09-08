package crypto_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"dash/internal/crypto"
)

func TestMasterKeyLoading(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. Missing file
	_, err := crypto.LoadMasterKey(filepath.Join(tmpDir, "nonexistent.key"))
	if err == nil {
		t.Fatal("expected error for missing master key, got nil")
	}

	// 2. 64-character hex key
	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatal(err)
	}
	hexKey := hex.EncodeToString(rawKey)
	hexPath := filepath.Join(tmpDir, "hex.key")
	if err := os.WriteFile(hexPath, []byte(hexKey+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loadedHexKey, err := crypto.LoadMasterKey(hexPath)
	if err != nil {
		t.Fatalf("failed to load hex key: %v", err)
	}
	if !bytes.Equal(loadedHexKey, rawKey) {
		t.Fatalf("loaded hex key does not match original: got %x, want %x", loadedHexKey, rawKey)
	}

	// 3. Raw 32 bytes
	rawPath := filepath.Join(tmpDir, "raw.key")
	if err := os.WriteFile(rawPath, rawKey, 0600); err != nil {
		t.Fatal(err)
	}
	loadedRawKey, err := crypto.LoadMasterKey(rawPath)
	if err != nil {
		t.Fatalf("failed to load raw key: %v", err)
	}
	if !bytes.Equal(loadedRawKey, rawKey) {
		t.Fatalf("loaded raw key does not match original: got %x, want %x", loadedRawKey, rawKey)
	}

	// 4. Invalid length
	invalidPath := filepath.Join(tmpDir, "invalid.key")
	if err := os.WriteFile(invalidPath, []byte("too-short"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.LoadMasterKey(invalidPath); err == nil {
		t.Fatal("expected error for invalid length key, got nil")
	}
}

func TestEnvelopeEncryptDecrypt(t *testing.T) {
	masterKey := make([]byte, 32)
	if _, err := rand.Read(masterKey); err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"access_key_id":"LTAI5t8abc123456","access_key_secret":"secret1234567890"}`)

	enc, err := crypto.EncryptEnvelope(masterKey, plaintext)
	if err != nil {
		t.Fatalf("EncryptEnvelope failed: %v", err)
	}

	if bytes.Contains(enc.Payload, plaintext) {
		t.Fatal("plaintext leaked inside encrypted payload!")
	}

	decrypted, err := crypto.DecryptEnvelope(masterKey, enc.Payload, enc.KeyID, enc.Nonce)
	if err != nil {
		t.Fatalf("DecryptEnvelope failed: %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted text does not match: got %s, want %s", decrypted, plaintext)
	}

	// Wrong master key fails
	wrongKey := make([]byte, 32)
	copy(wrongKey, masterKey)
	wrongKey[0] ^= 0xff
	if _, err := crypto.DecryptEnvelope(wrongKey, enc.Payload, enc.KeyID, enc.Nonce); err == nil {
		t.Fatal("expected decryption error with wrong master key, got nil")
	}

	// Tampered payload fails
	tamperedPayload := make([]byte, len(enc.Payload))
	copy(tamperedPayload, enc.Payload)
	tamperedPayload[len(tamperedPayload)-1] ^= 0xff
	if _, err := crypto.DecryptEnvelope(masterKey, tamperedPayload, enc.KeyID, enc.Nonce); err == nil {
		t.Fatal("expected decryption error with tampered payload, got nil")
	}

	// Tampered nonce fails
	tamperedNonce := enc.Nonce[:len(enc.Nonce)-2] + "ff"
	if _, err := crypto.DecryptEnvelope(masterKey, enc.Payload, enc.KeyID, tamperedNonce); err == nil {
		t.Fatal("expected decryption error with tampered nonce, got nil")
	}
}
