-- 010_alerting.sql
CREATE TYPE alert_state AS ENUM ('PENDING','DELIVERED','ACKNOWLEDGED','ESCALATED','RESOLVED');
CREATE TYPE alert_kind  AS ENUM (
    'TARGET_DOWN','TARGET_PARTIAL','MONITORING_GAP','CERT_EXPIRING','PROBE_UNHEALTHY'
);

CREATE TABLE alert_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    kind            alert_kind NOT NULL,
    dedupe_key      TEXT NOT NULL UNIQUE,  -- deterministic; used as Dispatcher idempotency key
    payload         JSONB NOT NULL DEFAULT '{}',
    state           alert_state NOT NULL DEFAULT 'PENDING',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ
);

CREATE INDEX alert_events_pending_idx ON alert_events(state) WHERE state = 'PENDING';

CREATE TABLE alert_state (
    alert_id            UUID PRIMARY KEY REFERENCES alert_events(id) ON DELETE CASCADE,
    acknowledged_at     TIMESTAMPTZ,
    escalation_due_at   TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
