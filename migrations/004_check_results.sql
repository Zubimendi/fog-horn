-- 004_check_results.sql
-- Day-partitioned raw results. Retention via DROP PARTITION.
CREATE TYPE check_class AS ENUM ('OK','OK_SLOW','TARGET_FAILURE','PROBE_ERROR','CONFIG_ERROR');

CREATE TABLE check_results (
    target_id       UUID NOT NULL,
    location_id     UUID NOT NULL,
    scheduled_for   TIMESTAMPTZ NOT NULL,
    executed_at     TIMESTAMPTZ NOT NULL,
    class           check_class NOT NULL,
    reason          TEXT,
    status_code     INT,
    dns_ms          NUMERIC(10,3),
    connect_ms      NUMERIC(10,3),
    tls_ms          NUMERIC(10,3),
    ttfb_ms         NUMERIC(10,3),
    total_ms        NUMERIC(10,3),
    UNIQUE (target_id, location_id, scheduled_for)
) PARTITION BY RANGE (scheduled_for);

-- Bootstrap two partitions (today and tomorrow); cron/pg_partman handles the rest.
CREATE TABLE check_results_default PARTITION OF check_results DEFAULT;

CREATE INDEX check_results_target_loc_idx
    ON check_results(target_id, location_id, scheduled_for DESC);
