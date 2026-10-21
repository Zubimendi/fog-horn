// Package snapshot handles status page data projection, HTML/JSON rendering,
// storage upload, atomic pointer swapping, and honest staleness detection.
package snapshot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/maintenance"
	"github.com/zubimendi/fog-horn/internal/model"
)

// StorageClient defines the object store interface needed by publisher and pagehost.
type StorageClient interface {
	PutObject(ctx context.Context, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, key string) ([]byte, string, error) // data, etag, error
}

// MemoryStorage provides an in-memory StorageClient for tests and single-node setups.
type MemoryStorage struct {
	objects map[string][]byte
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{objects: make(map[string][]byte)}
}

func (m *MemoryStorage) PutObject(ctx context.Context, key string, data []byte, contentType string) error {
	m.objects[key] = data
	return nil
}

func (m *MemoryStorage) GetObject(ctx context.Context, key string) ([]byte, string, error) {
	data, ok := m.objects[key]
	if !ok {
		return nil, "", fmt.Errorf("object %q not found", key)
	}
	hash := sha256.Sum256(data)
	return data, hex.EncodeToString(hash[:]), nil
}

// ComponentView represents a public component on the status page.
type ComponentView struct {
	Name        string                      `json:"name"`
	GroupName   string                      `json:"group_name,omitempty"`
	Status      maintenance.ComponentStatus `json:"status"`
	Description string                      `json:"description,omitempty"`
}

// IncidentView represents an ongoing or recent incident for public display.
type IncidentView struct {
	Title     string              `json:"title"`
	Status    model.IncidentStatus `json:"status"`
	StartedAt time.Time           `json:"started_at"`
	Updates   []UpdateView        `json:"updates"`
}

type UpdateView struct {
	Status   model.IncidentStatus `json:"status"`
	Message  string               `json:"message"`
	PostedAt time.Time            `json:"posted_at"`
}

// MaintenanceView represents active or scheduled maintenance for public display.
type MaintenanceView struct {
	Title    string    `json:"title"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Status   string    `json:"status"`
}

// Snapshot holds the complete, self-contained public status representation.
type Snapshot struct {
	TenantID            uuid.UUID         `json:"tenant_id"`
	Version             int64             `json:"version"`
	GeneratedAt         time.Time         `json:"generated_at"`
	OverallStatus       string            `json:"overall_status"`
	OverallSeverity     maintenance.ComponentStatus `json:"overall_severity"`
	Components          []ComponentView   `json:"components"`
	ActiveIncidents     []IncidentView    `json:"active_incidents"`
	UpcomingMaintenance []MaintenanceView `json:"upcoming_maintenance"`
	ETag                string            `json:"etag"`
}

// ComputeOverall computes the summary status across all public components.
func ComputeOverall(comps []ComponentView, activeIncidents []IncidentView) (string, maintenance.ComponentStatus) {
	if len(activeIncidents) > 0 {
		return "Active Incidents Reported", maintenance.StatusMajorOutage
	}

	highest := maintenance.StatusOperational
	for _, c := range comps {
		highest = maintenance.MaxSeverity(highest, c.Status)
	}

	switch highest {
	case maintenance.StatusMajorOutage:
		return "Major Service Outage", highest
	case maintenance.StatusPartialOutage:
		return "Partial System Outage", highest
	case maintenance.StatusDegradedPerformance:
		return "Degraded Performance", highest
	case maintenance.StatusUnderMaintenance:
		return "Scheduled Maintenance in Progress", highest
	default:
		return "All Systems Operational", maintenance.StatusOperational
	}
}

// RenderJSON serializes the snapshot to JSON bytes and calculates its ETag.
func RenderJSON(s *Snapshot) ([]byte, string, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(b)
	etag := fmt.Sprintf("\"%s\"", hex.EncodeToString(hash[:16]))
	s.ETag = etag
	return b, etag, nil
}

// RenderHTML produces clean, server-rendered HTML with zero client-side JavaScript required.
func RenderHTML(s Snapshot, isStale bool) string {
	var buf bytes.Buffer

	buf.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	buf.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	buf.WriteString("<title>System Status</title>\n")
	buf.WriteString("<style>\n")
	buf.WriteString(`
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0f172a; color: #f8fafc; margin: 0; padding: 2rem 1rem; }
.container { max-width: 800px; margin: 0 auto; }
.header { margin-bottom: 2rem; display: flex; justify-content: space-between; align-items: center; }
.banner { padding: 1.25rem; border-radius: 8px; margin-bottom: 2rem; font-weight: 600; font-size: 1.1rem; }
.banner.operational { background: #065f46; color: #a7f3d0; border: 1px solid #059669; }
.banner.degraded { background: #854d0e; color: #fef08a; border: 1px solid #ca8a04; }
.banner.outage { background: #991b1b; color: #fecaca; border: 1px solid #dc2626; }
.stale-warning { background: #7c2d12; color: #fed7aa; padding: 1rem; border-radius: 8px; margin-bottom: 1.5rem; border: 1px solid #ea580c; font-weight: 500; }
.card { background: #1e293b; border-radius: 8px; padding: 1.5rem; margin-bottom: 1.5rem; border: 1px solid #334155; }
.comp-row { display: flex; justify-content: space-between; padding: 0.75rem 0; border-bottom: 1px solid #334155; }
.comp-row:last-child { border-bottom: none; }
.badge { padding: 0.25rem 0.6rem; border-radius: 9999px; font-size: 0.8rem; font-weight: 600; text-transform: uppercase; }
.badge.operational { background: #065f46; color: #a7f3d0; }
.badge.degraded { background: #854d0e; color: #fef08a; }
.badge.partial { background: #9a3412; color: #ffedd5; }
.badge.major { background: #991b1b; color: #fecaca; }
.badge.maintenance { background: #1e40af; color: #bfdbfe; }
.footer { margin-top: 3rem; text-align: center; color: #64748b; font-size: 0.875rem; }
`)
	buf.WriteString("</style>\n</head>\n<body>\n<div class=\"container\">\n")

	// Header
	buf.WriteString("<div class=\"header\"><h1>System Status</h1></div>\n")

	// Honest Degraded Mode: Staleness Warning
	if isStale {
		buf.WriteString(fmt.Sprintf(
			"<div class=\"stale-warning\">⚠️ Status information may be out of date. Last refreshed %s.</div>\n",
			html.EscapeString(s.GeneratedAt.UTC().Format("15:04:05 UTC")),
		))
	}

	// Overall Status Banner
	bannerClass := "operational"
	if s.OverallSeverity == maintenance.StatusMajorOutage || s.OverallSeverity == maintenance.StatusPartialOutage {
		bannerClass = "outage"
	} else if s.OverallSeverity == maintenance.StatusDegradedPerformance || s.OverallSeverity == maintenance.StatusUnderMaintenance {
		bannerClass = "degraded"
	}
	buf.WriteString(fmt.Sprintf("<div class=\"banner %s\">%s</div>\n", bannerClass, html.EscapeString(s.OverallStatus)))

	// Components Card
	buf.WriteString("<div class=\"card\"><h2>Services &amp; Components</h2>\n")
	for _, c := range s.Components {
		bClass := "operational"
		switch c.Status {
		case maintenance.StatusDegradedPerformance:
			bClass = "degraded"
		case maintenance.StatusPartialOutage:
			bClass = "partial"
		case maintenance.StatusMajorOutage:
			bClass = "major"
		case maintenance.StatusUnderMaintenance:
			bClass = "maintenance"
		}
		buf.WriteString(fmt.Sprintf(
			"<div class=\"comp-row\"><span>%s</span><span class=\"badge %s\">%s</span></div>\n",
			html.EscapeString(c.Name), bClass, html.EscapeString(string(c.Status)),
		))
	}
	buf.WriteString("</div>\n")

	// Active Incidents Card if any
	if len(s.ActiveIncidents) > 0 {
		buf.WriteString("<div class=\"card\"><h2>Active Incidents</h2>\n")
		for _, inc := range s.ActiveIncidents {
			buf.WriteString(fmt.Sprintf("<h3>%s (%s)</h3>\n", html.EscapeString(inc.Title), html.EscapeString(string(inc.Status))))
			for _, u := range inc.Updates {
				buf.WriteString(fmt.Sprintf(
					"<p><strong>%s</strong>: %s <small style=\"color:#94a3b8\">%s</small></p>\n",
					html.EscapeString(string(u.Status)),
					html.EscapeString(u.Message),
					html.EscapeString(u.PostedAt.UTC().Format("Jan 02, 15:04 UTC")),
				))
			}
		}
		buf.WriteString("</div>\n")
	}

	// Footer
	buf.WriteString(fmt.Sprintf(
		"<div class=\"footer\"><p>Generated at %s • Powered by Foghorn</p></div>\n",
		html.EscapeString(s.GeneratedAt.UTC().Format(time.RFC1123)),
	))
	buf.WriteString("</div>\n</body>\n</html>\n")

	return buf.String()
}

// BuildSnapshot gathers data and builds an immutable snapshot for a tenant.
func BuildSnapshot(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Snapshot, error) {
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
		return Snapshot{}, fmt.Errorf("snapshot: query components: %w", err)
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
			return Snapshot{}, fmt.Errorf("snapshot: scan comp: %w", err)
		}
		compMetas = append(compMetas, cm)
	}
	rows.Close()

	var compViews []ComponentView
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
		compViews = append(compViews, ComponentView{
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
	var activeIncidents []IncidentView
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
				var updates []UpdateView
				if err == nil {
					for uRows.Next() {
						var (
							uSt  string
							uMsg string
							uAt  time.Time
						)
						if err := uRows.Scan(&uSt, &uMsg, &uAt); err == nil {
							updates = append(updates, UpdateView{
								Status:   model.IncidentStatus(uSt),
								Message:  uMsg,
								PostedAt: uAt,
							})
						}
					}
					uRows.Close()
				}

				activeIncidents = append(activeIncidents, IncidentView{
					Title:     title,
					Status:    model.IncidentStatus(statusStr),
					StartedAt: startedAt,
					Updates:   updates,
				})
			}
		}
		incRows.Close()
	}

	overallText, overallSev := ComputeOverall(compViews, activeIncidents)

	snap := Snapshot{
		TenantID:            tenantID,
		GeneratedAt:         now,
		OverallStatus:       overallText,
		OverallSeverity:     overallSev,
		Components:          compViews,
		ActiveIncidents:     activeIncidents,
		UpcomingMaintenance: nil,
	}

	_, etag, _ := RenderJSON(&snap)
	snap.ETag = etag

	return snap, nil
}

// Publish atomically publishes a snapshot to object storage and updates pointers.
func Publish(
	ctx context.Context,
	pool *pgxpool.Pool,
	storage StorageClient,
	s Snapshot,
) error {
	jsonBytes, etag, err := RenderJSON(&s)
	if err != nil {
		return fmt.Errorf("snapshot: render json: %w", err)
	}

	htmlContent := RenderHTML(s, false)

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
