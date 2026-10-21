// Package sweeper performs clock-driven checks to mark stale targets
// and missing heartbeat pings as UNKNOWN, enforcing the invariant: Absence = UNKNOWN.
package sweeper

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SweepStaleTargets scans for targets with no fresh results within 3x their interval
// and marks them UNKNOWN, closing the open interval and creating a MONITORING_GAP alert.
func SweepStaleTargets(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	// Find targets that are not UNKNOWN, not PAUSED, but have no fresh results in 3*interval
	query := `
SELECT t.id, t.tenant_id, ts.state, ts.last_fresh_result_at, extract(epoch from t.interval)
FROM targets t
JOIN target_states ts ON ts.target_id = t.id
WHERE NOT t.paused
  AND ts.state != 'UNKNOWN'
  AND ts.state != 'PAUSED'
  AND (ts.last_fresh_result_at IS NULL OR now() - ts.last_fresh_result_at > (t.interval * 3));
`

	rows, err := pool.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("sweeper: query stale: %w", err)
	}
	defer rows.Close()

	type staleItem struct {
		targetID    uuid.UUID
		tenantID    uuid.UUID
		prevState   string
		lastFreshAt *time.Time
		intervalSec float64
	}

	var stale []staleItem
	for rows.Next() {
		var item staleItem
		if err := rows.Scan(&item.targetID, &item.tenantID, &item.prevState, &item.lastFreshAt, &item.intervalSec); err != nil {
			return 0, fmt.Errorf("sweeper: scan stale: %w", err)
		}
		stale = append(stale, item)
	}
	rows.Close()

	count := 0
	for _, item := range stale {
		gapStart := time.Now()
		if item.lastFreshAt != nil {
			gapStart = item.lastFreshAt.Add(time.Duration(item.intervalSec*3) * time.Second)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			continue
		}

		// Close current interval
		_, _ = tx.Exec(ctx, `
UPDATE state_intervals
SET end_at = $1
WHERE target_id = $2 AND end_at IS NULL
`, gapStart, item.targetID)

		// Open UNKNOWN interval
		_, _ = tx.Exec(ctx, `
INSERT INTO state_intervals (target_id, state, start_at)
VALUES ($1, 'UNKNOWN', $2)
`, item.targetID, gapStart)

		// Update target_states
		_, _ = tx.Exec(ctx, `
UPDATE target_states
SET state = 'UNKNOWN', since = $1, updated_at = now()
WHERE target_id = $2
`, gapStart, item.targetID)

		// Emit MONITORING_GAP alert
		dedupeKey := fmt.Sprintf("%s:MONITORING_GAP:%s", item.targetID, gapStart.UTC().Format(time.RFC3339))
		payloadBytes, _ := json.Marshal(map[string]any{
			"target_id":      item.targetID.String(),
			"previous_state": item.prevState,
			"gap_start":      gapStart.UTC().Format(time.RFC3339),
			"reason":         "monitoring gap: no fresh results received within threshold",
		})

		_, _ = tx.Exec(ctx, `
INSERT INTO alert_events (tenant_id, kind, dedupe_key, payload, state)
VALUES ($1, 'MONITORING_GAP', $2, $3, 'PENDING')
ON CONFLICT (dedupe_key) DO NOTHING
`, item.tenantID, dedupeKey, payloadBytes)

		if err := tx.Commit(ctx); err == nil {
			count++
		}
	}

	return count, nil
}
