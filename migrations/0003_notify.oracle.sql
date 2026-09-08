-- 0003_notify.oracle.sql
-- 通知渠道、路由规则与投递记录表 (dash P2-01 / 09-events-notify.md §3-§6)

CREATE TABLE credentials (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    cred_kind VARCHAR2(32 CHAR) NOT NULL,
    enc_payload BLOB NOT NULL,
    enc_key_id VARCHAR2(32 CHAR) NOT NULL,
    enc_nonce VARCHAR2(64 CHAR) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_credentials PRIMARY KEY (id)
);

CREATE TABLE notify_channels (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    channel_kind VARCHAR2(32 CHAR) NOT NULL,
    credential_id VARCHAR2(26 CHAR),
    config_json CLOB,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_notify_channels PRIMARY KEY (id)
);

CREATE TABLE notify_rules (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    event_pattern VARCHAR2(64 CHAR) NOT NULL,
    min_severity VARCHAR2(16 CHAR) NOT NULL,
    notify_channel_id VARCHAR2(26 CHAR) NOT NULL,
    template_name VARCHAR2(64 CHAR),
    throttle_s NUMBER(10) DEFAULT 0 NOT NULL,
    quiet_start_min NUMBER(10),
    quiet_end_min NUMBER(10),
    display_order NUMBER(10) DEFAULT 0 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_notify_rules PRIMARY KEY (id)
);

CREATE INDEX ix_notify_rules_enabled ON notify_rules (is_enabled);

CREATE TABLE notify_deliveries (
    id VARCHAR2(26) NOT NULL,
    event_id VARCHAR2(26 CHAR) NOT NULL,
    channel_id VARCHAR2(26 CHAR) NOT NULL,
    rule_id VARCHAR2(26 CHAR),
    state VARCHAR2(16 CHAR) NOT NULL,
    attempt NUMBER(10) DEFAULT 0 NOT NULL,
    last_error CLOB,
    rendered_text CLOB,
    sent_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_notify_deliveries PRIMARY KEY (id)
);

CREATE INDEX ix_deliveries_event ON notify_deliveries (event_id);

CREATE INDEX ix_deliveries_channel ON notify_deliveries (channel_id);

CREATE INDEX ix_deliveries_state ON notify_deliveries (state, created_at_ms);
