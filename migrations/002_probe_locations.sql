-- 002_probe_locations.sql
CREATE TABLE probe_locations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    region           TEXT NOT NULL,
    display_name     TEXT NOT NULL,
    healthy          BOOLEAN NOT NULL DEFAULT TRUE,
    last_control_ok_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO probe_locations (region, display_name) VALUES
    ('us-east-1',  'US East (N. Virginia)'),
    ('eu-west-1',  'EU West (Ireland)'),
    ('ap-south-1', 'Asia Pacific (Mumbai)');
