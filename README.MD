# Foghorn

An uptime monitoring and public status page service in Go. Project 25 of a
15-week backend roadmap. It checks that services are up and tells people
honestly when they aren't, which is harder than it sounds because the
obvious implementation lies in four specific ways.

Ideas that carry the design (full detail in `docs/ARCHITECTURE.md`):

1. **Three separate layers:** observations (what probes saw), judgment
   (is it down?), and communication (what we tell customers). One failed
   check is not an outage, and a human statement is not a monitor state.
2. **A monitor is an SSRF machine unless proven otherwise.** The guard
   validates the IP actually dialed (not the hostname), defeating DNS
   rebinding and alternate IP encodings.
3. **Uptime math that doesn't lie.** Computed from state intervals (not
   check counts), with outage onset backdated to the first failure,
   coverage published beside the percentage, and missing data treated as
   `UNKNOWN`, never `UP`. Probe failures are not target downtime.
4. **A status page that survives the outage.** A pre-rendered snapshot
   served by a binary with no database dependency, and a visible
   staleness indicator instead of a confident wrong answer.

Alerts are edge-triggered through the transactional-outbox pattern and
delivered via Dispatcher with deterministic idempotency keys. Check
scheduling deliberately does *not* use QueueLine (a failed check is a
measurement, not a failed job); the doc explains why.

The demo: point it at the rest of the portfolio and run a chaos script
end to end.

Stack: Go, PostgreSQL (day-partitioned results), Docker. Documentation-
first; see `docs/CURSOR_CONTEXT.md` for the build plan.

MIT licensed.