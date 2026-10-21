# Product Requirements Document (PRD): Foghorn

## 1. Product Overview

Foghorn is an enterprise-grade uptime monitoring and public status page service. It provides continuous health monitoring of endpoints, interval-based availability analytics, incident communication workflows, and zero-database-dependency status page hosting.

---

## 2. Problem Statement

Most modern uptime monitoring services introduce systematic inaccuracies:
1. **SSRF Exposure:** Naive hostname validation enables attackers to probe cloud metadata (`169.254.169.254`) and internal networks via DNS rebinding.
2. **False Outage Reporting:** Transient network packet loss triggers alert storms; single probe failures are misclassified as target outages.
3. **Dishonest Uptime Metrics:** Calculating uptime by percentage of successful checks distorts true availability; outages confirmed by multi-check hysteresis are reported without backdating to the initial failure.
4. **Status Page Failure Under Stress:** Status pages that query the primary database crash during outages, going dark when users need them most.

---

## 3. Goals & Success Metrics

- **Zero False-Positive Outage Attribution:** Probe self-test failures must never mark a target DOWN.
- **Accurate Error Budgets:** Outage durations must be backdated to the start of the confirming failure streak.
- **Resilient Status Pages:** Status pages must serve cached snapshots with HTTP 304 ETags even during total database downtime.
- **SSRF Immunity:** 100% of internal, link-local, loopback, and CGNAT IP destinations blocked at dial time.

---

## 4. User Personas

- **SRE & DevOps Engineers:** Configure targets, define latency thresholds, monitor error budgets and multi-region quorum.
- **Incident Commanders:** Draft, update, and resolve incidents with append-only auditability.
- **Customers & End Users:** View live system status, active incidents, and scheduled maintenance with sub-second page loads.

---

## 5. Functional Requirements (MVP v1)

### 5.1 Monitoring Protocols
- **HTTP/HTTPS:** Method GET, status code expectations, regex assertions on response body, fresh connection per check, `httptrace` breakdown.
- **TCP:** Port reachability and connection latency.
- **DNS:** Query types A, AAAA, CNAME, MX, TXT with value assertions.
- **TLS:** Certificate expiry monitoring with configurable advance warning thresholds.
- **Heartbeat / Dead-Man:** Push-based pings (`/ping/{token}`) with SHA-256 hashed token storage and 24-hour grace periods on rotation.

### 5.2 State Machine & Analytics
- **Hysteresis:** Configurable `n_fail` and `n_recover` thresholds.
- **Quorum:** Consensus threshold across healthy, fresh probe locations.
- **Interval Backdating:** Backdates outage onset to the timestamp of the first failure in the confirming streak.
- **Absence Detection:** Sweeper automatically transitions silent targets to `UNKNOWN` and raises `MONITORING_GAP` alerts.
- **Interval Uptime:** Computes exact availability and coverage percentage from intervals with maintenance window subtraction.

### 5.3 Communication & Incident Management
- **Append-Only Updates:** Incident updates table strictly blocks `UPDATE`, `DELETE`, and `TRUNCATE` operations via database triggers.
- **Incident Retraction:** Retracting an incident requires a documented reason and creates an immutable audit log entry.
- **Component Status Derivation:** Component states are derived from member targets and active maintenance windows.
- **Maintenance Windows:** Supports scheduled, active, and retroactive maintenance with IANA timezone awareness.

### 5.4 Status Page Publishing & Hosting
- **Snapshot Architecture:** Publisher generates server-rendered HTML and JSON snapshots to MinIO/S3 with atomic pointer swaps.
- **Failure-Domain Isolation:** Page host operates with zero database dependencies.
- **Staleness Transparency:** Prominently displays a staleness warning if snapshot age exceeds the freshness threshold.

---

## 6. Non-Functional Requirements

- **Security:** Dial-time IP inspection in `net.Dialer.Control`. Secrets envelope-encrypted.
- **Performance:** Sub-10ms snapshot delivery from pagehost. Non-blocking `SKIP LOCKED` worker scheduling.
- **Scalability:** Day-partitioned raw check result tables with drop-partition retention.
- **Observability:** Native Prometheus metrics exporter for scheduler lag, snapshot age, outbox depth, and probe health.
