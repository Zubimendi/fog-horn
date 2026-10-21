// Package incidents manages the incident lifecycle, append-only updates,
// row-locked transitions, and retraction audits.
package incidents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
)

var (
	ErrInvalidTransition = errors.New("incidents: invalid status transition")
	ErrIncidentNotFound  = errors.New("incidents: incident not found")
	ErrRetractionReason  = errors.New("incidents: retraction requires non-empty reason")
)

// ValidateTransition enforces valid state machine progressions.
func ValidateTransition(from, to model.IncidentStatus) bool {
	if from == to {
		return true // status update with same status (new message) is valid
	}

	switch from {
	case model.IncidentInvestigating:
		return to == model.IncidentIdentified || to == model.IncidentMonitoring ||
			to == model.IncidentResolved || to == model.IncidentRetracted
	case model.IncidentIdentified:
		return to == model.IncidentMonitoring || to == model.IncidentResolved ||
			to == model.IncidentRetracted
	case model.IncidentMonitoring:
		return to == model.IncidentResolved || to == model.IncidentRetracted
	case model.IncidentResolved, model.IncidentRetracted:
		return false // terminal states
	default:
		return false
	}
}

// CreateIncidentParams holds input for opening an incident.
type CreateIncidentParams struct {
	TenantID     uuid.UUID
	Title        string
	InitialMsg   string
	StartedAt    time.Time // backdated onset of the outage
	AutoOpened   bool
	PostedBy     string
	ComponentIDs []uuid.UUID
	Impact       string // OPERATIONAL | DEGRADED | PARTIAL_OUTAGE | MAJOR_OUTAGE
}

// IncidentDetails returns incident and its chronological updates.
type IncidentDetails struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Title           string
	Status          model.IncidentStatus
	StartedAt       time.Time
	PostedAt        *time.Time
	ResolvedAt      *time.Time
	AutoOpened      bool
	RetractedReason string
	Updates         []IncidentUpdate
}

type IncidentUpdate struct {
	ID        uuid.UUID
	Status    model.IncidentStatus
	Message   string
	PostedBy  string
	PostedAt  time.Time
}

// Create opens a new incident and appends its first update in a single transaction.
func Create(ctx context.Context, pool *pgxpool.Pool, params CreateIncidentParams) (uuid.UUID, error) {
	if params.Title == "" {
		return uuid.Nil, errors.New("incidents: title cannot be empty")
	}
	if params.StartedAt.IsZero() {
		params.StartedAt = time.Now()
	}
	now := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("incidents: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	incidentID := uuid.New()
	status := model.IncidentInvestigating

	_, err = tx.Exec(ctx, `
INSERT INTO incidents (
    id, tenant_id, title, status, started_at, posted_at, auto_opened
) VALUES ($1, $2, $3, $4, $5, $6, $7)
`, incidentID, params.TenantID, params.Title, string(status), params.StartedAt, now, params.AutoOpened)
	if err != nil {
		return uuid.Nil, fmt.Errorf("incidents: insert incident: %w", err)
	}

	// Link affected components if any
	for _, compID := range params.ComponentIDs {
		impact := params.Impact
		if impact == "" {
			impact = "MAJOR_OUTAGE"
		}
		_, err = tx.Exec(ctx, `
INSERT INTO incident_components (incident_id, component_id, impact)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING
`, incidentID, compID, impact)
		if err != nil {
			return uuid.Nil, fmt.Errorf("incidents: insert component: %w", err)
		}
	}

	// Append initial update
	postedBy := params.PostedBy
	if postedBy == "" {
		postedBy = "system"
	}
	initialMsg := params.InitialMsg
	if initialMsg == "" {
		initialMsg = "We are currently investigating an issue affecting this service."
	}

	_, err = tx.Exec(ctx, `
INSERT INTO incident_updates (id, incident_id, status, message, posted_by, posted_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, uuid.New(), incidentID, string(status), initialMsg, postedBy, now)
	if err != nil {
		return uuid.Nil, fmt.Errorf("incidents: insert initial update: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("incidents: commit: %w", err)
	}

	return incidentID, nil
}

// AddUpdate appends a new message and transitions the incident status.
func AddUpdate(
	ctx context.Context,
	pool *pgxpool.Pool,
	incidentID uuid.UUID,
	newStatus model.IncidentStatus,
	message string,
	postedBy string,
	retractReason string,
) error {
	if postedBy == "" {
		postedBy = "system"
	}
	now := time.Now()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("incidents: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the incident row
	var curStatusStr string
	err = tx.QueryRow(ctx, `
SELECT status FROM incidents WHERE id = $1 FOR UPDATE
`, incidentID).Scan(&curStatusStr)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ErrIncidentNotFound
		}
		return fmt.Errorf("incidents: lock incident: %w", err)
	}

	curStatus := model.IncidentStatus(curStatusStr)
	if !ValidateTransition(curStatus, newStatus) {
		return fmt.Errorf("%w: cannot transition from %s to %s", ErrInvalidTransition, curStatus, newStatus)
	}

	if newStatus == model.IncidentRetracted && retractReason == "" {
		return ErrRetractionReason
	}

	var resolvedAt *time.Time
	if newStatus == model.IncidentResolved {
		resolvedAt = &now
	}

	// Update projection cache in incidents table
	_, err = tx.Exec(ctx, `
UPDATE incidents
SET status = $1, resolved_at = COALESCE($2, resolved_at),
    retracted_reason = COALESCE($3, retracted_reason), updated_at = now()
WHERE id = $4
`, string(newStatus), resolvedAt, retractReason, incidentID)
	if err != nil {
		return fmt.Errorf("incidents: update incident status: %w", err)
	}

	// Append-only insert into incident_updates
	_, err = tx.Exec(ctx, `
INSERT INTO incident_updates (id, incident_id, status, message, posted_by, posted_at)
VALUES ($1, $2, $3, $4, $5, $6)
`, uuid.New(), incidentID, string(newStatus), message, postedBy, now)
	if err != nil {
		return fmt.Errorf("incidents: append update: %w", err)
	}

	// If retracted, record audit log entry
	if newStatus == model.IncidentRetracted {
		_, err = tx.Exec(ctx, `
INSERT INTO audit_log (tenant_id, action, actor, details)
SELECT tenant_id, 'INCIDENT_RETRACTED', $1, jsonb_build_object('incident_id', $2::text, 'reason', $3)
FROM incidents WHERE id = $2
`, postedBy, incidentID, retractReason)
		if err != nil {
			return fmt.Errorf("incidents: write audit log: %w", err)
		}
	}

	return tx.Commit(ctx)
}
