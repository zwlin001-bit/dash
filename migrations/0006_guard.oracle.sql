-- 0006_guard.oracle.sql
-- ECS 保活与 CDT 流量守卫 (dash P2-04 / 02-database.md §4.1, P2-04 §1)

CREATE TABLE guard_rules (
    id VARCHAR2(26) NOT NULL,
    cloud_resource_id VARCHAR2(26) NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    actions_enabled NUMBER(1) DEFAULT 0 NOT NULL,
    traffic_limit_gb BINARY_DOUBLE,
    traffic_action VARCHAR2(16 CHAR) DEFAULT 'stop' NOT NULL,
    schedule_enabled NUMBER(1) DEFAULT 0 NOT NULL,
    schedule_start VARCHAR2(5 CHAR),
    schedule_stop VARCHAR2(5 CHAR),
    schedule_tz VARCHAR2(64 CHAR) DEFAULT 'Asia/Shanghai' NOT NULL,
    last_eval_at_ms NUMBER(19),
    last_action VARCHAR2(32 CHAR),
    last_action_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_guard_rules PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_guard_rules_res ON guard_rules (cloud_resource_id);

CREATE TABLE guard_cycles (
    id VARCHAR2(26) NOT NULL,
    cloud_account_id VARCHAR2(26) NOT NULL,
    started_at_ms NUMBER(19) NOT NULL,
    duration_ms NUMBER(10) NOT NULL,
    cdt_used_gb BINARY_DOUBLE,
    cdt_error CLOB,
    evaluated NUMBER(10) DEFAULT 0 NOT NULL,
    acted NUMBER(10) DEFAULT 0 NOT NULL,
    failed NUMBER(10) DEFAULT 0 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_guard_cycles PRIMARY KEY (id)
);

CREATE INDEX ix_guard_cycles_started ON guard_cycles (started_at_ms);
CREATE INDEX ix_guard_cycles_acc ON guard_cycles (cloud_account_id);
