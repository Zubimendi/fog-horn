# Foghorn

[![Go Version](https://img.shields.io/badge/go-1.22%2B-blue.svg)](https://golang.org)
[![Build Status](https://img.shields.io/badge/build-passing-brightgreen.svg)]()
[![Tests](https://img.shields.io/badge/tests-passing-brightgreen.svg)]()
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-16-blue.svg)](https://www.postgresql.org/)
[![MinIO](https://img.shields.io/badge/Storage-MinIO%2FS3-red.svg)](https://min.io/)
[![Invariants](https://img.shields.io/badge/invariants-10%2F10_enforced-success.svg)]()

> **Distributed uptime monitoring and public status page service in Go.**
> It monitors target services and communicates their status honestly. Unlike conventional uptime monitors that distort availability metrics or crash alongside your infrastructure, Foghorn enforces mathematical precision, dial-time SSRF security, backdated outage intervals, and complete failure-domain isolation.

---

## 📑 Table of Contents

- [The 4 Ways Conventional Status Pages Lie](#-the-4-ways-conventional-status-pages-lie)
- [System Architecture](#-system-architecture)
- [The Three Layers of Status](#-the-three-layers-of-status)
- [The 10 Non-Negotiable Invariants](#-the-10-non-negotiable-invariants)
- [Project Layout](#-project-layout)
- [Getting Started](#-getting-started)
- [API Reference](#-api-reference)
- [Postman Collection](#-postman-collection)
- [Chaos Verification & Testing](#-chaos-verification--testing)
- [Observability & Self-SLIs](#-observability--self-slis)
- [v2 Roadmap](#-v2-roadmap)
- [License](#-license)

---

## 🚫 The 4 Ways Conventional Status Pages Lie

1. **They confuse a probe network blip with customer downtime.** A single packet drop or local route flap causes an instant false-alarm outage alert.
2. **They lie about outage start times.** If confirmation requires 3 failed checks over 3 minutes, conventional tools record the outage start at minute 3, erasing the first 2 minutes of real customer impact from the SLA.
3. **They compute uptime from raw check counts.** 99 successful checks out of 100 is reported as 99% uptime, even if the one failed check represented a 4-hour blackout where checks were skipped.
4. **They go down when your database goes down.** Traditional status pages execute SQL queries to render status. When your infrastructure suffers a catastrophic database crash, the status page crashes with it.

---

## 🏗️ System Architecture

```
 target (HTTP/TCP/DNS/TLS)               cron job / batch (push model)
        ▲                                        │ heartbeat ping
        │ probe: fresh connection, SSRF-guarded  ▼
 ┌──────────────┐  claim due ticks   ┌────────────────────┐
 │ probe worker │◀─────────────────│ target_schedule    │ next_run_at, phase-preserving
 │ (per region) │  FOR UPDATE       └────────────────────┘
 └──────┬───────┘  SKIP LOCKED
        │ batched, idempotent insert  UNIQUE(target, location, scheduled_for)
        ▼
 ┌────────────────────┐  LAYER 1: OBSERVATIONS (raw, day-partitioned, short retention)
 │ check_results      │
 └──────┬─────────────┘
        ▼ evaluator (ordered per target+location)  +  sweeper (absence of data)
 ┌────────────────────┐  LAYER 2: JUDGMENT (machine-derived: hysteresis + quorum)
 │ target_states      │──▶ state_intervals (onset BACKDATED) ──▶ uptime %, coverage %
 └──────┬─────────────┘
        │ same transaction ──▶ alert outbox ──▶ Dispatcher (idempotent publish)
        ▼
 ┌────────────────────┐  LAYER 3: COMMUNICATION (human-curated, append-only)
 │ incidents + updates│──▶ components (status DERIVED, never set)
 │ maintenance windows│
 └──────┬─────────────┘
        ▼ snapshot publisher (atomic swap)
 ┌────────────────────┐
 │ object storage/S3  │◀── status page host (ZERO database dependency)
 └────────────────────┘
```

---

## 🧩 The Three Layers of Status

| Layer | Question it Answers | Produced By | Failure Mode Handled |
|---|---|---|---|
| **Observation** | *"What did probe P observe at time T?"* | Autonomous probe workers | Transient network errors, DNS timeouts |
| **Judgment** | *"Is the target system degraded or down?"* | Evaluator engine (hysteresis + quorum) | Flapping endpoints, false positives |
| **Communication** | *"What narrative are we communicating?"* | Incident commanders / System | Contextual updates, impact explanation |

Data flows unidirectionally: **Observation → Judgment → Communication**. Machine judgments can draft communication, but communication never mutates raw observations.

---

## 🛡️ The 10 Non-Negotiable Invariants

1. **SSRF Guard at Dial Time:** The SSRF filter executes inside `net.Dialer.Control` after DNS resolution. DNS rebinding and decimal IP encodings (`2130706433`) cannot bypass it.
2. **Probe Errors Never Mean Target Downtime:** Probe workers run continuous canary self-tests. If self-tests fail, workers mark themselves `UNHEALTHY` and emit `PROBE_ERROR`.
3. **Outage Onset Backdating:** Outage intervals are backdated to the timestamp of the first failure in the confirming streak via `intervals.BackdateOnset`.
4. **Absence = UNKNOWN, Never UP:** When a monitor stops reporting or heartbeats stop arriving, the sweeper marks the state `UNKNOWN` and triggers a `MONITORING_GAP` alert.
5. **Uptime from State Intervals Only:** Calculated mathematically from contiguous `state_intervals` with maintenance exclusions — never raw check percentages.
6. **Append-Only Incident Updates:** Database-level triggers block `UPDATE`, `DELETE`, and `TRUNCATE` operations on `incident_updates`.
7. **Component Status is Derived:** Component statuses are derived strictly from member targets, incidents, and maintenance windows; status can never be directly set via API.
8. **Zero Database Imports in Page Host:** `cmd/pagehost` imports zero database packages and serves pre-rendered snapshots exclusively from MinIO/S3.
9. **Edge-Triggered Transactional Alerts:** State interval transitions and `alert_events` outbox entries commit in the same transaction with a deterministic dedupe key.
10. **Fresh TCP Connection per Check:** `DisableKeepAlives: true` on probe transports ensures accurate TCP, TLS, and TTFB latency timings without connection reuse.

---

## 📁 Project Layout

```
.
├── cmd/
│   ├── api/          # Central management API service
│   ├── probe/        # Autonomous probe worker daemon
│   ├── evaluator/    # Hysteresis, quorum, sweeper, and alert outbox engine
│   ├── publisher/    # Snapshot compiler and S3 atomic publisher
│   └── pagehost/     # Zero-DB public status page host
├── internal/
│   ├── alerting/     # Transactional outbox & Dispatcher integration
│   ├── api/          # REST endpoints & HTTP handlers
│   ├── config/       # Environment configuration loader
│   ├── db/           # PostgreSQL connection pool (pgxpool)
│   ├── evaluator/    # Hysteresis, intervals, and quorum logic
│   ├── heartbeat/    # Push tokens, rotation grace period, dead-man emitter
│   ├── incidents/    # Append-only incident lifecycle management
│   ├── maintenance/  # Maintenance windows & derived component status
│   ├── metrics/      # Prometheus self-SLIs
│   ├── model/        # Core domain types
│   ├── prober/       # HTTP, TCP, DNS, TLS probers & self-test
│   ├── rollup/       # Hourly histograms & interval uptime analytics
│   ├── scheduler/    # SKIP LOCKED claim loop & phase jittering
│   ├── snapshot/     # Snapshot projection, HTML/JSON render & S3 client
│   ├── ssrf/         # Dial-time IP CIDR security classifier
│   └── writer/       # Batched idempotent result writer
├── migrations/       # Day-partitioned SQL migrations (001-013)
├── scripts/          # Migration runner and chaos verification suite
├── docs/             # Architecture, PRD, Story, and Postman collection
├── docker-compose.yml# PostgreSQL 16 Alpine + MinIO service definitions
└── Makefile          # Build, test, and container orchestration
```

---

## 🚀 Getting Started

### Prerequisites

- Go 1.22+
- Docker & Docker Compose
- PostgreSQL client (`psql`)

### 1. Start Infrastructure

```bash
make docker-up
```
This spins up PostgreSQL 16 on port `5432` and MinIO on port `9000` (console on `9001`).

### 2. Run Database Migrations

```bash
make migrate
```
Applies all 13 day-partitioned migrations and triggers.

### 3. Build All Binaries

```bash
make build
```
Compiles all 5 microservices into `bin/`:
- `bin/api`
- `bin/probe`
- `bin/evaluator`
- `bin/publisher`
- `bin/pagehost`

### 4. Run the Test Suite

```bash
make test
```
Executes all unit and integration tests across the repository with race detection enabled (`go test -v -race ./...`).

---

## 📡 API Reference

### Health & Metrics
- `GET /healthz` — Service liveness probe.
- `GET /readyz` — Service readiness probe (validates database pool).
- `GET /metrics` — Prometheus metrics endpoint.

### Target Management
- `POST /v1/targets` — Create a new target check.
  ```json
  {
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "name": "Production API",
    "type": "HTTP",
    "config": {
      "url": "https://api.example.com/healthz"
    },
    "expected_status": 200,
    "interval": "30s",
    "n_fail": 3,
    "n_recover": 2
  }
  ```
- `GET /v1/targets?tenant_id={id}` — List all targets for a tenant.
- `GET /v1/targets/{id}` — Get target state and metadata.
- `POST /v1/targets/{id}/pause` — Pause monitoring.
- `POST /v1/targets/{id}/resume` — Resume monitoring.

### Heartbeat / Dead-Man Monitor
- `POST /ping/{token}` — Send successful heartbeat ping.
- `POST /ping/{token}/fail` — Report explicit job failure.

### Incident Management
- `POST /v1/incidents` — Open an incident.
  ```json
  {
    "tenant_id": "00000000-0000-0000-0000-000000000001",
    "title": "Elevated Database Latency",
    "initial_message": "Investigating query slowdowns.",
    "posted_by": "oncall-sre",
    "impact": "DEGRADED"
  }
  ```
- `POST /v1/incidents/{id}/updates` — Append immutable update.
  ```json
  {
    "status": "IDENTIFIED",
    "message": "Index rebuild in progress.",
    "posted_by": "oncall-sre"
  }
  ```

### Status Page Host (Port 8081)
- `GET /status/{tenant_id}` — Server-rendered HTML status page.
- `GET /status/{tenant_id}.json` — Public JSON snapshot with conditional `ETag` / HTTP 304 support.

---

## 📬 Postman Collection

A full Postman collection with configured requests, parameters, and payloads is provided at:
👉 [`docs/foghorn.postman_collection.json`](docs/foghorn.postman_collection.json)

Import it directly into Postman to test the full lifecycle end-to-end.

---

## ⚡ Chaos Verification & Testing

Verify system invariants under stress:

```bash
./scripts/chaos.sh
```

The chaos script validates:
1. **SSRF Blocking:** Blocks private, cloud metadata (`169.254.169.254`), and loopback addresses.
2. **Append-Only Triggers:** Confirms PostgreSQL trigger rejection of updates to `incident_updates`.
3. **Database Isolation:** Confirms `bin/pagehost` contains zero database symbols via binary inspection.
4. **Staleness Handling:** Demonstrates honest degraded warning banners when snapshots age past thresholds.

---

## 📊 Observability & Self-SLIs

Foghorn exposes Prometheus metrics at `:9090/metrics` or service ports:

- `foghorn_scheduler_lag_seconds`: Histogram tracking `executed_at - scheduled_for`.
- `foghorn_snapshot_age_seconds`: Gauge of elapsed seconds since status page snapshot generation.
- `foghorn_alert_outbox_pending`: Current count of undelivered alerts in outbox.
- `foghorn_probe_unhealthy_locations`: Number of probe locations currently marked `UNHEALTHY`.
- `foghorn_probe_checks_total`: Total check executions partitioned by result class.

---

## 🗺️ v2 Roadmap

- [ ] **Synthetic Browser Journeys:** Multi-step Playwright/Chromium user transaction emulation.
- [ ] **Custom Domain & Automated TLS:** CNAME support with automatic Let's Encrypt certificate issuance.
- [ ] **Enterprise RBAC & OIDC:** Role-based access control and Single Sign-On integration.
- [ ] **Direct SMS & Voice Paging:** Twilio escalation pipelines with phone acknowledgment.
- [ ] **OpenTelemetry Propagation:** W3C Trace Context injection to link probe checks with backend spans.
- [ ] **Latency Anomaly Detection:** p95/p99 machine-learning anomaly detection on hourly histograms.

---

## 📄 License

MIT © [Zubimendi](https://github.com/zubimendi/fog-horn)