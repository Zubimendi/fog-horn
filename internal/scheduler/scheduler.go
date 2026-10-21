// Package scheduler manages target check scheduling, claim loops using
// SKIP LOCKED, phase jittering, and scheduler lag tracking.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
)

// ClaimedTarget represents a single target check tick claimed by a probe worker.
type ClaimedTarget struct {
	TargetID         uuid.UUID
	LocationID       uuid.UUID
	ScheduledFor     time.Time
	Type             model.TargetType
	Config           map[string]any
	ExpectedStatus   int
	Assertion        map[string]any
	LatencyThreshold time.Duration
}

// ComputePhaseOffset calculates a deterministic base offset from the target ID
// plus staggered location phase, distributing checks across the interval.
func ComputePhaseOffset(targetID uuid.UUID, locIndex int, totalLocs int, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	// Hash target ID to get base jitter
	h := sha256.Sum256(targetID[:])
	n := binary.BigEndian.Uint64(h[:8])
	baseNanos := n % uint64(interval.Nanoseconds())

	// Stagger locations across the interval
	var staggerNanos int64
	if totalLocs > 1 && locIndex >= 0 {
		staggerNanos = (interval.Nanoseconds() / int64(totalLocs)) * int64(locIndex%totalLocs)
	}

	totalOffset := (int64(baseNanos) + staggerNanos) % interval.Nanoseconds()
	return time.Duration(totalOffset)
}

// ComputeLag calculates the scheduler lag SLI: (executed_at - scheduled_for).
func ComputeLag(scheduledFor, executedAt time.Time) time.Duration {
	return executedAt.Sub(scheduledFor)
}

// Claim executes the SKIP LOCKED claim query to atomically claim due checks
// and skip-ahead advance their next_run_at strictly past now() without drift.
func Claim(ctx context.Context, pool *pgxpool.Pool, locationID uuid.UUID, limit int) ([]ClaimedTarget, error) {
	if limit <= 0 {
		limit = 50
	}

	query := `
WITH due AS (
    SELECT target_id, location_id FROM target_schedule
    WHERE location_id = $1 AND next_run_at <= now() AND NOT paused
    ORDER BY next_run_at LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE target_schedule s SET
    scheduled_for = s.next_run_at,
    next_run_at   = s.next_run_at + s.interval *
        (floor(extract(epoch FROM now() - s.next_run_at)
               / extract(epoch FROM s.interval)) + 1)
FROM due
JOIN targets t ON t.id = due.target_id
WHERE s.target_id = due.target_id AND s.location_id = due.location_id AND NOT t.paused
RETURNING s.target_id, s.location_id, s.scheduled_for, t.type, t.config,
          COALESCE(t.expected_status, 0), COALESCE(t.assertion, '{}'::jsonb),
          COALESCE(extract(epoch from t.latency_threshold), 0);
`

	rows, err := pool.Query(ctx, query, locationID, limit)
	if err != nil {
		return nil, fmt.Errorf("scheduler: claim: %w", err)
	}
	defer rows.Close()

	var claimed []ClaimedTarget
	for rows.Next() {
		var (
			ct            ClaimedTarget
			targetTypeStr string
			latencySec    float64
		)
		err := rows.Scan(
			&ct.TargetID,
			&ct.LocationID,
			&ct.ScheduledFor,
			&targetTypeStr,
			&ct.Config,
			&ct.ExpectedStatus,
			&ct.Assertion,
			&latencySec,
		)
		if err != nil {
			return nil, fmt.Errorf("scheduler: scan: %w", err)
		}
		ct.Type = model.TargetType(targetTypeStr)
		if latencySec > 0 {
			ct.LatencyThreshold = time.Duration(latencySec * float64(time.Second))
		}
		claimed = append(claimed, ct)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scheduler: rows err: %w", err)
	}

	return claimed, nil
}
