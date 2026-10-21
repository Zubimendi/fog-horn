// Binary api runs the central Foghorn API service.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zubimendi/fog-horn/internal/api"
	"github.com/zubimendi/fog-horn/internal/config"
	"github.com/zubimendi/fog-horn/internal/db"
	"github.com/zubimendi/fog-horn/internal/heartbeat"
	"github.com/zubimendi/fog-horn/internal/writer"
)

func main() {
	cfg := config.Load()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	pool, err := db.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("FATAL: failed to connect to database: %v", err)
	}
	defer pool.Close()

	resWriter := writer.New(pool, writer.Config{
		BatchSize:     cfg.SchedulerBatchSize,
		FlushInterval: 500 * time.Millisecond,
	})
	resWriter.Start(ctx)
	defer resWriter.Stop()

	// Dead-man monitor heartbeat emitter
	if cfg.DeadManURL != "" {
		emitter := heartbeat.NewDeadManEmitter(cfg.DeadManURL, cfg.DeadManInterval)
		emitter.Start(ctx)
	}

	server := api.NewServer(pool, resWriter)
	httpServer := &http.Server{
		Addr:         cfg.APIAddr,
		Handler:      server.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("Foghorn API server listening on %s", cfg.APIAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server failure: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("Shutting down API server gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Println("Foghorn API server stopped.")
}
