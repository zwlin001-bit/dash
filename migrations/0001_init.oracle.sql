-- 0001_init.oracle.sql
-- 初始化表结构与基础设置 (dash P1-04)

CREATE TABLE schema_migrations (
    version_no NUMBER(10) NOT NULL,
    name VARCHAR2(80 CHAR) NOT NULL,
    checksum VARCHAR2(64 CHAR) NOT NULL,
    applied_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_schema_migrations PRIMARY KEY (version_no)
);

CREATE TABLE settings (
    setting_key VARCHAR2(64 CHAR) NOT NULL,
    setting_val CLOB,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_settings PRIMARY KEY (setting_key)
);

CREATE TABLE account_users (
    id VARCHAR2(26) NOT NULL,
    username VARCHAR2(64 CHAR) NOT NULL,
    passwd_hash VARCHAR2(255 CHAR) NOT NULL,
    is_admin NUMBER(1) DEFAULT 0 NOT NULL,
    totp_secret VARCHAR2(255 CHAR),
    last_login_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_account_users PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_users_username ON account_users (username);

CREATE TABLE user_sessions (
    id VARCHAR2(26) NOT NULL,
    user_id VARCHAR2(26) NOT NULL,
    token_hash VARCHAR2(64 CHAR) NOT NULL,
    user_agent CLOB,
    ip VARCHAR2(64 CHAR),
    expires_at_ms NUMBER(19) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_user_sessions PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_sessions_token ON user_sessions (token_hash);

CREATE INDEX ix_sessions_user ON user_sessions (user_id);

CREATE TABLE audit_log (
    id VARCHAR2(26) NOT NULL,
    actor_kind VARCHAR2(16 CHAR) NOT NULL,
    actor_id VARCHAR2(26 CHAR),
    action VARCHAR2(64 CHAR) NOT NULL,
    target_kind VARCHAR2(32 CHAR),
    target_id VARCHAR2(26 CHAR),
    detail CLOB,
    result VARCHAR2(16 CHAR) NOT NULL,
    ip VARCHAR2(64 CHAR),
    created_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_audit_log PRIMARY KEY (id)
);

CREATE INDEX ix_audit_created ON audit_log (created_at_ms);

CREATE INDEX ix_audit_target ON audit_log (target_kind, target_id);

CREATE TABLE node_groups (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    display_order NUMBER(10) DEFAULT 0 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_node_groups PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_groups_name ON node_groups (name);

CREATE TABLE tags (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(48 CHAR) NOT NULL,
    color VARCHAR2(16 CHAR),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_tags PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_tags_name ON tags (name);

CREATE TABLE node_tags (
    node_id VARCHAR2(26) NOT NULL,
    tag_id VARCHAR2(26) NOT NULL,
    CONSTRAINT pk_node_tags PRIMARY KEY (node_id, tag_id)
);

CREATE INDEX ix_node_tags_tag ON node_tags (tag_id);

CREATE TABLE nodes (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    node_group_id VARCHAR2(26),
    display_order NUMBER(10) DEFAULT 0 NOT NULL,
    is_hidden NUMBER(1) DEFAULT 0 NOT NULL,
    agent_token_hash VARCHAR2(64 CHAR),
    agent_version VARCHAR2(32 CHAR),
    conn_state VARCHAR2(16 CHAR) DEFAULT 'never' NOT NULL,
    last_seen_at_ms NUMBER(19),
    clock_skew_ms NUMBER(19),
    note CLOB,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_nodes PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_nodes_token ON nodes (agent_token_hash);

CREATE INDEX ix_nodes_group ON nodes (node_group_id);

CREATE TABLE node_facts (
    node_id VARCHAR2(26) NOT NULL,
    arch VARCHAR2(16 CHAR),
    os_name VARCHAR2(64 CHAR),
    os_version VARCHAR2(48 CHAR),
    kernel VARCHAR2(64 CHAR),
    virt VARCHAR2(32 CHAR),
    cpu_model VARCHAR2(128 CHAR),
    cpu_cores NUMBER(10),
    cpu_threads NUMBER(10),
    mem_total NUMBER(19),
    swap_total NUMBER(19),
    disk_total NUMBER(19),
    ipv4 VARCHAR2(64 CHAR),
    ipv6 VARCHAR2(128 CHAR),
    boot_at_ms NUMBER(19),
    facts_hash VARCHAR2(32 CHAR),
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_node_facts PRIMARY KEY (node_id)
);

CREATE TABLE node_billing (
    node_id VARCHAR2(26) NOT NULL,
    currency VARCHAR2(8 CHAR),
    price BINARY_DOUBLE,
    cycle_days NUMBER(10),
    is_auto_renew NUMBER(1) DEFAULT 0 NOT NULL,
    expires_at_ms NUMBER(19),
    traffic_limit NUMBER(19),
    traffic_limit_kind VARCHAR2(8 CHAR),
    traffic_reset_day NUMBER(10),
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_node_billing PRIMARY KEY (node_id)
);

CREATE TABLE enroll_tokens (
    id VARCHAR2(26) NOT NULL,
    token_hash VARCHAR2(64 CHAR) NOT NULL,
    preset_name VARCHAR2(64 CHAR),
    preset_group_id VARCHAR2(26),
    expires_at_ms NUMBER(19) NOT NULL,
    used_at_ms NUMBER(19),
    used_node_id VARCHAR2(26),
    created_by VARCHAR2(26),
    created_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_enroll_tokens PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_enroll_token ON enroll_tokens (token_hash);

CREATE TABLE metric_defs (
    metric_code VARCHAR2(40 CHAR) NOT NULL,
    display_name VARCHAR2(64 CHAR) NOT NULL,
    unit VARCHAR2(16 CHAR),
    value_kind VARCHAR2(16 CHAR) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_metric_defs PRIMARY KEY (metric_code)
);

CREATE TABLE metric_series (
    id VARCHAR2(26) NOT NULL,
    node_id VARCHAR2(26) NOT NULL,
    metric_code VARCHAR2(40 CHAR) NOT NULL,
    dim_key VARCHAR2(128 CHAR) NOT NULL,
    dim_json CLOB,
    first_at_ms NUMBER(19) NOT NULL,
    last_at_ms NUMBER(19) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_metric_series PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_series ON metric_series (node_id, metric_code, dim_key);

CREATE TABLE sample_host (
    node_id VARCHAR2(26) NOT NULL,
    ts_ms NUMBER(19) NOT NULL,
    cpu_pct BINARY_DOUBLE,
    mem_used NUMBER(19),
    swap_used NUMBER(19),
    load1 BINARY_DOUBLE,
    load5 BINARY_DOUBLE,
    load15 BINARY_DOUBLE,
    disk_used NUMBER(19),
    net_up_bps NUMBER(19),
    net_down_bps NUMBER(19),
    net_total_up NUMBER(19),
    net_total_down NUMBER(19),
    traffic_up NUMBER(19),
    traffic_down NUMBER(19),
    proc_count NUMBER(10),
    tcp_count NUMBER(10),
    udp_count NUMBER(10),
    uptime_s NUMBER(19),
    CONSTRAINT pk_sample_host PRIMARY KEY (node_id, ts_ms)
);

CREATE TABLE sample_host_1m (
    node_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    cpu_pct_avg BINARY_DOUBLE,
    cpu_pct_max BINARY_DOUBLE,
    cpu_pct_min BINARY_DOUBLE,
    mem_used_avg BINARY_DOUBLE,
    mem_used_max BINARY_DOUBLE,
    mem_used_min BINARY_DOUBLE,
    swap_used_avg BINARY_DOUBLE,
    swap_used_max BINARY_DOUBLE,
    swap_used_min BINARY_DOUBLE,
    load1_avg BINARY_DOUBLE,
    load1_max BINARY_DOUBLE,
    load1_min BINARY_DOUBLE,
    load5_avg BINARY_DOUBLE,
    load5_max BINARY_DOUBLE,
    load5_min BINARY_DOUBLE,
    load15_avg BINARY_DOUBLE,
    load15_max BINARY_DOUBLE,
    load15_min BINARY_DOUBLE,
    disk_used_avg BINARY_DOUBLE,
    disk_used_max BINARY_DOUBLE,
    disk_used_min BINARY_DOUBLE,
    net_up_bps_avg BINARY_DOUBLE,
    net_up_bps_max BINARY_DOUBLE,
    net_up_bps_min BINARY_DOUBLE,
    net_down_bps_avg BINARY_DOUBLE,
    net_down_bps_max BINARY_DOUBLE,
    net_down_bps_min BINARY_DOUBLE,
    proc_count_avg BINARY_DOUBLE,
    proc_count_max BINARY_DOUBLE,
    proc_count_min BINARY_DOUBLE,
    tcp_count_avg BINARY_DOUBLE,
    tcp_count_max BINARY_DOUBLE,
    tcp_count_min BINARY_DOUBLE,
    udp_count_avg BINARY_DOUBLE,
    udp_count_max BINARY_DOUBLE,
    udp_count_min BINARY_DOUBLE,
    net_total_up_last NUMBER(19),
    net_total_down_last NUMBER(19),
    uptime_s_last NUMBER(19),
    traffic_up_sum NUMBER(19),
    traffic_down_sum NUMBER(19),
    CONSTRAINT pk_sample_host_1m PRIMARY KEY (node_id, bucket_ms)
);

CREATE TABLE sample_host_1h (
    node_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    cpu_pct_avg BINARY_DOUBLE,
    cpu_pct_max BINARY_DOUBLE,
    cpu_pct_min BINARY_DOUBLE,
    mem_used_avg BINARY_DOUBLE,
    mem_used_max BINARY_DOUBLE,
    mem_used_min BINARY_DOUBLE,
    swap_used_avg BINARY_DOUBLE,
    swap_used_max BINARY_DOUBLE,
    swap_used_min BINARY_DOUBLE,
    load1_avg BINARY_DOUBLE,
    load1_max BINARY_DOUBLE,
    load1_min BINARY_DOUBLE,
    load5_avg BINARY_DOUBLE,
    load5_max BINARY_DOUBLE,
    load5_min BINARY_DOUBLE,
    load15_avg BINARY_DOUBLE,
    load15_max BINARY_DOUBLE,
    load15_min BINARY_DOUBLE,
    disk_used_avg BINARY_DOUBLE,
    disk_used_max BINARY_DOUBLE,
    disk_used_min BINARY_DOUBLE,
    net_up_bps_avg BINARY_DOUBLE,
    net_up_bps_max BINARY_DOUBLE,
    net_up_bps_min BINARY_DOUBLE,
    net_down_bps_avg BINARY_DOUBLE,
    net_down_bps_max BINARY_DOUBLE,
    net_down_bps_min BINARY_DOUBLE,
    proc_count_avg BINARY_DOUBLE,
    proc_count_max BINARY_DOUBLE,
    proc_count_min BINARY_DOUBLE,
    tcp_count_avg BINARY_DOUBLE,
    tcp_count_max BINARY_DOUBLE,
    tcp_count_min BINARY_DOUBLE,
    udp_count_avg BINARY_DOUBLE,
    udp_count_max BINARY_DOUBLE,
    udp_count_min BINARY_DOUBLE,
    net_total_up_last NUMBER(19),
    net_total_down_last NUMBER(19),
    uptime_s_last NUMBER(19),
    traffic_up_sum NUMBER(19),
    traffic_down_sum NUMBER(19),
    CONSTRAINT pk_sample_host_1h PRIMARY KEY (node_id, bucket_ms)
);

CREATE TABLE sample_host_1d (
    node_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    cpu_pct_avg BINARY_DOUBLE,
    cpu_pct_max BINARY_DOUBLE,
    cpu_pct_min BINARY_DOUBLE,
    mem_used_avg BINARY_DOUBLE,
    mem_used_max BINARY_DOUBLE,
    mem_used_min BINARY_DOUBLE,
    swap_used_avg BINARY_DOUBLE,
    swap_used_max BINARY_DOUBLE,
    swap_used_min BINARY_DOUBLE,
    load1_avg BINARY_DOUBLE,
    load1_max BINARY_DOUBLE,
    load1_min BINARY_DOUBLE,
    load5_avg BINARY_DOUBLE,
    load5_max BINARY_DOUBLE,
    load5_min BINARY_DOUBLE,
    load15_avg BINARY_DOUBLE,
    load15_max BINARY_DOUBLE,
    load15_min BINARY_DOUBLE,
    disk_used_avg BINARY_DOUBLE,
    disk_used_max BINARY_DOUBLE,
    disk_used_min BINARY_DOUBLE,
    net_up_bps_avg BINARY_DOUBLE,
    net_up_bps_max BINARY_DOUBLE,
    net_up_bps_min BINARY_DOUBLE,
    net_down_bps_avg BINARY_DOUBLE,
    net_down_bps_max BINARY_DOUBLE,
    net_down_bps_min BINARY_DOUBLE,
    proc_count_avg BINARY_DOUBLE,
    proc_count_max BINARY_DOUBLE,
    proc_count_min BINARY_DOUBLE,
    tcp_count_avg BINARY_DOUBLE,
    tcp_count_max BINARY_DOUBLE,
    tcp_count_min BINARY_DOUBLE,
    udp_count_avg BINARY_DOUBLE,
    udp_count_max BINARY_DOUBLE,
    udp_count_min BINARY_DOUBLE,
    net_total_up_last NUMBER(19),
    net_total_down_last NUMBER(19),
    uptime_s_last NUMBER(19),
    traffic_up_sum NUMBER(19),
    traffic_down_sum NUMBER(19),
    CONSTRAINT pk_sample_host_1d PRIMARY KEY (node_id, bucket_ms)
);

CREATE TABLE sample_dim (
    series_id VARCHAR2(26) NOT NULL,
    ts_ms NUMBER(19) NOT NULL,
    val BINARY_DOUBLE,
    CONSTRAINT pk_sample_dim PRIMARY KEY (series_id, ts_ms)
);

CREATE TABLE sample_dim_1m (
    series_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    val_avg BINARY_DOUBLE,
    val_max BINARY_DOUBLE,
    val_min BINARY_DOUBLE,
    val_last BINARY_DOUBLE,
    CONSTRAINT pk_sample_dim_1m PRIMARY KEY (series_id, bucket_ms)
);

CREATE TABLE sample_dim_1h (
    series_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    val_avg BINARY_DOUBLE,
    val_max BINARY_DOUBLE,
    val_min BINARY_DOUBLE,
    val_last BINARY_DOUBLE,
    CONSTRAINT pk_sample_dim_1h PRIMARY KEY (series_id, bucket_ms)
);

CREATE TABLE sample_dim_1d (
    series_id VARCHAR2(26) NOT NULL,
    bucket_ms NUMBER(19) NOT NULL,
    sample_cnt NUMBER(10) NOT NULL,
    val_avg BINARY_DOUBLE,
    val_max BINARY_DOUBLE,
    val_min BINARY_DOUBLE,
    val_last BINARY_DOUBLE,
    CONSTRAINT pk_sample_dim_1d PRIMARY KEY (series_id, bucket_ms)
);


-- 默认保留期种子数据 (10-schema-spec.md §5)

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.raw_days', '3', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1m_days', '30', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1h_days', '400', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.1d_days', '0', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.events_days', '90', 0);

INSERT INTO settings (setting_key, setting_val, updated_at_ms) VALUES ('retention.audit_days', '365', 0);
