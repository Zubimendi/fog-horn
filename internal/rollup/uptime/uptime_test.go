package uptime_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/rollup/uptime"
)

var targetID = uuid.New()
var t0 = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

func openInterval(state model.TargetStateValue, start, end time.Time) model.StateInterval {
	e := end
	return model.StateInterval{
		ID:       uuid.New(),
		TargetID: targetID,
		State:    state,
		StartAt:  start,
		EndAt:    &e,
	}
}

func TestFullWindowUp(t *testing.T) {
	window := uptime.TimeRange{Start: t0, End: t0.Add(24 * time.Hour)}
	si := openInterval(model.StateUp, t0, t0.Add(24*time.Hour))
	r := uptime.Calculate([]model.StateInterval{si}, window, nil)
	if r.UptimePct != 1.0 {
		t.Errorf("full window UP → want 1.0, got %v", r.UptimePct)
	}
	if r.CoveragePct != 1.0 {
		t.Errorf("full window UP → coverage want 1.0, got %v", r.CoveragePct)
	}
}

func TestFullWindowDown(t *testing.T) {
	window := uptime.TimeRange{Start: t0, End: t0.Add(24 * time.Hour)}
	si := openInterval(model.StateDown, t0, t0.Add(24*time.Hour))
	r := uptime.Calculate([]model.StateInterval{si}, window, nil)
	if r.UptimePct != 0.0 {
		t.Errorf("full window DOWN → want 0.0, got %v", r.UptimePct)
	}
	if r.CoveragePct != 1.0 {
		t.Errorf("DOWN still observed → coverage want 1.0, got %v", r.CoveragePct)
	}
}

func TestUnknownReducesCoverage(t *testing.T) {
	window := uptime.TimeRange{Start: t0, End: t0.Add(2 * time.Hour)}
	// 1h UP, 1h UNKNOWN
	intervals := []model.StateInterval{
		openInterval(model.StateUp, t0, t0.Add(1*time.Hour)),
		openInterval(model.StateUnknown, t0.Add(1*time.Hour), t0.Add(2*time.Hour)),
	}
	r := uptime.Calculate(intervals, window, nil)
	if r.CoveragePct != 0.5 {
		t.Errorf("1h UP + 1h UNKNOWN → coverage want 0.5, got %v", r.CoveragePct)
	}
	if r.UptimePct != 1.0 {
		t.Errorf("1h UP out of 1h observed → uptime want 1.0, got %v", r.UptimePct)
	}
}

func TestMaintenanceExcludedFromBothNumeratorAndDenominator(t *testing.T) {
	window := uptime.TimeRange{Start: t0, End: t0.Add(3 * time.Hour)}
	// 1h DOWN, 1h maintenance (DOWN during maintenance), 1h UP
	intervals := []model.StateInterval{
		openInterval(model.StateDown, t0, t0.Add(1*time.Hour)),
		openInterval(model.StateDown, t0.Add(1*time.Hour), t0.Add(2*time.Hour)), // covered by maintenance
		openInterval(model.StateUp, t0.Add(2*time.Hour), t0.Add(3*time.Hour)),
	}
	exclusions := []uptime.ExclusionWindow{
		{Range: uptime.TimeRange{Start: t0.Add(1 * time.Hour), End: t0.Add(2 * time.Hour)}},
	}
	r := uptime.Calculate(intervals, window, exclusions)
	// effective window = 3h - 1h = 2h
	// observed: 1h DOWN + 1h UP = 2h
	// uptime: 1h UP / 2h = 0.5
	if r.ExcludedDuration != time.Hour {
		t.Errorf("excluded = %v, want 1h", r.ExcludedDuration)
	}
	if r.UptimePct != 0.5 {
		t.Errorf("uptime = %v, want 0.5", r.UptimePct)
	}
}

// TestNinesTable spot-checks the allowed downtime calculation from §4 of ARCHITECTURE.
func TestNinesTable(t *testing.T) {
	month := 30 * 24 * time.Hour
	cases := []struct {
		pct     float64
		wantMin time.Duration
		wantMax time.Duration
	}{
		{0.999, 43*time.Minute + 10*time.Second, 43*time.Minute + 15*time.Second},  // 43.2 min
		{0.9999, 4*time.Minute + 18*time.Second, 4*time.Minute + 25*time.Second},  // 4.32 min
		{0.99, 7*time.Hour + 11*time.Minute, 7*time.Hour + 13*time.Minute},         // 7.2 h
	}
	for _, tc := range cases {
		got := uptime.AllowedDowntime(tc.pct, month)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("AllowedDowntime(%.4f, 30d) = %v, want [%v, %v]",
				tc.pct, got, tc.wantMin, tc.wantMax)
		}
	}
}

func TestRetroactiveExclusionFlagged(t *testing.T) {
	window := uptime.TimeRange{Start: t0, End: t0.Add(time.Hour)}
	si := openInterval(model.StateUp, t0, t0.Add(time.Hour))
	exclusions := []uptime.ExclusionWindow{
		{
			Range:       uptime.TimeRange{Start: t0, End: t0.Add(30 * time.Minute)},
			Retroactive: true,
		},
	}
	r := uptime.Calculate([]model.StateInterval{si}, window, exclusions)
	if !r.HasRetroactiveExclusion {
		t.Error("retroactive exclusion should be flagged")
	}
}
