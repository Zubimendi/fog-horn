// Package metrics registers and exports Prometheus metrics for Foghorn's self-SLIs.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// SchedulerLag measures (executed_at - scheduled_for).
	SchedulerLag = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "foghorn",
			Subsystem: "scheduler",
			Name:      "lag_seconds",
			Help:      "Scheduler lag between scheduled time and execution time.",
			Buckets:   []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"location_id"},
	)

	// SnapshotAge measures time since the current snapshot was generated.
	SnapshotAge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "foghorn",
			Subsystem: "snapshot",
			Name:      "age_seconds",
			Help:      "Age in seconds of the current published snapshot per tenant.",
		},
		[]string{"tenant_id"},
	)

	// OutboxPending tracks pending alert notifications waiting for delivery.
	OutboxPending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "foghorn",
			Subsystem: "alerting",
			Name:      "outbox_pending",
			Help:      "Current count of undelivered alert events in the outbox.",
		},
	)

	// ProbeUnhealthyCount tracks how many probe locations currently fail self-test.
	ProbeUnhealthyCount = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "foghorn",
			Subsystem: "probe",
			Name:      "unhealthy_locations",
			Help:      "Number of probe locations currently marked UNHEALTHY.",
		},
	)

	// ProbeChecksTotal counts total checks executed by class.
	ProbeChecksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "foghorn",
			Subsystem: "probe",
			Name:      "checks_total",
			Help:      "Total number of probe checks executed.",
		},
		[]string{"location_id", "class"},
	)
)

func init() {
	prometheus.MustRegister(
		SchedulerLag,
		SnapshotAge,
		OutboxPending,
		ProbeUnhealthyCount,
		ProbeChecksTotal,
	)
}

// Handler returns the Prometheus HTTP metrics handler.
func Handler() http.Handler {
	return promhttp.Handler()
}
