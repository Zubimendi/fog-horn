package selftest_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/prober/selftest"
)

func TestRunnerInitialState(t *testing.T) {
	runner := selftest.New(selftest.Config{Timeout: 1 * time.Second})
	if !runner.IsHealthy() {
		t.Fatalf("expected runner to be initially healthy")
	}

	runner.SetHealthy(false, "simulated outage")
	if runner.IsHealthy() {
		t.Fatalf("expected runner to be unhealthy after override")
	}
	if runner.Status().Reason != "simulated outage" {
		t.Fatalf("unexpected reason: %s", runner.Status().Reason)
	}
}

func TestRunnerRunOnce(t *testing.T) {
	runner := selftest.New(selftest.Config{Timeout: 2 * time.Second})
	ctx := context.Background()
	status := runner.RunOnce(ctx)
	// Even if network lookup succeeds or fails in test sandbox, status fields are populated
	if status.LastCheck.IsZero() {
		t.Errorf("LastCheck was not updated")
	}
}
