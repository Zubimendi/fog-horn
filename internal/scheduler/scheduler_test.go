package scheduler_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/scheduler"
)

func TestComputePhaseOffset(t *testing.T) {
	interval := 60 * time.Second
	targetID1 := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	targetID2 := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	// 1. Determinism: same input gives same offset
	off1a := scheduler.ComputePhaseOffset(targetID1, 0, 3, interval)
	off1b := scheduler.ComputePhaseOffset(targetID1, 0, 3, interval)
	if off1a != off1b {
		t.Fatalf("expected deterministic phase offset, got %v and %v", off1a, off1b)
	}

	// 2. Different targets have different offsets
	off2 := scheduler.ComputePhaseOffset(targetID2, 0, 3, interval)
	if off1a == off2 {
		t.Logf("Notice: target1 and target2 happened to have same offset: %v", off1a)
	}

	// 3. Offsets stay within [0, interval)
	if off1a < 0 || off1a >= interval {
		t.Fatalf("offset %v outside [0, %v)", off1a, interval)
	}

	// 4. Staggering between locations for same target
	loc0 := scheduler.ComputePhaseOffset(targetID1, 0, 3, interval)
	loc1 := scheduler.ComputePhaseOffset(targetID1, 1, 3, interval)
	loc2 := scheduler.ComputePhaseOffset(targetID1, 2, 3, interval)

	diff01 := (loc1 - loc0 + interval) % interval
	diff12 := (loc2 - loc1 + interval) % interval

	// 60s interval / 3 locations = 20s stagger
	expectedStagger := 20 * time.Second
	if diff01 != expectedStagger {
		t.Errorf("expected stagger of %v between loc0 and loc1, got %v", expectedStagger, diff01)
	}
	if diff12 != expectedStagger {
		t.Errorf("expected stagger of %v between loc1 and loc2, got %v", expectedStagger, diff12)
	}
}

func TestComputeLag(t *testing.T) {
	sched := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	exec := sched.Add(150 * time.Millisecond)

	lag := scheduler.ComputeLag(sched, exec)
	if lag != 150*time.Millisecond {
		t.Errorf("expected 150ms lag, got %v", lag)
	}
}
