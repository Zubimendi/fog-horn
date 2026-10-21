// Package tcpprober implements SSRF-safe TCP connect checks.
package tcpprober

import (
	"context"
	"fmt"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/prober/dialer"
)

// Config holds TCP check configuration.
type Config struct {
	Host    string
	Port    int
	Timeout time.Duration
}

// Result is the output of a single TCP check.
type Result struct {
	Class     model.CheckClass
	Reason    string
	ConnectMS float64
}

// Check performs a TCP connect check against the given host:port.
func Check(ctx context.Context, cfg Config) Result {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	start := time.Now()

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	connectMS := float64(time.Since(start)) / float64(time.Millisecond)

	if err != nil {
		var ssrfErr *dialer.ErrSSRFBlocked
		if isSSRFError(err, &ssrfErr) {
			return Result{Class: model.ClassConfigError, Reason: "SSRF: blocked address"}
		}
		return Result{
			Class:     model.ClassTargetFailure,
			Reason:    "connection failed",
			ConnectMS: connectMS,
		}
	}
	conn.Close()

	return Result{
		Class:     model.ClassOK,
		ConnectMS: connectMS,
	}
}

func isSSRFError(err error, target **dialer.ErrSSRFBlocked) bool {
	if err == nil {
		return false
	}
	var e *dialer.ErrSSRFBlocked
	if asErr(err, &e) {
		if target != nil {
			*target = e
		}
		return true
	}
	return false
}

func asErr(err error, target interface{}) bool {
	type asI interface {
		As(interface{}) bool
	}
	if a, ok := err.(asI); ok {
		return a.As(target)
	}
	return false
}
