// Package selftest executes canary checks for probe workers before running target checks.
// If control checks fail, the probe marks itself UNHEALTHY so that its results
// do not cause false target outages (in accordance with invariant: probe errors are never target downtime).
package selftest

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

// Status holds the current health status of a probe worker.
type Status struct {
	Healthy   bool
	LastCheck time.Time
	Reason    string
}

// Runner manages periodic canary checks for a probe location.
type Runner struct {
	mu        sync.RWMutex
	status    Status
	resolver  *net.Resolver
	canaryURL string
	timeout   time.Duration
}

// Config configures the probe self-test runner.
type Config struct {
	CanaryURL string        // optional external canary URL or host
	Timeout   time.Duration // per-check timeout
}

// New creates a new Runner with healthy initial status.
func New(cfg Config) *Runner {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &Runner{
		status: Status{
			Healthy:   true,
			LastCheck: time.Now(),
		},
		resolver:  net.DefaultResolver,
		canaryURL: cfg.CanaryURL,
		timeout:   timeout,
	}
}

// Status returns a copy of the current status.
func (r *Runner) Status() Status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

// IsHealthy returns true if the probe is currently healthy.
func (r *Runner) IsHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status.Healthy
}

// RunOnce performs the canary check.
func (r *Runner) RunOnce(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Canary test 1: Local network interface sanity / DNS resolution
	// We attempt to resolve a well-known reliable domain (e.g., example.com or one.one.one.one)
	_, err := r.resolver.LookupHost(ctx, "one.one.one.one")
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.status.LastCheck = now

	if err != nil {
		r.status.Healthy = false
		r.status.Reason = fmt.Sprintf("canary DNS lookup failed: %v", err)
		return r.status
	}

	r.status.Healthy = true
	r.status.Reason = "OK"
	return r.status
}

// SetHealthy allows explicitly setting health status (e.g. for testing or override).
func (r *Runner) SetHealthy(healthy bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.Healthy = healthy
	r.status.Reason = reason
	r.status.LastCheck = time.Now()
}
