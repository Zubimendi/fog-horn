// Package nines provides error budget and "nines" availability calculations.
// Pure: no I/O, no database access.
package nines

import (
	"fmt"
	"math"
	"time"
)

// Budget represents the allowed downtime budget for an SLO over a time period.
type Budget struct {
	SLO              float64       // e.g. 99.9 (percent)
	Period           time.Duration // e.g. 30 * 24 * time.Hour
	TotalSeconds     float64       // total seconds in the period
	AllowedDowntime  time.Duration // maximum allowable downtime
	ActualDowntime   time.Duration // observed downtime
	RemainingDowntime time.Duration // budget left (can be negative if breached)
	BudgetConsumed   float64       // percent of allowed downtime consumed (e.g. 50.0%)
	Breached         bool          // true if actual > allowed
}

// ComputeBudget computes error budget consumption for a given SLO and period.
func ComputeBudget(slo float64, period time.Duration, actualDowntime time.Duration) Budget {
	if slo < 0 {
		slo = 0
	}
	if slo > 100 {
		slo = 100
	}

	totalSec := period.Seconds()
	unavailabilityFraction := (100.0 - slo) / 100.0
	allowedSec := totalSec * unavailabilityFraction
	allowedDur := time.Duration(math.Round(allowedSec * float64(time.Second)))

	remainingSec := allowedSec - actualDowntime.Seconds()
	remainingDur := time.Duration(math.Round(remainingSec * float64(time.Second)))

	var consumed float64
	if allowedSec > 0 {
		consumed = (actualDowntime.Seconds() / allowedSec) * 100.0
	} else if actualDowntime > 0 {
		consumed = math.Inf(1)
	}

	return Budget{
		SLO:               slo,
		Period:            period,
		TotalSeconds:      totalSec,
		AllowedDowntime:   allowedDur,
		ActualDowntime:    actualDowntime,
		RemainingDowntime: remainingDur,
		BudgetConsumed:    consumed,
		Breached:          actualDowntime > allowedDur,
	}
}

// CalculateNines returns the approximate number of nines for a given uptime percentage.
// e.g. 99.0 -> 2.0, 99.9 -> 3.0, 99.99 -> 4.0.
func CalculateNines(uptimePercent float64) float64 {
	if uptimePercent >= 100.0 {
		return math.Inf(1)
	}
	if uptimePercent <= 0.0 {
		return 0.0
	}
	unavailability := (100.0 - uptimePercent) / 100.0
	return -math.Log10(unavailability)
}

// FormatDowntime formats a duration in a human-friendly format (e.g. "43m 12s").
func FormatDowntime(d time.Duration) string {
	if d < 0 {
		return "-" + FormatDowntime(-d)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
