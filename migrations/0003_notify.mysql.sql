-- 0003_notify.mysql.sql
-- 通知渠道、路由规则与投递记录表 (dash P2-01 / 09-events-notify.md §3-§6)

CREATE TABLE IF NOT EXISTS credentials (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    cred_kind VARCHAR(32) NOT NULL,
    enc_payload BLOB NOT NULL,
    enc_key_id VARCHAR(32) NOT NULL,
    enc_nonce VARCHAR(64) NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS notify_channels (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    channel_kind VARCHAR(32) NOT NULL,
    credential_id VARCHAR(26),
    config_json LONGTEXT,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS notify_rules (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    event_pattern VARCHAR(64) NOT NULL,
    min_severity VARCHAR(16) NOT NULL,
    notify_channel_id VARCHAR(26) NOT NULL,
    template_name VARCHAR(64),
    throttle_s INT DEFAULT 0 NOT NULL,
    quiet_start_min INT,
    quiet_end_min INT,
    display_order INT DEFAULT 0 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_notify_rules_enabled (is_enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS notify_deliveries (
    id VARCHAR(26) NOT NULL,
    event_id VARCHAR(26) NOT NULL,
    channel_id VARCHAR(26) NOT NULL,
    rule_id VARCHAR(26),
    state VARCHAR(16) NOT NULL,
    attempt INT DEFAULT 0 NOT NULL,
    last_error LONGTEXT,
    rendered_text LONGTEXT,
    sent_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_deliveries_event (event_id),
    KEY ix_deliveries_channel (channel_id),
    KEY ix_deliveries_state (state, created_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
