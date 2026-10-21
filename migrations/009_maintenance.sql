-- 009_maintenance.sql
CREATE TYPE maintenance_status AS ENUM ('SCHEDULED','IN_PROGRESS','COMPLETED','CANCELLED');

CREATE TABLE maintenance_windows (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ NOT NULL,
    tz              TEXT NOT NULL DEFAULT 'UTC',   -- IANA timezone name
    rrule           TEXT,                          -- RFC 5545 RRULE for recurrence
    status          maintenance_status NOT NULL DEFAULT 'SCHEDULED',
    retroactive     BOOLEAN NOT NULL DEFAULT FALSE,
    reason          TEXT,
    created_by      TEXT NOT NULL DEFAULT 'system',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT retroactive_requires_reason CHECK (
        NOT retroactive OR (retroactive AND reason IS NOT NULL)
    )
);

CREATE TABLE maintenance_components (
    maintenance_id  UUID NOT NULL REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    component_id    UUID NOT NULL REFERENCES components(id),
    PRIMARY KEY (maintenance_id, component_id)
);
