-- 0008_billing.oracle.sql
-- 云账单周期总览、明细条目与预算表 (dash P2-09 / 15-cloud-billing.md §2)

CREATE TABLE bill_periods (
    id VARCHAR2(26) NOT NULL,
    cloud_account_id VARCHAR2(26 CHAR) NOT NULL,
    period VARCHAR2(7 CHAR) NOT NULL,
    currency VARCHAR2(8 CHAR) NOT NULL,
    total_amount NUMBER(20, 4) NOT NULL,
    pretax_amount NUMBER(20, 4),
    discount_amount NUMBER(20, 4),
    sync_state VARCHAR2(16 CHAR) NOT NULL,
    synced_at_ms NUMBER(19),
    error_text CLOB,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_bill_periods PRIMARY KEY (id),
    CONSTRAINT ux_bill_periods UNIQUE (cloud_account_id, period)
);

CREATE INDEX ix_bill_periods_period ON bill_periods (period);

CREATE TABLE bill_items (
    id VARCHAR2(26) NOT NULL,
    cloud_account_id VARCHAR2(26 CHAR) NOT NULL,
    period VARCHAR2(7 CHAR) NOT NULL,
    res_kind VARCHAR2(32 CHAR) NOT NULL,
    res_ref VARCHAR2(191 CHAR),
    cloud_resource_id VARCHAR2(26 CHAR),
    item_name VARCHAR2(128 CHAR),
    product_code VARCHAR2(64 CHAR),
    currency VARCHAR2(8 CHAR) NOT NULL,
    amount NUMBER(20, 4) NOT NULL,
    usage_text VARCHAR2(64 CHAR),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_bill_items PRIMARY KEY (id)
);

CREATE INDEX ix_bill_items_period ON bill_items (cloud_account_id, period);
CREATE INDEX ix_bill_items_res ON bill_items (cloud_resource_id);
CREATE INDEX ix_bill_items_ref ON bill_items (res_ref);

CREATE TABLE bill_budgets (
    id VARCHAR2(26) NOT NULL,
    scope_kind VARCHAR2(16 CHAR) NOT NULL,
    scope_ref VARCHAR2(26 CHAR),
    period_kind VARCHAR2(8 CHAR) DEFAULT 'month' NOT NULL,
    currency VARCHAR2(8 CHAR) NOT NULL,
    amount NUMBER(20, 4) NOT NULL,
    warn_ratio NUMBER(10, 4) DEFAULT 0.8 NOT NULL,
    is_enabled NUMBER(1) DEFAULT 1 NOT NULL,
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_bill_budgets PRIMARY KEY (id)
);

CREATE INDEX ix_bill_budgets_scope ON bill_budgets (scope_kind, is_enabled);
