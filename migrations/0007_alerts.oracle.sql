-- 0007_alerts.oracle.sql
-- 告警引擎、渠道绑定与告警事件表 (dash P2-08 / 07-monitoring.md §2, §4)

CREATE TABLE alert_rules (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    rule_kind VARCHAR2(16 CHAR) NOT NULL,
    scope_kind VARCHAR2(16 CHAR) NOT NULL,
    scope_ref VARCHAR2(26 CHAR),
    metric_code VARCHAR2(40 CHAR),
    compare_op VARCHAR2(4 CHAR) NOT NULL,
    threshold NUMBER(20, 4) NOT NULL,
    duration_s NUMBER(10) DEFAULT 0 NOT NULL,
    severity VARCHAR2(16 CHAR) DEFAULT 'warning' NOT NULL,
    silence_s NUMBER(10) DEFAULT 0 NOT NULL,
    include_auto_renew NUMBER(1) DEFAULT 0 NOT NULL,
    extra_json CLOB,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_alert_rules PRIMARY KEY (id)
);

CREATE INDEX ix_alert_rules_kind ON alert_rules (rule_kind, is_enabled);

CREATE TABLE alert_rule_channels (
    alert_rule_id VARCHAR2(26) NOT NULL,
    notify_channel_id VARCHAR2(26 CHAR) NOT NULL,
    CONSTRAINT pk_alert_rule_channels PRIMARY KEY (alert_rule_id, notify_channel_id)
);

CREATE INDEX ix_arc_channel ON alert_rule_channels (notify_channel_id);

CREATE TABLE alert_events (
    id VARCHAR2(26) NOT NULL,
    alert_rule_id VARCHAR2(26 CHAR) NOT NULL,
    node_id VARCHAR2(26 CHAR) NOT NULL,
    event_state VARCHAR2(16 CHAR) NOT NULL,
    fired_at_ms NUMBER(19) NOT NULL,
    resolved_at_ms NUMBER(19),
    peak_value NUMBER(20, 4),
    detail CLOB,
    notified_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_alert_events PRIMARY KEY (id)
);

CREATE INDEX ix_alert_events_node ON alert_events (node_id, fired_at_ms);
CREATE INDEX ix_alert_events_rule ON alert_events (alert_rule_id, event_state);
