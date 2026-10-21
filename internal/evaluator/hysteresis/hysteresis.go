// Package hysteresis implements the per-(target,location) state machine.
// It is pure: no I/O, no database access, fully table-driven testable.
package hysteresis

import (
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
)

// Config holds the thresholds for state transitions.
type Config struct {
	NFail    int // consecutive TARGET_FAILUREs required to go DOWN
	NRecover int // consecutive OKs required to return to UP
}

// State is the immutable snapshot of a location's hysteresis counters.
type State struct {
	Value               model.LocationStateValue
	ConsecutiveFailures int
	ConsecutiveSuccesses int
	StreakStartedAt     time.Time
}

// Evaluate applies a single check result to the current state and returns
// the new state. It does not write to the database.
func Evaluate(s State, class model.CheckClass, cfg Config, resultTime time.Time) State {
	next := s

	switch class {
	case model.ClassTargetFailure:
		next.ConsecutiveSuccesses = 0
		if s.ConsecutiveFailures == 0 {
			next.StreakStartedAt = resultTime
		}
		next.ConsecutiveFailures++
		if next.ConsecutiveFailures >= cfg.NFail {
			next.Value = model.LocationDown
		}

	case model.ClassOK, model.ClassOKSlow:
		next.ConsecutiveFailures = 0
		if s.Value == model.LocationDown || s.ConsecutiveSuccesses == 0 {
			if s.ConsecutiveSuccesses == 0 {
				next.StreakStartedAt = resultTime
			}
		}
		next.ConsecutiveSuccesses++
		if s.Value == model.LocationDown && next.ConsecutiveSuccesses >= cfg.NRecover {
			next.Value = model.LocationUp
			next.ConsecutiveSuccesses = 0
		} else if s.Value != model.LocationDown {
			next.Value = model.LocationUp
		}

	case model.ClassProbeError, model.ClassConfigError:
		// Probe/config errors do not change hysteresis counters or state.
		// They are not target downtime.

	default:
		// Unknown class: treat as probe error (safe default).
	}

	return next
}
