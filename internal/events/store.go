package events

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dash/internal/db"
	"dash/internal/logx"
)

const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewULID generates a standard 26-character Crockford Base32 ULID.
func NewULID() string {
	var b [26]byte
	now := time.Now().UnixMilli()

	// 48-bit timestamp (10 characters)
	b[0] = crockfordAlphabet[(now>>45)&31]
	b[1] = crockfordAlphabet[(now>>40)&31]
	b[2] = crockfordAlphabet[(now>>35)&31]
	b[3] = crockfordAlphabet[(now>>30)&31]
	b[4] = crockfordAlphabet[(now>>25)&31]
	b[5] = crockfordAlphabet[(now>>20)&31]
	b[6] = crockfordAlphabet[(now>>15)&31]
	b[7] = crockfordAlphabet[(now>>10)&31]
	b[8] = crockfordAlphabet[(now>>5)&31]
	b[9] = crockfordAlphabet[now&31]

	// 80-bit random entropy (16 characters)
	var entropy [10]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		// Fallback to time-based entropy if rand fails
		tNano := time.Now().UnixNano()
		for i := 0; i < 10; i++ {
			entropy[i] = byte((tNano >> (i * 6)) & 0xff)
		}
	}

	b[10] = crockfordAlphabet[(entropy[0]>>3)&31]
	b[11] = crockfordAlphabet[((entropy[0]&7)<<2)|(entropy[1]>>6)]
	b[12] = crockfordAlphabet[(entropy[1]>>1)&31]
	b[13] = crockfordAlphabet[((entropy[1]&1)<<4)|(entropy[2]>>4)]
	b[14] = crockfordAlphabet[((entropy[2]&15)<<1)|(entropy[3]>>7)]
	b[15] = crockfordAlphabet[(entropy[3]>>2)&31]
	b[16] = crockfordAlphabet[((entropy[3]&3)<<3)|(entropy[4]>>5)]
	b[17] = crockfordAlphabet[entropy[4]&31]
	b[18] = crockfordAlphabet[(entropy[5]>>3)&31]
	b[19] = crockfordAlphabet[((entropy[5]&7)<<2)|(entropy[6]>>6)]
	b[20] = crockfordAlphabet[(entropy[6]>>1)&31]
	b[21] = crockfordAlphabet[((entropy[6]&1)<<4)|(entropy[7]>>4)]
	b[22] = crockfordAlphabet[((entropy[7]&15)<<1)|(entropy[8]>>7)]
	b[23] = crockfordAlphabet[(entropy[8]>>2)&31]
	b[24] = crockfordAlphabet[((entropy[8]&3)<<3)|(entropy[9]>>5)]
	b[25] = crockfordAlphabet[entropy[9]&31]

	return string(b[:])
}

// EventRecord represents an event record stored in the events table (10-schema-spec.md §4).
type EventRecord struct {
	ID           string         `json:"id"`
	Type         string         `json:"event_type"`
	Severity     string         `json:"severity"`
	SourceModule string         `json:"source_module"`
	TargetKind   string         `json:"target_kind,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	Title        string         `json:"title"`
	Payload      map[string]any `json:"payload,omitempty"`
	PayloadJSON  string         `json:"payload_json,omitempty"`
	DedupKey     string         `json:"dedup_key,omitempty"`
	IsRead       bool           `json:"is_read"`
	OccurredAtMs int64          `json:"occurred_at_ms"`
	CreatedAtMs  int64          `json:"created_at_ms"`
}

// Filter defines search criteria for querying events.
type Filter struct {
	EventType string
	Severity  string
	TargetID  string
	IsRead    *bool
	FromMs    int64
	ToMs      int64
	Limit     int
	Offset    int
}

// MarkReadRequest specifies parameters for marking events read.
type MarkReadRequest struct {
	IDs      []string `json:"ids,omitempty"`
	BeforeMs int64    `json:"before_ms,omitempty"`
	All      bool     `json:"all,omitempty"`
}

// Store handles async event persistence, event retrieval and event type management.
type Store struct {
	db   *db.DB
	ch   chan Event
	stop chan struct{}
	wg   sync.WaitGroup
	mu   sync.RWMutex
}

// NewStore initializes a new event store with a bounded channel.
func NewStore(database *db.DB, bufferSize int) *Store {
	if bufferSize <= 0 {
		bufferSize = 2048
	}
	s := &Store{
		db:   database,
		ch:   make(chan Event, bufferSize),
		stop: make(chan struct{}),
	}
	return s
}

// Start launches the background consumer worker.
func (s *Store) Start() {
	s.wg.Add(1)
	go s.worker()
}

// Stop signals the background worker to shut down and waits for remaining channel items.
func (s *Store) Stop() {
	s.mu.Lock()
	select {
	case <-s.stop:
		s.mu.Unlock()
		return
	default:
		close(s.stop)
	}
	s.mu.Unlock()

	s.wg.Wait()
}

// Enqueue delivers an event to the background channel non-blockingly.
// If buffer is full, logs warning and drops without blocking caller.
func (s *Store) Enqueue(e Event) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	select {
	case <-s.stop:
		logx.Warn(fmt.Sprintf("events: store is stopped, dropping event %s", e.Type))
		return
	default:
	}

	select {
	case s.ch <- e:
	default:
		logx.Warn(fmt.Sprintf("events: queue buffer full, dropping event %s (title=%s)", e.Type, e.Title))
	}
}

// worker drains events from the channel and persists them based on policy.
func (s *Store) worker() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stop:
			// Drain remaining events in channel
			for {
				select {
				case e := <-s.ch:
					s.processEvent(e)
				default:
					return
				}
			}
		case e := <-s.ch:
			s.processEvent(e)
		}
	}
}

// processEvent applies disposition rules and writes to database.
func (s *Store) processEvent(e Event) {
	severity, disposition := GetPolicy(e.Type)

	// 1. Check disposition
	if disposition == "drop" {
		return
	}

	if s.db == nil {
		logx.Warn(fmt.Sprintf("events: database connection not available, cannot persist event %s", e.Type))
		return
	}

	// 2. Prepare event record
	nowMs := time.Now().UnixMilli()
	occurredAt := e.OccurredAt
	if occurredAt == 0 {
		occurredAt = nowMs
	}

	var payloadJSON string
	if len(e.Payload) > 0 {
		if b, err := json.Marshal(e.Payload); err == nil {
			payloadJSON = string(b)
		}
	}

	id := NewULID()

	// 3. Insert into database
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	insertSQL := `INSERT INTO events (
		id, event_type, severity, source_module, target_kind, target_id,
		title, payload_json, dedup_key, is_read, occurred_at_ms, created_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)`

	var targetKindVal, targetIDVal, payloadJSONVal, dedupKeyVal any
	if e.TargetKind != "" {
		targetKindVal = e.TargetKind
	}
	if e.TargetID != "" {
		targetIDVal = e.TargetID
	}
	if payloadJSON != "" {
		payloadJSONVal = payloadJSON
	}
	if e.DedupKey != "" {
		dedupKeyVal = e.DedupKey
	}

	_, err := s.db.Exec(ctx, insertSQL,
		id,
		e.Type,
		severity,
		e.Source,
		targetKindVal,
		targetIDVal,
		e.Title,
		payloadJSONVal,
		dedupKeyVal,
		occurredAt,
		nowMs,
	)
	if err != nil {
		// Non-blocking log per P1-20: 事件写不进去只能打日志，绝不允许让业务失败
		logx.Error(fmt.Sprintf("events: failed to write event %s (%s) to database: %v", e.Type, id, err))
	}
}

// SyncBuiltinTypes synchronizes registered built-in types to event_types table.
// Preserves existing user overrides for severity and disposition.
func (s *Store) SyncBuiltinTypes(ctx context.Context) error {
	if s.db == nil {
		return errors.New("events: database is nil")
	}

	types := GetRegisteredTypes()
	nowMs := time.Now().UnixMilli()

	for _, t := range types {
		var existingSeverity, existingDisposition string
		querySQL := "SELECT severity, disposition FROM event_types WHERE event_type = ?"
		err := s.db.QueryRow(ctx, querySQL, t.Type).Scan(&existingSeverity, &existingDisposition)

		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			// Insert new type
			insSQL := `INSERT INTO event_types (
				event_type, display_name, default_severity, severity,
				default_disposition, disposition, description, is_builtin, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)`

			var descVal any
			if t.Description != "" {
				descVal = t.Description
			}

			_, err = s.db.Exec(ctx, insSQL,
				t.Type,
				t.DisplayName,
				t.DefaultSeverity,
				t.DefaultSeverity,
				t.DefaultDisposition,
				t.DefaultDisposition,
				descVal,
				nowMs,
			)
			if err != nil {
				logx.Warn(fmt.Sprintf("events: failed to register event_type %s: %v", t.Type, err))
				continue
			}
			UpdatePolicyCache(t.Type, t.DefaultSeverity, t.DefaultDisposition)
		} else if err == nil {
			// Update defaults and metadata, but preserve existing user customizations
			updSQL := `UPDATE event_types SET
				display_name = ?, default_severity = ?, default_disposition = ?,
				description = ?, is_builtin = 1, updated_at_ms = ?
				WHERE event_type = ?`

			var descVal any
			if t.Description != "" {
				descVal = t.Description
			}

			_, err = s.db.Exec(ctx, updSQL,
				t.DisplayName,
				t.DefaultSeverity,
				t.DefaultDisposition,
				descVal,
				nowMs,
				t.Type,
			)
			if err != nil {
				logx.Warn(fmt.Sprintf("events: failed to update event_type metadata %s: %v", t.Type, err))
			}
			// Update in-memory cache with persisted user policy
			UpdatePolicyCache(t.Type, existingSeverity, existingDisposition)
		} else {
			logx.Warn(fmt.Sprintf("events: check event_type %s failed: %v", t.Type, err))
		}
	}

	return nil
}

// ListEvents queries events with filtering and pagination.
func (s *Store) ListEvents(ctx context.Context, f Filter) ([]EventRecord, int, error) {
	if s.db == nil {
		return nil, 0, errors.New("events: db is nil")
	}

	var whereClauses []string
	var args []any

	if f.EventType != "" {
		whereClauses = append(whereClauses, "event_type = ?")
		args = append(args, f.EventType)
	}
	if f.Severity != "" {
		whereClauses = append(whereClauses, "severity = ?")
		args = append(args, f.Severity)
	}
	if f.TargetID != "" {
		whereClauses = append(whereClauses, "target_id = ?")
		args = append(args, f.TargetID)
	}
	if f.IsRead != nil {
		whereClauses = append(whereClauses, "is_read = ?")
		if *f.IsRead {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if f.FromMs > 0 {
		whereClauses = append(whereClauses, "occurred_at_ms >= ?")
		args = append(args, f.FromMs)
	}
	if f.ToMs > 0 {
		whereClauses = append(whereClauses, "occurred_at_ms <= ?")
		args = append(args, f.ToMs)
	}

	whereSQL := ""
	if len(whereClauses) > 0 {
		whereSQL = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// 1. Total count
	countQuery := "SELECT COUNT(*) FROM events" + whereSQL
	var total int
	if err := s.db.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("events: count query failed: %w", err)
	}

	// 2. Query items
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

	selectSQL := `SELECT id, event_type, severity, source_module,
		target_kind, target_id, title, payload_json, dedup_key,
		is_read, occurred_at_ms, created_at_ms
		FROM events` + whereSQL + " ORDER BY occurred_at_ms DESC, id DESC"

	rows, err := s.db.QueryPage(ctx, selectSQL, limit, offset, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("events: query page failed: %w", err)
	}
	defer rows.Close()

	var records []EventRecord
	for rows.Next() {
		var rec EventRecord
		var targetKind, targetID, payloadJSON, dedupKey sql.NullString
		var isReadInt int

		err := rows.Scan(
			&rec.ID,
			&rec.Type,
			&rec.Severity,
			&rec.SourceModule,
			&targetKind,
			&targetID,
			&rec.Title,
			&payloadJSON,
			&dedupKey,
			&isReadInt,
			&rec.OccurredAtMs,
			&rec.CreatedAtMs,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("events: row scan failed: %w", err)
		}

		if targetKind.Valid {
			rec.TargetKind = targetKind.String
		}
		if targetID.Valid {
			rec.TargetID = targetID.String
		}
		if dedupKey.Valid {
			rec.DedupKey = dedupKey.String
		}
		rec.IsRead = (isReadInt == 1)

		if payloadJSON.Valid && payloadJSON.String != "" {
			rec.PayloadJSON = payloadJSON.String
			var p map[string]any
			if err := json.Unmarshal([]byte(payloadJSON.String), &p); err == nil {
				rec.Payload = p
			}
		}

		records = append(records, rec)
	}

	return records, total, rows.Err()
}

// MarkRead marks events as read by IDs, timestamp threshold, or all.
func (s *Store) MarkRead(ctx context.Context, req MarkReadRequest) (int64, error) {
	if s.db == nil {
		return 0, errors.New("events: db is nil")
	}

	if len(req.IDs) > 0 {
		placeholders := make([]string, len(req.IDs))
		args := make([]any, len(req.IDs))
		for i, id := range req.IDs {
			placeholders[i] = "?"
			args[i] = id
		}
		q := fmt.Sprintf("UPDATE events SET is_read = 1 WHERE id IN (%s) AND is_read = 0", strings.Join(placeholders, ","))
		res, err := s.db.Exec(ctx, q, args...)
		if err != nil {
			return 0, fmt.Errorf("events: mark read by IDs failed: %w", err)
		}
		return res.RowsAffected()
	}

	if req.BeforeMs > 0 {
		q := "UPDATE events SET is_read = 1 WHERE occurred_at_ms <= ? AND is_read = 0"
		res, err := s.db.Exec(ctx, q, req.BeforeMs)
		if err != nil {
			return 0, fmt.Errorf("events: mark read before timestamp failed: %w", err)
		}
		return res.RowsAffected()
	}

	if req.All {
		q := "UPDATE events SET is_read = 1 WHERE is_read = 0"
		res, err := s.db.Exec(ctx, q)
		if err != nil {
			return 0, fmt.Errorf("events: mark all read failed: %w", err)
		}
		return res.RowsAffected()
	}

	return 0, nil
}

// GetUnreadCount returns total number of unread events.
func (s *Store) GetUnreadCount(ctx context.Context) (int64, error) {
	if s.db == nil {
		return 0, errors.New("events: db is nil")
	}

	var count int64
	err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM events WHERE is_read = 0").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("events: get unread count failed: %w", err)
	}
	return count, nil
}

// ListEventTypes returns all configured event types and policies.
func (s *Store) ListEventTypes(ctx context.Context) ([]TypeDef, error) {
	if s.db == nil {
		return nil, errors.New("events: db is nil")
	}

	q := `SELECT event_type, display_name, default_severity, severity,
		default_disposition, disposition, description, is_builtin, updated_at_ms
		FROM event_types ORDER BY event_type ASC`

	rows, err := s.db.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("events: list event types failed: %w", err)
	}
	defer rows.Close()

	var types []TypeDef
	for rows.Next() {
		var t TypeDef
		var desc sql.NullString
		var isBuiltinInt int

		err := rows.Scan(
			&t.Type,
			&t.DisplayName,
			&t.DefaultSeverity,
			&t.Severity,
			&t.DefaultDisposition,
			&t.Disposition,
			&desc,
			&isBuiltinInt,
			&t.UpdatedAtMs,
		)
		if err != nil {
			return nil, fmt.Errorf("events: scan event type failed: %w", err)
		}

		if desc.Valid {
			t.Description = desc.String
		}
		t.IsBuiltin = (isBuiltinInt == 1)
		types = append(types, t)
	}

	return types, rows.Err()
}

// UpdateEventType modifies severity and disposition for an existing event type.
// Does NOT allow creating new types or deleting types.
func (s *Store) UpdateEventType(ctx context.Context, eventType, severity, disposition string) (*TypeDef, error) {
	if s.db == nil {
		return nil, errors.New("events: db is nil")
	}

	// Validate inputs
	if severity != "" {
		switch severity {
		case "info", "warning", "critical":
		default:
			return nil, fmt.Errorf("invalid severity: %q, must be info/warning/critical", severity)
		}
	}
	if disposition != "" {
		switch disposition {
		case "drop", "store", "store+ui", "store+notify":
		default:
			return nil, fmt.Errorf("invalid disposition: %q, must be drop/store/store+ui/store+notify", disposition)
		}
	}

	// Fetch current type
	var current TypeDef
	var desc sql.NullString
	var isBuiltinInt int
	checkSQL := `SELECT event_type, display_name, default_severity, severity,
		default_disposition, disposition, description, is_builtin, updated_at_ms
		FROM event_types WHERE event_type = ?`

	err := s.db.QueryRow(ctx, checkSQL, eventType).Scan(
		&current.Type,
		&current.DisplayName,
		&current.DefaultSeverity,
		&current.Severity,
		&current.DefaultDisposition,
		&current.Disposition,
		&desc,
		&isBuiltinInt,
		&current.UpdatedAtMs,
	)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
		return nil, fmt.Errorf("event type %q not found", eventType)
	}
	if err != nil {
		return nil, fmt.Errorf("query event type failed: %w", err)
	}
	if desc.Valid {
		current.Description = desc.String
	}
	current.IsBuiltin = (isBuiltinInt == 1)

	// Apply updates
	if severity != "" {
		current.Severity = severity
	}
	if disposition != "" {
		current.Disposition = disposition
	}
	nowMs := time.Now().UnixMilli()
	current.UpdatedAtMs = nowMs

	updSQL := "UPDATE event_types SET severity = ?, disposition = ?, updated_at_ms = ? WHERE event_type = ?"
	if _, err := s.db.Exec(ctx, updSQL, current.Severity, current.Disposition, nowMs, eventType); err != nil {
		return nil, fmt.Errorf("update event type failed: %w", err)
	}

	// Invalidate and update in-memory policy cache immediately
	UpdatePolicyCache(eventType, current.Severity, current.Disposition)

	return &current, nil
}
