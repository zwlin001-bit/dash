-- 0005_cloud_provider.mysql.sql
-- 凭据与云资产表 (dash P2-02 / 02-database.md §3, §4.1)

CREATE TABLE IF NOT EXISTS credentials (
    id VARCHAR(26) NOT NULL,
    name VARCHAR(64) NOT NULL,
    cred_kind VARCHAR(32) NOT NULL,
    enc_payload BLOB NOT NULL,
    enc_key_id VARCHAR(32) NOT NULL,
    enc_nonce VARCHAR(64) NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_credentials_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS providers (
    provider_code VARCHAR(32) NOT NULL,
    display_name VARCHAR(64) NOT NULL,
    exec_path VARCHAR(255) NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    config_json LONGTEXT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (provider_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS cloud_accounts (
    id VARCHAR(26) NOT NULL,
    provider_code VARCHAR(32) NOT NULL,
    name VARCHAR(64) NOT NULL,
    credential_id VARCHAR(26) NOT NULL,
    default_region VARCHAR(64),
    account_site VARCHAR(32),
    config_json LONGTEXT,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    last_sync_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_cloud_accounts_name (name),
    KEY ix_cloud_accounts_cred (credential_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS cloud_resources (
    id VARCHAR(26) NOT NULL,
    cloud_account_id VARCHAR(26) NOT NULL,
    provider_code VARCHAR(32) NOT NULL,
    res_kind VARCHAR(32) NOT NULL,
    res_ref VARCHAR(191) NOT NULL,
    name VARCHAR(128),
    region VARCHAR(64),
    status VARCHAR(32),
    public_ips VARCHAR(255),
    private_ips VARCHAR(255),
    specs_json LONGTEXT,
    billing_json LONGTEXT,
    attrs_json LONGTEXT,
    node_id VARCHAR(26),
    synced_at_ms BIGINT NOT NULL,
    is_deleted TINYINT DEFAULT 0 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_cloud_res_ref (cloud_account_id, res_kind, res_ref),
    KEY ix_cloud_res_node (node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
