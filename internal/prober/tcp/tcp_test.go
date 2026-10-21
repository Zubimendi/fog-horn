package tcpprober_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	tcpprober "github.com/zubimendi/fog-horn/internal/prober/tcp"
)

func TestTCPProberSSRF(t *testing.T) {
	ctx := context.Background()

	res := tcpprober.Check(ctx, tcpprober.Config{
		Host:    "127.0.0.1",
		Port:    5432,
		Timeout: 1 * time.Second,
	})

	if res.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for loopback TCP, got %v (%s)", res.Class, res.Reason)
	}

	resMeta := tcpprober.Check(ctx, tcpprober.Config{
		Host:    "169.254.169.254",
		Port:    80,
		Timeout: 1 * time.Second,
	})

	if resMeta.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for metadata TCP, got %v (%s)", resMeta.Class, resMeta.Reason)
	}
}
