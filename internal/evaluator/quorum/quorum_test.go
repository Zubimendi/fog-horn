package quorum_test

import (
	"testing"

	"github.com/zubimendi/fog-horn/internal/evaluator/quorum"
	"github.com/zubimendi/fog-horn/internal/model"
)

var defaultCfg = quorum.Config{QuorumFraction: 0.5, MinReporting: 1}

func mkLoc(state model.LocationStateValue, healthy, fresh bool) quorum.LocationReport {
	return quorum.LocationReport{State: state, Healthy: healthy, HasFresh: fresh}
}

func TestAllUp(t *testing.T) {
	locs := []quorum.LocationReport{
		mkLoc(model.LocationUp, true, true),
		mkLoc(model.LocationUp, true, true),
		mkLoc(model.LocationUp, true, true),
	}
	if got := quorum.Evaluate(locs, defaultCfg); got != model.StateUp {
		t.Errorf("all UP → want UP, got %v", got)
	}
}

func TestAllDown(t *testing.T) {
	locs := []quorum.LocationReport{
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationDown, true, true),
	}
	if got := quorum.Evaluate(locs, defaultCfg); got != model.StateDown {
		t.Errorf("all DOWN → want DOWN, got %v", got)
	}
}

func TestOneOfThreeDownIsPartial(t *testing.T) {
	locs := []quorum.LocationReport{
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationUp, true, true),
		mkLoc(model.LocationUp, true, true),
	}
	got := quorum.Evaluate(locs, defaultCfg)
	if got != model.StatePartial {
		t.Errorf("1/3 DOWN → want PARTIAL, got %v", got)
	}
}

func TestTwoOfThreeDownIsDown(t *testing.T) {
	locs := []quorum.LocationReport{
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationUp, true, true),
	}
	got := quorum.Evaluate(locs, defaultCfg)
	if got != model.StateDown {
		t.Errorf("2/3 DOWN → want DOWN (quorum=0.5), got %v", got)
	}
}

func TestUnhealthyLocationExcludedFromDenominator(t *testing.T) {
	// 2 locations: 1 healthy+down, 1 unhealthy+down
	// Only 1 healthy location → 1/1 = DOWN
	locs := []quorum.LocationReport{
		mkLoc(model.LocationDown, true, true),
		mkLoc(model.LocationDown, false, true), // unhealthy: excluded
	}
	got := quorum.Evaluate(locs, defaultCfg)
	if got != model.StateDown {
		t.Errorf("unhealthy location excluded: 1/1 DOWN → want DOWN, got %v", got)
	}
}

func TestNoFreshDataIsUnknown(t *testing.T) {
	locs := []quorum.LocationReport{
		mkLoc(model.LocationUp, true, false), // no fresh data
		mkLoc(model.LocationUp, true, false),
	}
	cfg := quorum.Config{QuorumFraction: 0.5, MinReporting: 1}
	got := quorum.Evaluate(locs, cfg)
	if got != model.StateUnknown {
		t.Errorf("no fresh data → want UNKNOWN, got %v", got)
	}
}

func TestBelowMinReportingIsUnknown(t *testing.T) {
	// 1 fresh location but MinReporting=2
	cfg := quorum.Config{QuorumFraction: 0.5, MinReporting: 2}
	locs := []quorum.LocationReport{
		mkLoc(model.LocationUp, true, true),
	}
	got := quorum.Evaluate(locs, cfg)
	if got != model.StateUnknown {
		t.Errorf("below MinReporting → want UNKNOWN, got %v", got)
	}
}

func TestEmptyLocationsIsUnknown(t *testing.T) {
	got := quorum.Evaluate(nil, defaultCfg)
	if got != model.StateUnknown {
		t.Errorf("empty locations → want UNKNOWN, got %v", got)
	}
}
