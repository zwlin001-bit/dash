-- 0004_jobs.oracle.sql
-- Job 引擎核心表 (dash P2-03 / 02-database.md §4.4)

CREATE TABLE jobs (
    id VARCHAR2(26 CHAR) NOT NULL,
    job_kind VARCHAR2(48 CHAR) NOT NULL,
    job_state VARCHAR2(16 CHAR) NOT NULL,
    target_kind VARCHAR2(32 CHAR),
    target_id VARCHAR2(26 CHAR),
    params_json CLOB,
    result_json CLOB,
    error_text CLOB,
    attempt NUMBER(9) DEFAULT 0 NOT NULL,
    max_attempt NUMBER(9) DEFAULT 1 NOT NULL,
    scheduled_at_ms NUMBER(19) NOT NULL,
    started_at_ms NUMBER(19),
    finished_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_jobs PRIMARY KEY (id)
);

CREATE INDEX ix_jobs_state_sched ON jobs (job_state, scheduled_at_ms);

CREATE INDEX ix_jobs_target ON jobs (target_kind, target_id);

CREATE INDEX ix_jobs_created ON jobs (created_at_ms);

CREATE TABLE job_steps (
    id VARCHAR2(26 CHAR) NOT NULL,
    job_id VARCHAR2(26 CHAR) NOT NULL,
    step_index NUMBER(9) NOT NULL,
    name VARCHAR2(64 CHAR) NOT NULL,
    step_state VARCHAR2(16 CHAR) NOT NULL,
    log_text CLOB,
    started_at_ms NUMBER(19),
    finished_at_ms NUMBER(19),
    created_at_ms NUMBER(19) NOT NULL,
    updated_at_ms NUMBER(19) NOT NULL,
    CONSTRAINT pk_job_steps PRIMARY KEY (id)
);

CREATE INDEX ix_job_steps_job ON job_steps (job_id, step_index);
