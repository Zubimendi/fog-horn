package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/zubimendi/fog-horn/internal/api"
)

func TestHealthEndpoints(t *testing.T) {
	s := api.NewServer(nil, nil)
	handler := s.Handler()

	// 1. /healthz
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 from /healthz, got %d", rec.Code)
	}

	// 2. /readyz
	reqReady := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	recReady := httptest.NewRecorder()
	handler.ServeHTTP(recReady, reqReady)

	if recReady.Code != http.StatusOK {
		t.Fatalf("want 200 from /readyz, got %d", recReady.Code)
	}
}
