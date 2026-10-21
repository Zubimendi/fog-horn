package hysteresis_test

import (
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/evaluator/hysteresis"
	"github.com/zubimendi/fog-horn/internal/model"
)

var defaultCfg = hysteresis.Config{NFail: 3, NRecover: 2}
var t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func TestUpStaysUpOnOK(t *testing.T) {
	s := hysteresis.State{Value: model.LocationUp}
	got := hysteresis.Evaluate(s, model.ClassOK, defaultCfg, t0)
	if got.Value != model.LocationUp {
		t.Errorf("UP + OK should stay UP, got %v", got.Value)
	}
}

func TestUpGoesDownAfterNFail(t *testing.T) {
	s := hysteresis.State{Value: model.LocationUp}
	for i := 0; i < defaultCfg.NFail-1; i++ {
		s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, t0)
		if s.Value == model.LocationDown {
			t.Fatalf("should not be DOWN yet after %d failures (need %d)", i+1, defaultCfg.NFail)
		}
	}
	// One more failure should tip it over
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, t0)
	if s.Value != model.LocationDown {
		t.Errorf("should be DOWN after %d failures, got %v", defaultCfg.NFail, s.Value)
	}
}

func TestDownRequiresNRecoverNotNFail(t *testing.T) {
	// Start in DOWN state
	s := hysteresis.State{Value: model.LocationDown, ConsecutiveFailures: defaultCfg.NFail}

	// NRecover - 1 OKs: should still be DOWN
	for i := 0; i < defaultCfg.NRecover-1; i++ {
		s = hysteresis.Evaluate(s, model.ClassOK, defaultCfg, t0)
		if s.Value == model.LocationUp {
			t.Fatalf("should not recover after only %d OKs (need %d)", i+1, defaultCfg.NRecover)
		}
	}

	// One more OK tips recovery
	s = hysteresis.Evaluate(s, model.ClassOK, defaultCfg, t0)
	if s.Value != model.LocationUp {
		t.Errorf("should be UP after %d OKs, got %v", defaultCfg.NRecover, s.Value)
	}
}

func TestNRecoverDiffersFromNFail(t *testing.T) {
	// With NFail=2, NRecover=4, confirm different thresholds apply
	cfg := hysteresis.Config{NFail: 2, NRecover: 4}

	s := hysteresis.State{Value: model.LocationUp}
	// 2 failures → DOWN
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, cfg, t0)
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, cfg, t0)
	if s.Value != model.LocationDown {
		t.Fatal("expected DOWN after NFail=2 failures")
	}

	// 2 OKs → still DOWN (NRecover=4)
	s = hysteresis.Evaluate(s, model.ClassOK, cfg, t0)
	s = hysteresis.Evaluate(s, model.ClassOK, cfg, t0)
	if s.Value != model.LocationDown {
		t.Errorf("expected still DOWN (NRecover=4, only 2 OKs), got %v", s.Value)
	}

	// 2 more OKs (total 4) → UP
	s = hysteresis.Evaluate(s, model.ClassOK, cfg, t0)
	s = hysteresis.Evaluate(s, model.ClassOK, cfg, t0)
	if s.Value != model.LocationUp {
		t.Errorf("expected UP after 4 OKs with NRecover=4, got %v", s.Value)
	}
}

func TestProbeErrorDoesNotAffectState(t *testing.T) {
	s := hysteresis.State{Value: model.LocationUp, ConsecutiveFailures: 1}
	got := hysteresis.Evaluate(s, model.ClassProbeError, defaultCfg, t0)
	if got.Value != model.LocationUp {
		t.Errorf("PROBE_ERROR should not change state, got %v", got.Value)
	}
	if got.ConsecutiveFailures != 1 {
		t.Errorf("PROBE_ERROR should not change ConsecutiveFailures, got %d", got.ConsecutiveFailures)
	}
}

func TestConfigErrorDoesNotAffectState(t *testing.T) {
	s := hysteresis.State{Value: model.LocationDown, ConsecutiveFailures: 5}
	got := hysteresis.Evaluate(s, model.ClassConfigError, defaultCfg, t0)
	if got.Value != model.LocationDown {
		t.Errorf("CONFIG_ERROR should not change state, got %v", got.Value)
	}
}

func TestStreakStartedAtSetOnFirstFailure(t *testing.T) {
	s := hysteresis.State{Value: model.LocationUp}
	firstFailTime := t0.Add(30 * time.Second)
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, firstFailTime)
	if !s.StreakStartedAt.Equal(firstFailTime) {
		t.Errorf("StreakStartedAt should be set to first failure time %v, got %v",
			firstFailTime, s.StreakStartedAt)
	}

	// Second failure should NOT reset StreakStartedAt
	secondFailTime := t0.Add(90 * time.Second)
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, secondFailTime)
	if !s.StreakStartedAt.Equal(firstFailTime) {
		t.Errorf("StreakStartedAt should remain at first failure, got %v", s.StreakStartedAt)
	}
}

func TestFailureThenOKResetsCounter(t *testing.T) {
	s := hysteresis.State{Value: model.LocationUp}
	// 2 failures (below NFail=3)
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, t0)
	s = hysteresis.Evaluate(s, model.ClassTargetFailure, defaultCfg, t0)
	if s.ConsecutiveFailures != 2 {
		t.Fatalf("expected 2 consecutive failures, got %d", s.ConsecutiveFailures)
	}
	// OK resets counter
	s = hysteresis.Evaluate(s, model.ClassOK, defaultCfg, t0)
	if s.ConsecutiveFailures != 0 {
		t.Errorf("OK should reset ConsecutiveFailures to 0, got %d", s.ConsecutiveFailures)
	}
}
