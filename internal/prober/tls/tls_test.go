package tlsprober_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	tlsprober "github.com/zubimendi/fog-horn/internal/prober/tls"
)

func TestTLSProberSSRF(t *testing.T) {
	ctx := context.Background()

	res := tlsprober.Check(ctx, tlsprober.Config{
		Host:    "127.0.0.1",
		Port:    443,
		Timeout: 1 * time.Second,
	})

	if res.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for loopback TLS, got %v (%s)", res.Class, res.Reason)
	}

	resMeta := tlsprober.Check(ctx, tlsprober.Config{
		Host:    "169.254.169.254",
		Port:    443,
		Timeout: 1 * time.Second,
	})

	if resMeta.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for metadata TLS, got %v (%s)", resMeta.Class, resMeta.Reason)
	}
}
