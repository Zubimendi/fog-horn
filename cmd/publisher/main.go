// Binary publisher regenerates and atomically uploads tenant status page snapshots.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/zubimendi/fog-horn/internal/config"
	"github.com/zubimendi/fog-horn/internal/db"
	"github.com/zubimendi/fog-horn/internal/metrics"
	"github.com/zubimendi/fog-horn/internal/publisher"
	"github.com/zubimendi/fog-horn/internal/snapshot"
)

func main() {
	cfg := config.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("FATAL: failed to connect to database: %v", err)
	}
	defer pool.Close()

	store := snapshot.NewHTTPStorageClient(cfg.S3Endpoint, cfg.S3Bucket, cfg.S3UseSSL)

	// Expose Prometheus metrics
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		_ = http.ListenAndServe(cfg.MetricsAddr, mux)
	}()

	log.Printf("Foghorn snapshot publisher started (periodic interval %v)", cfg.SnapshotPeriodicTTL)

	ticker := time.NewTicker(cfg.SnapshotPeriodicTTL)
	defer ticker.Stop()

	// Initial run
	publishAllTenants(ctx, pool, store)

	for {
		select {
		case <-ctx.Done():
			log.Println("Foghorn snapshot publisher stopped.")
			return
		case <-ticker.C:
			publishAllTenants(ctx, pool, store)
		}
	}
}

func publishAllTenants(ctx context.Context, pool *pgxpool.Pool, store snapshot.StorageClient) {
	rows, err := pool.Query(ctx, `SELECT id FROM tenants ORDER BY created_at ASC`)
	if err != nil {
		log.Printf("publisher: query tenants: %v", err)
		return
	}
	defer rows.Close()

	var tenantIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			tenantIDs = append(tenantIDs, id)
		}
	}
	rows.Close()

	for _, tid := range tenantIDs {
		snap, err := publisher.BuildSnapshot(ctx, pool, tid)
		if err != nil {
			log.Printf("publisher: build snapshot for %s: %v", tid, err)
			continue
		}

		if err := publisher.Publish(ctx, pool, store, snap); err != nil {
			log.Printf("publisher: publish snapshot for %s: %v", tid, err)
			continue
		}

		metrics.SnapshotAge.WithLabelValues(tid.String()).Set(0)
		log.Printf("Published snapshot for tenant %s (status: %s)", tid, snap.OverallStatus)
	}
}
