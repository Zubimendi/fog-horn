# Architecture: principles → design

## System shape

```
 target (HTTP/TCP/DNS/TLS)               cron job / batch (push model)
        ▲                                        │ heartbeat ping
        │ probe: fresh connection, SSRF-guarded  ▼
 ┌─────────────┐  claim due ticks   ┌────────────────────┐
 │ probe workers │◀─────────────────│ target_schedule      │ next_run_at, phase-preserving
 │ (per region)  │  FOR UPDATE       └────────────────────┘
 └──────┬──────┘  SKIP LOCKED
        │ batched, idempotent insert  UNIQUE(target, location, scheduled_for)
        ▼
 ┌────────────────────┐  LAYER 1: OBSERVATIONS (raw, day-partitioned, short retention)
 │ check_results        │
 └──────┬─────────────┘
        ▼ evaluator (ordered per target+location)  +  sweeper (absence of data)
 ┌────────────────────┐  LAYER 2: JUDGMENT (machine-derived: hysteresis + quorum)
 │ target_states        │──▶ state_intervals (onset BACKDATED) ──▶ uptime %, coverage %
 └──────┬─────────────┘
        │ same transaction ──▶ alert outbox ──▶ Dispatcher (idempotent publish)
        ▼
 ┌────────────────────┐  LAYER 3: COMMUNICATION (human-curated, append-only)
 │ incidents + updates  │──▶ components (status DERIVED, never set)
 │ maintenance windows  │
 └──────┬─────────────┘
        ▼ snapshot publisher (atomic swap)
 ┌────────────────────┐
 │ object storage / CDN │◀── status page host (NO database dependency)
 └────────────────────┘
```

---

## 0. Three different things people call "status"

| Layer | Question it answers | Produced by | Who can be wrong |
|---|---|---|---|
| Observation | "What did probe P see at time T?" | Probes | The probe, the network |
| Judgment | "Is the target down?" | Evaluator (hysteresis + quorum) | The thresholds |
| Communication | "What are we telling customers?" | Humans (optionally auto-drafted) | The communicator |

Collapsing them is the root cause of most monitoring pain. If one failed
check is "an outage," you get alert storms and a public page that cries
wolf. If a human statement is "just the monitor's state," the page can
never say anything the probes didn't detect (a broken checkout flow behind
a healthy `/healthz`). Each layer is stored separately, has its own state
machine, and flows one way:
**observations → judgment → communication.** Nothing flows backward.
Machine detection can *draft* or *update* communication; communication
never edits observations.

---

## 1. Scheduling checks across many targets

**Requirement:** thousands of targets, several probe locations, every
check running on time, no check running twice, no thundering herd.

**Mechanism.** One row per `(target, location)` in `target_schedule` with a
`next_run_at`. A probe worker claims due rows for *its own location* and
advances them in the same statement:

```sql
-- sketch
WITH due AS (
  SELECT target_id, location_id FROM target_schedule
  WHERE location_id = $1 AND next_run_at <= now() AND NOT paused
  ORDER BY next_run_at LIMIT $2
  FOR UPDATE SKIP LOCKED
)
UPDATE target_schedule s SET
  scheduled_for = s.next_run_at,                       -- old value captured
  next_run_at   = s.next_run_at + s.interval *
      (floor(extract(epoch FROM now() - s.next_run_at)
             / extract(epoch FROM s.interval)) + 1)
FROM due WHERE s.target_id = due.target_id AND s.location_id = due.location_id
RETURNING s.target_id, s.location_id, s.scheduled_for;
```

Properties, each deliberate:

- **`SKIP LOCKED`** lets any number of workers pull from the same table with
  no coordination and no double-claims (the technique from Dispatcher and
  QueueLine). No leader is needed for correctness.
- **Phase-preserving, skip-ahead advance.** `next_run_at` moves to the first
  scheduled tick strictly *after now*, in whole intervals from the original
  phase. If a worker paused for 5 minutes there is **no catch-up burst** of
  missed checks. Missed ticks are skipped and become honest gaps (§3).
  Advancing from `now()` instead would make every check drift later each
  cycle.
- **The claim itself advances the schedule**, before the check runs. If the
  worker dies mid-check, that tick produces no result and the next tick
  proceeds on time. There is no lease to expire and no reaper, because a
  check is a measurement, not work that must complete. This is the
  opposite policy to CronMesh's misfire handling (where a missed run may
  still be owed), and the difference is the point.
- **Jitter by construction.** A target's phase offset within its interval is
  derived from a hash of its ID, so 5,000 targets with a 60 s interval don't
  all fire at :00. Locations get *staggered* phases for the same target (3
  locations on a 60 s interval ≈ one observation every 20 s), which shortens
  detection time without increasing per-location load.
- **The database clock decides "due,"** not each worker's clock, so skewed
  workers can't disagree about what's overdue.
- **Result writes are idempotent.** `UNIQUE (target_id, location_id,
  scheduled_for)` means a retried batch insert collapses harmlessly.
- **Scheduler lag is an SLI of Foghorn itself:** `executed_at - scheduled_for`
  per check, alerting when it grows.

**Why not QueueLine.** QueueLine's strengths (retry with backoff,
dead-lettering, lease reaping) are wrong for measurements. Retrying a failed
check *changes what was measured*; the only legitimate retry is an explicit
confirmation policy at the judgment layer (§3). Per-check job rows would
also be enormous churn for no benefit. Forcing a pattern onto a problem it
doesn't fit is the mirror image of rebuilding one you already have.

---

## 2. Probe correctness and safety

### 2.1 SSRF: a monitor is a request-forgery machine by construction

A service that fetches URLs supplied by users, then reports "up/down,"
status, and latency, is an oracle for the *prober's* network: "does
`10.0.3.7:6379` accept connections?" is answerable by creating a monitor.
Cloud metadata endpoints (`169.254.169.254`) hand out credentials. Every
defense below is required; none is sufficient alone.

1. **Validate the address actually dialed, at connect time.** In Go, use
   `net.Dialer.Control` (runs after DNS resolution, before the connection,
   receives `ip:port`). Checking the hostname when the monitor is created
   is not enough: DNS rebinding (public at validation, private at connect),
   alternate encodings (`2130706433`, `0x7f.1`, `127.1`) that resolvers turn
   into loopback, IPv4-mapped IPv6 (`::ffff:169.254.169.254`), and redirects
   all bypass a hostname check. A dial-time check on the resolved IP is
   indifferent to how the address was spelled or how it was reached.
2. **Explicit deny list**, not only stdlib helper methods: loopback,
   unspecified, link-local (includes metadata), RFC 1918, CGNAT
   `100.64.0.0/10` (not covered by `IsPrivate`), IPv6 ULA `fc00::/7` and
   link-local `fe80::/10`, multicast, benchmarking/documentation ranges.
   Normalize IPv4-mapped IPv6 to IPv4 *before* testing.
3. **Redirects:** cap hops (5), allow only `http`/`https`. Every hop dials
   through the same guarded dialer, so each is re-validated automatically.
4. **Port policy:** default 80/443; other ports only if the tenant has
   verified ownership of the host. Raw **TCP checks are the highest-risk
   type** (a port scanner); gate them behind ownership verification.
5. **Return no content.** Results store status code, timings, a boolean
   assertion outcome, and a bounded error *class*, never body excerpts or
   raw error strings that could echo internal responses.
6. **Private networks are served by a private probe running inside the
   customer's network** (out of scope for v1), never by loosening the guard
   on shared probes.
7. **Network-layer egress rules** on probe hosts as defense in depth. The
   application guard is the control that travels with the code.

### 2.2 Measure what a new client would experience

- **Fresh connection per check** (disable keep-alive). A monitor that reuses
  connections reports "up" while new connections would be refused.
- **Timing breakdown** via `httptrace` (DNS, connect, TLS, time to first
  byte, total). "Slow" means different things at each stage.
- **Bounded resources:** hard total deadline, a connect timeout, a read cap
  on body bytes (enough for an assertion, then close), a worker-level
  concurrency limit, and a per-host cap so a tenant with 1,000 monitors
  on one host can't hammer it.
- **Identification:** a clear `User-Agent` and a published list of probe
  IPs so customers can allowlist them.

### 2.3 Classify the failure, because the classes mean different things

| Class | Examples | Counts as target downtime? |
|---|---|---|
| `OK` / `OK_SLOW` | success; success above latency threshold | No (slow → DEGRADED) |
| `TARGET_FAILURE` | timeout, refused, NXDOMAIN, TLS error, bad status, assertion failed | **Yes** |
| `PROBE_ERROR` | probe out of file descriptors, probe's own resolver down | **No** |
| `CONFIG_ERROR` | blocked by SSRF guard, malformed URL | **No**, surfaced to the owner |

Treating a probe's own failure as target downtime is how a monitoring
outage becomes a false mass-incident. Each probe also runs periodic
**control checks** (a local sink and a couple of independent reference
endpoints). If controls fail, the probe marks itself `UNHEALTHY`; its
results are flagged and excluded from quorum.

---

## 3. Judgment: hysteresis, quorum, and the difference between "down" and "unknown"

### 3.1 Per-location hysteresis

Each `(target, location)` tracks consecutive failures and successes. It
turns `DOWN` after `N_fail` consecutive `TARGET_FAILURE`s and returns to
`UP` only after `N_recover` consecutive `OK`s (usually larger, so recovery
is conservative). Between thresholds it keeps its previous state.

**Detection latency is a formula, not a vibe.** With interval `I`, timeout
`T`, and `N_fail` consecutive failures at one location:
best case ≈ `(N_fail − 1)·I + T`, worst case ≈ `N_fail·I + T`. For
`I=60 s, N=3, T=10 s`: **130-190 s**. That is the price of avoiding false
alarms, and it must be a documented, configurable tradeoff per monitor.

### 3.2 Quorum across locations

A single location's network trouble looks identical to a target outage. The
target's state is derived from the *healthy, reporting* locations:

- `DOWN` if at least `Q` locations are `DOWN` (default: majority);
- `PARTIAL` if some, but fewer than `Q`, are down (regional problem);
- `UP` otherwise;
- `UNKNOWN` if fewer than `min_reporting` locations have fresh data.

Quorum assumes probe failures are *independent*. Probes all hosted in one
cloud region share a failure domain, and a regional cloud outage can blind
probes and target together. That is a real limitation (§15), not a footnote.

### 3.3 Missing data is not "up"

Absence of results cannot trigger a result-handling code path, because
nothing arrives to handle. A **sweeper** driven by the clock marks a target
`UNKNOWN` when no fresh result exists within `3·I` (the same
"absence needs a timer" lesson as presence TTLs and lease expiry elsewhere
in this portfolio). `UNKNOWN` is its own state: it does **not** create an
outage incident (we don't know), it raises a *monitoring-gap* alert, and it
is reported separately in uptime (§4).

### 3.4 Out-of-order results

Probes can deliver late (a partitioned worker flushing a backlog). The
evaluator keeps `last_evaluated_scheduled_for` per `(target, location)` and
**ignores results older than that for state purposes** (they're still stored
for history and latency). Late data must not make state regress.

### 3.5 Backdate the outage to its real onset

When a streak of failures finally confirms `DOWN`, the outage did not begin
at confirmation. It began at the first failing check. If the timeline
records the transition at confirmation time, **every outage's duration is
understated by the detection delay**, and reported uptime is inflated
systematically. So the DOWN interval starts at the *onset* (the moment the
`Q`-th location's failing streak began) and the previous UP interval is
closed there. Recovery is symmetric: DOWN ends at the first success of the
recovering streak. Closed intervals are never edited; backdating only
chooses where the open interval ends and the next begins.

---

## 4. Uptime math that doesn't lie

**Compute uptime from state intervals, not from check counts.** "Successful
checks ÷ total checks" silently ignores gaps (a missing check counts as
neither success nor failure) and treats every check as equally long.
Intervals cover time; gaps are visible.

```
uptime % = (UP + DEGRADED*)  /  (window − excluded)
excluded = PAUSED ∪ MAINTENANCE   (by interval intersection)
coverage % = observed time / window     (UNKNOWN is the unobserved part)
* DEGRADED counts as up by default, configurable
```

Rules:

- **Always publish coverage next to uptime.** "99.99% uptime" over 40%
  observed time is a misleading number. `UNKNOWN` policy is explicit:
  excluded (default) or counted as down.
- **Maintenance exclusion is subtractive and auditable.** Windows can't be
  created in the past without a `retroactive` flag, a reason, and an audit
  event, and such exclusions are marked on SLA reports. Quietly excluding
  downtime after the fact is exactly how SLAs get gamed.
- **Resolution is the check interval.** An outage shorter than `I` can be
  missed entirely. A 99.99% target allows 4.3 min/month, and detection
  latency alone (§3.1) can consume a large fraction of it. Say so.
- **Measurement window matters:** rolling 30 days vs. calendar month give
  different numbers; contractual SLAs define exclusions and measurement
  source themselves. Foghorn computes a measured **SLI from its own
  probes**, which is not automatically the customer's contractual figure.

| Target | Allowed downtime / 30 days | / year |
|---|---|---|
| 99% | 7.2 h | 3.65 d |
| 99.5% | 3.6 h | 1.83 d |
| 99.9% | 43.2 min | 8.76 h |
| 99.95% | 21.6 min | 4.38 h |
| 99.99% | 4.32 min | 52.6 min |

---

## 5. Storage and rollups

- **`check_results`** is raw, high-volume (10k targets × 3 locations × 1/min
  ≈ 43M rows/day), **range-partitioned by day**. Retention is then
  `DROP PARTITION` (instant, no table bloat), never `DELETE`.
- **Uptime comes from `state_intervals`**, which store only *transitions*, so
  they are tiny and retained indefinitely. Long-window uptime never scans raw
  data.
- **Latency uses per-hour histogram rollups with fixed buckets.** Histograms
  are mergeable (sum buckets); **percentiles are not** (you cannot average
  p99s). Rollups are **recomputed idempotently** for the last `K` hours from
  raw on each run (not incremented), so late-arriving data is absorbed without
  double counting. This is the "idempotent task keyed by logical window"
  discipline from PyDataRex.
- Raw retention is short (days). Rollups and intervals outlive it.

---

## 6. The incident lifecycle: a signal is not a statement

**Failing checks are signals. An incident is a public statement.** A monitor
can *draft or update* an incident; a human owns what gets said.

```
INVESTIGATING ─▶ IDENTIFIED ─▶ MONITORING ─▶ RESOLVED
      │              │              │
      └──────────────┴──────────────┴──▶ RETRACTED   (created in error; terminal)
 (any non-terminal state may also move BACK, e.g. MONITORING → INVESTIGATING on recurrence)
```

Design rules:

- **Updates are append-only.** Each `incident_update` carries a status and a
  message; corrections are new updates, never edits. A status page whose
  history can be rewritten isn't "honest." A `BEFORE UPDATE OR DELETE` row
  trigger **plus a statement-level `BEFORE TRUNCATE` trigger** (row triggers
  don't fire on `TRUNCATE`) enforce it, along with `REVOKE`.
- **Current status is a projection** of the latest update (the cache vs.
  append-only-source shape used throughout this portfolio), and a replay
  check can verify it.
- **Transitions are validated under a row lock** on the incident, so two
  operators posting at once serialize, and the second is checked against the
  already-updated state.
- **The machine can detect "up," but only a human declares "over."** Monitor
  recovery moves an auto-opened incident to `MONITORING`, never straight to
  `RESOLVED`. A target that recovers for two minutes and fails again should
  not produce a resolve/reopen cycle on a public page.
- **Auto-open policy is configuration:** none (humans only), draft (an
  internal incident awaiting confirmation), or auto-publish after `X`
  minutes of *confirmed* `DOWN`. Never "publish on first failure."
- **`started_at` ≠ `posted_at`.** `started_at` is the backdated onset (§3.5)
  and drives duration math; `posted_at` is when it was communicated. The
  gap between them is a metric (time-to-communicate).
- **No deletion, only retraction.** A false-positive incident becomes
  `RETRACTED` with a reason. It stays in the internal audit trail.

---

## 7. Components, derived status, and maintenance

**Component status is derived, never set.** Anything a human wants to say
goes through an incident (auditable, append-only); nobody edits a
component's status directly. The effective status is the maximum severity
over: active incident impacts, active maintenance, and (if enabled) the
mapped monitor state.

```
operational < under_maintenance < degraded_performance < partial_outage < major_outage
```

The ordering is explicit configuration. A concurrent real outage during
maintenance shows the outage, not "under maintenance." Each active incident
declares per-component impact; removing one incident recomputes the max.

**Maintenance** has its own lifecycle (`SCHEDULED → IN_PROGRESS →
COMPLETED/CANCELLED`) and must be announced in advance. While active:
checks **keep running and data is kept** (so you can still see what
happened), alerts are suppressed, and downtime inside the window is excluded
from uptime (§4). Recurrence rules store an **IANA time zone**, not an
offset: "Sunday 02:00 local" may not exist, or may occur twice, on a DST
change day, and the expansion must define behavior for both.

---

## 8. The status page must survive the outage

A status page that depends on the failing system is a rumor. Traffic to it
also spikes at exactly the worst moment: everyone who normally uses your
product goes looking for news.

- **Snapshot architecture.** The page never reads the live database. A
  **publisher** renders a per-tenant snapshot (server-rendered HTML + JSON,
  no JavaScript required to read status) from the three layers and writes it
  to object storage. Regeneration triggers: incident/maintenance/component
  changes (via the outbox, debounced) plus a periodic 30-60 s pass for
  monitor-derived status.
- **Atomic publish.** Write the new object, then swap a pointer, so a reader
  never sees a half-written page (the same atomic-cutover idea as
  SearchCraft's index swap).
- **Failure-domain isolation.** The page host is a **separate binary whose
  only dependency is the snapshot store**, with no Postgres connection. It
  keeps serving the last snapshot with Postgres down. Ideally it lives on
  different infrastructure, region, and DNS from the thing being monitored.
- **CDN-friendly:** short TTL, `ETag`/conditional requests, `stale-if-error`.
- **Honest degraded mode.** Every snapshot carries `generated_at`. If it is
  older than a threshold, the page *says so* ("status may be out of date")
  instead of confidently showing a stale "all operational."
- **Information hygiene.** Public pages show only components flagged public,
  with public IDs. Never hostnames, IPs, check configs, or raw errors.
- **Subscriptions:** RSS/Atom and webhooks in v1, delivered through
  Dispatcher. (Email needs double opt-in and abuse handling; out of scope.)

---

## 9. Alerting: transactional outbox, edge-triggered, deduplicated

Reuses Dispatcher's pattern deliberately; recognizing it is the exercise.

- **Edge-triggered, not level-triggered.** Alert on *transitions*, not on
  every failed check. 100 consecutive failures produce **one** alert.
- **Outbox in the same transaction as the state change.** The `UP→DOWN`
  interval write and the `alert_events` row commit together, so a crash
  can't produce a state change with no alert (or an alert for a state that
  never committed).
- **Deterministic dedupe key** `(target, state_interval_id, kind)`.
  Delivery uses **Dispatcher's idempotent publish** with that key as the
  idempotency key, so a retried delivery is a no-op. Delivery is
  at-least-once; receivers dedupe on the alert ID.
- **Lifecycle:** `TRIGGERED → ACKNOWLEDGED → RESOLVED`, with one escalation
  step: if not acknowledged within `T`, notify the next target. (On-call
  scheduling is out of scope.)
- **Suppression rules:** active maintenance suppresses alerts (data still
  recorded); flapping suppression collapses repeated transitions into one
  "flapping" alert plus a summary; recovery notifications are explicit.
- **Different kinds:** `TARGET_DOWN`, `TARGET_PARTIAL`, `MONITORING_GAP`
  (unknown ≠ down), `CERT_EXPIRING`, `PROBE_UNHEALTHY`.

---

## 10. Who watches the watcher

A monitor that is down alerts no one, and a monitoring outage must not
silently look like "all green."

- **Dead-man's switch:** Foghorn emits a heartbeat to an *external* service
  on a fixed cadence (a free hosted heartbeat checker, or **a second Foghorn
  instance in mutual watch**). If the heartbeat stops, something outside
  Foghorn alerts.
- **Self-observation:** scheduler lag, result-write lag, rollup lag, snapshot
  age, outbox delivery lag, quorum disagreement rate. Each has an alert.
- **Staleness is surfaced, not hidden:** snapshot age on the page (§8),
  `UNKNOWN` instead of `UP` (§3.3), coverage next to uptime (§4).

---

## 11. Push (heartbeat) monitors: the inverse model

Pull-based probing can't see a cron job or a batch pipeline. The inverse
model: the job pings Foghorn when it finishes; **absence of a ping beyond
`expected_interval + grace` is the failure.** This is a dead-man's switch
and the natural way to monitor CronMesh runs and PyDataRex DAGs (pointing
it at the portfolio is the demo, §14).

- The ping URL embeds a random capability token (≥128 bits), **stored
  hashed**, rotatable. Pings are idempotent; duplicates are harmless.
- Optional explicit `/fail` ping marks a run failed immediately instead of
  waiting for the grace period.
- Detection of *absence* is the sweeper's job (§3.3), the same mechanism.
- The endpoint sits behind GateKeeper-style rate limiting.

---

## 12. Multi-tenancy, abuse, and secrets

- Everything is tenant-scoped at the query level.
- **Quotas:** maximum monitors, **minimum interval** per plan, maximum
  locations. A tenant with 100k targets would otherwise dominate a
  time-ordered claim queue.
- **Abuse:** Foghorn is a traffic generator. Minimum intervals, per-host
  caps, ownership verification for sensitive check types, and rate limiting
  on creation (GateKeeper) keep it from becoming a DoS amplifier.
- **Check secrets** (auth headers, basic auth) are stored with envelope
  encryption (VaultKeep's pattern), decrypted only inside probe workers, never
  logged, and redacted from stored results.

---

## 13. Observability of Foghorn itself

Page-worthy: **scheduler lag**, **snapshot age**, **dead-man heartbeat
missed**, **outbox delivery lag**, **probe-unhealthy count**. Diagnostic:
failure-class mix per probe (a spike in `PROBE_ERROR` is a probe problem),
quorum disagreement rate, sweeper `UNKNOWN` transitions, rollup recompute
duration, claim contention, results rejected as out-of-order.

---

## 14. The demo: monitor the other 29 projects

Every project in this portfolio exposes `/healthz` (process up) and
`/readyz` (dependencies up). Create monitors for the readiness endpoints,
heartbeat monitors for CronMesh jobs, and run a **chaos script**: stop Redis
under GateKeeper, and watch (1) `/readyz` flip, (2) quorum confirm `DOWN`,
(3) the interval backdate to the true onset, (4) an incident auto-draft,
(5) the alert reach a webhook through Dispatcher, (6) the status page
snapshot update while Postgres is *also* stopped, (7) recovery move the
incident to `MONITORING`, (8) a human resolve it. Record it.

---

## 15. Out of scope / known limitations

- **Synthetic multi-step browser journeys** (Playwright-style) and **real-
  user monitoring**: different products. Foghorn measures from its probes,
  not from users.
- **Metrics and log ingestion** is ObserveStack's territory (Project 30).
- **On-call scheduling and paging policy** beyond one escalation step.
- **Custom-domain TLS provisioning, SMS, email double opt-in, theming.**
- **Private probes/agents** for internal networks (the correct answer to
  §2.1 item 6, not built).
- **Correlated failure domains:** probes sharing a cloud/region with a
  target can fail together; quorum assumes independence.
- **Partial breakage** invisible to a shallow check (a broken feature behind
  a healthy `/healthz`) is only caught if the check asserts it. That is why
  incidents (§6) can be opened by humans independent of any monitor.
- Uptime is a **measured SLI**, not a legal SLA computation.