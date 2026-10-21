// Package writer provides batched, idempotent persistence of check results
// into PostgreSQL using ON CONFLICT DO NOTHING.
package writer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/model"
)

// Config configures the batched result writer.
type Config struct {
	BatchSize     int
	FlushInterval time.Duration
}

// Writer buffers and periodically writes CheckResults to the database in batches.
type Writer struct {
	pool          *pgxpool.Pool
	batchSize     int
	flushInterval time.Duration

	mu      sync.Mutex
	buffer  []model.CheckResult
	done    chan struct{}
	flushCh chan chan struct{}
}

// New creates a new Writer.
func New(pool *pgxpool.Pool, cfg Config) *Writer {
	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	flushInterval := cfg.FlushInterval
	if flushInterval <= 0 {
		flushInterval = 500 * time.Millisecond
	}

	w := &Writer{
		pool:          pool,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		buffer:        make([]model.CheckResult, 0, batchSize),
		done:          make(chan struct{}),
		flushCh:       make(chan chan struct{}, 1),
	}
	return w
}

// Start launches the background flusher goroutine.
func (w *Writer) Start(ctx context.Context) {
	go w.run(ctx)
}

// Stop stops the background flusher and flushes remaining results.
func (w *Writer) Stop() {
	close(w.done)
}

// Enqueue adds a check result to the buffer.
func (w *Writer) Enqueue(res model.CheckResult) {
	w.mu.Lock()
	w.buffer = append(w.buffer, res)
	shouldFlush := len(w.buffer) >= w.batchSize
	w.mu.Unlock()

	if shouldFlush {
		select {
		case w.flushCh <- nil:
		default:
		}
	}
}

func (w *Writer) run(ctx context.Context) {
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			_ = w.flush(ctx)
			return
		case <-ctx.Done():
			_ = w.flush(context.Background())
			return
		case ack := <-w.flushCh:
			_ = w.flush(ctx)
			if ack != nil {
				close(ack)
			}
		case <-ticker.C:
			_ = w.flush(ctx)
		}
	}
}

// Flush manually triggers a flush and waits for it to complete.
func (w *Writer) Flush(ctx context.Context) error {
	ack := make(chan struct{})
	select {
	case w.flushCh <- ack:
		select {
		case <-ack:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Writer) flush(ctx context.Context) error {
	w.mu.Lock()
	if len(w.buffer) == 0 {
		w.mu.Unlock()
		return nil
	}
	items := w.buffer
	w.buffer = make([]model.CheckResult, 0, w.batchSize)
	w.mu.Unlock()

	return WriteBatch(ctx, w.pool, items)
}

// WriteBatch performs an idempotent batch insert of check results.
func WriteBatch(ctx context.Context, pool *pgxpool.Pool, results []model.CheckResult) error {
	if pool == nil || len(results) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	query := `
INSERT INTO check_results (
    target_id, location_id, scheduled_for, executed_at,
    class, reason, status_code,
    dns_ms, connect_ms, tls_ms, ttfb_ms, total_ms
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (target_id, location_id, scheduled_for) DO NOTHING;
`

	for _, r := range results {
		batch.Queue(
			query,
			r.TargetID,
			r.LocationID,
			r.ScheduledFor,
			r.ExecutedAt,
			r.Class,
			r.Reason,
			r.StatusCode,
			r.DNSMS,
			r.ConnectMS,
			r.TLSMS,
			r.TTFBMS,
			r.TotalMS,
		)
	}

	br := pool.SendBatch(ctx, batch)
	defer br.Close()

	for range results {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("writer: exec batch row: %w", err)
		}
	}

	return nil
}
