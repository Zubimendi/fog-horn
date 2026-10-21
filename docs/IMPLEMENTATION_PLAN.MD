# Foghorn — Implementation Plan & Roadmap

## Overview & Architecture Retrospective

Foghorn is a multi-tenant, distributed uptime monitoring and public status page service built in Go. It delivers reliable health checks, honest interval-based availability calculations, edge-triggered alerting via transactional outbox, and failure-domain-isolated status page hosting.

### The 10 Enforced Architectural Invariants

| # | Invariant | Implementation Mechanism |
|---|---|---|
| **1** | SSRF guard at dial time | `net.Dialer.Control` validates dialed IP after DNS resolution; eliminates rebinding and decimal bypasses |
| **2** | Probe errors never target downtime | Self-test canaries mark worker `UNHEALTHY` upon control failure; errors classified as `PROBE_ERROR` |
| **3** | Backdated outage onset | Interval start time backdated to first failure of the confirming streak via `intervals.BackdateOnset` |
| **4** | Absence = `UNKNOWN`, never `UP` | Clock-driven sweeper transitions silent/stale monitors to `UNKNOWN` and raises `MONITORING_GAP` |
| **5** | Uptime from state intervals only | Integration of half-open `[start, end)` intervals with maintenance window exclusions; never check counts |
| **6** | Append-only incident updates | Database-level triggers on `incident_updates` raise exceptions on `UPDATE`, `DELETE`, and `TRUNCATE` |
| **7** | Component status derived | Calculated via severity hierarchy (`major_outage > partial_outage > degraded > maintenance > operational`) |
| **8** | Zero database imports in `cmd/pagehost` | Page host imports only S3/MinIO storage client; zero SQL/pgx dependencies |
| **9** | Edge-triggered transactional alerting | State transitions and alert outbox committed in identical SQL transaction with deterministic dedupe key |
| **10** | Fresh TCP connection per check | `DisableKeepAlives: true` on transport; ensures true end-to-end socket handshake timings |

---

## MVP v1 Delivery Status: Complete ✅

### Phase Breakdown

- [x] **Phase 0: Infrastructure & Skeleton**
  - Project directory layout (`cmd/api`, `cmd/probe`, `cmd/evaluator`, `cmd/publisher`, `cmd/pagehost`, `internal/...`)
  - `docker-compose.yml` for PostgreSQL 16 Alpine and MinIO object storage
  - Connection pooling with `pgxpool` and centralized configuration loading
  - `Makefile` with `build`, `test`, `migrate`, and container automation

- [x] **Phase 1: SQL Migrations**
  - Hand-crafted idempotent migrations `001_` through `013_`
  - Range-partitioned `check_results` table with daily partitions
  - Append-only `incident_updates` triggers and public permission revocations
  - Hashed token storage and transactional alert outbox schemas

- [x] **Phase 2: Pure Calculation Modules**
  - `internal/ssrf`: CIDR classifier with IPv4-mapped normalization
  - `internal/evaluator/hysteresis`: pure per-location state machine
  - `internal/evaluator/quorum`: multi-location quorum consensus
  - `internal/evaluator/intervals`: backdated onset interval builder
  - `internal/rollup/uptime`: exact interval coverage & maintenance exclusion
  - `internal/rollup/histogram`: mergeable fixed-bucket latency histograms
  - `internal/rollup/nines`: error budget and SLA burn-rate calculator

- [x] **Phase 3: Guarded Probers & Heartbeat**
  - `internal/prober/dialer`: connect-time IP interception
  - `internal/prober/http`: HTTP prober with `httptrace` breakdown (DNS, TCP, TLS, TTFB)
  - `internal/prober/tcp`: TCP socket handshake prober
  - `internal/prober/dns`: DNS resolver with record assertion
  - `internal/prober/tls`: TLS certificate expiry prober with warning threshold
  - `internal/prober/selftest`: control canaries and probe health state
  - `internal/heartbeat`: SHA-256 hashed push token verification with 24h rotation grace period

- [x] **Phase 4: Scheduler Claim Loop**
  - `FOR UPDATE SKIP LOCKED` non-blocking worker claims
  - Skip-ahead phase-preserving tick advance (prevents thundering herd)
  - Hash-derived phase jittering and location staggering
  - Lag metric SLI tracking (`executed_at - scheduled_for`)

- [x] **Phase 5: Batched Result Writer**
  - Micro-batched inserts with `ON CONFLICT (target_id, location_id, scheduled_for) DO NOTHING`

- [x] **Phase 6: Evaluator & Sweeper**
  - In-order evaluation with target row locks
  - Interval opening/closing with backdated outage onset
  - Clock-driven sweeper marking stale targets `UNKNOWN`

- [x] **Phase 7: Rollups**
  - Idempotent hourly histogram aggregation
  - Interval-based uptime calculation

- [x] **Phase 8: Incidents**
  - Strict lifecycle state transitions (`INVESTIGATING -> IDENTIFIED -> MONITORING -> RESOLVED`)
  - Retraction workflow with reason requirement and audit logging

- [x] **Phase 9: Components & Maintenance**
  - Status derivation engine
  - Maintenance windows with IANA timezone support
  - Retroactive maintenance auditing

- [x] **Phase 10: Alerting**
  - Transactional outbox pattern
  - Idempotent Dispatcher publish with deduplication keys

- [x] **Phase 11: Snapshot Publisher & Page Host**
  - Server-rendered HTML and JSON snapshot publisher
  - Atomic object pointer swap
  - Zero-database page host with ETag 304 and honest staleness indicator

- [x] **Phase 12 & 13: Observability & Self-SLIs**
  - Prometheus metrics (`scheduler_lag`, `snapshot_age`, `outbox_pending`, `unhealthy_locations`)
  - Dead-man monitor heartbeat emitter
  - Comprehensive chaos verification test script

---

## Roadmap: v2 Feature Specifications

The following features are designed for the v2 milestone:

### 1. Synthetic Browser Checks (Playwright / Chromium)
- **Goal:** Emulate user transactions (e.g. login, shopping cart checkout) in addition to protocol pings.
- **Architecture:** Headless browser runner container pool executing scripted user journeys with DOM assertions, screenshot capture on failure, and step-level waterfall timings.

### 2. Custom Domain CNAME & Automated TLS Issuance
- **Goal:** Enable customers to host their status page on `status.customer.com`.
- **Architecture:** ACME/Let's Encrypt integration inside `cmd/pagehost` via `golang.org/x/crypto/acme/autocert` with SNI certificate provisioning and HTTP-01 challenge routing.

### 3. Granular RBAC & Enterprise SAML / OIDC SSO
- **Goal:** Support large enterprise security requirements with role separation.
- **Architecture:** Multi-role authorization (`Viewer`, `Operator`, `IncidentCommander`, `TenantAdmin`) and OpenID Connect / SAML 2.0 integration.

### 4. Direct Paging & On-Call Schedule Escalations
- **Goal:** Native escalation policies without requiring third-party dispatchers.
- **Architecture:** In-tree schedule rotation engine, voice call/SMS delivery via Twilio, and multi-tier escalation timelines with SMS acknowledgment.

### 5. OpenTelemetry Distributed Trace Correlation
- **Goal:** Correlate probe requests with backend application traces.
- **Architecture:** Probers inject W3C Trace Context headers (`traceparent`, `tracestate`) into HTTP checks; target applications link the probe request to their internal traces in Jaeger/Tempo.

### 6. Latency Anomaly Detection (p99 ML Baseline)
- **Goal:** Detect performance regressions before full outages occur.
- **Architecture:** Rolling Gaussian or Holt-Winters forecasting on hourly latency histograms to alert on sudden shifts in tail latencies (p95/p99) despite 200 OK responses.
