-- 0010_account_guard.mysql.sql
-- 账号级保活策略与设备覆盖 (dash P2-11 / 02-database.md §4.1)

CREATE TABLE IF NOT EXISTS guard_account_policies (
    cloud_account_id VARCHAR(26) NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    actions_enabled TINYINT DEFAULT 0 NOT NULL,
    traffic_limit_gb DOUBLE,
    traffic_action VARCHAR(16) DEFAULT 'stop' NOT NULL,
    warn_ratio DOUBLE DEFAULT 0.8 NOT NULL,
    schedule_enabled TINYINT DEFAULT 0 NOT NULL,
    schedule_start VARCHAR(5),
    schedule_stop VARCHAR(5),
    schedule_tz VARCHAR(64) DEFAULT 'Asia/Shanghai' NOT NULL,
    eval_interval_s INT DEFAULT 60 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (cloud_account_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

ALTER TABLE guard_rules ADD COLUMN inherit_account TINYINT DEFAULT 1 NOT NULL;
