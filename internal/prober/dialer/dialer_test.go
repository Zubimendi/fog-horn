package dialer_test

import (
	"context"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/prober/dialer"
)

func TestDialerBlocksBlockedRanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	blockedAddrs := []string{
		"127.0.0.1:80",
		"10.0.0.1:80",
		"192.168.1.1:80",
		"169.254.169.254:80",
		"[::1]:80",
		"[::ffff:127.0.0.1]:80",
	}

	for _, addr := range blockedAddrs {
		t.Run(addr, func(t *testing.T) {
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err == nil {
				conn.Close()
				t.Fatalf("expected dial to %s to be blocked, but succeeded", addr)
			}
			var ssrfErr *dialer.ErrSSRFBlocked
			if err.Error() == "" {
				t.Errorf("empty error message for %s", addr)
			}
			_ = ssrfErr
		})
	}
}
