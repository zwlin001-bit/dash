-- 0008_billing.mysql.sql
-- 云账单周期总览、明细条目与预算表 (dash P2-09 / 15-cloud-billing.md §2)

CREATE TABLE IF NOT EXISTS bill_periods (
    id VARCHAR(26) NOT NULL,
    cloud_account_id VARCHAR(26) NOT NULL,
    period VARCHAR(7) NOT NULL,
    currency VARCHAR(8) NOT NULL,
    total_amount DOUBLE NOT NULL,
    pretax_amount DOUBLE,
    discount_amount DOUBLE,
    sync_state VARCHAR(16) NOT NULL,
    synced_at_ms BIGINT,
    error_text LONGTEXT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY ux_bill_periods (cloud_account_id, period),
    KEY ix_bill_periods_period (period)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bill_items (
    id VARCHAR(26) NOT NULL,
    cloud_account_id VARCHAR(26) NOT NULL,
    period VARCHAR(7) NOT NULL,
    res_kind VARCHAR(32) NOT NULL,
    res_ref VARCHAR(191),
    cloud_resource_id VARCHAR(26),
    item_name VARCHAR(128),
    product_code VARCHAR(64),
    currency VARCHAR(8) NOT NULL,
    amount DOUBLE NOT NULL,
    usage_text VARCHAR(64),
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_bill_items_period (cloud_account_id, period),
    KEY ix_bill_items_res (cloud_resource_id),
    KEY ix_bill_items_ref (res_ref)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS bill_budgets (
    id VARCHAR(26) NOT NULL,
    scope_kind VARCHAR(16) NOT NULL,
    scope_ref VARCHAR(26),
    period_kind VARCHAR(8) DEFAULT 'month' NOT NULL,
    currency VARCHAR(8) NOT NULL,
    amount DOUBLE NOT NULL,
    warn_ratio DOUBLE DEFAULT 0.8 NOT NULL,
    is_enabled TINYINT DEFAULT 1 NOT NULL,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_bill_budgets_scope (scope_kind, is_enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
