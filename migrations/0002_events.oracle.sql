-- 0002_events.oracle.sql
-- 事件总线与事件类型表 (dash P1-20 / 10-schema-spec.md §4)

CREATE TABLE event_types (
    event_type VARCHAR2(64 CHAR) NOT NULL,
    display_name VARCHAR2(64 CHAR) NOT NULL,
    default_severity VARCHAR2(16 CHAR) NOT NULL,
    severity VARCHAR2(16 CHAR) NOT NULL,
    default_disposition VARCHAR2(16 CHAR) NOT NULL,
    disposition VARCHAR2(16 CHAR) NOT NULL,
    description CLOB,
    is_builtin NUMBER(1) DEFAULT 1 NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_event_types PRIMARY KEY (event_type)
);

CREATE TABLE events (
    id VARCHAR2(26) NOT NULL,
    event_type VARCHAR2(64 CHAR) NOT NULL,
    severity VARCHAR2(16 CHAR) NOT NULL,
    source_module VARCHAR2(32 CHAR) NOT NULL,
    target_kind VARCHAR2(32 CHAR),
    target_id VARCHAR2(26 CHAR),
    title VARCHAR2(255 CHAR) NOT NULL,
    payload_json CLOB,
    dedup_key VARCHAR2(128 CHAR),
    is_read NUMBER(1) DEFAULT 0 NOT NULL,
    occurred_at_ms NUMBER(19) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_events PRIMARY KEY (id)
);

CREATE INDEX ix_events_occurred ON events (occurred_at_ms);

CREATE INDEX ix_events_type ON events (event_type, occurred_at_ms);

CREATE INDEX ix_events_target ON events (target_kind, target_id, occurred_at_ms);
