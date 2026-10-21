// Package evaluator performs target and location state evaluation,
// hysteresis, quorum, backdated interval management, and alert outbox emission.
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/evaluator/hysteresis"
	"github.com/zubimendi/fog-horn/internal/evaluator/intervals"
	"github.com/zubimendi/fog-horn/internal/evaluator/quorum"
	"github.com/zubimendi/fog-horn/internal/model"
)

// ProcessResult processes a single CheckResult within a serializable/row-locked
// transaction to guarantee in-order per-(target,location) state transitions.
func ProcessResult(ctx context.Context, pool *pgxpool.Pool, r model.CheckResult) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("evaluator: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Fetch target details
	var (
		tenantID       uuid.UUID
		nFail          int
		nRecover       int
		quorumFraction float64
		intervalSec    float64
		paused         bool
	)
	err = tx.QueryRow(ctx, `
SELECT tenant_id, n_fail, n_recover, quorum, extract(epoch from interval), paused
FROM targets
WHERE id = $1
FOR SHARE
`, r.TargetID).Scan(&tenantID, &nFail, &nRecover, &quorumFraction, &intervalSec, &paused)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil // Target deleted
		}
		return fmt.Errorf("evaluator: fetch target: %w", err)
	}

	if paused {
		return tx.Commit(ctx)
	}

	// 2. Fetch and lock location_state
	var (
		consecFailures  int
		consecSuccesses int
		locStateValStr  string
		streakStartedAt time.Time
		lastEvaluated   *time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT consecutive_failures, consecutive_successes, state, streak_started_at, last_evaluated_scheduled_for
FROM location_state
WHERE target_id = $1 AND location_id = $2
FOR UPDATE
`, r.TargetID, r.LocationID).Scan(&consecFailures, &consecSuccesses, &locStateValStr, &streakStartedAt, &lastEvaluated)

	if err == pgx.ErrNoRows {
		// Initialize location_state
		streakStartedAt = r.ExecutedAt
		locStateValStr = string(model.LocationUnknown)
		_, err = tx.Exec(ctx, `
INSERT INTO location_state (target_id, location_id, consecutive_failures, consecutive_successes, state, streak_started_at)
VALUES ($1, $2, 0, 0, 'UNKNOWN', $3)
ON CONFLICT (target_id, location_id) DO NOTHING
`, r.TargetID, r.LocationID, streakStartedAt)
		if err != nil {
			return fmt.Errorf("evaluator: init location_state: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("evaluator: fetch location_state: %w", err)
	}

	// In-order check: ignore older out-of-order check results for state progression
	if lastEvaluated != nil && !r.ScheduledFor.After(*lastEvaluated) {
		return tx.Commit(ctx)
	}

	// 3. Apply hysteresis
	curLocState := hysteresis.State{
		Value:                model.LocationStateValue(locStateValStr),
		ConsecutiveFailures:  consecFailures,
		ConsecutiveSuccesses: consecSuccesses,
		StreakStartedAt:      streakStartedAt,
	}
	nextLocState := hysteresis.Evaluate(curLocState, r.Class, hysteresis.Config{
		NFail:    nFail,
		NRecover: nRecover,
	}, r.ExecutedAt)

	// Update location_state
	_, err = tx.Exec(ctx, `
UPDATE location_state
SET consecutive_failures = $1,
    consecutive_successes = $2,
    state = $3,
    streak_started_at = $4,
    last_evaluated_scheduled_for = $5,
    updated_at = now()
WHERE target_id = $6 AND location_id = $7
`, nextLocState.ConsecutiveFailures, nextLocState.ConsecutiveSuccesses,
		string(nextLocState.Value), nextLocState.StreakStartedAt, r.ScheduledFor,
		r.TargetID, r.LocationID)
	if err != nil {
		return fmt.Errorf("evaluator: update location_state: %w", err)
	}

	// 4. Lock target_states
	var (
		curTargetStateStr string
		curSince          time.Time
	)
	err = tx.QueryRow(ctx, `
SELECT state, since
FROM target_states
WHERE target_id = $1
FOR UPDATE
`, r.TargetID).Scan(&curTargetStateStr, &curSince)

	if err == pgx.ErrNoRows {
		curTargetStateStr = string(model.StateUnknown)
		curSince = r.ExecutedAt
		_, err = tx.Exec(ctx, `
INSERT INTO target_states (target_id, state, since, last_fresh_result_at)
VALUES ($1, 'UNKNOWN', $2, now())
ON CONFLICT (target_id) DO NOTHING
`, r.TargetID, curSince)
		if err != nil {
			return fmt.Errorf("evaluator: init target_states: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("evaluator: fetch target_states: %w", err)
	}

	curTargetState := model.TargetStateValue(curTargetStateStr)

	// 5. Gather all location reports for quorum
	locRows, err := tx.Query(ctx, `
SELECT l.location_id::text, l.state, p.healthy,
       COALESCE(extract(epoch from (now() - l.updated_at)) <= ($1 * 3), false) as is_fresh
FROM location_state l
JOIN probe_locations p ON p.id = l.location_id
WHERE l.target_id = $2
`, intervalSec, r.TargetID)
	if err != nil {
		return fmt.Errorf("evaluator: query quorum locations: %w", err)
	}
	defer locRows.Close()

	var reports []quorum.LocationReport
	for locRows.Next() {
		var (
			locIDStr string
			stStr    string
			healthy  bool
			fresh    bool
		)
		if err := locRows.Scan(&locIDStr, &stStr, &healthy, &fresh); err != nil {
			return fmt.Errorf("evaluator: scan quorum loc: %w", err)
		}
		reports = append(reports, quorum.LocationReport{
			LocationID: locIDStr,
			State:      model.LocationStateValue(stStr),
			Healthy:    healthy,
			HasFresh:   fresh,
		})
	}
	locRows.Close()

	// 6. Evaluate Quorum
	newTargetState := quorum.Evaluate(reports, quorum.Config{
		QuorumFraction: quorumFraction,
		MinReporting:   1,
	})

	// 7. Handle Target State Transition
	if newTargetState != curTargetState && newTargetState != model.StateUnknown {
		onsetAt := r.ExecutedAt
		if newTargetState == model.StateDown || newTargetState == model.StatePartial {
			// Invariant: backdated onset to the first failure of confirming streak
			onsetAt = intervals.BackdateOnset(nextLocState.StreakStartedAt, r.ExecutedAt)
		}

		// Close current open interval
		_, err = tx.Exec(ctx, `
UPDATE state_intervals
SET end_at = $1
WHERE target_id = $2 AND end_at IS NULL
`, onsetAt, r.TargetID)
		if err != nil {
			return fmt.Errorf("evaluator: close interval: %w", err)
		}

		// Open new interval
		_, err = tx.Exec(ctx, `
INSERT INTO state_intervals (target_id, state, start_at)
VALUES ($1, $2, $3)
`, r.TargetID, string(newTargetState), onsetAt)
		if err != nil {
			return fmt.Errorf("evaluator: open interval: %w", err)
		}

		// Update target_states
		_, err = tx.Exec(ctx, `
UPDATE target_states
SET state = $1, since = $2, last_fresh_result_at = now(), updated_at = now()
WHERE target_id = $3
`, string(newTargetState), onsetAt, r.TargetID)
		if err != nil {
			return fmt.Errorf("evaluator: update target_states: %w", err)
		}

		// 8. Transactional Outbox for Alert Events
		if newTargetState == model.StateDown || newTargetState == model.StatePartial {
			kind := model.AlertTargetDown
			if newTargetState == model.StatePartial {
				kind = model.AlertTargetPartial
			}

			dedupeKey := fmt.Sprintf("%s:%s:%s", r.TargetID, newTargetState, onsetAt.UTC().Format(time.RFC3339))
			payloadBytes, _ := json.Marshal(map[string]any{
				"target_id": r.TargetID.String(),
				"state":     string(newTargetState),
				"onset_at":  onsetAt.UTC().Format(time.RFC3339),
				"reason":    r.Reason,
			})

			_, err = tx.Exec(ctx, `
INSERT INTO alert_events (tenant_id, kind, dedupe_key, payload, state)
VALUES ($1, $2, $3, $4, 'PENDING')
ON CONFLICT (dedupe_key) DO NOTHING
`, tenantID, string(kind), dedupeKey, payloadBytes)
			if err != nil {
				return fmt.Errorf("evaluator: insert alert event: %w", err)
			}
		}
	} else {
		// Just update freshness timestamp
		_, err = tx.Exec(ctx, `
UPDATE target_states
SET last_fresh_result_at = now(), updated_at = now()
WHERE target_id = $1
`, r.TargetID)
		if err != nil {
			return fmt.Errorf("evaluator: refresh target_states: %w", err)
		}
	}

	return tx.Commit(ctx)
}
