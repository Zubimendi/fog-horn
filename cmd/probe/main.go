// Binary probe runs an autonomous Foghorn probe agent for a specific location.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/config"
	"github.com/zubimendi/fog-horn/internal/db"
	"github.com/zubimendi/fog-horn/internal/metrics"
	"github.com/zubimendi/fog-horn/internal/model"
	dnsprober "github.com/zubimendi/fog-horn/internal/prober/dns"
	httpprober "github.com/zubimendi/fog-horn/internal/prober/http"
	"github.com/zubimendi/fog-horn/internal/prober/selftest"
	tcpprober "github.com/zubimendi/fog-horn/internal/prober/tcp"
	tlsprober "github.com/zubimendi/fog-horn/internal/prober/tls"
	"github.com/zubimendi/fog-horn/internal/scheduler"
	"github.com/zubimendi/fog-horn/internal/writer"
)

func main() {
	cfg := config.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	locationID, err := uuid.Parse(cfg.ProbeLocationID)
	if err != nil {
		// Use deterministic fallback UUID for local testing
		locationID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	}

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("FATAL: failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Ensure probe location row exists
	_, _ = pool.Exec(ctx, `
INSERT INTO probe_locations (id, region, healthy, last_control_ok_at)
VALUES ($1, $2, true, now())
ON CONFLICT (id) DO UPDATE SET last_control_ok_at = now()
`, locationID, cfg.ProbeRegion)

	// Self-test runner
	selfTestRunner := selftest.New(selftest.Config{Timeout: 5 * time.Second})

	// Result writer
	resWriter := writer.New(pool, writer.Config{
		BatchSize:     cfg.SchedulerBatchSize,
		FlushInterval: 500 * time.Millisecond,
	})
	resWriter.Start(ctx)
	defer resWriter.Stop()

	// Expose Prometheus metrics
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		_ = http.ListenAndServe(cfg.MetricsAddr, mux)
	}()

	log.Printf("Foghorn probe worker started for location %s (%s)", locationID, cfg.ProbeRegion)

	// Background self-test canary loop
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				st := selfTestRunner.RunOnce(ctx)
				_, _ = pool.Exec(ctx, `
UPDATE probe_locations
SET healthy = $1, last_control_ok_at = CASE WHEN $1 THEN now() ELSE last_control_ok_at END
WHERE id = $2
`, st.Healthy, locationID)
				if !st.Healthy {
					log.Printf("WARNING: probe self-test failed: %s", st.Reason)
					metrics.ProbeUnhealthyCount.Set(1)
				} else {
					metrics.ProbeUnhealthyCount.Set(0)
				}
			}
		}
	}()

	// Worker claim pool
	workers := cfg.SchedulerWorkers
	if workers <= 0 {
		workers = 4
	}

	workCh := make(chan scheduler.ClaimedTarget, cfg.SchedulerBatchSize*2)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ct, ok := <-workCh:
					if !ok {
						return
					}
					execCheck(ctx, ct, locationID, selfTestRunner, resWriter)
				}
			}
		}(i)
	}

	// Claim loop
	claimTicker := time.NewTicker(250 * time.Millisecond)
	defer claimTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			close(workCh)
			wg.Wait()
			log.Println("Foghorn probe worker stopped.")
			return
		case <-claimTicker.C:
			claimed, err := scheduler.Claim(ctx, pool, locationID, cfg.SchedulerBatchSize)
			if err != nil {
				log.Printf("scheduler claim error: %v", err)
				continue
			}
			for _, ct := range claimed {
				workCh <- ct
			}
		}
	}
}

func execCheck(
	ctx context.Context,
	ct scheduler.ClaimedTarget,
	locationID uuid.UUID,
	selfTest *selftest.Runner,
	resWriter *writer.Writer,
) {
	now := time.Now()
	lag := scheduler.ComputeLag(ct.ScheduledFor, now)
	metrics.SchedulerLag.WithLabelValues(locationID.String()).Observe(lag.Seconds())

	// Invariant: Probe errors are never target downtime
	// If self-test fails, mark result PROBE_ERROR
	if !selfTest.IsHealthy() {
		res := model.CheckResult{
			TargetID:     ct.TargetID,
			LocationID:   locationID,
			ScheduledFor: ct.ScheduledFor,
			ExecutedAt:   now,
			Class:        model.ClassProbeError,
			Reason:       "probe self-test unhealthy: refusing to report target failure",
		}
		resWriter.Enqueue(res)
		metrics.ProbeChecksTotal.WithLabelValues(locationID.String(), string(res.Class)).Inc()
		return
	}

	var res model.CheckResult
	res.TargetID = ct.TargetID
	res.LocationID = locationID
	res.ScheduledFor = ct.ScheduledFor

	switch ct.Type {
	case model.TargetHTTP:
		rawURL, _ := ct.Config["url"].(string)
		regex, _ := ct.Config["regex"].(string)
		httpRes := httpprober.Check(ctx, httpprober.Config{
			URL:              rawURL,
			ExpectedStatus:   ct.ExpectedStatus,
			BodyAssertRegex:  regex,
			LatencyThreshold: ct.LatencyThreshold,
			Timeout:          10 * time.Second,
		})
		res.ExecutedAt = time.Now()
		res.Class = httpRes.Class
		res.Reason = httpRes.Reason
		res.StatusCode = httpRes.StatusCode
		res.DNSMS = httpRes.Timings.DNSMS
		res.ConnectMS = httpRes.Timings.ConnectMS
		res.TLSMS = httpRes.Timings.TLSMS
		res.TTFBMS = httpRes.Timings.TTFBMS
		res.TotalMS = httpRes.Timings.TotalMS

	case model.TargetTCP:
		host, _ := ct.Config["host"].(string)
		port, _ := ct.Config["port"].(float64)
		tcpRes := tcpprober.Check(ctx, tcpprober.Config{
			Host:    host,
			Port:    int(port),
			Timeout: 5 * time.Second,
		})
		res.ExecutedAt = time.Now()
		res.Class = tcpRes.Class
		res.Reason = tcpRes.Reason
		res.TotalMS = tcpRes.ConnectMS

	case model.TargetDNS:
		host, _ := ct.Config["host"].(string)
		qType, _ := ct.Config["query_type"].(string)
		expectedVal, _ := ct.Config["expected_value"].(string)
		dnsRes := dnsprober.Check(ctx, dnsprober.Config{
			Hostname:    host,
			RecordType:  dnsprober.RecordType(qType),
			AssertValue: expectedVal,
			Timeout:     5 * time.Second,
		})
		res.ExecutedAt = time.Now()
		res.Class = dnsRes.Class
		res.Reason = dnsRes.Reason
		res.DNSMS = dnsRes.DNSMS
		res.TotalMS = dnsRes.DNSMS

	case model.TargetTLS:
		host, _ := ct.Config["host"].(string)
		port, _ := ct.Config["port"].(float64)
		tlsRes := tlsprober.Check(ctx, tlsprober.Config{
			Host:           host,
			Port:           int(port),
			WarnWithinDays: 30,
			Timeout:        5 * time.Second,
		})
		res.ExecutedAt = time.Now()
		res.Class = tlsRes.Class
		res.Reason = tlsRes.Reason
		res.TLSMS = tlsRes.TLSMS
		res.TotalMS = tlsRes.TLSMS

	default:
		res.ExecutedAt = time.Now()
		res.Class = model.ClassConfigError
		res.Reason = "unsupported target type"
	}

	metrics.ProbeChecksTotal.WithLabelValues(locationID.String(), string(res.Class)).Inc()
	resWriter.Enqueue(res)
}
