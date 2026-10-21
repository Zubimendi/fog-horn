-- 003_targets.sql
CREATE TYPE target_type AS ENUM ('HTTP','TCP','DNS','TLS','HEARTBEAT');

CREATE TABLE targets (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    type                target_type NOT NULL,
    config              JSONB NOT NULL DEFAULT '{}',  -- secrets envelope-encrypted
    expected_status     INT,
    assertion           JSONB,
    latency_threshold   INTERVAL,
    n_fail              INT NOT NULL DEFAULT 3,
    n_recover           INT NOT NULL DEFAULT 2,
    quorum              NUMERIC(4,3) NOT NULL DEFAULT 0.5,
    interval            INTERVAL NOT NULL DEFAULT '60 seconds',
    paused              BOOLEAN NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX targets_tenant_idx ON targets(tenant_id);
CREATE INDEX targets_active_idx ON targets(tenant_id) WHERE NOT paused;

CREATE TABLE target_schedule (
    target_id       UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    location_id     UUID NOT NULL REFERENCES probe_locations(id),
    interval        INTERVAL NOT NULL,
    scheduled_for   TIMESTAMPTZ,
    next_run_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    paused          BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (target_id, location_id)
);

CREATE INDEX target_schedule_due_idx ON target_schedule(location_id, next_run_at)
    WHERE NOT paused;
