-- 0007_alerts.mysql.sql
-- 告警引擎、渠道绑定与告警事件表 (dash P2-08 / 07-monitoring.md §2, §4)

CREATE TABLE IF NOT EXISTS alert_rules (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    rule_kind VARCHAR(16) NOT NULL,
    scope_kind VARCHAR(16) NOT NULL,
    scope_ref VARCHAR(26),
    metric_code VARCHAR(40),
    compare_op VARCHAR(4) NOT NULL,
    threshold DOUBLE NOT NULL,
    duration_s INT DEFAULT 0 NOT NULL,
    severity VARCHAR(16) DEFAULT 'warning' NOT NULL,
    silence_s INT DEFAULT 0 NOT NULL,
    include_auto_renew TINYINT DEFAULT 0 NOT NULL,
    extra_json LONGTEXT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_alert_rules_kind (rule_kind, is_enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS alert_rule_channels (
    alert_rule_id VARCHAR(26) NOT NULL,
    notify_channel_id VARCHAR(26) NOT NULL,
    PRIMARY KEY (alert_rule_id, notify_channel_id),
    KEY ix_arc_channel (notify_channel_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS alert_events (
    id VARCHAR(26) NOT NULL,
    alert_rule_id VARCHAR(26) NOT NULL,
    node_id VARCHAR(26) NOT NULL,
    event_state VARCHAR(16) NOT NULL,
    fired_at_ms BIGINT NOT NULL,
    resolved_at_ms BIGINT,
    peak_value DOUBLE,
    detail LONGTEXT,
    notified_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_alert_events_node (node_id, fired_at_ms),
    KEY ix_alert_events_rule (alert_rule_id, event_state)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
