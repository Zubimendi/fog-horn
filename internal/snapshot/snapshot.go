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
	Name        string                `json:"name"`
	GroupName   string                `json:"group_name,omitempty"`
	Status      model.ComponentStatus `json:"status"`
	Description string                `json:"description,omitempty"`
}

// IncidentView represents an ongoing or recent incident for public display.
type IncidentView struct {
	Title     string               `json:"title"`
	Status    model.IncidentStatus `json:"status"`
	StartedAt time.Time            `json:"started_at"`
	Updates   []UpdateView         `json:"updates"`
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
	TenantID            uuid.UUID           `json:"tenant_id"`
	Version             int64               `json:"version"`
	GeneratedAt         time.Time           `json:"generated_at"`
	OverallStatus       string              `json:"overall_status"`
	OverallSeverity     model.ComponentStatus `json:"overall_severity"`
	Components          []ComponentView     `json:"components"`
	ActiveIncidents     []IncidentView      `json:"active_incidents"`
	UpcomingMaintenance []MaintenanceView   `json:"upcoming_maintenance"`
	ETag                string              `json:"etag"`
}

// ComputeOverall computes the summary status across all public components.
func ComputeOverall(comps []ComponentView, activeIncidents []IncidentView) (string, model.ComponentStatus) {
	if len(activeIncidents) > 0 {
		return "Active Incidents Reported", model.StatusMajorOutage
	}

	highest := model.StatusOperational
	for _, c := range comps {
		highest = model.MaxSeverity(highest, c.Status)
	}

	switch highest {
	case model.StatusMajorOutage:
		return "Major Service Outage", highest
	case model.StatusPartialOutage:
		return "Partial System Outage", highest
	case model.StatusDegradedPerformance:
		return "Degraded Performance", highest
	case model.StatusUnderMaintenance:
		return "Scheduled Maintenance in Progress", highest
	default:
		return "All Systems Operational", model.StatusOperational
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
	if s.OverallSeverity == model.StatusMajorOutage || s.OverallSeverity == model.StatusPartialOutage {
		bannerClass = "outage"
	} else if s.OverallSeverity == model.StatusDegradedPerformance || s.OverallSeverity == model.StatusUnderMaintenance {
		bannerClass = "degraded"
	}
	buf.WriteString(fmt.Sprintf("<div class=\"banner %s\">%s</div>\n", bannerClass, html.EscapeString(s.OverallStatus)))

	// Components Card
	buf.WriteString("<div class=\"card\"><h2>Services &amp; Components</h2>\n")
	for _, c := range s.Components {
		bClass := "operational"
		switch c.Status {
		case model.StatusDegradedPerformance:
			bClass = "degraded"
		case model.StatusPartialOutage:
			bClass = "partial"
		case model.StatusMajorOutage:
			bClass = "major"
		case model.StatusUnderMaintenance:
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

