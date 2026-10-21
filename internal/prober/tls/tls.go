// Package tlsprober implements TLS certificate expiry checks.
package tlsprober

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/prober/dialer"
)

// Config holds the TLS check configuration.
type Config struct {
	Host                 string
	Port                 int
	WarnWithinDays       int // issue CERT_EXPIRING alert if cert expires within this many days
	Timeout              time.Duration
}

// Result is the output of a single TLS check.
type Result struct {
	Class      model.CheckClass
	Reason     string
	TLSMS      float64
	DaysUntilExpiry int
	CertExpiring    bool // true if within WarnWithinDays
}

// Check dials host:port, performs a TLS handshake, and checks certificate validity.
func Check(ctx context.Context, cfg Config) Result {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 15 * time.Second
	}
	warnDays := cfg.WarnWithinDays
	if warnDays == 0 {
		warnDays = 30
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	guardedDialer := dialer.New()

	start := time.Now()
	rawConn, err := guardedDialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		var ssrfErr *dialer.ErrSSRFBlocked
		if isSsrf(err, ssrfErr) {
			return Result{Class: model.ClassConfigError, Reason: "SSRF: blocked address"}
		}
		return Result{Class: model.ClassTargetFailure, Reason: "connection failed"}
	}
	defer rawConn.Close()

	tlsConn := tls.Client(rawConn.(*net.TCPConn), &tls.Config{
		ServerName: cfg.Host,
	})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return Result{Class: model.ClassTargetFailure, Reason: "TLS handshake failed: cert error"}
	}
	tlsMS := float64(time.Since(start)) / float64(time.Millisecond)
	defer tlsConn.Close()

	// Check certificate expiry
	certs := tlsConn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return Result{Class: model.ClassTargetFailure, Reason: "no peer certificates"}
	}

	leaf := certs[0]
	now := time.Now()
	if now.After(leaf.NotAfter) {
		return Result{
			Class:  model.ClassTargetFailure,
			Reason: "certificate expired",
			TLSMS:  tlsMS,
		}
	}

	daysLeft := int(leaf.NotAfter.Sub(now).Hours() / 24)
	expiring := daysLeft <= warnDays

	return Result{
		Class:           model.ClassOK,
		TLSMS:           tlsMS,
		DaysUntilExpiry: daysLeft,
		CertExpiring:    expiring,
	}
}

func isSsrf(err error, target *dialer.ErrSSRFBlocked) bool {
	if err == nil {
		return false
	}
	_, ok := err.(*dialer.ErrSSRFBlocked)
	return ok
}
