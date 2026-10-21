package maintenance_test

import (
	"testing"

	"github.com/zubimendi/fog-horn/internal/maintenance"
	"github.com/zubimendi/fog-horn/internal/model"
)

func TestDeriveComponentStatus(t *testing.T) {
	// 1. All UP, no maintenance -> operational
	s1 := maintenance.DeriveComponentStatus(false, []model.TargetStateValue{model.StateUp, model.StateUp}, nil)
	if s1 != maintenance.StatusOperational {
		t.Errorf("want operational, got %v", s1)
	}

	// 2. Under maintenance, targets UP -> under_maintenance
	s2 := maintenance.DeriveComponentStatus(true, []model.TargetStateValue{model.StateUp}, nil)
	if s2 != maintenance.StatusUnderMaintenance {
		t.Errorf("want under_maintenance, got %v", s2)
	}

	// 3. One target DEGRADED -> degraded_performance
	s3 := maintenance.DeriveComponentStatus(false, []model.TargetStateValue{model.StateUp, model.StateDegraded}, nil)
	if s3 != maintenance.StatusDegradedPerformance {
		t.Errorf("want degraded_performance, got %v", s3)
	}

	// 4. One target DOWN -> partial_outage
	s4 := maintenance.DeriveComponentStatus(false, []model.TargetStateValue{model.StateUp, model.StateDown}, nil)
	if s4 != maintenance.StatusPartialOutage {
		t.Errorf("want partial_outage, got %v", s4)
	}

	// 5. All targets DOWN -> major_outage
	s5 := maintenance.DeriveComponentStatus(false, []model.TargetStateValue{model.StateDown, model.StateDown}, nil)
	if s5 != maintenance.StatusMajorOutage {
		t.Errorf("want major_outage, got %v", s5)
	}

	// 6. Major outage supersedes under_maintenance
	s6 := maintenance.DeriveComponentStatus(true, []model.TargetStateValue{model.StateDown, model.StateDown}, nil)
	if s6 != maintenance.StatusMajorOutage {
		t.Errorf("want major_outage, got %v", s6)
	}

	// 7. Active incident override takes priority if higher
	impact := maintenance.StatusMajorOutage
	s7 := maintenance.DeriveComponentStatus(false, []model.TargetStateValue{model.StateUp}, &impact)
	if s7 != maintenance.StatusMajorOutage {
		t.Errorf("want major_outage from incident impact, got %v", s7)
	}
}
