package snapshot_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/maintenance"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/snapshot"
)

func TestRenderSnapshot(t *testing.T) {
	snap := snapshot.Snapshot{
		TenantID:        uuid.New(),
		GeneratedAt:     time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		OverallStatus:   "All Systems Operational",
		OverallSeverity: maintenance.StatusOperational,
		Components: []snapshot.ComponentView{
			{Name: "API Gateway", Status: maintenance.StatusOperational},
			{Name: "Database Cluster", Status: maintenance.StatusDegradedPerformance},
		},
		ActiveIncidents: []snapshot.IncidentView{
			{
				Title:  "Latency degradation",
				Status: model.IncidentInvestigating,
				Updates: []snapshot.UpdateView{
					{
						Status:   model.IncidentInvestigating,
						Message:  "Investigating database connectivity.",
						PostedAt: time.Date(2026, 1, 1, 11, 55, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	// 1. JSON rendering and ETag
	jsonBytes, etag, err := snapshot.RenderJSON(&snap)
	if err != nil {
		t.Fatalf("failed to render JSON: %v", err)
	}
	if len(jsonBytes) == 0 || etag == "" {
		t.Fatalf("empty JSON or ETag")
	}

	// 2. HTML rendering
	html := snapshot.RenderHTML(snap, false)
	if !strings.Contains(html, "API Gateway") {
		t.Errorf("HTML missing component name")
	}
	if !strings.Contains(html, "Latency degradation") {
		t.Errorf("HTML missing incident title")
	}
	if strings.Contains(html, "Status information may be out of date") {
		t.Errorf("fresh snapshot should not have staleness warning")
	}

	// 3. Stale HTML rendering
	staleHTML := snapshot.RenderHTML(snap, true)
	if !strings.Contains(staleHTML, "Status information may be out of date") {
		t.Errorf("stale snapshot must display staleness warning banner")
	}
}
