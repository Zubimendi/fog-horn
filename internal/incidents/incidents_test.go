package incidents_test

import (
	"testing"

	"github.com/zubimendi/fog-horn/internal/incidents"
	"github.com/zubimendi/fog-horn/internal/model"
)

func TestValidateTransition(t *testing.T) {
	tests := []struct {
		from model.IncidentStatus
		to   model.IncidentStatus
		want bool
	}{
		// Same status updates are always valid
		{model.IncidentInvestigating, model.IncidentInvestigating, true},
		{model.IncidentIdentified, model.IncidentIdentified, true},

		// Standard progression
		{model.IncidentInvestigating, model.IncidentIdentified, true},
		{model.IncidentInvestigating, model.IncidentMonitoring, true},
		{model.IncidentInvestigating, model.IncidentResolved, true},
		{model.IncidentInvestigating, model.IncidentRetracted, true},

		{model.IncidentIdentified, model.IncidentMonitoring, true},
		{model.IncidentIdentified, model.IncidentResolved, true},
		{model.IncidentIdentified, model.IncidentRetracted, true},

		{model.IncidentMonitoring, model.IncidentResolved, true},
		{model.IncidentMonitoring, model.IncidentRetracted, true},

		// Terminal states cannot transition
		{model.IncidentResolved, model.IncidentInvestigating, false},
		{model.IncidentResolved, model.IncidentRetracted, false},
		{model.IncidentRetracted, model.IncidentInvestigating, false},
		{model.IncidentRetracted, model.IncidentResolved, false},
	}

	for _, tc := range tests {
		got := incidents.ValidateTransition(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("ValidateTransition(%v -> %v) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}
