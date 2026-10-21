-- 001_tenants_plans.sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE plans (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    max_monitors    INT  NOT NULL DEFAULT 50,
    min_interval    INTERVAL NOT NULL DEFAULT '60 seconds',
    max_locations   INT  NOT NULL DEFAULT 3,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO plans (name, max_monitors, min_interval, max_locations) VALUES
    ('free',       10,  '60 seconds',  1),
    ('starter',    50,  '60 seconds',  2),
    ('pro',        500, '30 seconds',  3),
    ('enterprise', 5000,'15 seconds',  5);

CREATE TABLE tenants (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    slug        TEXT NOT NULL UNIQUE,
    plan_id     UUID NOT NULL REFERENCES plans(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
