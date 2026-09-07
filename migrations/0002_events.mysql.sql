-- 0002_events.mysql.sql
-- 事件总线与事件类型表 (dash P1-20 / 10-schema-spec.md §4)

CREATE TABLE IF NOT EXISTS event_types (
    event_type VARCHAR(64) NOT NULL,
    display_name VARCHAR(64) NOT NULL,
    default_severity VARCHAR(16) NOT NULL,
    severity VARCHAR(16) NOT NULL,
    default_disposition VARCHAR(16) NOT NULL,
    disposition VARCHAR(16) NOT NULL,
    description LONGTEXT,
    is_builtin TINYINT DEFAULT 1 NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (event_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS events (
    id VARCHAR(26) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    severity VARCHAR(16) NOT NULL,
    source_module VARCHAR(32) NOT NULL,
    target_kind VARCHAR(32),
    target_id VARCHAR(26),
    title VARCHAR(255) NOT NULL,
    payload_json LONGTEXT,
    dedup_key VARCHAR(128),
    is_read TINYINT DEFAULT 0 NOT NULL,
    occurred_at_ms BIGINT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_events_occurred (occurred_at_ms),
    KEY ix_events_type (event_type, occurred_at_ms),
    KEY ix_events_target (target_kind, target_id, occurred_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
