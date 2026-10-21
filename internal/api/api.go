// Package api implements the HTTP API server for Foghorn, including target CRUD,
// heartbeat ingestion, incident lifecycle, maintenance management, and health checks.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/evaluator"
	"github.com/zubimendi/fog-horn/internal/heartbeat"
	"github.com/zubimendi/fog-horn/internal/incidents"
	"github.com/zubimendi/fog-horn/internal/maintenance"
	"github.com/zubimendi/fog-horn/internal/metrics"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/scheduler"
	"github.com/zubimendi/fog-horn/internal/writer"
)

// Server coordinates the API handlers.
type Server struct {
	pool   *pgxpool.Pool
	writer *writer.Writer
	mux    *http.ServeMux
}

// NewServer builds and configures a new Server.
func NewServer(pool *pgxpool.Pool, w *writer.Writer) *Server {
	s := &Server{
		pool:   pool,
		writer: w,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	// Probes and Observability
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	s.mux.Handle("GET /metrics", metrics.Handler())

	// Heartbeat / Dead-man Ping endpoints
	s.mux.HandleFunc("POST /ping/{token}", s.handlePing(false))
	s.mux.HandleFunc("GET /ping/{token}", s.handlePing(false))
	s.mux.HandleFunc("POST /ping/{token}/fail", s.handlePing(true))
	s.mux.HandleFunc("GET /ping/{token}/fail", s.handlePing(true))

	// Target management
	s.mux.HandleFunc("POST /v1/targets", s.handleCreateTarget)
	s.mux.HandleFunc("GET /v1/targets", s.handleListTargets)
	s.mux.HandleFunc("GET /v1/targets/{id}", s.handleGetTarget)
	s.mux.HandleFunc("POST /v1/targets/{id}/pause", s.handlePauseTarget(true))
	s.mux.HandleFunc("POST /v1/targets/{id}/resume", s.handlePauseTarget(false))

	// Incident management
	s.mux.HandleFunc("POST /v1/incidents", s.handleCreateIncident)
	s.mux.HandleFunc("POST /v1/incidents/{id}/updates", s.handleAddIncidentUpdate)

	// Maintenance management
	s.mux.HandleFunc("POST /v1/maintenance", s.handleCreateMaintenance)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready (mock)"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unreachable", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// Heartbeat ping handler
func (s *Server) handlePing(isFail bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.PathValue("token")
		if token == "" {
			http.Error(w, "missing token", http.StatusBadRequest)
			return
		}

		if s.pool == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "recorded (mock)"})
			return
		}

		ctx := r.Context()
		tokenHash := heartbeat.HashToken(token)

		// Find target matching token_hash or previous_token_hash within grace period
		var targetID uuid.UUID
		err := s.pool.QueryRow(ctx, `
SELECT target_id
FROM heartbeat_tokens
WHERE token_hash = $1
   OR (previous_token_hash = $1 AND previous_expires_at > now())
`, tokenHash).Scan(&targetID)

		if err != nil {
			http.Error(w, "invalid or expired token", http.StatusNotFound)
			return
		}

		now := time.Now()
		locID := uuid.Nil
		res := heartbeat.BuildPingResult(targetID, locID, isFail, now)

		// Persist result and evaluate state
		if s.writer != nil {
			s.writer.Enqueue(res)
		}
		_ = evaluator.ProcessResult(ctx, s.pool, res)

		writeJSON(w, http.StatusOK, map[string]any{
			"status":    "acknowledged",
			"target_id": targetID,
			"class":     res.Class,
		})
	}
}

// Create Target
func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID         uuid.UUID              `json:"tenant_id"`
		Name             string                 `json:"name"`
		Type             model.TargetType       `json:"type"`
		Config           map[string]any         `json:"config"`
		ExpectedStatus   int                    `json:"expected_status"`
		Assertion        map[string]any         `json:"assertion"`
		LatencyThreshold string                 `json:"latency_threshold"`
		NFail            int                    `json:"n_fail"`
		NRecover         int                    `json:"n_recover"`
		Quorum           float64                `json:"quorum"`
		Interval         string                 `json:"interval"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.TenantID == uuid.Nil || req.Name == "" || req.Type == "" {
		http.Error(w, "tenant_id, name, and type are required", http.StatusBadRequest)
		return
	}

	if req.NFail <= 0 {
		req.NFail = 3
	}
	if req.NRecover <= 0 {
		req.NRecover = 2
	}
	if req.Quorum <= 0 {
		req.Quorum = 0.5
	}
	if req.Interval == "" {
		req.Interval = "60s"
	}

	durInterval, err := time.ParseDuration(req.Interval)
	if err != nil {
		http.Error(w, "invalid interval duration", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": uuid.New(), "name": req.Name})
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)

	targetID := uuid.New()
	_, err = tx.Exec(ctx, `
INSERT INTO targets (
    id, tenant_id, name, type, config, expected_status, assertion,
    n_fail, n_recover, quorum, interval
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::interval)
`, targetID, req.TenantID, req.Name, req.Type, req.Config, req.ExpectedStatus, req.Assertion,
		req.NFail, req.NRecover, req.Quorum, req.Interval)
	if err != nil {
		http.Error(w, "failed to insert target: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Initialize schedule across active probe locations
	rows, err := tx.Query(ctx, `SELECT id FROM probe_locations WHERE healthy ORDER BY id ASC`)
	if err == nil {
		var locIDs []uuid.UUID
		for rows.Next() {
			var lid uuid.UUID
			if err := rows.Scan(&lid); err == nil {
				locIDs = append(locIDs, lid)
			}
		}
		rows.Close()

		now := time.Now()
		for i, lid := range locIDs {
			offset := scheduler.ComputePhaseOffset(targetID, i, len(locIDs), durInterval)
			nextRun := now.Add(offset)

			_, _ = tx.Exec(ctx, `
INSERT INTO target_schedule (target_id, location_id, interval, next_run_at)
VALUES ($1, $2, $3::interval, $4)
ON CONFLICT DO NOTHING
`, targetID, lid, req.Interval, nextRun)
		}
	}

	// If HEARTBEAT target, generate token
	var rawToken string
	if req.Type == model.TargetHeartbeat {
		rawToken, _ = heartbeat.GenerateRawToken()
		tokenHash := heartbeat.HashToken(rawToken)
		_, _ = tx.Exec(ctx, `
INSERT INTO heartbeat_tokens (target_id, token_hash)
VALUES ($1, $2)
`, targetID, tokenHash)
	}

	if err := tx.Commit(ctx); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"id":        targetID,
		"name":      req.Name,
		"type":      req.Type,
		"interval":  req.Interval,
	}
	if rawToken != "" {
		resp["heartbeat_ping_url"] = fmt.Sprintf("/ping/%s", rawToken)
		resp["heartbeat_token"] = rawToken
	}

	writeJSON(w, http.StatusCreated, resp)
}

// List targets
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	tenantIDStr := r.URL.Query().Get("tenant_id")
	if tenantIDStr == "" {
		http.Error(w, "tenant_id query param is required", http.StatusBadRequest)
		return
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		http.Error(w, "invalid tenant_id", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	rows, err := s.pool.Query(r.Context(), `
SELECT t.id, t.name, t.type, t.paused, COALESCE(ts.state, 'UNKNOWN'), t.created_at
FROM targets t
LEFT JOIN target_states ts ON ts.target_id = t.id
WHERE t.tenant_id = $1
ORDER BY t.created_at DESC
`, tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type targetItem struct {
		ID        uuid.UUID `json:"id"`
		Name      string    `json:"name"`
		Type      string    `json:"type"`
		Paused    bool      `json:"paused"`
		State     string    `json:"state"`
		CreatedAt time.Time `json:"created_at"`
	}

	var list []targetItem
	for rows.Next() {
		var item targetItem
		if err := rows.Scan(&item.ID, &item.Name, &item.Type, &item.Paused, &item.State, &item.CreatedAt); err == nil {
			list = append(list, item)
		}
	}

	writeJSON(w, http.StatusOK, list)
}

// Get target
func (s *Server) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	targetID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid target ID", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"id": targetID})
		return
	}

	var (
		name      string
		tType     string
		paused    bool
		state     string
		since     time.Time
		createdAt time.Time
	)
	err = s.pool.QueryRow(r.Context(), `
SELECT t.name, t.type, t.paused, COALESCE(ts.state, 'UNKNOWN'), COALESCE(ts.since, t.created_at), t.created_at
FROM targets t
LEFT JOIN target_states ts ON ts.target_id = t.id
WHERE t.id = $1
`, targetID).Scan(&name, &tType, &paused, &state, &since, &createdAt)
	if err != nil {
		http.Error(w, "target not found", http.StatusNotFound)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":         targetID,
		"name":       name,
		"type":       tType,
		"paused":     paused,
		"state":      state,
		"state_since": since,
		"created_at": createdAt,
	})
}

// Pause/Resume target
func (s *Server) handlePauseTarget(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := r.PathValue("id")
		targetID, err := uuid.Parse(idStr)
		if err != nil {
			http.Error(w, "invalid target ID", http.StatusBadRequest)
			return
		}

		if s.pool != nil {
			ctx := r.Context()
			_, _ = s.pool.Exec(ctx, `UPDATE targets SET paused = $1 WHERE id = $2`, paused, targetID)
			_, _ = s.pool.Exec(ctx, `UPDATE target_schedule SET paused = $1 WHERE target_id = $2`, paused, targetID)
			if paused {
				_, _ = s.pool.Exec(ctx, `UPDATE target_states SET state = 'PAUSED', since = now() WHERE target_id = $1`, targetID)
			}
		}

		writeJSON(w, http.StatusOK, map[string]any{"id": targetID, "paused": paused})
	}
}

// Create Incident
func (s *Server) handleCreateIncident(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID     uuid.UUID   `json:"tenant_id"`
		Title        string      `json:"title"`
		InitialMsg   string      `json:"initial_message"`
		StartedAt    time.Time   `json:"started_at"`
		PostedBy     string      `json:"posted_by"`
		ComponentIDs []uuid.UUID `json:"component_ids"`
		Impact       string      `json:"impact"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.TenantID == uuid.Nil || req.Title == "" {
		http.Error(w, "tenant_id and title required", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": uuid.New(), "title": req.Title})
		return
	}

	id, err := incidents.Create(r.Context(), s.pool, incidents.CreateIncidentParams{
		TenantID:     req.TenantID,
		Title:        req.Title,
		InitialMsg:   req.InitialMsg,
		StartedAt:    req.StartedAt,
		PostedBy:     req.PostedBy,
		ComponentIDs: req.ComponentIDs,
		Impact:       req.Impact,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "title": req.Title})
}

// Add Incident Update
func (s *Server) handleAddIncidentUpdate(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	incidentID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid incident ID", http.StatusBadRequest)
		return
	}

	var req struct {
		Status        model.IncidentStatus `json:"status"`
		Message       string               `json:"message"`
		PostedBy      string               `json:"posted_by"`
		RetractReason string               `json:"retract_reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "updated (mock)"})
		return
	}

	err = incidents.AddUpdate(r.Context(), s.pool, incidentID, req.Status, req.Message, req.PostedBy, req.RetractReason)
	if err != nil {
		if strings.Contains(err.Error(), "invalid status transition") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// Create Maintenance
func (s *Server) handleCreateMaintenance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TenantID     uuid.UUID   `json:"tenant_id"`
		Title        string      `json:"title"`
		StartsAt     time.Time   `json:"starts_at"`
		EndsAt       time.Time   `json:"ends_at"`
		TZ           string      `json:"tz"`
		RRULE        string      `json:"rrule"`
		Retroactive  bool        `json:"retroactive"`
		Reason       string      `json:"reason"`
		CreatedBy    string      `json:"created_by"`
		ComponentIDs []uuid.UUID `json:"component_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if s.pool == nil {
		writeJSON(w, http.StatusCreated, map[string]any{"id": uuid.New(), "title": req.Title})
		return
	}

	id, err := maintenance.Create(r.Context(), s.pool, maintenance.CreateMaintenanceParams{
		TenantID:     req.TenantID,
		Title:        req.Title,
		StartsAt:     req.StartsAt,
		EndsAt:       req.EndsAt,
		TZ:           req.TZ,
		RRULE:        req.RRULE,
		Retroactive:  req.Retroactive,
		Reason:       req.Reason,
		CreatedBy:    req.CreatedBy,
		ComponentIDs: req.ComponentIDs,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "title": req.Title})
}

func writeJSON(w http.ResponseWriter, code int, val any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(val)
}
