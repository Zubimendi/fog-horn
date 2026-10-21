-- 006_rollups.sql
CREATE TABLE latency_rollups_hourly (
    target_id       UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    location_id     UUID NOT NULL REFERENCES probe_locations(id),
    hour            TIMESTAMPTZ NOT NULL,  -- truncated to hour
    bucket_counts   BIGINT[] NOT NULL,     -- fixed bucket array (e.g. 20 buckets)
    count           BIGINT NOT NULL DEFAULT 0,
    sum_ms          NUMERIC(14,3) NOT NULL DEFAULT 0,
    recomputed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, location_id, hour)
);
