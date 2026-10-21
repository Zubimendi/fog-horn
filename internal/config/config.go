// Package config loads and validates all environment-driven configuration.
// The process exits immediately on any missing required variable.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for Foghorn services.
type Config struct {
	// Database
	DatabaseURL string

	// Object storage (MinIO / S3-compatible)
	S3Endpoint        string
	S3AccessKey       string
	S3SecretKey       string
	S3Bucket          string
	S3UseSSL          bool

	// API server
	APIAddr string

	// Probe worker
	ProbeLocationID string
	ProbeRegion     string

	// Scheduler
	SchedulerBatchSize int
	SchedulerWorkers   int

	// Evaluator
	DefaultNFail    int
	DefaultNRecover int

	// Sweeper
	SweeperInterval time.Duration

	// Snapshot publisher
	SnapshotDebounce    time.Duration
	SnapshotPeriodicTTL time.Duration

	// Page host
	PageHostAddr          string
	PageHostStaleThreshold time.Duration

	// Alerting – Dispatcher integration
	DispatcherURL    string
	DispatcherAPIKey string

	// Dead-man emitter
	DeadManURL      string
	DeadManInterval time.Duration

	// Observability
	MetricsAddr string
}

// Load reads all configuration from environment variables.
// It exits immediately if a required variable is missing or invalid.
func Load() *Config {
	c := &Config{
		DatabaseURL: required("DATABASE_URL"),

		S3Endpoint:  getOrDefault("S3_ENDPOINT", "localhost:9000"),
		S3AccessKey:  required("S3_ACCESS_KEY"),
		S3SecretKey:  required("S3_SECRET_KEY"),
		S3Bucket:    getOrDefault("S3_BUCKET", "snapshots"),
		S3UseSSL:    boolOr("S3_USE_SSL", false),

		APIAddr: getOrDefault("API_ADDR", ":8080"),

		ProbeLocationID: getOrDefault("PROBE_LOCATION_ID", "local"),
		ProbeRegion:     getOrDefault("PROBE_REGION", "local"),

		SchedulerBatchSize: intOr("SCHEDULER_BATCH_SIZE", 50),
		SchedulerWorkers:   intOr("SCHEDULER_WORKERS", 8),

		DefaultNFail:    intOr("DEFAULT_N_FAIL", 3),
		DefaultNRecover: intOr("DEFAULT_N_RECOVER", 2),

		SweeperInterval: durationOr("SWEEPER_INTERVAL", 15*time.Second),

		SnapshotDebounce:    durationOr("SNAPSHOT_DEBOUNCE", 5*time.Second),
		SnapshotPeriodicTTL: durationOr("SNAPSHOT_PERIODIC_TTL", 30*time.Second),

		PageHostAddr:           getOrDefault("PAGE_HOST_ADDR", ":8081"),
		PageHostStaleThreshold: durationOr("PAGE_HOST_STALE_THRESHOLD", 2*time.Minute),

		DispatcherURL:    getOrDefault("DISPATCHER_URL", ""),
		DispatcherAPIKey: getOrDefault("DISPATCHER_API_KEY", ""),

		DeadManURL:      getOrDefault("DEAD_MAN_URL", ""),
		DeadManInterval: durationOr("DEAD_MAN_INTERVAL", 60*time.Second),

		MetricsAddr: getOrDefault("METRICS_ADDR", ":9090"),
	}
	return c
}

func required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "FATAL: required environment variable %q is not set\n", key)
		os.Exit(1)
	}
	return v
}

func getOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intOr(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %q must be an integer, got %q\n", key, s)
		os.Exit(1)
	}
	return v
}

func boolOr(key string, def bool) bool {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.ParseBool(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %q must be a boolean, got %q\n", key, s)
		os.Exit(1)
	}
	return v
}

func durationOr(key string, def time.Duration) time.Duration {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %q must be a duration (e.g. 30s), got %q\n", key, s)
		os.Exit(1)
	}
	return v
}
