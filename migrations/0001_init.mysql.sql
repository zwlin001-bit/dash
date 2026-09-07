-- 0001_init.mysql.sql
-- 初始化表结构与基础设置 (dash P1-04)

CREATE TABLE IF NOT EXISTS schema_migrations (
    version_no INT NOT NULL,
    name VARCHAR(80) NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    applied_at_ms BIGINT NOT NULL,
    PRIMARY KEY (version_no)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS settings (
    setting_key VARCHAR(64) NOT NULL,
    setting_val LONGTEXT,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (setting_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS account_users (
    id VARCHAR(26) NOT NULL,
    username VARCHAR(64) NOT NULL,
    passwd_hash VARCHAR(255) NOT NULL,
    is_admin TINYINT DEFAULT 0 NOT NULL,
    totp_secret VARCHAR(255),
    last_login_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_users_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS user_sessions (
    id VARCHAR(26) NOT NULL,
    user_id VARCHAR(26) NOT NULL,
    token_hash VARCHAR(64) NOT NULL,
    user_agent LONGTEXT,
    ip VARCHAR(64),
    expires_at_ms BIGINT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_sessions_token (token_hash),
    KEY ix_sessions_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS audit_log (
    id VARCHAR(26) NOT NULL,
    actor_kind VARCHAR(16) NOT NULL,
    actor_id VARCHAR(26),
    action VARCHAR(64) NOT NULL,
    target_kind VARCHAR(32),
    target_id VARCHAR(26),
    detail LONGTEXT,
    result VARCHAR(16) NOT NULL,
    ip VARCHAR(64),
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_audit_created (created_at_ms),
    KEY ix_audit_target (target_kind, target_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS node_groups (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    display_order INT DEFAULT 0 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_groups_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS tags (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(48) NOT NULL,
    color VARCHAR(16),
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_tags_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS node_tags (
    node_id VARCHAR(26) NOT NULL,
    tag_id VARCHAR(26) NOT NULL,
    PRIMARY KEY (node_id, tag_id),
    KEY ix_node_tags_tag (tag_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS nodes (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    node_group_id VARCHAR(26),
    display_order INT DEFAULT 0 NOT NULL,
    is_hidden TINYINT DEFAULT 0 NOT NULL,
    agent_token_hash VARCHAR(64),
    agent_version VARCHAR(32),
    conn_state VARCHAR(16) DEFAULT 'never' NOT NULL,
    last_seen_at_ms BIGINT,
    clock_skew_ms BIGINT,
    note LONGTEXT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_nodes_token (agent_token_hash),
    KEY ix_nodes_group (node_group_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS node_facts (
    node_id VARCHAR(26) NOT NULL,
    arch VARCHAR(16),
    os_name VARCHAR(64),
    os_version VARCHAR(48),
    kernel VARCHAR(64),
    virt VARCHAR(32),
    cpu_model VARCHAR(128),
    cpu_cores INT,
    cpu_threads INT,
    mem_total BIGINT,
    swap_total BIGINT,
    disk_total BIGINT,
    ipv4 VARCHAR(64),
    ipv6 VARCHAR(128),
    boot_at_ms BIGINT,
    facts_hash VARCHAR(32),
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS node_billing (
    node_id VARCHAR(26) NOT NULL,
    currency VARCHAR(8),
    price DOUBLE,
    cycle_days INT,
    is_auto_renew TINYINT DEFAULT 0 NOT NULL,
    expires_at_ms BIGINT,
    traffic_limit BIGINT,
    traffic_limit_kind VARCHAR(8),
    traffic_reset_day INT,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS enroll_tokens (
    id VARCHAR(26) NOT NULL,
    token_hash VARCHAR(64) NOT NULL,
    preset_name VARCHAR(64),
    preset_group_id VARCHAR(26),
    expires_at_ms BIGINT NOT NULL,
    used_at_ms BIGINT,
    used_node_id VARCHAR(26),
    created_by VARCHAR(26),
    created_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_enroll_token (token_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS metric_defs (
    metric_code VARCHAR(40) NOT NULL,
    display_name VARCHAR(64) NOT NULL,
    unit VARCHAR(16),
    value_kind VARCHAR(16) NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (metric_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS metric_series (
    id VARCHAR(26) NOT NULL,
    node_id VARCHAR(26) NOT NULL,
    metric_code VARCHAR(40) NOT NULL,
    dim_key VARCHAR(128) NOT NULL,
    dim_json LONGTEXT,
    first_at_ms BIGINT NOT NULL,
    last_at_ms BIGINT NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_series (node_id, metric_code, dim_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_host (
    node_id VARCHAR(26) NOT NULL,
    ts_ms BIGINT NOT NULL,
    cpu_pct DOUBLE,
    mem_used BIGINT,
    swap_used BIGINT,
    load1 DOUBLE,
    load5 DOUBLE,
    load15 DOUBLE,
    disk_used BIGINT,
    net_up_bps BIGINT,
    net_down_bps BIGINT,
    net_total_up BIGINT,
    net_total_down BIGINT,
    traffic_up BIGINT,
    traffic_down BIGINT,
    proc_count INT,
    tcp_count INT,
    udp_count INT,
    uptime_s BIGINT,
    PRIMARY KEY (node_id, ts_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_host_1m (
    node_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    cpu_pct_avg DOUBLE,
    cpu_pct_max DOUBLE,
    cpu_pct_min DOUBLE,
    mem_used_avg DOUBLE,
    mem_used_max DOUBLE,
    mem_used_min DOUBLE,
    swap_used_avg DOUBLE,
    swap_used_max DOUBLE,
    swap_used_min DOUBLE,
    load1_avg DOUBLE,
    load1_max DOUBLE,
    load1_min DOUBLE,
    load5_avg DOUBLE,
    load5_max DOUBLE,
    load5_min DOUBLE,
    load15_avg DOUBLE,
    load15_max DOUBLE,
    load15_min DOUBLE,
    disk_used_avg DOUBLE,
    disk_used_max DOUBLE,
    disk_used_min DOUBLE,
    net_up_bps_avg DOUBLE,
    net_up_bps_max DOUBLE,
    net_up_bps_min DOUBLE,
    net_down_bps_avg DOUBLE,
    net_down_bps_max DOUBLE,
    net_down_bps_min DOUBLE,
    proc_count_avg DOUBLE,
    proc_count_max DOUBLE,
    proc_count_min DOUBLE,
    tcp_count_avg DOUBLE,
    tcp_count_max DOUBLE,
    tcp_count_min DOUBLE,
    udp_count_avg DOUBLE,
    udp_count_max DOUBLE,
    udp_count_min DOUBLE,
    net_total_up_last BIGINT,
    net_total_down_last BIGINT,
    uptime_s_last BIGINT,
    traffic_up_sum BIGINT,
    traffic_down_sum BIGINT,
    PRIMARY KEY (node_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_host_1h (
    node_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    cpu_pct_avg DOUBLE,
    cpu_pct_max DOUBLE,
    cpu_pct_min DOUBLE,
    mem_used_avg DOUBLE,
    mem_used_max DOUBLE,
    mem_used_min DOUBLE,
    swap_used_avg DOUBLE,
    swap_used_max DOUBLE,
    swap_used_min DOUBLE,
    load1_avg DOUBLE,
    load1_max DOUBLE,
    load1_min DOUBLE,
    load5_avg DOUBLE,
    load5_max DOUBLE,
    load5_min DOUBLE,
    load15_avg DOUBLE,
    load15_max DOUBLE,
    load15_min DOUBLE,
    disk_used_avg DOUBLE,
    disk_used_max DOUBLE,
    disk_used_min DOUBLE,
    net_up_bps_avg DOUBLE,
    net_up_bps_max DOUBLE,
    net_up_bps_min DOUBLE,
    net_down_bps_avg DOUBLE,
    net_down_bps_max DOUBLE,
    net_down_bps_min DOUBLE,
    proc_count_avg DOUBLE,
    proc_count_max DOUBLE,
    proc_count_min DOUBLE,
    tcp_count_avg DOUBLE,
    tcp_count_max DOUBLE,
    tcp_count_min DOUBLE,
    udp_count_avg DOUBLE,
    udp_count_max DOUBLE,
    udp_count_min DOUBLE,
    net_total_up_last BIGINT,
    net_total_down_last BIGINT,
    uptime_s_last BIGINT,
    traffic_up_sum BIGINT,
    traffic_down_sum BIGINT,
    PRIMARY KEY (node_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_host_1d (
    node_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    cpu_pct_avg DOUBLE,
    cpu_pct_max DOUBLE,
    cpu_pct_min DOUBLE,
    mem_used_avg DOUBLE,
    mem_used_max DOUBLE,
    mem_used_min DOUBLE,
    swap_used_avg DOUBLE,
    swap_used_max DOUBLE,
    swap_used_min DOUBLE,
    load1_avg DOUBLE,
    load1_max DOUBLE,
    load1_min DOUBLE,
    load5_avg DOUBLE,
    load5_max DOUBLE,
    load5_min DOUBLE,
    load15_avg DOUBLE,
    load15_max DOUBLE,
    load15_min DOUBLE,
    disk_used_avg DOUBLE,
    disk_used_max DOUBLE,
    disk_used_min DOUBLE,
    net_up_bps_avg DOUBLE,
    net_up_bps_max DOUBLE,
    net_up_bps_min DOUBLE,
    net_down_bps_avg DOUBLE,
    net_down_bps_max DOUBLE,
    net_down_bps_min DOUBLE,
    proc_count_avg DOUBLE,
    proc_count_max DOUBLE,
    proc_count_min DOUBLE,
    tcp_count_avg DOUBLE,
    tcp_count_max DOUBLE,
    tcp_count_min DOUBLE,
    udp_count_avg DOUBLE,
    udp_count_max DOUBLE,
    udp_count_min DOUBLE,
    net_total_up_last BIGINT,
    net_total_down_last BIGINT,
    uptime_s_last BIGINT,
    traffic_up_sum BIGINT,
    traffic_down_sum BIGINT,
    PRIMARY KEY (node_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_dim (
    series_id VARCHAR(26) NOT NULL,
    ts_ms BIGINT NOT NULL,
    val DOUBLE,
    PRIMARY KEY (series_id, ts_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_dim_1m (
    series_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    val_avg DOUBLE,
    val_max DOUBLE,
    val_min DOUBLE,
    val_last DOUBLE,
    PRIMARY KEY (series_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_dim_1h (
    series_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    val_avg DOUBLE,
    val_max DOUBLE,
    val_min DOUBLE,
    val_last DOUBLE,
    PRIMARY KEY (series_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS sample_dim_1d (
    series_id VARCHAR(26) NOT NULL,
    bucket_ms BIGINT NOT NULL,
    sample_cnt INT NOT NULL,
    val_avg DOUBLE,
    val_max DOUBLE,
    val_min DOUBLE,
    val_last DOUBLE,
    PRIMARY KEY (series_id, bucket_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;


-- 默认保留期种子数据 (10-schema-spec.md §5)

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.raw_days', '3', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1m_days', '30', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1h_days', '400', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1d_days', '0', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.events_days', '90', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.audit_days', '365', 0);
