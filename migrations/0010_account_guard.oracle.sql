-- 0010_account_guard.oracle.sql
-- 账号级保活策略与设备覆盖 (dash P2-11 / 02-database.md §4.1)

CREATE TABLE guard_account_policies (
    cloud_account_id VARCHAR2(26) NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    actions_enabled NUMBER(1) DEFAULT 0 NOT NULL,
    traffic_limit_gb BINARY_DOUBLE,
    traffic_action VARCHAR2(16 CHAR) DEFAULT 'stop' NOT NULL,
    warn_ratio BINARY_DOUBLE DEFAULT 0.8 NOT NULL,
    schedule_enabled NUMBER(1) DEFAULT 0 NOT NULL,
    schedule_start VARCHAR2(5 CHAR),
    schedule_stop VARCHAR2(5 CHAR),
    schedule_tz VARCHAR2(64 CHAR) DEFAULT 'Asia/Shanghai' NOT NULL,
    eval_interval_s NUMBER(10) DEFAULT 60 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_guard_account_policies PRIMARY KEY (cloud_account_id)
);

ALTER TABLE guard_rules ADD (inherit_account NUMBER(1) DEFAULT 1 NOT NULL);
