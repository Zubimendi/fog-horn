// Package publisher builds tenant status snapshots from PostgreSQL
// and atomically publishes them to object storage.
package publisher

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/maintenance"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/snapshot"
)

// BuildSnapshot gathers data and builds an immutable snapshot for a tenant.
func BuildSnapshot(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (snapshot.Snapshot, error) {
	now := time.Now()

	// 1. Fetch public components with their mapped target states
	compQuery := `
SELECT c.id, c.name, COALESCE(c.group_name, ''), COALESCE(c.description, '')
FROM components c
WHERE c.tenant_id = $1 AND c.public
ORDER BY c.sort_order ASC, c.name ASC;
`
	rows, err := pool.Query(ctx, compQuery, tenantID)
	if err != nil {
		return snapshot.Snapshot{}, fmt.Errorf("snapshot: query components: %w", err)
	}
	defer rows.Close()

	type compMeta struct {
		id    uuid.UUID
		name  string
		group string
		desc  string
	}
	var compMetas []compMeta
	for rows.Next() {
		var cm compMeta
		if err := rows.Scan(&cm.id, &cm.name, &cm.group, &cm.desc); err != nil {
			return snapshot.Snapshot{}, fmt.Errorf("snapshot: scan comp: %w", err)
		}
		compMetas = append(compMetas, cm)
	}
	rows.Close()

	var compViews []snapshot.ComponentView
	for _, cm := range compMetas {
		// Fetch target states for this component
		tRows, err := pool.Query(ctx, `
SELECT ts.state
FROM component_targets ct
JOIN target_states ts ON ts.target_id = ct.target_id
WHERE ct.component_id = $1
`, cm.id)
		var tStates []model.TargetStateValue
		if err == nil {
			for tRows.Next() {
				var stStr string
				if err := tRows.Scan(&stStr); err == nil {
					tStates = append(tStates, model.TargetStateValue(stStr))
				}
			}
			tRows.Close()
		}

		// Check maintenance
		var underMaint bool
		_ = pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM maintenance_components mc
    JOIN maintenance_windows mw ON mw.id = mc.maintenance_id
    WHERE mc.component_id = $1 AND mw.status = 'IN_PROGRESS'
)
`, cm.id).Scan(&underMaint)

		status := maintenance.DeriveComponentStatus(underMaint, tStates, nil)
		compViews = append(compViews, snapshot.ComponentView{
			Name:        cm.name,
			GroupName:   cm.group,
			Status:      status,
			Description: cm.desc,
		})
	}

	// 2. Fetch active incidents
	incQuery := `
SELECT i.id, i.title, i.status, i.started_at
FROM incidents i
WHERE i.tenant_id = $1 AND i.status != 'RESOLVED' AND i.status != 'RETRACTED'
ORDER BY i.started_at DESC;
`
	incRows, err := pool.Query(ctx, incQuery, tenantID)
	var activeIncidents []snapshot.IncidentView
	if err == nil {
		for incRows.Next() {
			var (
				incID     uuid.UUID
				title     string
				statusStr string
				startedAt time.Time
			)
			if err := incRows.Scan(&incID, &title, &statusStr, &startedAt); err == nil {
				// Fetch chronological updates for incident
				uRows, err := pool.Query(ctx, `
SELECT status, message, posted_at
FROM incident_updates
WHERE incident_id = $1
ORDER BY posted_at ASC
`, incID)
				var updates []snapshot.UpdateView
				if err == nil {
					for uRows.Next() {
						var (
							uSt  string
							uMsg string
							uAt  time.Time
						)
						if err := uRows.Scan(&uSt, &uMsg, &uAt); err == nil {
							updates = append(updates, snapshot.UpdateView{
								Status:   model.IncidentStatus(uSt),
								Message:  uMsg,
								PostedAt: uAt,
							})
						}
					}
					uRows.Close()
				}

				activeIncidents = append(activeIncidents, snapshot.IncidentView{
					Title:     title,
					Status:    model.IncidentStatus(statusStr),
					StartedAt: startedAt,
					Updates:   updates,
				})
			}
		}
		incRows.Close()
	}

	overallText, overallSev := snapshot.ComputeOverall(compViews, activeIncidents)

	snap := snapshot.Snapshot{
		TenantID:            tenantID,
		GeneratedAt:         now,
		OverallStatus:       overallText,
		OverallSeverity:     overallSev,
		Components:          compViews,
		ActiveIncidents:     activeIncidents,
		UpcomingMaintenance: nil,
	}

	_, etag, _ := snapshot.RenderJSON(&snap)
	snap.ETag = etag

	return snap, nil
}

// Publish atomically publishes a snapshot to object storage and updates pointers.
func Publish(
	ctx context.Context,
	pool *pgxpool.Pool,
	storage snapshot.StorageClient,
	s snapshot.Snapshot,
) error {
	jsonBytes, etag, err := snapshot.RenderJSON(&s)
	if err != nil {
		return fmt.Errorf("snapshot: render json: %w", err)
	}

	htmlContent := snapshot.RenderHTML(s, false)

	// Object storage keys
	tenantStr := s.TenantID.String()
	versionKeyJSON := fmt.Sprintf("snapshots/%s/%d.json", tenantStr, s.GeneratedAt.UnixNano())
	versionKeyHTML := fmt.Sprintf("snapshots/%s/%d.html", tenantStr, s.GeneratedAt.UnixNano())
	currentKeyJSON := fmt.Sprintf("snapshots/%s/current.json", tenantStr)
	currentKeyHTML := fmt.Sprintf("snapshots/%s/current.html", tenantStr)

	// 1. Write immutable versioned objects
	if err := storage.PutObject(ctx, versionKeyJSON, jsonBytes, "application/json"); err != nil {
		return fmt.Errorf("snapshot: write version json: %w", err)
	}
	if err := storage.PutObject(ctx, versionKeyHTML, []byte(htmlContent), "text/html"); err != nil {
		return fmt.Errorf("snapshot: write version html: %w", err)
	}

	// 2. Atomic pointer swap in object storage
	if err := storage.PutObject(ctx, currentKeyJSON, jsonBytes, "application/json"); err != nil {
		return fmt.Errorf("snapshot: swap current json: %w", err)
	}
	if err := storage.PutObject(ctx, currentKeyHTML, []byte(htmlContent), "text/html"); err != nil {
		return fmt.Errorf("snapshot: swap current html: %w", err)
	}

	// 3. Record in database
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("snapshot: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	snapID := uuid.New()
	_, err = tx.Exec(ctx, `
INSERT INTO status_snapshots (id, tenant_id, version, etag, generated_at, object_key)
VALUES ($1, $2, $3, $4, $5, $6)
`, snapID, s.TenantID, s.GeneratedAt.UnixNano(), etag, s.GeneratedAt, currentKeyJSON)
	if err != nil {
		return fmt.Errorf("snapshot: insert status_snapshots: %w", err)
	}

	_, err = tx.Exec(ctx, `
INSERT INTO current_snapshot (tenant_id, snapshot_id, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (tenant_id) DO UPDATE SET
    snapshot_id = EXCLUDED.snapshot_id,
    updated_at  = now();
`, s.TenantID, snapID)
	if err != nil {
		return fmt.Errorf("snapshot: upsert current_snapshot: %w", err)
	}

	return tx.Commit(ctx)
}
