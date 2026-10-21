// Package alerting provides the transactional outbox reader, Dispatcher
// HTTP integration, deterministic dedupe keys, and alert suppression.
package alerting

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
)

// DedupeKey generates a deterministic deduplication and idempotency key.
func DedupeKey(targetID uuid.UUID, kind model.AlertKind, onset time.Time) string {
	return fmt.Sprintf("%s:%s:%s", targetID, kind, onset.UTC().Format(time.RFC3339))
}

// OutboxItem represents a pending alert event from the outbox table.
type OutboxItem struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Kind      model.AlertKind
	DedupeKey string
	Payload   map[string]any
}

// DispatcherClient sends outbound alert notifications.
type DispatcherClient struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewDispatcherClient creates a new Dispatcher client.
func NewDispatcherClient(baseURL, apiKey string) *DispatcherClient {
	return &DispatcherClient{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    baseURL,
		apiKey:     apiKey,
	}
}

// Send delivers an alert payload to Dispatcher with idempotency key.
func (c *DispatcherClient) Send(ctx context.Context, item OutboxItem) error {
	if c.baseURL == "" {
		// Mock / dry-run mode when Dispatcher URL is not configured
		return nil
	}

	body, err := json.Marshal(item.Payload)
	if err != nil {
		return fmt.Errorf("alerting: marshal payload: %w", err)
	}

	url := fmt.Sprintf("%s/v1/alerts", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alerting: new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", item.DedupeKey)
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("alerting: send post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("alerting: dispatcher returned status %d", resp.StatusCode)
	}

	return nil
}

// ProcessOutbox claims pending alert events and delivers them via the DispatcherClient.
func ProcessOutbox(ctx context.Context, pool *pgxpool.Pool, client *DispatcherClient, limit int) (int, error) {
	if limit <= 0 {
		limit = 50
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("alerting: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	query := `
SELECT id, tenant_id, kind, dedupe_key, payload
FROM alert_events
WHERE state = 'PENDING'
ORDER BY created_at ASC
LIMIT $1
FOR UPDATE SKIP LOCKED;
`
	rows, err := tx.Query(ctx, query, limit)
	if err != nil {
		return 0, fmt.Errorf("alerting: query pending: %w", err)
	}
	defer rows.Close()

	var items []OutboxItem
	for rows.Next() {
		var item OutboxItem
		var kindStr string
		if err := rows.Scan(&item.ID, &item.TenantID, &kindStr, &item.DedupeKey, &item.Payload); err != nil {
			return 0, fmt.Errorf("alerting: scan item: %w", err)
		}
		item.Kind = model.AlertKind(kindStr)
		items = append(items, item)
	}
	rows.Close()

	deliveredCount := 0
	for _, item := range items {
		// Deliver via Dispatcher
		if err := client.Send(ctx, item); err != nil {
			// On transient delivery failure, leave PENDING for retry
			continue
		}

		// Mark DELIVERED and set escalation deadline
		_, err = tx.Exec(ctx, `
UPDATE alert_events
SET state = 'DELIVERED', delivered_at = now()
WHERE id = $1
`, item.ID)
		if err != nil {
			return deliveredCount, fmt.Errorf("alerting: mark delivered: %w", err)
		}

		_, err = tx.Exec(ctx, `
INSERT INTO alert_state (alert_id, escalation_due_at)
VALUES ($1, now() + interval '30 minutes')
ON CONFLICT (alert_id) DO NOTHING
`, item.ID)
		if err != nil {
			return deliveredCount, fmt.Errorf("alerting: insert alert_state: %w", err)
		}

		deliveredCount++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("alerting: commit tx: %w", err)
	}

	return deliveredCount, nil
}
