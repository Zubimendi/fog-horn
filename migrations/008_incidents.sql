-- 008_incidents.sql
CREATE TYPE incident_status AS ENUM (
    'INVESTIGATING','IDENTIFIED','MONITORING','RESOLVED','RETRACTED'
);

CREATE TABLE incidents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title               TEXT NOT NULL,
    status              incident_status NOT NULL DEFAULT 'INVESTIGATING',  -- projection cache
    started_at          TIMESTAMPTZ NOT NULL,   -- backdated onset
    posted_at           TIMESTAMPTZ,            -- when first communicated
    resolved_at         TIMESTAMPTZ,
    auto_opened         BOOLEAN NOT NULL DEFAULT FALSE,
    retracted_reason    TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE incident_components (
    incident_id     UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    component_id    UUID NOT NULL REFERENCES components(id),
    impact          component_status NOT NULL,
    PRIMARY KEY (incident_id, component_id)
);

-- append-only: updates table, never editable
CREATE TABLE incident_updates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id     UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status          incident_status NOT NULL,
    message         TEXT NOT NULL,
    posted_by       TEXT NOT NULL DEFAULT 'system',
    posted_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Row-level trigger: block UPDATE and DELETE
CREATE OR REPLACE FUNCTION incident_updates_no_edit()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'incident_updates is append-only: UPDATE and DELETE are forbidden';
END;
$$;

CREATE TRIGGER incident_updates_no_update
    BEFORE UPDATE ON incident_updates
    FOR EACH ROW EXECUTE FUNCTION incident_updates_no_edit();

CREATE TRIGGER incident_updates_no_delete
    BEFORE DELETE ON incident_updates
    FOR EACH ROW EXECUTE FUNCTION incident_updates_no_edit();

-- Statement-level trigger: block TRUNCATE
CREATE OR REPLACE FUNCTION incident_updates_no_truncate()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'incident_updates is append-only: TRUNCATE is forbidden';
END;
$$;

CREATE TRIGGER incident_updates_no_truncate
    BEFORE TRUNCATE ON incident_updates
    FOR EACH STATEMENT EXECUTE FUNCTION incident_updates_no_truncate();

-- Revoke direct mutation access (app connects as foghorn user)
REVOKE UPDATE, DELETE ON incident_updates FROM PUBLIC;
REVOKE TRUNCATE ON incident_updates FROM PUBLIC;
