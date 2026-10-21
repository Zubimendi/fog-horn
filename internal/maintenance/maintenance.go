// Package maintenance handles scheduled and retroactive maintenance windows,
// IANA timezone conversions, and derived component status calculation.
package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
)

// ComponentStatus enumerates the derived status values.
type ComponentStatus string

const (
	StatusOperational         ComponentStatus = "operational"
	StatusUnderMaintenance    ComponentStatus = "under_maintenance"
	StatusDegradedPerformance ComponentStatus = "degraded_performance"
	StatusPartialOutage       ComponentStatus = "partial_outage"
	StatusMajorOutage         ComponentStatus = "major_outage"
)

// SeverityRank maps a status to an integer for max-severity comparison.
func SeverityRank(s ComponentStatus) int {
	switch s {
	case StatusMajorOutage:
		return 5
	case StatusPartialOutage:
		return 4
	case StatusDegradedPerformance:
		return 3
	case StatusUnderMaintenance:
		return 2
	case StatusOperational:
		return 1
	default:
		return 0
	}
}

// MaxSeverity returns the higher severity status.
func MaxSeverity(a, b ComponentStatus) ComponentStatus {
	if SeverityRank(a) >= SeverityRank(b) {
		return a
	}
	return b
}

// DeriveComponentStatus computes the component status from active maintenance,
// mapped targets' current states, and active incident impacts.
// Invariant 7: Component status is derived, never set via API.
func DeriveComponentStatus(
	underMaintenance bool,
	targetStates []model.TargetStateValue,
	incidentImpact *ComponentStatus,
) ComponentStatus {
	derived := StatusOperational

	if underMaintenance {
		derived = MaxSeverity(derived, StatusUnderMaintenance)
	}

	if incidentImpact != nil {
		derived = MaxSeverity(derived, *incidentImpact)
	}

	if len(targetStates) > 0 {
		downCount := 0
		partialCount := 0
		degradedCount := 0

		for _, ts := range targetStates {
			switch ts {
			case model.StateDown:
				downCount++
			case model.StatePartial:
				partialCount++
			case model.StateDegraded:
				degradedCount++
			}
		}

		if downCount == len(targetStates) {
			derived = MaxSeverity(derived, StatusMajorOutage)
		} else if downCount > 0 || partialCount > 0 {
			derived = MaxSeverity(derived, StatusPartialOutage)
		} else if degradedCount > 0 {
			derived = MaxSeverity(derived, StatusDegradedPerformance)
		}
	}

	return derived
}

// CreateMaintenanceParams holds parameters to create a maintenance window.
type CreateMaintenanceParams struct {
	TenantID     uuid.UUID
	Title        string
	StartsAt     time.Time
	EndsAt       time.Time
	TZ           string // IANA timezone name (e.g. "America/New_York", "Europe/London", "UTC")
	RRULE        string
	Retroactive  bool
	Reason       string
	CreatedBy    string
	ComponentIDs []uuid.UUID
}

// Create creates a maintenance window with validation and audit logging for retroactive windows.
func Create(ctx context.Context, pool *pgxpool.Pool, p CreateMaintenanceParams) (uuid.UUID, error) {
	if p.Title == "" {
		return uuid.Nil, errors.New("maintenance: title cannot be empty")
	}
	if p.EndsAt.Before(p.StartsAt) {
		return uuid.Nil, errors.New("maintenance: ends_at must be after starts_at")
	}
	if p.TZ == "" {
		p.TZ = "UTC"
	}
	if _, err := time.LoadLocation(p.TZ); err != nil {
		return uuid.Nil, fmt.Errorf("maintenance: invalid IANA timezone %q: %w", p.TZ, err)
	}
	if p.Retroactive && p.Reason == "" {
		return uuid.Nil, errors.New("maintenance: retroactive maintenance requires a reason")
	}
	if p.CreatedBy == "" {
		p.CreatedBy = "system"
	}

	now := time.Now()
	status := "SCHEDULED"
	if now.After(p.StartsAt) && now.Before(p.EndsAt) {
		status = "IN_PROGRESS"
	} else if now.After(p.EndsAt) {
		status = "COMPLETED"
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("maintenance: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	id := uuid.New()
	_, err = tx.Exec(ctx, `
INSERT INTO maintenance_windows (
    id, tenant_id, title, starts_at, ends_at, tz, rrule, status, retroactive, reason, created_by
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
`, id, p.TenantID, p.Title, p.StartsAt, p.EndsAt, p.TZ, p.RRULE, status, p.Retroactive, p.Reason, p.CreatedBy)
	if err != nil {
		return uuid.Nil, fmt.Errorf("maintenance: insert window: %w", err)
	}

	for _, cid := range p.ComponentIDs {
		_, err = tx.Exec(ctx, `
INSERT INTO maintenance_components (maintenance_id, component_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING
`, id, cid)
		if err != nil {
			return uuid.Nil, fmt.Errorf("maintenance: link component: %w", err)
		}
	}

	// Invariant: retroactive maintenance writes audit event
	if p.Retroactive {
		_, err = tx.Exec(ctx, `
INSERT INTO audit_log (tenant_id, action, actor, details)
VALUES ($1, 'MAINTENANCE_RETROACTIVE', $2, jsonb_build_object(
    'maintenance_id', $3::text,
    'reason', $4,
    'starts_at', $5,
    'ends_at', $6
))
`, p.TenantID, p.CreatedBy, id, p.Reason, p.StartsAt.Format(time.RFC3339), p.EndsAt.Format(time.RFC3339))
		if err != nil {
			return uuid.Nil, fmt.Errorf("maintenance: write audit log: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("maintenance: commit: %w", err)
	}

	return id, nil
}
