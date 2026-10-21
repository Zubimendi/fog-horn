// Binary evaluator runs the central state evaluation engine, sweeper, and alert outbox dispatcher.
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
	"github.com/zubimendi/fog-horn/internal/alerting"
	"github.com/zubimendi/fog-horn/internal/config"
	"github.com/zubimendi/fog-horn/internal/db"
	"github.com/zubimendi/fog-horn/internal/evaluator"
	"github.com/zubimendi/fog-horn/internal/metrics"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/sweeper"
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

	dispatcherClient := alerting.NewDispatcherClient(cfg.DispatcherURL, cfg.DispatcherAPIKey)

	// Expose Prometheus metrics
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		_ = http.ListenAndServe(cfg.MetricsAddr, mux)
	}()

	log.Println("Foghorn evaluator service started.")

	// Background Sweeper loop: marks stale targets UNKNOWN
	go func() {
		ticker := time.NewTicker(cfg.SweeperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				count, err := sweeper.SweepStaleTargets(ctx, pool)
				if err != nil {
					log.Printf("sweeper error: %v", err)
				} else if count > 0 {
					log.Printf("sweeper marked %d stale targets as UNKNOWN", count)
				}
			}
		}
	}()

	// Background Alert Outbox loop: delivers pending alerts to Dispatcher
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				delivered, err := alerting.ProcessOutbox(ctx, pool, dispatcherClient, 50)
				if err != nil {
					log.Printf("alert outbox error: %v", err)
				} else if delivered > 0 {
					log.Printf("alert outbox delivered %d events", delivered)
				}
			}
		}
	}()

	// Evaluation loop: polls recent un-evaluated check results
	evalTicker := time.NewTicker(500 * time.Millisecond)
	defer evalTicker.Stop()

	var lastCheckScheduledFor time.Time

	for {
		select {
		case <-ctx.Done():
			log.Println("Foghorn evaluator service stopped.")
			return
		case <-evalTicker.C:
			// Fetch check results executed since last cycle
			query := `
SELECT target_id, location_id, scheduled_for, executed_at, class, reason, status_code
FROM check_results
WHERE scheduled_for > $1
ORDER BY scheduled_for ASC
LIMIT 100;
`
			rows, err := pool.Query(ctx, query, lastCheckScheduledFor)
			if err != nil {
				continue
			}

			var batch []model.CheckResult
			for rows.Next() {
				var (
					r        model.CheckResult
					classStr string
				)
				if err := rows.Scan(&r.TargetID, &r.LocationID, &r.ScheduledFor, &r.ExecutedAt, &classStr, &r.Reason, &r.StatusCode); err == nil {
					r.Class = model.CheckClass(classStr)
					batch = append(batch, r)
					if r.ScheduledFor.After(lastCheckScheduledFor) {
						lastCheckScheduledFor = r.ScheduledFor
					}
				}
			}
			rows.Close()

			for _, r := range batch {
				if r.TargetID != uuid.Nil {
					_ = evaluator.ProcessResult(ctx, pool, r)
				}
			}
		}
	}
}
