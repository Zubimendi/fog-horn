// Package intervals manages state interval open/close with backdated onset.
// Pure: no I/O, no database access.
package intervals

import (
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/model"
)

// OpenInterval constructs a new open StateInterval.
// onsetAt is the backdated start — the first failure of the confirming streak,
// NOT the time the DOWN state was confirmed.
func OpenInterval(targetID uuid.UUID, state model.TargetStateValue, onsetAt time.Time) model.StateInterval {
	return model.StateInterval{
		ID:       uuid.New(),
		TargetID: targetID,
		State:    state,
		StartAt:  onsetAt,
		EndAt:    nil,
	}
}

// CloseInterval returns a copy of the interval with EndAt set.
func CloseInterval(si model.StateInterval, endAt time.Time) model.StateInterval {
	si.EndAt = &endAt
	return si
}

// BackdateOnset computes the true onset of a confirmed DOWN/PARTIAL state.
// streakStartedAt is the time of the first failure of the confirming streak,
// stored in location_state.streak_started_at.
// confirmationTime is when the Nth failure arrived.
// By invariant: onset <= confirmationTime always.
func BackdateOnset(streakStartedAt, confirmationTime time.Time) time.Time {
	if streakStartedAt.IsZero() || streakStartedAt.After(confirmationTime) {
		return confirmationTime
	}
	return streakStartedAt
}
