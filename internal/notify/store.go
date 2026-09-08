package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"dash/internal/crypto"
	"dash/internal/db"
	"dash/internal/ulid"
)

// Store provides persistence methods for notify channels, rules, credentials and deliveries.
type Store struct {
	db        *db.DB
	cryptoMgr *crypto.Manager
}

// NewStore initializes a notify Store.
func NewStore(database *db.DB, cryptoMgr *crypto.Manager) *Store {
	return &Store{
		db:        database,
		cryptoMgr: cryptoMgr,
	}
}

// CreateChannel stores a new notification channel, encrypting any provided secret in credentials.
func (s *Store) CreateChannel(ctx context.Context, name, kind, secret, configJSON string) (*Channel, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("notify: channel name is required")
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "telegram" && kind != "webhook" {
		return nil, fmt.Errorf("notify: unsupported channel kind %q, must be telegram or webhook", kind)
	}

	nowMs := time.Now().UnixMilli()
	channelID := ulid.New()
	var credentialID string

	// Store secret in credentials table with envelope encryption if provided
	secret = strings.TrimSpace(secret)
	if secret != "" {
		if s.cryptoMgr == nil {
			return nil, errors.New("notify: crypto manager is not configured, cannot encrypt secret")
		}
		ciphertext, nonce, keyID, err := s.cryptoMgr.Encrypt([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("notify: encrypt secret failed: %w", err)
		}
		credentialID = ulid.New()
		credKind := kind + "_secret"
		if kind == "telegram" {
			credKind = "telegram_bot"
		}

		insertCredSQL := `INSERT INTO credentials (
			id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`

		_, err = s.db.Exec(ctx, insertCredSQL, credentialID, name, credKind, ciphertext, keyID, nonce, nowMs, nowMs)
		if err != nil {
			return nil, fmt.Errorf("notify: insert credential failed: %w", err)
		}
	}

	insertChanSQL := `INSERT INTO notify_channels (
		id, name, channel_kind, credential_id, config_json, is_enabled, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, 1, ?, ?)`

	var credIDVal any
	if credentialID != "" {
		credIDVal = credentialID
	}

	_, err := s.db.Exec(ctx, insertChanSQL, channelID, name, kind, credIDVal, configJSON, nowMs, nowMs)
	if err != nil {
		return nil, fmt.Errorf("notify: insert channel failed: %w", err)
	}

	ch := &Channel{
		ID:           channelID,
		Name:         name,
		Kind:         kind,
		CredentialID: credentialID,
		ConfigJSON:   configJSON,
		IsEnabled:    true,
		CreatedAtMs:  nowMs,
		UpdatedAtMs:  nowMs,
		MaskedSecret: crypto.Mask(secret),
	}
	return ch, nil
}

// UpdateChannel updates channel metadata, config, and optionally updates the secret.
func (s *Store) UpdateChannel(ctx context.Context, id, name string, isEnabled bool, secret, configJSON string) (*Channel, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	cur, err := s.GetChannel(ctx, id)
	if err != nil {
		return nil, err
	}

	nowMs := time.Now().UnixMilli()
	if name != "" {
		cur.Name = strings.TrimSpace(name)
	}
	cur.IsEnabled = isEnabled
	if configJSON != "" {
		cur.ConfigJSON = configJSON
	}

	secret = strings.TrimSpace(secret)
	if secret != "" {
		if s.cryptoMgr == nil {
			return nil, errors.New("notify: crypto manager is not configured, cannot encrypt secret")
		}
		ciphertext, nonce, keyID, err := s.cryptoMgr.Encrypt([]byte(secret))
		if err != nil {
			return nil, fmt.Errorf("notify: encrypt secret failed: %w", err)
		}

		if cur.CredentialID != "" {
			updCredSQL := `UPDATE credentials SET enc_payload = ?, enc_key_id = ?, enc_nonce = ?, updated_at_ms = ? WHERE id = ?`
			_, err = s.db.Exec(ctx, updCredSQL, ciphertext, keyID, nonce, nowMs, cur.CredentialID)
			if err != nil {
				return nil, fmt.Errorf("notify: update credential failed: %w", err)
			}
		} else {
			credID := ulid.New()
			credKind := cur.Kind + "_secret"
			if cur.Kind == "telegram" {
				credKind = "telegram_bot"
			}
			insCredSQL := `INSERT INTO credentials (
				id, name, cred_kind, enc_payload, enc_key_id, enc_nonce, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
			_, err = s.db.Exec(ctx, insCredSQL, credID, cur.Name, credKind, ciphertext, keyID, nonce, nowMs, nowMs)
			if err != nil {
				return nil, fmt.Errorf("notify: insert credential failed: %w", err)
			}
			cur.CredentialID = credID
		}
		cur.MaskedSecret = crypto.Mask(secret)
	}

	updChanSQL := `UPDATE notify_channels SET
		name = ?, is_enabled = ?, config_json = ?, credential_id = ?, updated_at_ms = ?
		WHERE id = ?`

	var credIDVal any
	if cur.CredentialID != "" {
		credIDVal = cur.CredentialID
	}
	enabledInt := 0
	if cur.IsEnabled {
		enabledInt = 1
	}

	_, err = s.db.Exec(ctx, updChanSQL, cur.Name, enabledInt, cur.ConfigJSON, credIDVal, nowMs, id)
	if err != nil {
		return nil, fmt.Errorf("notify: update channel failed: %w", err)
	}

	cur.UpdatedAtMs = nowMs
	return cur, nil
}

// DeleteChannel removes a channel and its encrypted credential.
func (s *Store) DeleteChannel(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("notify: db is nil")
	}

	cur, err := s.GetChannel(ctx, id)
	if err != nil {
		return err
	}

	if cur.CredentialID != "" {
		_, _ = s.db.Exec(ctx, "DELETE FROM credentials WHERE id = ?", cur.CredentialID)
	}

	// Delete associated rules
	_, _ = s.db.Exec(ctx, "DELETE FROM notify_rules WHERE notify_channel_id = ?", id)

	_, err = s.db.Exec(ctx, "DELETE FROM notify_channels WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("notify: delete channel failed: %w", err)
	}
	return nil
}

// GetChannel retrieves a single channel by ID.
func (s *Store) GetChannel(ctx context.Context, id string) (*Channel, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	q := `SELECT id, name, channel_kind, credential_id, config_json, is_enabled, created_at_ms, updated_at_ms
		FROM notify_channels WHERE id = ?`

	var ch Channel
	var credID, configJSON sql.NullString
	var enabledInt int

	err := s.db.QueryRow(ctx, q, id).Scan(
		&ch.ID,
		&ch.Name,
		&ch.Kind,
		&credID,
		&configJSON,
		&enabledInt,
		&ch.CreatedAtMs,
		&ch.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("channel %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("notify: query channel failed: %w", err)
	}

	if credID.Valid {
		ch.CredentialID = credID.String
	}
	if configJSON.Valid {
		ch.ConfigJSON = configJSON.String
	}
	ch.IsEnabled = (enabledInt == 1)

	// Populate masked secret
	if ch.CredentialID != "" {
		secret, err := s.GetDecryptedSecret(ctx, ch.CredentialID)
		if err == nil && secret != "" {
			ch.MaskedSecret = crypto.Mask(secret)
		}
	}

	return &ch, nil
}

// ListChannels returns all configured channels.
func (s *Store) ListChannels(ctx context.Context) ([]*Channel, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	q := `SELECT id, name, channel_kind, credential_id, config_json, is_enabled, created_at_ms, updated_at_ms
		FROM notify_channels ORDER BY created_at_ms ASC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("notify: list channels failed: %w", err)
	}
	defer rows.Close()

	var channels []*Channel
	for rows.Next() {
		var ch Channel
		var credID, configJSON sql.NullString
		var enabledInt int

		err := rows.Scan(
			&ch.ID,
			&ch.Name,
			&ch.Kind,
			&credID,
			&configJSON,
			&enabledInt,
			&ch.CreatedAtMs,
			&ch.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("notify: scan channel failed: %w", err)
		}

		if credID.Valid {
			ch.CredentialID = credID.String
		}
		if configJSON.Valid {
			ch.ConfigJSON = configJSON.String
		}
		ch.IsEnabled = (enabledInt == 1)

		if ch.CredentialID != "" {
			if secret, err := s.GetDecryptedSecret(ctx, ch.CredentialID); err == nil && secret != "" {
				ch.MaskedSecret = crypto.Mask(secret)
			}
		}

		channels = append(channels, &ch)
	}

	if channels == nil {
		channels = []*Channel{}
	}
	return channels, rows.Err()
}

// GetDecryptedSecret fetches and decrypts a secret from the credentials table.
func (s *Store) GetDecryptedSecret(ctx context.Context, credentialID string) (string, error) {
	if s.db == nil || credentialID == "" {
		return "", nil
	}
	if s.cryptoMgr == nil {
		return "", errors.New("notify: crypto manager is not configured")
	}

	q := `SELECT enc_payload, enc_key_id, enc_nonce FROM credentials WHERE id = ?`
	var payload []byte
	var keyID, nonce string

	err := s.db.QueryRow(ctx, q, credentialID).Scan(&payload, &keyID, &nonce)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return "", fmt.Errorf("credential %s not found", credentialID)
	}
	if err != nil {
		return "", fmt.Errorf("query credential failed: %w", err)
	}

	decrypted, err := s.cryptoMgr.Decrypt(payload, nonce, keyID)
	if err != nil {
		return "", fmt.Errorf("decrypt credential failed: %w", err)
	}

	return string(decrypted), nil
}

// CreateRule stores a new notification rule.
func (s *Store) CreateRule(ctx context.Context, r *Rule) (*Rule, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}
	if r.Name == "" {
		return nil, errors.New("notify: rule name is required")
	}
	if r.EventPattern == "" {
		return nil, errors.New("notify: event_pattern is required")
	}
	if r.NotifyChannelID == "" {
		return nil, errors.New("notify: notify_channel_id is required")
	}
	if r.MinSeverity == "" {
		r.MinSeverity = "info"
	}

	nowMs := time.Now().UnixMilli()
	if r.ID == "" {
		r.ID = ulid.New()
	}
	r.CreatedAtMs = nowMs
	r.UpdatedAtMs = nowMs

	enabledInt := 0
	if r.IsEnabled {
		enabledInt = 1
	}

	var tmplNameVal, quietStartVal, quietEndVal any
	if r.TemplateName != "" {
		tmplNameVal = r.TemplateName
	}
	if r.QuietStartMin != nil {
		quietStartVal = *r.QuietStartMin
	}
	if r.QuietEndMin != nil {
		quietEndVal = *r.QuietEndMin
	}

	insertSQL := `INSERT INTO notify_rules (
		id, name, is_enabled, event_pattern, min_severity, notify_channel_id,
		template_name, throttle_s, quiet_start_min, quiet_end_min, display_order,
		created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(ctx, insertSQL,
		r.ID,
		r.Name,
		enabledInt,
		r.EventPattern,
		r.MinSeverity,
		r.NotifyChannelID,
		tmplNameVal,
		r.ThrottleS,
		quietStartVal,
		quietEndVal,
		r.DisplayOrder,
		nowMs,
		nowMs,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: insert rule failed: %w", err)
	}

	return r, nil
}

// UpdateRule updates an existing notification rule.
func (s *Store) UpdateRule(ctx context.Context, r *Rule) (*Rule, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	nowMs := time.Now().UnixMilli()
	r.UpdatedAtMs = nowMs

	enabledInt := 0
	if r.IsEnabled {
		enabledInt = 1
	}

	var tmplNameVal, quietStartVal, quietEndVal any
	if r.TemplateName != "" {
		tmplNameVal = r.TemplateName
	}
	if r.QuietStartMin != nil {
		quietStartVal = *r.QuietStartMin
	}
	if r.QuietEndMin != nil {
		quietEndVal = *r.QuietEndMin
	}

	updSQL := `UPDATE notify_rules SET
		name = ?, is_enabled = ?, event_pattern = ?, min_severity = ?,
		notify_channel_id = ?, template_name = ?, throttle_s = ?,
		quiet_start_min = ?, quiet_end_min = ?, display_order = ?, updated_at_ms = ?
		WHERE id = ?`

	_, err := s.db.Exec(ctx, updSQL,
		r.Name,
		enabledInt,
		r.EventPattern,
		r.MinSeverity,
		r.NotifyChannelID,
		tmplNameVal,
		r.ThrottleS,
		quietStartVal,
		quietEndVal,
		r.DisplayOrder,
		nowMs,
		r.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: update rule failed: %w", err)
	}

	return r, nil
}

// DeleteRule removes a rule by ID.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("notify: db is nil")
	}
	_, err := s.db.Exec(ctx, "DELETE FROM notify_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("notify: delete rule failed: %w", err)
	}
	return nil
}

// GetRule retrieves a rule by ID.
func (s *Store) GetRule(ctx context.Context, id string) (*Rule, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	q := `SELECT id, name, is_enabled, event_pattern, min_severity, notify_channel_id,
		template_name, throttle_s, quiet_start_min, quiet_end_min, display_order,
		created_at_ms, updated_at_ms
		FROM notify_rules WHERE id = ?`

	var r Rule
	var tmplName sql.NullString
	var quietStart, quietEnd sql.NullInt64
	var enabledInt int

	err := s.db.QueryRow(ctx, q, id).Scan(
		&r.ID,
		&r.Name,
		&enabledInt,
		&r.EventPattern,
		&r.MinSeverity,
		&r.NotifyChannelID,
		&tmplName,
		&r.ThrottleS,
		&quietStart,
		&quietEnd,
		&r.DisplayOrder,
		&r.CreatedAtMs,
		&r.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("rule %s not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("notify: query rule failed: %w", err)
	}

	r.IsEnabled = (enabledInt == 1)
	if tmplName.Valid {
		r.TemplateName = tmplName.String
	}
	if quietStart.Valid {
		v := int(quietStart.Int64)
		r.QuietStartMin = &v
	}
	if quietEnd.Valid {
		v := int(quietEnd.Int64)
		r.QuietEndMin = &v
	}

	return &r, nil
}

// ListRules lists all notification rules.
func (s *Store) ListRules(ctx context.Context) ([]*Rule, error) {
	if s.db == nil {
		return nil, errors.New("notify: db is nil")
	}

	q := `SELECT id, name, is_enabled, event_pattern, min_severity, notify_channel_id,
		template_name, throttle_s, quiet_start_min, quiet_end_min, display_order,
		created_at_ms, updated_at_ms
		FROM notify_rules ORDER BY display_order ASC, created_at_ms ASC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("notify: list rules failed: %w", err)
	}
	defer rows.Close()

	var rules []*Rule
	for rows.Next() {
		var r Rule
		var tmplName sql.NullString
		var quietStart, quietEnd sql.NullInt64
		var enabledInt int

		err := rows.Scan(
			&r.ID,
			&r.Name,
			&enabledInt,
			&r.EventPattern,
			&r.MinSeverity,
			&r.NotifyChannelID,
			&tmplName,
			&r.ThrottleS,
			&quietStart,
			&quietEnd,
			&r.DisplayOrder,
			&r.CreatedAtMs,
			&r.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("notify: scan rule failed: %w", err)
		}

		r.IsEnabled = (enabledInt == 1)
		if tmplName.Valid {
			r.TemplateName = tmplName.String
		}
		if quietStart.Valid {
			v := int(quietStart.Int64)
			r.QuietStartMin = &v
		}
		if quietEnd.Valid {
			v := int(quietEnd.Int64)
			r.QuietEndMin = &v
		}

		rules = append(rules, &r)
	}

	if rules == nil {
		rules = []*Rule{}
	}
	return rules, rows.Err()
}

// CreateDelivery inserts a delivery attempt record.
func (s *Store) CreateDelivery(ctx context.Context, d *Delivery) error {
	if s.db == nil {
		return errors.New("notify: db is nil")
	}

	nowMs := time.Now().UnixMilli()
	if d.ID == "" {
		d.ID = ulid.New()
	}
	d.CreatedAtMs = nowMs
	d.UpdatedAtMs = nowMs

	var ruleIDVal, lastErrVal, renderedVal, sentAtVal any
	if d.RuleID != "" {
		ruleIDVal = d.RuleID
	}
	if d.LastError != "" {
		lastErrVal = d.LastError
	}
	if d.RenderedText != "" {
		renderedVal = d.RenderedText
	}
	if d.SentAtMs != nil {
		sentAtVal = *d.SentAtMs
	}

	insertSQL := `INSERT INTO notify_deliveries (
		id, event_id, channel_id, rule_id, state, attempt, last_error,
		rendered_text, sent_at_ms, created_at_ms, updated_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.Exec(ctx, insertSQL,
		d.ID,
		d.EventID,
		d.ChannelID,
		ruleIDVal,
		d.State,
		d.Attempt,
		lastErrVal,
		renderedVal,
		sentAtVal,
		nowMs,
		nowMs,
	)
	if err != nil {
		return fmt.Errorf("notify: insert delivery failed: %w", err)
	}

	return nil
}

// UpdateDeliveryState updates delivery state, attempt count, last error, and sent timestamp.
func (s *Store) UpdateDeliveryState(ctx context.Context, id, state string, attempt int, lastError string, sentAtMs *int64) error {
	if s.db == nil {
		return errors.New("notify: db is nil")
	}

	nowMs := time.Now().UnixMilli()
	var lastErrVal, sentAtVal any
	if lastError != "" {
		lastErrVal = lastError
	}
	if sentAtMs != nil {
		sentAtVal = *sentAtMs
	}

	updSQL := `UPDATE notify_deliveries SET
		state = ?, attempt = ?, last_error = ?, sent_at_ms = ?, updated_at_ms = ?
		WHERE id = ?`

	_, err := s.db.Exec(ctx, updSQL, state, attempt, lastErrVal, sentAtVal, nowMs, id)
	if err != nil {
		return fmt.Errorf("notify: update delivery state failed: %w", err)
	}

	return nil
}

// ListDeliveries retrieves delivery history with optional filtering and pagination.
func (s *Store) ListDeliveries(ctx context.Context, f DeliveryFilter) ([]*Delivery, int, error) {
	if s.db == nil {
		return nil, 0, errors.New("notify: db is nil")
	}

	var whereClauses []string
	var args []any

	if f.EventID != "" {
		whereClauses = append(whereClauses, "event_id = ?")
		args = append(args, f.EventID)
	}
	if f.ChannelID != "" {
		whereClauses = append(whereClauses, "channel_id = ?")
		args = append(args, f.ChannelID)
	}
	if f.State != "" {
		whereClauses = append(whereClauses, "state = ?")
		args = append(args, f.State)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	countSQL := "SELECT COUNT(*) FROM notify_deliveries" + whereSQL
	var total int
	if err := s.db.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("notify: count deliveries failed: %w", err)
	}

	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	selectSQL := `SELECT id, event_id, channel_id, rule_id, state, attempt,
		last_error, rendered_text, sent_at_ms, created_at_ms, updated_at_ms
		FROM notify_deliveries` + whereSQL + " ORDER BY created_at_ms DESC, id DESC"

	rows, err := s.db.QueryPage(ctx, selectSQL, limit, offset, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("notify: query page failed: %w", err)
	}
	defer rows.Close()

	var deliveries []*Delivery
	for rows.Next() {
		var d Delivery
		var ruleID, lastErr, renderedText sql.NullString
		var sentAt sql.NullInt64

		err := rows.Scan(
			&d.ID,
			&d.EventID,
			&d.ChannelID,
			&ruleID,
			&d.State,
			&d.Attempt,
			&lastErr,
			&renderedText,
			&sentAt,
			&d.CreatedAtMs,
			&d.UpdatedAtMs,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("notify: scan delivery failed: %w", err)
		}

		if ruleID.Valid {
			d.RuleID = ruleID.String
		}
		if lastErr.Valid {
			d.LastError = lastErr.String
		}
		if renderedText.Valid {
			d.RenderedText = renderedText.String
		}
		if sentAt.Valid {
			v := sentAt.Int64
			d.SentAtMs = &v
		}

		deliveries = append(deliveries, &d)
	}

	if deliveries == nil {
		deliveries = []*Delivery{}
	}

	return deliveries, total, rows.Err()
}
