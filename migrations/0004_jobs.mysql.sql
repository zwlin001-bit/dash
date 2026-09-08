-- 0004_jobs.mysql.sql
-- Job 引擎核心表 (dash P2-03 / 02-database.md §4.4)

CREATE TABLE IF NOT EXISTS jobs (
    id VARCHAR(26) NOT NULL,
    job_kind VARCHAR(48) NOT NULL,
    job_state VARCHAR(16) NOT NULL,
    target_kind VARCHAR(32),
    target_id VARCHAR(26),
    params_json LONGTEXT,
    result_json LONGTEXT,
    error_text LONGTEXT,
    attempt INT DEFAULT 0 NOT NULL,
    max_attempt INT DEFAULT 1 NOT NULL,
    scheduled_at_ms BIGINT NOT NULL,
    started_at_ms BIGINT,
    finished_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_jobs_state_sched (job_state, scheduled_at_ms),
    KEY ix_jobs_target (target_kind, target_id),
    KEY ix_jobs_created (created_at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS job_steps (
    id VARCHAR(26) NOT NULL,
    job_id VARCHAR(26) NOT NULL,
    step_index INT NOT NULL,
    name VARCHAR(64) NOT NULL,
    step_state VARCHAR(16) NOT NULL,
    log_text LONGTEXT,
    started_at_ms BIGINT,
    finished_at_ms BIGINT,
    created_at_ms BIGINT NOT NULL,
    updated_at_ms BIGINT NOT NULL,
    PRIMARY KEY (id),
    KEY ix_job_steps_job (job_id, step_index)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
