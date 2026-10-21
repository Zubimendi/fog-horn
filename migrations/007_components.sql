-- 007_components.sql
CREATE TYPE component_status AS ENUM (
    'operational',
    'under_maintenance',
    'degraded_performance',
    'partial_outage',
    'major_outage'
);

CREATE TABLE components (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT,
    public      BOOLEAN NOT NULL DEFAULT TRUE,
    group_name  TEXT,
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE component_targets (
    component_id    UUID NOT NULL REFERENCES components(id) ON DELETE CASCADE,
    target_id       UUID NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    PRIMARY KEY (component_id, target_id)
);
