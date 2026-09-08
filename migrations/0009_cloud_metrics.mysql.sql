-- 0009_cloud_metrics.mysql.sql
-- 云侧监控指标时序表 (dash P2-10 / 16-cloud-metrics.md §2)

CREATE TABLE IF NOT EXISTS cloud_samples (
    cloud_account_id VARCHAR(26) NOT NULL,
    res_ref VARCHAR(191) NOT NULL,
    metric_code VARCHAR(40) NOT NULL,
    ts_ms BIGINT NOT NULL,
    value DOUBLE NOT NULL,
    PRIMARY KEY (cloud_account_id, res_ref, metric_code, ts_ms),
    KEY ix_cloud_samples_ts (ts_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
