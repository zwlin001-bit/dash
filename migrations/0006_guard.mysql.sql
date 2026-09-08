-- 0006_guard.mysql.sql
-- ECS 保活与 CDT 流量守卫 (dash P2-04 / 02-database.md §4.1, P2-04 §1)

CREATE TABLE IF NOT EXISTS guard_rules (
    id VARCHAR(26) NOT NULL,
    cloud_resource_id VARCHAR(26) NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    actions_enabled TINYINT DEFAULT 0 NOT NULL,
    traffic_limit_gb DOUBLE,
    traffic_action VARCHAR(16) DEFAULT 'stop' NOT NULL,
    schedule_enabled TINYINT DEFAULT 0 NOT NULL,
    schedule_start VARCHAR(5),
    schedule_stop VARCHAR(5),
    schedule_tz VARCHAR(64) DEFAULT 'Asia/Shanghai' NOT NULL,
    last_eval_at_ms BIGINT,
    last_action VARCHAR(32),
    last_action_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_guard_rules_res (cloud_resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS guard_cycles (
    id VARCHAR(26) NOT NULL,
    cloud_account_id VARCHAR(26) NOT NULL,
    started_at_ms BIGINT NOT NULL,
    duration_ms INT NOT NULL,
    cdt_used_gb DOUBLE,
    cdt_error TEXT,
    evaluated INT DEFAULT 0 NOT NULL,
    acted INT DEFAULT 0 NOT NULL,
    failed INT DEFAULT 0 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_guard_cycles_started (started_at_ms),
    KEY ix_guard_cycles_acc (cloud_account_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
