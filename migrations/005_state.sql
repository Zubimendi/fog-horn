-- 005_state.sql
CREATE TYPE location_state_val AS ENUM ('UP','DOWN','UNKNOWN');
CREATE TYPE target_state_val   AS ENUM ('UP','DOWN','PARTIAL','DEGRADED','UNKNOWN','PAUSED');

CREATE TABLE location_state (
    target_id                   UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    location_id                 UUID NOT NULL REFERENCES probe_locations(id),
    consecutive_failures        INT NOT NULL DEFAULT 0,
    consecutive_successes       INT NOT NULL DEFAULT 0,
    state                       location_state_val NOT NULL DEFAULT 'UNKNOWN',
    streak_started_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_evaluated_scheduled_for TIMESTAMPTZ,
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target_id, location_id)
);

CREATE TABLE target_states (
    target_id           UUID PRIMARY KEY REFERENCES targets(id) ON DELETE CASCADE,
    state               target_state_val NOT NULL DEFAULT 'UNKNOWN',
    since               TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_fresh_result_at TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE state_intervals (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_id   UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    state       target_state_val NOT NULL,
    start_at    TIMESTAMPTZ NOT NULL,
    end_at      TIMESTAMPTZ  -- NULL while the interval is open
);

CREATE INDEX state_intervals_target_idx ON state_intervals(target_id, start_at DESC);
CREATE INDEX state_intervals_open_idx   ON state_intervals(target_id) WHERE end_at IS NULL;
