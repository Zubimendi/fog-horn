// Package dialer provides an SSRF-guarded net.Dialer.
// The guard runs at connect time via net.Dialer.Control, after DNS resolution,
// validating the IP actually dialed — not the hostname.
package dialer

import (
	"context"
	"fmt"
	"net"
	"syscall"

	"github.com/zubimendi/fog-horn/internal/ssrf"
)

// ErrSSRFBlocked is returned when the resolved IP is in a blocked range.
type ErrSSRFBlocked struct {
	Addr string
}

func (e *ErrSSRFBlocked) Error() string {
	return fmt.Sprintf("ssrf: dialing %q is blocked (private/reserved address)", e.Addr)
}

// New returns a net.Dialer whose Control func validates the dialed IP against
// the SSRF deny list. Fresh connections are enforced by the caller disabling
// keep-alives on the HTTP transport.
func New() *net.Dialer {
	return &net.Dialer{
		Control: func(network, addr string, c syscall.RawConn) error {
			if ssrf.ClassifyAddr(network, addr) == ssrf.Blocked {
				return &ErrSSRFBlocked{Addr: addr}
			}
			return nil
		},
	}
}

// DialContext is a convenience wrapper that dials using the guarded dialer.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return New().DialContext(ctx, network, addr)
}
