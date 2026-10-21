package dnsprober_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	dnsprober "github.com/zubimendi/fog-horn/internal/prober/dns"
)

func TestDNSProberUnsupportedType(t *testing.T) {
	ctx := context.Background()

	res := dnsprober.Check(ctx, dnsprober.Config{
		Hostname:   "example.com",
		RecordType: "INVALID",
		Timeout:    1 * time.Second,
	})

	if res.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for invalid record type, got %v (%s)", res.Class, res.Reason)
	}
}

func TestDNSProberLookup(t *testing.T) {
	ctx := context.Background()

	res := dnsprober.Check(ctx, dnsprober.Config{
		Hostname:   "localhost",
		RecordType: dnsprober.TypeA,
		Timeout:    2 * time.Second,
	})

	// Localhost should resolve or at least return a result
	if res.Class != model.ClassOK && res.Class != model.ClassTargetFailure {
		t.Fatalf("unexpected result class: %v", res.Class)
	}
}
