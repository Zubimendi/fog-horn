// Package httpprober implements SSRF-safe HTTP checks with httptrace timings.
package httpprober

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strings"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/prober/dialer"
)

const (
	// maxBodyBytes is the maximum body read for assertion checks.
	maxBodyBytes = 1 << 17 // 128 KiB
	// maxRedirects is the maximum number of followed redirects.
	maxRedirects = 5
	// userAgent is advertised so customers can allow-list probe IPs.
	userAgent = "Foghorn-Probe/1.0 (+https://foghorn.io/probe-ips)"
)

// Config holds the configuration for a single HTTP check.
type Config struct {
	URL              string
	ExpectedStatus   int
	BodyAssertRegex  string
	LatencyThreshold time.Duration
	Timeout          time.Duration // hard deadline; 0 = 30s default
}

// Timings holds the httptrace breakdown in milliseconds.
type Timings struct {
	DNSMS     float64
	ConnectMS float64
	TLSMS     float64
	TTFBMS    float64
	TotalMS   float64
}

// Result is the output of a single HTTP check.
type Result struct {
	Class      model.CheckClass
	Reason     string
	StatusCode int
	Timings    Timings
}

// Check runs a single HTTP probe and returns the classified result.
// Each call creates a fresh connection (no keep-alive).
func Check(ctx context.Context, cfg Config) Result {
	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	guardedDialer := dialer.New()

	transport := &http.Transport{
		DialContext:           guardedDialer.DialContext,
		DisableKeepAlives:     true, // fresh connection per check
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{},
	}

	redirectCount := 0
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("redirect to disallowed scheme: %s", req.URL.Scheme)
			}
			redirectCount++
			if redirectCount > maxRedirects {
				return fmt.Errorf("too many redirects (max %d)", maxRedirects)
			}
			return nil
		},
	}

	var (
		dnsStart, dnsDone         time.Time
		connectStart, connectDone time.Time
		tlsStart, tlsDone         time.Time
		ttfb                      time.Time
		reqStart                  time.Time
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
	if err != nil {
		return Result{Class: model.ClassConfigError, Reason: "invalid URL"}
	}
	req.Header.Set("User-Agent", userAgent)

	trace := &httptrace.ClientTrace{
		DNSStart:          func(_ httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:           func(_ httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:      func(_, _ string) { connectStart = time.Now() },
		ConnectDone:       func(_, _ string, _ error) { connectDone = time.Now() },
		TLSHandshakeStart: func() { tlsStart = time.Now() },
		TLSHandshakeDone:  func(_ tls.ConnectionState, _ error) { tlsDone = time.Now() },
		GotFirstResponseByte: func() { ttfb = time.Now() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	reqStart = time.Now()

	resp, err := client.Do(req)
	totalMS := float64(time.Since(reqStart)) / float64(time.Millisecond)

	if err != nil {
		// Check if this was our SSRF guard
		var ssrfErr *dialer.ErrSSRFBlocked
		if errors.As(err, &ssrfErr) {
			return Result{Class: model.ClassConfigError, Reason: "SSRF: blocked address"}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return Result{Class: model.ClassTargetFailure, Reason: "timeout",
				Timings: Timings{TotalMS: totalMS}}
		}
		return Result{Class: model.ClassTargetFailure, Reason: "connection failed",
			Timings: Timings{TotalMS: totalMS}}
	}
	defer resp.Body.Close()

	// Read limited body for assertion
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	// Drain to allow connection reuse (not that we reuse, but avoids RST)
	_, _ = io.Copy(io.Discard, resp.Body)

	timings := Timings{
		DNSMS:     msFrom(dnsStart, dnsDone),
		ConnectMS: msFrom(connectStart, connectDone),
		TLSMS:     msFrom(tlsStart, tlsDone),
		TTFBMS:    msFrom(reqStart, ttfb),
		TotalMS:   totalMS,
	}

	// Status code check
	if cfg.ExpectedStatus > 0 && resp.StatusCode != cfg.ExpectedStatus {
		return Result{
			Class:      model.ClassTargetFailure,
			Reason:     fmt.Sprintf("status %d, expected %d", resp.StatusCode, cfg.ExpectedStatus),
			StatusCode: resp.StatusCode,
			Timings:    timings,
		}
	}

	// Body assertion
	if cfg.BodyAssertRegex != "" {
		re, err := regexp.Compile(cfg.BodyAssertRegex)
		if err != nil {
			return Result{Class: model.ClassConfigError, Reason: "invalid body assertion regex"}
		}
		if !re.Match(body) {
			return Result{
				Class:      model.ClassTargetFailure,
				Reason:     "body assertion failed",
				StatusCode: resp.StatusCode,
				Timings:    timings,
			}
		}
	}

	class := model.ClassOK
	reason := ""
	if cfg.LatencyThreshold > 0 && time.Duration(totalMS*float64(time.Millisecond)) > cfg.LatencyThreshold {
		class = model.ClassOKSlow
		reason = fmt.Sprintf("latency %.1fms exceeds threshold %v", totalMS, cfg.LatencyThreshold)
	}

	return Result{
		Class:      class,
		Reason:     reason,
		StatusCode: resp.StatusCode,
		Timings:    timings,
	}
}

func msFrom(start, end time.Time) float64 {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	return float64(end.Sub(start)) / float64(time.Millisecond)
}

// ClassifyConnError maps a connection error string to a reason without
// echoing internal response content.
func ClassifyConnError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return "connection refused"
	case strings.Contains(s, "no such host"):
		return "DNS: no such host"
	case strings.Contains(s, "timeout"):
		return "timeout"
	case strings.Contains(s, "certificate"):
		return "TLS certificate error"
	default:
		return "connection failed"
	}
}
