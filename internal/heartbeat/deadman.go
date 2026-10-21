package heartbeat

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// DeadManEmitter periodically pings an external monitor URL (e.g. Healthchecks.io / Cronitor)
// to prove Foghorn itself is operational.
type DeadManEmitter struct {
	client   *http.Client
	url      string
	interval time.Duration
}

// NewDeadManEmitter creates a new DeadManEmitter.
func NewDeadManEmitter(url string, interval time.Duration) *DeadManEmitter {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	return &DeadManEmitter{
		client:   &http.Client{Timeout: 10 * time.Second},
		url:      url,
		interval: interval,
	}
}

// Start begins the periodic ping loop in a background goroutine.
func (e *DeadManEmitter) Start(ctx context.Context) {
	if e.url == "" {
		return
	}
	go e.run(ctx)
}

func (e *DeadManEmitter) run(ctx context.Context) {
	// Ping immediately on start
	_ = e.Ping(ctx)

	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = e.Ping(ctx)
		}
	}
}

// Ping performs a single external dead-man ping.
func (e *DeadManEmitter) Ping(ctx context.Context) error {
	if e.url == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.url, nil)
	if err != nil {
		return fmt.Errorf("deadman: request: %w", err)
	}

	req.Header.Set("User-Agent", "Foghorn-DeadMan/1.0")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("deadman: send ping: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("deadman: non-2xx status: %d", resp.StatusCode)
	}

	return nil
}
