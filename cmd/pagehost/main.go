// Binary pagehost serves public status pages directly from object storage (MinIO/S3).
// INVARIANT: zero database imports. The page host can remain fully operational even if Postgres is down.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zubimendi/fog-horn/internal/snapshot"
)

func main() {
	// Read standalone environment variables without importing db config
	pageHostAddr := getEnv("PAGE_HOST_ADDR", ":8081")
	s3Endpoint := getEnv("S3_ENDPOINT", "localhost:9000")
	s3Bucket := getEnv("S3_BUCKET", "snapshots")
	s3UseSSL := getEnv("S3_USE_SSL", "false") == "true"
	staleThresholdStr := getEnv("PAGE_HOST_STALE_THRESHOLD", "2m")
	staleThreshold, err := time.ParseDuration(staleThresholdStr)
	if err != nil {
		staleThreshold = 2 * time.Minute
	}

	storage := snapshot.NewHTTPStorageClient(s3Endpoint, s3Bucket, s3UseSSL)

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","service":"pagehost"}`))
	})

	// JSON Status Endpoint: GET /status/{tenant_id}.json
	mux.HandleFunc("GET /status/{tenant_id}.json", func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.PathValue("tenant_id")
		if tenantID == "" {
			http.Error(w, "missing tenant_id", http.StatusBadRequest)
			return
		}

		key := fmt.Sprintf("snapshots/%s/current.json", tenantID)
		data, etag, err := storage.GetObject(r.Context(), key)
		if err != nil {
			http.Error(w, "status page not found", http.StatusNotFound)
			return
		}

		// Conditional GET: ETag handling
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=15, stale-if-error=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})

	// HTML Status Endpoint: GET /status/{tenant_id}
	mux.HandleFunc("GET /status/{tenant_id}", func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.PathValue("tenant_id")
		if tenantID == "" {
			http.Error(w, "missing tenant_id", http.StatusBadRequest)
			return
		}

		// Fetch current JSON to check staleness and ETag
		keyJSON := fmt.Sprintf("snapshots/%s/current.json", tenantID)
		jsonData, etag, err := storage.GetObject(r.Context(), keyJSON)
		if err != nil {
			http.Error(w, "status page not found", http.StatusNotFound)
			return
		}

		var snap snapshot.Snapshot
		if err := json.Unmarshal(jsonData, &snap); err != nil {
			http.Error(w, "corrupt snapshot data", http.StatusInternalServerError)
			return
		}

		isStale := time.Since(snap.GeneratedAt) > staleThreshold

		// If not stale and ETag matches, return 304
		if !isStale {
			if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}

		htmlContent := snapshot.RenderHTML(snap, isStale)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=15, stale-if-error=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(htmlContent))
	})

	server := &http.Server{
		Addr:         pageHostAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		log.Printf("Foghorn pagehost running on %s (connected only to object storage %s/%s)",
			pageHostAddr, s3Endpoint, s3Bucket)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("pagehost server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down pagehost...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	log.Println("Pagehost stopped.")
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
