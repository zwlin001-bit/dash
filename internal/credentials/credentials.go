package credentials

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash/internal/crypto"
	"dash/internal/db"
	"dash/internal/ulid"
)

var (
	ErrNotFound = errors.New("credentials: not found")
	ErrInUse    = errors.New("credentials: in use by cloud account and cannot be deleted")
)

// Credential represents a row in the credentials table.
type Credential struct {
	ID          string
	Name        string
	CredKind    string
	EncPayload  []byte
	EncKeyID    string
	EncNonce    string
	CreatedAtMs int64
	UpdatedAtMs int64
}

// Summary represents safe-to-expose metadata with a masked fingerprint.
// Plaintext secrets MUST NEVER be returned.
type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CredKind    string `json:"cred_kind"`
	Fingerprint string `json:"fingerprint"`
	CreatedAtMs int64  `json:"created_at_ms"`
	UpdatedAtMs int64  `json:"updated_at_ms"`
}

// AliyunAKPayload is the plaintext JSON structure for Aliyun credentials.
type AliyunAKPayload struct {
	AccessKeyID     string `json:"access_key_id"`
	AccessKeySecret string `json:"access_key_secret"`
}

// Store handles encrypted credential persistence.
type Store struct {
	db        *db.DB
	masterKey []byte
}

func NewStore(d *db.DB, masterKey []byte) *Store {
	return &Store{
		db:        d,
		masterKey: masterKey,
	}
}

// MaskFingerprint generates a masked fingerprint (e.g. LTAI****3f2a).
func MaskFingerprint(credKind string, plaintext []byte) string {
	if credKind == "aliyun_ak" {
		var ak AliyunAKPayload
		if err := json.Unmarshal(plaintext, &ak); err == nil && ak.AccessKeyID != "" {
			s := ak.AccessKeyID
			if len(s) > 8 {
				return s[:4] + "****" + s[len(s)-4:]
			}
			if len(s) > 4 {
				return s[:2] + "****" + s[len(s)-2:]
			}
			return "****"
		}
	}
	return "********"
}

// Create encrypts the plaintext using envelope encryption and persists the credential.
func (s *Store) Create(ctx context.Context, name, credKind string, plaintext []byte) (*Summary, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("credentials: name cannot be empty")
	}
	credKind = strings.TrimSpace(credKind)
	if credKind == "" {
		return nil, errors.New("credentials: cred_kind cannot be empty")
	}
	if len(plaintext) == 0 {
		return nil, errors.New("credentials: empty payload")
	}

	enc, err := crypto.EncryptEnvelope(s.masterKey, plaintext)
	if err != nil {
		return nil, fmt.Errorf("credentials: encrypt failed: %w", err)
	}

	id := ulid.New()
	now := time.Now().UnixMilli()

	q := `INSERT INTO credentials (id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.db.Exec(ctx, q, id, name, credKind, enc.Payload, enc.KeyID, enc.Nonce, now, now)
	if err != nil {
		return nil, fmt.Errorf("credentials: insert failed: %w", err)
	}

	return &Summary{
		ID:          id,
		Name:        name,
		CredKind:    credKind,
		Fingerprint: MaskFingerprint(credKind, plaintext),
		CreatedAtMs: now,
		UpdatedAtMs: now,
	}, nil
}

// List returns safe summaries of all credentials.
func (s *Store) List(ctx context.Context) ([]Summary, error) {
	q := `SELECT id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms
FROM credentials ORDER BY created_at_ms DESC`
	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("credentials: query failed: %w", err)
	}
	defer rows.Close()

	var list []Summary
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.ID, &c.Name, &c.CredKind, &c.EncPayload, &c.EncKeyID, &c.EncNonce, &c.CreatedAtMs, &c.UpdatedAtMs); err != nil {
			return nil, fmt.Errorf("credentials: scan failed: %w", err)
		}

		fp := "********"
		if len(s.masterKey) == 32 {
			if pt, err := crypto.DecryptEnvelope(s.masterKey, c.EncPayload, c.EncKeyID, c.EncNonce); err == nil {
				fp = MaskFingerprint(c.CredKind, pt)
			}
		}

		list = append(list, Summary{
			ID:          c.ID,
			Name:        c.Name,
			CredKind:    c.CredKind,
			Fingerprint: fp,
			CreatedAtMs: c.CreatedAtMs,
			UpdatedAtMs: c.UpdatedAtMs,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if list == nil {
		list = []Summary{}
	}
	return list, nil
}

// GetSummary returns safe metadata for a specific credential ID.
func (s *Store) GetSummary(ctx context.Context, id string) (*Summary, error) {
	q := `SELECT id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms
FROM credentials WHERE id = ?`
	var c Credential
	err := s.db.QueryRow(ctx, q, id).Scan(&c.ID, &c.Name, &c.CredKind, &c.EncPayload, &c.EncKeyID, &c.EncNonce, &c.CreatedAtMs, &c.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("credentials: get failed: %w", err)
	}

	fp := "********"
	if len(s.masterKey) == 32 {
		if pt, err := crypto.DecryptEnvelope(s.masterKey, c.EncPayload, c.EncKeyID, c.EncNonce); err == nil {
			fp = MaskFingerprint(c.CredKind, pt)
		}
	}

	return &Summary{
		ID:          c.ID,
		Name:        c.Name,
		CredKind:    c.CredKind,
		Fingerprint: fp,
		CreatedAtMs: c.CreatedAtMs,
		UpdatedAtMs: c.UpdatedAtMs,
	}, nil
}

// GetDecrypted decrypts and returns the plaintext credential payload.
// MUST ONLY be called for internal dispatch to providers over Unix socket RPC.
func (s *Store) GetDecrypted(ctx context.Context, id string) (*Credential, []byte, error) {
	q := `SELECT id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms
FROM credentials WHERE id = ?`
	var c Credential
	err := s.db.QueryRow(ctx, q, id).Scan(&c.ID, &c.Name, &c.CredKind, &c.EncPayload, &c.EncKeyID, &c.EncNonce, &c.CreatedAtMs, &c.UpdatedAtMs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("credentials: get failed: %w", err)
	}

	pt, err := crypto.DecryptEnvelope(s.masterKey, c.EncPayload, c.EncKeyID, c.EncNonce)
	if err != nil {
		return nil, nil, fmt.Errorf("credentials: decrypt failed: %w", err)
	}

	return &c, pt, nil
}

// Delete removes a credential, strictly checking whether it is referenced by any cloud account.
func (s *Store) Delete(ctx context.Context, id string) error {
	// Rule: 删凭据前检查有没有 cloud_accounts 在用，有就拒绝
	var count int
	err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM cloud_accounts WHERE credential_id = ?", id).Scan(&count)
	if err != nil {
		return fmt.Errorf("credentials: check cloud_accounts usage failed: %w", err)
	}
	if count > 0 {
		return ErrInUse
	}

	res, err := s.db.Exec(ctx, "DELETE FROM credentials WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("credentials: delete failed: %w", err)
	}
	affected, err := res.RowsAffected()
	if err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}
