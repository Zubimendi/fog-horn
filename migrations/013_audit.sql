-- 013_audit.sql
CREATE TYPE audit_action AS ENUM (
    'MAINTENANCE_RETROACTIVE',
    'INCIDENT_RETRACTED',
    'TARGET_CONFIG_CHANGED',
    'TOKEN_ROTATED'
);

CREATE TABLE audit_log (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    action      audit_action NOT NULL,
    actor       TEXT NOT NULL DEFAULT 'system',
    entity_id   UUID,
    reason      TEXT,
    payload     JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Append-only: no updates or deletes on audit_log
CREATE OR REPLACE FUNCTION audit_log_no_edit()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log is append-only';
END;
$$;

CREATE TRIGGER audit_log_no_update
    BEFORE UPDATE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_edit();

CREATE TRIGGER audit_log_no_delete
    BEFORE DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_no_edit();
