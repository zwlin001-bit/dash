-- 0005_cloud_provider.oracle.sql
-- 凭据与云资产表 (dash P2-02 / 02-database.md §3, §4.1)

CREATE TABLE credentials (
    id VARCHAR2(26) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    cred_kind VARCHAR2(32 CHAR) NOT NULL,
    enc_payload BLOB NOT NULL,
    enc_key_id VARCHAR2(32 CHAR) NOT NULL,
    enc_nonce VARCHAR2(64 CHAR) NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_credentials PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_credentials_name ON credentials (name);

CREATE TABLE providers (
    provider_code VARCHAR2(32 CHAR) NOT NULL,
    display_name VARCHAR2(64 CHAR) NOT NULL,
    exec_path VARCHAR2(255 CHAR) NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    config_json CLOB,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_providers PRIMARY KEY (provider_code)
);

CREATE TABLE cloud_accounts (
    id VARCHAR2(26) NOT NULL,
    provider_code VARCHAR2(32 CHAR) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    credential_id VARCHAR2(26) NOT NULL,
    default_region VARCHAR2(64 CHAR),
    account_site VARCHAR2(32 CHAR),
    config_json CLOB,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    last_sync_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_cloud_accounts PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_cloud_accounts_name ON cloud_accounts (name);

CREATE INDEX ix_cloud_accounts_cred ON cloud_accounts (credential_id);

CREATE TABLE cloud_resources (
    id VARCHAR2(26) NOT NULL,
    cloud_account_id VARCHAR2(26) NOT NULL,
    provider_code VARCHAR2(32 CHAR) NOT NULL,
    res_kind VARCHAR2(32 CHAR) NOT NULL,
    res_ref VARCHAR2(191 CHAR) NOT NULL,
    name VARCHAR2(128 CHAR),
    region VARCHAR2(64 CHAR),
    status VARCHAR2(32 CHAR),
    public_ips VARCHAR2(255 CHAR),
    private_ips VARCHAR2(255 CHAR),
    specs_json CLOB,
    billing_json CLOB,
    attrs_json CLOB,
    node_id VARCHAR2(26),
    synced_at_ms NUMBER(19) NOT NULL,
    is_deleted NUMBER(1) DEFAULT 0 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_cloud_resources PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ux_cloud_res_ref ON cloud_resources (cloud_account_id, res_kind, res_ref);

CREATE INDEX ix_cloud_res_node ON cloud_resources (node_id);
