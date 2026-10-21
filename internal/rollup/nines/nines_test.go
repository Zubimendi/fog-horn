package nines_test

import (
	"math"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/rollup/nines"
)

func TestComputeBudget(t *testing.T) {
	period := 30 * 24 * time.Hour // 30 days = 2,592,000s
	slo := 99.9                   // 0.1% allowed downtime = 2592s = 43m 12s

	// Case 1: Zero downtime
	b0 := nines.ComputeBudget(slo, period, 0)
	if b0.Breached {
		t.Fatalf("expected not breached, got breached")
	}
	if b0.AllowedDowntime != 2592*time.Second {
		t.Errorf("expected 2592s allowed, got %v", b0.AllowedDowntime)
	}
	if b0.BudgetConsumed != 0 {
		t.Errorf("expected 0%% consumed, got %f", b0.BudgetConsumed)
	}

	// Case 2: Half budget consumed (21m 36s = 1296s)
	b1 := nines.ComputeBudget(slo, period, 1296*time.Second)
	if math.Abs(b1.BudgetConsumed-50.0) > 0.01 {
		t.Errorf("expected 50%% consumed, got %f", b1.BudgetConsumed)
	}
	if b1.RemainingDowntime != 1296*time.Second {
		t.Errorf("expected 1296s remaining, got %v", b1.RemainingDowntime)
	}
	if b1.Breached {
		t.Errorf("expected not breached")
	}

	// Case 3: Breached budget (50m = 3000s)
	b2 := nines.ComputeBudget(slo, period, 3000*time.Second)
	if !b2.Breached {
		t.Errorf("expected breached")
	}
	if b2.RemainingDowntime >= 0 {
		t.Errorf("expected negative remaining, got %v", b2.RemainingDowntime)
	}
}

func TestCalculateNines(t *testing.T) {
	tests := []struct {
		uptime float64
		want   float64
	}{
		{90.0, 1.0},
		{99.0, 2.0},
		{99.9, 3.0},
		{99.99, 4.0},
		{99.999, 5.0},
	}

	for _, tc := range tests {
		got := nines.CalculateNines(tc.uptime)
		if math.Abs(got-tc.want) > 0.001 {
			t.Errorf("CalculateNines(%f) = %f; want %f", tc.uptime, got, tc.want)
		}
	}
}

func TestFormatDowntime(t *testing.T) {
	if s := nines.FormatDowntime(43*time.Minute + 12*time.Second); s != "43m 12s" {
		t.Errorf("got %q, want '43m 12s'", s)
	}
	if s := nines.FormatDowntime(2*time.Hour + 5*time.Minute + 10*time.Second); s != "2h 5m 10s" {
		t.Errorf("got %q, want '2h 5m 10s'", s)
	}
}
