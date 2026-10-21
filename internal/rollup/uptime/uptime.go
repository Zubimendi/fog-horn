// Package uptime computes uptime percentage and coverage from state intervals.
// Pure: no I/O, no database access.
package uptime

import (
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
)

// Result holds the computed uptime metrics for a window.
type Result struct {
	// UptimePct is the fraction of observed time in UP or DEGRADED state.
	// Range [0.0, 1.0].
	UptimePct float64

	// CoveragePct is the fraction of the window that has observed data.
	// UNKNOWN time reduces coverage. Range [0.0, 1.0].
	CoveragePct float64

	// TotalWindow is the total duration of the measurement window.
	TotalWindow time.Duration

	// ExcludedDuration is the total time excluded (maintenance + paused).
	ExcludedDuration time.Duration

	// ObservedDuration is window minus excluded minus UNKNOWN time.
	ObservedDuration time.Duration

	// UptimeDuration is the UP + DEGRADED time within the observed window.
	UptimeDuration time.Duration

	// HasRetroactiveExclusion flags if any exclusion was applied retroactively.
	HasRetroactiveExclusion bool
}

// TimeRange is a half-open interval [Start, End).
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// ExclusionWindow is a time range that should be subtracted from both
// numerator and denominator (maintenance, paused).
type ExclusionWindow struct {
	Range       TimeRange
	Retroactive bool
}

// Calculate computes uptime from state intervals within a window,
// subtracting exclusion windows from both numerator and denominator.
// DEGRADED is counted as UP by default (matching architecture §4).
func Calculate(
	stateIntervals []model.StateInterval,
	window TimeRange,
	exclusions []ExclusionWindow,
) Result {
	windowDur := window.End.Sub(window.Start)
	if windowDur <= 0 {
		return Result{}
	}

	// Build a flat list of excluded ranges clamped to the window.
	var excludedRanges []TimeRange
	hasRetro := false
	for _, ex := range exclusions {
		clamped := clamp(ex.Range, window)
		if clamped.End.After(clamped.Start) {
			excludedRanges = append(excludedRanges, clamped)
			if ex.Retroactive {
				hasRetro = true
			}
		}
	}

	// Compute total excluded duration (merge overlapping excluded ranges).
	excludedDur := sumDuration(merge(excludedRanges))

	// For each state interval, compute the effective (non-excluded) duration
	// falling within the window.
	var uptimeDur, observedDur time.Duration

	for _, si := range stateIntervals {
		end := window.End
		if si.EndAt != nil && si.EndAt.Before(end) {
			end = *si.EndAt
		}
		start := si.StartAt
		if start.Before(window.Start) {
			start = window.Start
		}
		if !start.Before(end) {
			continue
		}

		// Subtract exclusions from this interval.
		effective := effectiveDuration(TimeRange{Start: start, End: end}, excludedRanges)

		switch si.State {
		case model.StateUp, model.StateDegraded:
			uptimeDur += effective
			observedDur += effective
		case model.StateDown, model.StatePartial:
			observedDur += effective
		case model.StateUnknown, model.StatePaused:
			// Unknown / paused does not count toward observed time.
		}
	}

	effectiveWindow := windowDur - excludedDur
	if effectiveWindow <= 0 {
		return Result{
			TotalWindow:             windowDur,
			ExcludedDuration:        excludedDur,
			HasRetroactiveExclusion: hasRetro,
		}
	}

	var uptimePct float64
	if observedDur > 0 {
		uptimePct = float64(uptimeDur) / float64(observedDur)
	}

	return Result{
		UptimePct:               uptimePct,
		CoveragePct:             float64(observedDur) / float64(effectiveWindow),
		TotalWindow:             windowDur,
		ExcludedDuration:        excludedDur,
		ObservedDuration:        observedDur,
		UptimeDuration:          uptimeDur,
		HasRetroactiveExclusion: hasRetro,
	}
}

// AllowedDowntime returns the allowed downtime for a given uptime target.
func AllowedDowntime(uptimeFraction float64, window time.Duration) time.Duration {
	downFraction := 1.0 - uptimeFraction
	return time.Duration(float64(window) * downFraction)
}

func clamp(r, window TimeRange) TimeRange {
	start := r.Start
	if start.Before(window.Start) {
		start = window.Start
	}
	end := r.End
	if end.After(window.End) {
		end = window.End
	}
	return TimeRange{Start: start, End: end}
}

func merge(ranges []TimeRange) []TimeRange {
	if len(ranges) == 0 {
		return nil
	}
	// Sort by start
	sorted := make([]TimeRange, len(ranges))
	copy(sorted, ranges)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j].Start.Before(sorted[j-1].Start); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	merged := []TimeRange{sorted[0]}
	for _, r := range sorted[1:] {
		last := &merged[len(merged)-1]
		if !r.Start.After(last.End) {
			if r.End.After(last.End) {
				last.End = r.End
			}
		} else {
			merged = append(merged, r)
		}
	}
	return merged
}

func sumDuration(ranges []TimeRange) time.Duration {
	var total time.Duration
	for _, r := range ranges {
		total += r.End.Sub(r.Start)
	}
	return total
}

// effectiveDuration returns the duration of r minus any overlapping exclusion.
func effectiveDuration(r TimeRange, exclusions []TimeRange) time.Duration {
	dur := r.End.Sub(r.Start)
	for _, ex := range exclusions {
		// Overlap of r and ex
		overlapStart := r.Start
		if ex.Start.After(overlapStart) {
			overlapStart = ex.Start
		}
		overlapEnd := r.End
		if ex.End.Before(overlapEnd) {
			overlapEnd = ex.End
		}
		if overlapEnd.After(overlapStart) {
			dur -= overlapEnd.Sub(overlapStart)
		}
	}
	if dur < 0 {
		return 0
	}
	return dur
}
