# Agent context: building Foghorn from scratch

Read `docs/ARCHITECTURE.md` fully first. The sections most likely to be
quietly "simplified" into something wrong: §2.1 (SSRF at dial time),
§3.3/§3.5 (UNKNOWN and backdated onset), §4 (interval-based uptime), §8
(snapshot isolation). Do not.

## Step 0: infra (set up yourself, not included)
Go layout matching Dispatcher/LedgerLine (`cmd/api`, `cmd/probe`,
`cmd/evaluator`, `cmd/publisher`, `cmd/pagehost`, `internal/...`).
`docker-compose.yml` with Postgres and MinIO (snapshot store). Deps:
`pgx/v5`, `chi`, `zap`, `prometheus/client_golang`. Hand-written SQL
migrations. Dispatcher/QueueLine must be reachable for alert delivery.
`cmd/pagehost` must NOT import the database package.

## Step 1: data model (tenant-scoped everywhere)
- `tenants`, `plans` (max_monitors, min_interval, max_locations).
- `probe_locations` (id, region, healthy, last_control_ok_at).
- `targets` (id, tenant_id, type HTTP|TCP|DNS|TLS|HEARTBEAT, config jsonb
  with secrets encrypted, expected_status, assertion, latency_threshold,
  n_fail, n_recover, quorum, interval, paused).
- `target_schedule` (target_id, location_id, interval, scheduled_for,
  next_run_at, paused). PK `(target_id, location_id)`.
- `check_results` (target_id, location_id, scheduled_for, executed_at,
  class OK|OK_SLOW|TARGET_FAILURE|PROBE_ERROR|CONFIG_ERROR, reason,
  status_code, dns_ms, connect_ms, tls_ms, ttfb_ms, total_ms).
  **Partition by range on `scheduled_for`, daily.**
  `UNIQUE (target_id, location_id, scheduled_for)`.
- `location_state` (target_id, location_id, consecutive_failures,
  consecutive_successes, state, streak_started_at,
  last_evaluated_scheduled_for).
- `target_states` (target_id, state UP|DOWN|PARTIAL|DEGRADED|UNKNOWN|PAUSED,
  since, last_fresh_result_at).
- `state_intervals` (id, target_id, state, start_at, end_at NULL while open).
- `latency_rollups_hourly` (target_id, location_id, hour, bucket_counts
  int[], count, sum_ms).
- `components` (id, tenant_id, name, public, group), `component_targets`.
- `incidents` (id, tenant_id, title, status [projection], started_at,
  posted_at, auto_opened, retracted_reason), `incident_components`
  (impact), `incident_updates` (append-only: id, incident_id, status,
  message, posted_by, posted_at).
- `maintenance_windows` (id, tenant_id, starts_at, ends_at, tz, rrule,
  status, retroactive, reason, created_by).
- `alert_events` (outbox: id, tenant_id, kind, dedupe_key UNIQUE, payload,
  state, created_at, delivered_at), `alert_state` (ack, escalation_due_at).
- `heartbeat_tokens` (target_id, token_hash, rotated_at).
- `status_snapshots` (tenant_id, version, etag, generated_at, object_key),
  `current_snapshot` pointer.
- `audit_log` (append-only: retroactive maintenance, retractions, config
  changes).

## Step 2: build in this order
1. **Pure modules first (no I/O):** hysteresis state machine, quorum
   evaluator, interval builder with backdating, uptime/coverage calculator
   with exclusions, latency histogram merge, nines table helper, SSRF
   address classifier. Table-driven tests before anything else.
2. **Guarded HTTP prober:** `net.Dialer.Control` IP check, deny list with
   IPv4-mapped normalization, redirect cap, scheme allow-list, fresh
   connection per check, `httptrace` timings, body read cap, hard deadline,
   failure classification into the four classes.
3. **Scheduler claim loop:** the `SKIP LOCKED` + skip-ahead UPDATE from
   §1, hash-derived phase offset, staggered per-location phase, lag metric.
4. **Batched idempotent result writer.**
5. **Evaluator:** in-order per `(target, location)`, ignore older results
   for state, apply hysteresis, apply quorum, open/close `state_intervals`
   with **backdated onset**, write alert outbox in the same transaction,
   row-lock the target state.
6. **Sweeper:** clock-driven `UNKNOWN` marking for stale targets and
   heartbeat-monitor absence; monitoring-gap alerts.
7. **Probe self-test:** control checks, `UNHEALTHY` marking, exclusion from
   quorum.
8. **Rollups:** idempotent last-`K`-hours latency histogram recompute;
   uptime from intervals with maintenance/pause exclusion.
9. **Incidents:** transition table, row-locked transitions, append-only
   triggers **including statement-level `BEFORE TRUNCATE`**, projection +
   replay check, auto-open policy (none/draft/auto-publish), auto-recovery
   to `MONITORING` only, retraction.
10. **Components + maintenance:** derived status with explicit severity
    ordering; maintenance lifecycle; IANA-zone recurrence expansion;
    retroactive-window audit rule.
11. **Alerting:** edge-triggered outbox, deterministic dedupe key, publish
    to Dispatcher with the dedupe key as idempotency key, ack and one
    escalation step, maintenance and flap suppression, recovery alerts.
12. **Snapshot publisher:** render HTML+JSON from the three layers, write
    to MinIO, atomic pointer swap, `generated_at`, debounce, periodic pass.
13. **`cmd/pagehost`:** serves current snapshot from MinIO only, ETag/304,
    cache headers, stale banner past threshold.
14. **Heartbeat endpoints** (`/ping/{token}`, `/ping/{token}/fail`),
    hashed tokens, rotation.
15. **Dead-man's heartbeat emitter** to an external URL; self-SLI metrics.
16. **Chaos demo script** (§14) against the other projects.

## Step 3: tests that prove the claims
- **Hysteresis table:** every transition and threshold boundary;
  recovery needs `N_recover`, not `N_fail`.
- **Backdated onset:** simulate failures at t=0,60,120 with `N_fail=3`;
  the DOWN interval must start at t=0, not t=120. Uptime for the window
  reflects the true duration.
- **Absence ≠ up (key test):** stop all probes. After the staleness
  threshold the target is `UNKNOWN`, **no outage incident** is created, a
  monitoring-gap alert fires, and coverage drops.
- **Probe error vs target failure:** break the probe's resolver; results
  are `PROBE_ERROR`, no downtime interval, no outage alert. Fail control
  checks; the probe is excluded from quorum.
- **Quorum:** 1 of 3 locations down → `PARTIAL`, no DOWN; 2 of 3 → `DOWN`;
  unhealthy locations excluded from the denominator.
- **Out-of-order results:** deliver an older result after a newer one; state
  does not regress; result still stored.
- **SSRF classifier:** large table: `127.0.0.1`, `::1`, `0.0.0.0`, RFC 1918
  ranges, `169.254.169.254`, `100.64.0.1`, `fc00::1`, `fe80::1`,
  multicast, `::ffff:127.0.0.1`, `::ffff:169.254.169.254` all blocked;
  public addresses allowed.
- **DNS rebinding:** test resolver returns a public IP for validation and a
  private IP at dial; the connection is refused at dial time.
- **Alternate encodings:** monitors for `2130706433`, `0x7f.0.0.1`,
  `127.1` are blocked (class `CONFIG_ERROR`).
- **Redirect to internal:** public URL redirecting to `http://169.254.169.254/`
  is blocked on the second hop; >5 hops rejected.
- **Resource bounds:** a slow-drip server hits the total deadline; a huge
  body is cut at the read cap; per-host concurrency cap respected.
- **No connection reuse:** a test server counts TCP accepts; N checks → N
  connections.
- **Scheduler:** 8 workers, concurrent claims; no `(target, location,
  scheduled_for)` ever claimed twice; pause a worker 5 min, resume: no
  catch-up burst, phase preserved; phase offsets spread (not all at :00);
  staggered locations.
- **Uptime math:** gaps excluded from numerator and denominator and shown
  as lower coverage; maintenance intersection subtracts; retroactive
  window requires reason + audit event and is flagged on the report; nines
  table spot checks (99.9% over 30 d = 43.2 min).
- **Histograms:** merging two hours' buckets equals the bucket counts of
  the combined raw data; percentile from merged buckets is within bucket
  resolution; recompute is idempotent with late data.
- **Partition retention:** dropping a partition leaves intervals and
  rollups intact.
- **Incidents:** every legal and illegal transition; concurrent updates
  serialize; direct UPDATE/DELETE/**TRUNCATE** on `incident_updates`
  rejected; projection equals replay; monitor recovery → `MONITORING` only;
  retraction keeps audit history.
- **Derived component status:** overlapping incidents and maintenance
  resolve by the severity ordering; resolving one incident recomputes.
- **Maintenance:** alerts suppressed, data still collected, uptime
  exclusion applied; DST-day recurrence test (nonexistent and repeated
  local times).
- **Alerts:** 100 consecutive failures → exactly one alert; republish with
  the same dedupe key is a no-op at Dispatcher; ack stops escalation;
  unacknowledged escalates once; recovery alert sent.
- **Heartbeat:** expected 60 s + 30 s grace; no ping → DOWN at ~90 s; late
  ping recovers; duplicates harmless; token stored hashed; explicit `/fail`
  marks failure immediately.
- **Snapshot isolation (key test):** stop Postgres; `cmd/pagehost` still
  serves the last snapshot; staleness banner appears past threshold;
  ETag/304 works.
- **Atomic publish:** hammer reads while publishing repeatedly; no reader
  ever receives a partial snapshot.
- **Information hygiene:** scan the public snapshot for hostnames, IPs,
  check configs, internal IDs; zero matches.
- **Dead-man:** stop the emitter; the external endpoint's miss is
  detectable.

## Design decisions already made (don't relitigate)
1. Three layers (observation, judgment, communication), one-way flow.
2. SSRF guard validates the dialed IP at connect time via the dialer hook;
   deny list with IPv4-mapped normalization; no response content returned.
3. Fresh connection per check; failures classified into four classes;
   probe errors never count as downtime.
4. Claim advances the schedule (skip-ahead, phase-preserving); a missed
   check is a gap, never retried. Do not move scheduling onto QueueLine.
5. `UNKNOWN` is a first-class state; absence is detected by a sweeper.
6. Outage onset is backdated to the first failure of the confirming streak.
7. Uptime from state intervals, with coverage always reported; maintenance
   exclusion is auditable and retroactive exclusion is flagged.
8. Incident updates append-only (row triggers + TRUNCATE trigger +
   REVOKE); status is a projection; machines detect up, humans declare over.
9. Component status is derived by explicit severity ordering, never set.
10. Status page is a snapshot served by a database-free binary, with
    atomic publish and an honest staleness indicator.
11. Alerts are edge-triggered via outbox with deterministic dedupe keys,
    delivered through Dispatcher; do not rebuild delivery.