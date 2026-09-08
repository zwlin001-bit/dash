package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// DefaultKeyID specifies current key identifier for envelope encryption.
	DefaultKeyID = "master-v1"
	nonceSize    = 12
	keySize      = 32
)

// Manager handles envelope encryption and decryption with AES-256-GCM.
type Manager struct {
	masterKey []byte
}

// NewManager loads the master key from a specified file path.
// Supports both 32 raw bytes or 64 hex-encoded characters.
func NewManager(path string) (*Manager, error) {
	if path == "" {
		return nil, errors.New("crypto: master key path is empty")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("crypto: failed to read master key from %s: %w", path, err)
	}

	trimmed := strings.TrimSpace(string(data))
	var key []byte

	if len(trimmed) == 64 {
		decoded, err := hex.DecodeString(trimmed)
		if err == nil && len(decoded) == keySize {
			key = decoded
		}
	}

	if key == nil {
		if len(data) == keySize {
			key = make([]byte, keySize)
			copy(key, data)
		} else if len(trimmed) == keySize {
			key = []byte(trimmed)
		} else {
			return nil, fmt.Errorf("crypto: invalid master key size in %s (expected 32 raw bytes or 64 hex chars, got %d bytes)", path, len(data))
		}
	}

	return &Manager{masterKey: key}, nil
}

// NewManagerWithKey constructs a Manager directly with a 32-byte master key.
func NewManagerWithKey(key []byte) (*Manager, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("crypto: master key must be 32 bytes, got %d bytes", len(key))
	}
	k := make([]byte, keySize)
	copy(k, key)
	return &Manager{masterKey: k}, nil
}

// Encrypt encrypts plaintext using AES-256-GCM with the master key.
// Returns ciphertext bytes, hex-encoded 12-byte nonce, and key identifier.
func (m *Manager) Encrypt(plaintext []byte) (ciphertext []byte, nonceHex string, keyID string, err error) {
	if m == nil || len(m.masterKey) != keySize {
		return nil, "", "", errors.New("crypto: manager not initialized with valid master key")
	}

	block, err := aes.NewCipher(m.masterKey)
	if err != nil {
		return nil, "", "", fmt.Errorf("crypto: create aes cipher failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, "", "", fmt.Errorf("crypto: create gcm failed: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, "", "", fmt.Errorf("crypto: generate nonce failed: %w", err)
	}

	sealed := gcm.Seal(nil, nonce, plaintext, nil)
	return sealed, hex.EncodeToString(nonce), DefaultKeyID, nil
}

// Decrypt decrypts ciphertext using AES-256-GCM with the master key and given nonce.
func (m *Manager) Decrypt(ciphertext []byte, nonceHex string, keyID string) ([]byte, error) {
	if m == nil || len(m.masterKey) != keySize {
		return nil, errors.New("crypto: manager not initialized with valid master key")
	}

	nonce, err := hex.DecodeString(strings.TrimSpace(nonceHex))
	if err != nil {
		return nil, fmt.Errorf("crypto: decode nonce hex failed: %w", err)
	}

	block, err := aes.NewCipher(m.masterKey)
	if err != nil {
		return nil, fmt.Errorf("crypto: create aes cipher failed: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: create gcm failed: %w", err)
	}

	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("crypto: invalid nonce size: %d, expected %d", len(nonce), gcm.NonceSize())
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm decryption failed: %w", err)
	}

	return plaintext, nil
}

// Mask returns a redacted representation of a secret string (e.g. "••••1234").
// If the string is 4 characters or shorter, it returns "••••".
func Mask(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "••••"
	}
	return "••••" + s[len(s)-4:]
}
