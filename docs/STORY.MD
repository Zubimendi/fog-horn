# The Status Page That Refuses to Lie: Building Foghorn from Scratch

> *A deep dive into distributed uptime monitoring, dial-time SSRF defenses, interval-based uptime math, and why your public status page must never touch your primary database.*

---

![Foghorn Medium Article Thumbnail](assets/medium_article_thumbnail.jpg)

## 📢 Social Media Posts & Tags

### 🏷️ Medium Publication Tags

**Primary Story Topics (Select 5 when publishing on Medium):**
1. `Golang`
2. `Software Engineering`
3. `DevOps`
4. `System Design`
5. `Site Reliability Engineering`

**In-Article Search Tags:**
`#Golang` `#DistributedSystems` `#DevOps` `#SRE` `#SystemDesign` `#CloudComputing` `#PostgreSQL` `#OpenSource` `#SoftwareArchitecture` `#WebDevelopment`

---

### 💼 LinkedIn Post & Hashtags

```markdown
Most uptime monitors lie. Not maliciously, but by architecture.

Here are the 4 ways conventional status pages mislead you:
1. They equate a single network hiccup with a customer outage.
2. They calculate uptime by counting successful checks instead of measuring true time intervals.
3. When confirmation requires 3 failed checks, they claim the outage started at the 3rd check, ignoring that the service went down on check #1.
4. When your database crashes, your status page crashes with it — going dark precisely when your users need it most.

We built Foghorn — a production-grade, distributed uptime monitoring and public status page service in Go designed from day one to reject these failure modes.

Core architectural decisions:
🛡️ Dial-Time SSRF Defense: Validates the raw IP dialed inside net.Dialer.Control after DNS resolution. No DNS rebinding. No IPv4-mapped 0x7f000001 bypasses.
⏱️ Backdated Outage Onset: Hysteresis confirms the outage, but the interval is mathematically backdated to the first failure of the confirming streak.
📐 True Interval-Based Uptime: Uptime and coverage are calculated from contiguous state intervals with maintenance window exclusions — never raw check percentages.
⚡ Zero-DB Status Page Host: The page host has ZERO PostgreSQL dependencies. It serves pre-rendered HTML/JSON snapshots atomically swapped in object storage with conditional ETag 304s and honest staleness warnings.
🔒 Append-Only Incident Ledger: Database triggers block UPDATE, DELETE, and TRUNCATE on incident updates.

Check out the full open-source architecture, implementation plan, and Postman collection on GitHub:
👉 https://github.com/zubimendi/fog-horn

#Golang #SystemDesign #DistributedSystems #DevOps #SRE #SoftwareEngineering #CloudArchitecture #BackendEngineering #PostgreSQL #Observability #OpenSource #Reliability
```

**LinkedIn Hashtag List:**
`#Golang` `#SystemDesign` `#DistributedSystems` `#DevOps` `#SRE` `#SoftwareEngineering` `#CloudArchitecture` `#BackendEngineering` `#PostgreSQL` `#Observability` `#OpenSource` `#Reliability`

---

## 📖 The Story: Why We Built Foghorn

### 1. The Three Layers of "Status"

In conventional systems, "status" is overloaded into a single field:
1. **Observations:** *"What did probe location EU-West see at 12:04:02?"* (Raw probe data, subject to local packet loss or route flapping).
2. **Judgment:** *"Is the target system down?"* (Machine-derived consensus using hysteresis thresholds and multi-region quorum).
3. **Communication:** *"What are we telling customers?"* (Human-curated incident narrative with impact analysis).

When you collapse these three into one, you either get alert storms that wake up on-call engineers for transient router blips, or a robotically generated status page that posts "System Down" during rolling zero-downtime deploys.

In Foghorn, each layer is stored separately, has its own state machine, and flows unidirectionally:
`Observations → Judgment → Communication`.

### 2. Guarding the Prober: SSRF at Connect Time

Any monitoring system that allows users to configure a custom URL is an SSRF weapon. Naive implementations validate `req.URL.Hostname()` before dialing. Attackers defeat this with:
- DNS rebinding (resolving to a public IP on check #1, then `169.254.169.254` or `127.0.0.1` on check #2).
- Decimal/hexadecimal IP encodings (`2130706433`).
- IPv4-mapped IPv6 addresses (`::ffff:127.0.0.1`).

Foghorn intercepts connections at the kernel socket level using `net.Dialer.Control`:
```go
Control: func(network, addr string, c syscall.RawConn) error {
    if ssrf.ClassifyAddr(network, addr) == ssrf.Blocked {
        return &ErrSSRFBlocked{Addr: addr}
    }
    return nil
}
```
Because this hook executes *after* DNS resolution on the actual IP about to be dialed, DNS rebinding is physically impossible.

### 3. Backdated Onset: Uptime That Doesn't Lie

If your target check interval is 60 seconds and your threshold requires 3 consecutive failures to alert:
- `10:00:00` — Check 1 fails (transient or real?)
- `10:01:00` — Check 2 fails
- `10:02:00` — Check 3 fails (OUTAGE CONFIRMED)

When did the outage start?
Most tools log `started_at = 10:02:00`. That is a lie: your customers have been down since `10:00:00`.
Foghorn maintains a stateful streak tracker. When the 3rd failure confirms the outage, the state interval is backdated to the timestamp of the first failure in that confirming streak:
```go
onsetAt := intervals.BackdateOnset(streakStartedAt, confirmationTime)
```
Every second of customer pain is honestly captured in the SLO error budget.

### 4. Zero-Database Page Host Isolation

When your primary Postgres database goes down, your monitoring system cannot report status if its status page requires a database query to render!

Foghorn enforces an absolute physical boundary:
- `cmd/publisher` queries PostgreSQL, compiles state intervals, active incidents, and public components into an immutable snapshot, and writes versioned JSON and HTML into object storage (MinIO/S3), followed by an atomic pointer swap.
- `cmd/pagehost` serves the status page. **It has ZERO database driver imports.**
Even if the database cluster catches fire and every API server dies, `cmd/pagehost` serves status with conditional ETags and an honest staleness banner if the snapshot exceeds the freshness threshold.

### 5. Summary

Building reliable monitoring is a study in edge cases: clock drift, hysteresis, quorum, backdating, and failure isolation. Foghorn proves that honesty in monitoring can be achieved by design.
