// Package quorum derives a target's overall state from the per-location states.
// Pure: no I/O, no database access.
package quorum

import (
	"github.com/zubimendi/fog-horn/internal/model"
)

// LocationReport is the input view of a single reporting location.
type LocationReport struct {
	LocationID string
	State      model.LocationStateValue
	Healthy    bool // probe self-test passing; unhealthy locations excluded from denominator
	HasFresh   bool // has a result fresher than 3·interval
}

// Config controls quorum thresholds.
type Config struct {
	// QuorumFraction is the fraction of healthy reporting locations that must
	// be DOWN before the target is considered DOWN. Default 0.5 (majority).
	QuorumFraction float64
	// MinReporting is the minimum number of healthy locations that must have
	// fresh data before a non-UNKNOWN state can be derived.
	MinReporting int
}

// Evaluate computes the target state from all location reports.
func Evaluate(locations []LocationReport, cfg Config) model.TargetStateValue {
	if cfg.QuorumFraction == 0 {
		cfg.QuorumFraction = 0.5
	}
	if cfg.MinReporting == 0 {
		cfg.MinReporting = 1
	}

	var (
		totalHealthy   int
		freshHealthy   int
		downCount      int
		partialAnywhere bool
	)

	for _, loc := range locations {
		if !loc.Healthy {
			continue // excluded from denominator
		}
		totalHealthy++
		if !loc.HasFresh {
			continue
		}
		freshHealthy++
		switch loc.State {
		case model.LocationDown:
			downCount++
		}
	}

	// Not enough fresh data
	if freshHealthy < cfg.MinReporting {
		return model.StateUnknown
	}

	_ = partialAnywhere // used below

	downFraction := float64(downCount) / float64(freshHealthy)

	switch {
	case downCount == freshHealthy:
		return model.StateDown
	case downFraction >= cfg.QuorumFraction:
		return model.StateDown
	case downCount > 0:
		return model.StatePartial
	default:
		return model.StateUp
	}
}
