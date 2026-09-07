package audit

import (
	"context"
	"fmt"
	"time"

	"dash/internal/db"
	"dash/internal/ulid"
)

// Entry 表示一条审计日志。
type Entry struct {
	ActorKind  string // "user" / "agent" / "system"
	ActorID    string // 用户 ID 或 Agent 标识，可空
	Action     string // 例如 "node.create", "node.delete", "tag.create", "auth.login"
	TargetKind string // 例如 "node", "node_group", "tag", "enroll_token"
	TargetID   string // 目标资源 ID，可空
	Detail     string // 详细说明或 JSON，可空
	Result     string // "ok" 或 "failed"
	IP         string // 客户端 IP，可空
}

// Log 记录一条审计日志到数据库。
func Log(ctx context.Context, database *db.DB, entry Entry) error {
	if database == nil {
		return nil
	}
	id := ulid.New()
	now := time.Now().UnixMilli()

	if entry.ActorKind == "" {
		entry.ActorKind = "system"
	}
	if entry.Result == "" {
		entry.Result = "ok"
	}

	q := `INSERT INTO audit_log (id, actor_kind, actor_id, action, target_kind, target_id, detail, result, ip, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var actorID, targetKind, targetID, detail, ip any
	if entry.ActorID != "" {
		actorID = entry.ActorID
	}
	if entry.TargetKind != "" {
		targetKind = entry.TargetKind
	}
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	if entry.Detail != "" {
		detail = entry.Detail
	}
	if entry.IP != "" {
		ip = entry.IP
	}

	_, err := database.Exec(ctx, q,
		id,
		entry.ActorKind,
		actorID,
		entry.Action,
		targetKind,
		targetID,
		detail,
		entry.Result,
		ip,
		now,
	)
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	return nil
}

// LogTx 在事务中记录一条审计日志。
func LogTx(ctx context.Context, tx *db.Tx, entry Entry) error {
	if tx == nil {
		return nil
	}
	id := ulid.New()
	now := time.Now().UnixMilli()

	if entry.ActorKind == "" {
		entry.ActorKind = "system"
	}
	if entry.Result == "" {
		entry.Result = "ok"
	}

	q := `INSERT INTO audit_log (id, actor_kind, actor_id, action, target_kind, target_id, detail, result, ip, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	var actorID, targetKind, targetID, detail, ip any
	if entry.ActorID != "" {
		actorID = entry.ActorID
	}
	if entry.TargetKind != "" {
		targetKind = entry.TargetKind
	}
	if entry.TargetID != "" {
		targetID = entry.TargetID
	}
	if entry.Detail != "" {
		detail = entry.Detail
	}
	if entry.IP != "" {
		ip = entry.IP
	}

	_, err := tx.Exec(ctx, q,
		id,
		entry.ActorKind,
		actorID,
		entry.Action,
		targetKind,
		targetID,
		detail,
		entry.Result,
		ip,
		now,
	)
	if err != nil {
		return fmt.Errorf("audit log tx: %w", err)
	}
	return nil
}
