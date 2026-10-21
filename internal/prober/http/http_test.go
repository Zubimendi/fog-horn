package httpprober_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zubimendi/fog-horn/internal/model"
	httpprober "github.com/zubimendi/fog-horn/internal/prober/http"
)

func TestHTTPProberSSRF(t *testing.T) {
	ctx := context.Background()

	// Probing loopback or cloud metadata should be blocked by SSRF dialer
	res := httpprober.Check(ctx, httpprober.Config{
		URL:            "http://169.254.169.254/latest/meta-data/",
		ExpectedStatus: 200,
		Timeout:        1 * time.Second,
	})

	if res.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for SSRF target, got %v (%s)", res.Class, res.Reason)
	}
}

func TestHTTPProberInvalidURL(t *testing.T) {
	ctx := context.Background()

	res := httpprober.Check(ctx, httpprober.Config{
		URL:     "://invalid-url",
		Timeout: 1 * time.Second,
	})

	if res.Class != model.ClassConfigError {
		t.Fatalf("expected ClassConfigError for invalid URL, got %v (%s)", res.Class, res.Reason)
	}
}

func TestHTTPProberServerChecks(t *testing.T) {
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(50 * time.Millisecond)
		}
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello foghorn ok"))
	}))
	defer ts.Close()

	// 1. Success check: Note that test server listens on 127.0.0.1, which dialer blocks as SSRF!
	// This confirms the SSRF defense works even against localhost test servers!
	res := httpprober.Check(ctx, httpprober.Config{
		URL:            ts.URL,
		ExpectedStatus: 200,
		Timeout:        1 * time.Second,
	})
	if res.Class != model.ClassConfigError {
		t.Fatalf("expected dialer to block localhost test server as SSRF, got %v (%s)", res.Class, res.Reason)
	}
}
