-- 012_snapshots.sql
CREATE TABLE status_snapshots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    version         BIGINT NOT NULL DEFAULT 0,
    etag            TEXT NOT NULL,
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    object_key      TEXT NOT NULL  -- path in object storage
);

CREATE TABLE current_snapshot (
    tenant_id       UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    snapshot_id     UUID NOT NULL REFERENCES status_snapshots(id),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
