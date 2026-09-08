-- 0009_cloud_metrics.oracle.sql
-- 云侧监控指标时序表 (dash P2-10 / 16-cloud-metrics.md §2)

CREATE TABLE cloud_samples (
    cloud_account_id VARCHAR2(26) NOT NULL,
    res_ref VARCHAR2(191) NOT NULL,
    metric_code VARCHAR2(40) NOT NULL,
    ts_ms NUMBER(19) NOT NULL,
    value BINARY_DOUBLE NOT NULL,
    CONSTRAINT pk_cloud_samples PRIMARY KEY (cloud_account_id, res_ref, metric_code, ts_ms)
);

CREATE INDEX ix_cloud_samples_ts ON cloud_samples (ts_ms);
