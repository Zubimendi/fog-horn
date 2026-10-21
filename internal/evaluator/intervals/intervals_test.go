package intervals_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/evaluator/intervals"
	"github.com/zubimendi/fog-horn/internal/model"
)

var (
	targetID = uuid.New()
	t0       = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
)

// TestBackdatedOnset is the key test from CURSOR_CONTEXT §Step 3:
// Failures at t=0,60,120 with N_fail=3; DOWN interval must start at t=0.
func TestBackdatedOnset(t *testing.T) {
	firstFailureAt := t0                         // t=0
	confirmationAt := t0.Add(120 * time.Second) // t=120

	onset := intervals.BackdateOnset(firstFailureAt, confirmationAt)
	if !onset.Equal(firstFailureAt) {
		t.Errorf("onset = %v, want %v (first failure time)", onset, firstFailureAt)
	}

	si := intervals.OpenInterval(targetID, model.StateDown, onset)
	if !si.StartAt.Equal(firstFailureAt) {
		t.Errorf("interval.StartAt = %v, want %v", si.StartAt, firstFailureAt)
	}
	if si.EndAt != nil {
		t.Error("new interval should be open (EndAt == nil)")
	}
}

func TestOnsetNeverAfterConfirmation(t *testing.T) {
	// If streak_started_at is zero (shouldn't happen, but must be safe)
	onset := intervals.BackdateOnset(time.Time{}, t0)
	if !onset.Equal(t0) {
		t.Errorf("zero streak time → should use confirmation time %v, got %v", t0, onset)
	}
}

func TestCloseInterval(t *testing.T) {
	si := intervals.OpenInterval(targetID, model.StateDown, t0)
	endAt := t0.Add(5 * time.Minute)
	closed := intervals.CloseInterval(si, endAt)

	if closed.EndAt == nil {
		t.Error("closed interval should have EndAt set")
	}
	if !closed.EndAt.Equal(endAt) {
		t.Errorf("closed.EndAt = %v, want %v", closed.EndAt, endAt)
	}
	// Original should be unchanged (value semantics)
	if si.EndAt != nil {
		t.Error("CloseInterval should not mutate original")
	}
}

func TestOpenIntervalState(t *testing.T) {
	si := intervals.OpenInterval(targetID, model.StateUp, t0)
	if si.State != model.StateUp {
		t.Errorf("state = %v, want UP", si.State)
	}
	if si.TargetID != targetID {
		t.Errorf("targetID mismatch")
	}
}

// TestUptimeUnderstatedWithoutBackdating verifies that NOT backdating
// would understate the downtime duration.
func TestUptimeUnderstatedWithoutBackdating(t *testing.T) {
	firstFailure := t0
	confirmation := t0.Add(120 * time.Second)
	end := t0.Add(300 * time.Second)

	// With backdating: downtime = 300 - 0 = 300s
	backdated := intervals.BackdateOnset(firstFailure, confirmation)
	correct := end.Sub(backdated)

	// Without backdating: downtime = 300 - 120 = 180s (understated by 120s)
	wrong := end.Sub(confirmation)

	if correct <= wrong {
		t.Errorf("backdated downtime (%v) should be > non-backdated (%v)", correct, wrong)
	}
	if correct != 300*time.Second {
		t.Errorf("backdated downtime = %v, want 300s", correct)
	}
	if wrong != 180*time.Second {
		t.Errorf("non-backdated downtime = %v, want 180s", wrong)
	}
}
