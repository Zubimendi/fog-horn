// Package rollup coordinates periodic and on-demand aggregation of
// latency histograms and interval-based uptime.
package rollup

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/rollup/histogram"
	"github.com/zubimendi/fog-horn/internal/rollup/uptime"
)

// RecomputeHourlyLatency aggregates raw check results for a specific hour bucket
// into a latency histogram and upserts the result. Idempotent across re-runs.
func RecomputeHourlyLatency(
	ctx context.Context,
	pool *pgxpool.Pool,
	targetID, locationID uuid.UUID,
	hour time.Time,
) error {
	hourStart := hour.Truncate(time.Hour)
	hourEnd := hourStart.Add(time.Hour)

	query := `
SELECT total_ms
FROM check_results
WHERE target_id = $1
  AND location_id = $2
  AND scheduled_for >= $3
  AND scheduled_for < $4
  AND class IN ('OK', 'OK_SLOW')
`
	rows, err := pool.Query(ctx, query, targetID, locationID, hourStart, hourEnd)
	if err != nil {
		return fmt.Errorf("rollup: query latency checks: %w", err)
	}
	defer rows.Close()

	var h histogram.Histogram
	for rows.Next() {
		var ms *float64
		if err := rows.Scan(&ms); err != nil {
			return fmt.Errorf("rollup: scan latency ms: %w", err)
		}
		if ms != nil {
			h.Observe(*ms)
		}
	}
	rows.Close()

	// Convert fixed-size array to slice for postgres BIGINT[]
	countsSlice := make([]int64, len(h.Counts))
	for i, c := range h.Counts {
		countsSlice[i] = c
	}

	upsert := `
INSERT INTO latency_rollups_hourly (
    target_id, location_id, hour, bucket_counts, count, sum_ms, recomputed_at
) VALUES ($1, $2, $3, $4, $5, $6, now())
ON CONFLICT (target_id, location_id, hour) DO UPDATE SET
    bucket_counts = EXCLUDED.bucket_counts,
    count         = EXCLUDED.count,
    sum_ms        = EXCLUDED.sum_ms,
    recomputed_at = now();
`
	_, err = pool.Exec(ctx, upsert, targetID, locationID, hourStart, countsSlice, h.Count, h.SumMS)
	if err != nil {
		return fmt.Errorf("rollup: upsert hourly latency: %w", err)
	}

	return nil
}

// ComputeTargetUptime calculates uptime and coverage for a target strictly
// from state intervals, excluding maintenance windows.
func ComputeTargetUptime(
	ctx context.Context,
	pool *pgxpool.Pool,
	targetID uuid.UUID,
	window uptime.TimeRange,
) (uptime.Result, error) {
	// 1. Fetch state intervals overlapping the window
	intQuery := `
SELECT id, target_id, state, start_at, end_at
FROM state_intervals
WHERE target_id = $1
  AND start_at < $2
  AND (end_at IS NULL OR end_at > $3)
ORDER BY start_at ASC;
`
	rows, err := pool.Query(ctx, intQuery, targetID, window.End, window.Start)
	if err != nil {
		return uptime.Result{}, fmt.Errorf("rollup: query intervals: %w", err)
	}
	defer rows.Close()

	var intervals []model.StateInterval
	for rows.Next() {
		var si model.StateInterval
		var stStr string
		if err := rows.Scan(&si.ID, &si.TargetID, &stStr, &si.StartAt, &si.EndAt); err != nil {
			return uptime.Result{}, fmt.Errorf("rollup: scan interval: %w", err)
		}
		si.State = model.TargetStateValue(stStr)
		intervals = append(intervals, si)
	}
	rows.Close()

	// 2. Fetch maintenance windows for tenant overlapping the window
	maintQuery := `
SELECT m.starts_at, m.ends_at, m.retroactive
FROM maintenance_windows m
JOIN targets t ON t.tenant_id = m.tenant_id
WHERE t.id = $1
  AND m.status IN ('COMPLETED', 'IN_PROGRESS', 'SCHEDULED')
  AND m.starts_at < $2
  AND m.ends_at > $3;
`
	mRows, err := pool.Query(ctx, maintQuery, targetID, window.End, window.Start)
	if err != nil {
		return uptime.Result{}, fmt.Errorf("rollup: query maintenance: %w", err)
	}
	defer mRows.Close()

	var exclusions []uptime.ExclusionWindow
	for mRows.Next() {
		var (
			start, end time.Time
			retro      bool
		)
		if err := mRows.Scan(&start, &end, &retro); err != nil {
			return uptime.Result{}, fmt.Errorf("rollup: scan maintenance: %w", err)
		}
		exclusions = append(exclusions, uptime.ExclusionWindow{
			Range:       uptime.TimeRange{Start: start, End: end},
			Retroactive: retro,
		})
	}
	mRows.Close()

	return uptime.Calculate(intervals, window, exclusions), nil
}
