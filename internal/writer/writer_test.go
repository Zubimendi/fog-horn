package writer_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/model"
	"github.com/zubimendi/fog-horn/internal/writer"
)

func TestWriterLifecycle(t *testing.T) {
	// Nil pool handles WriteBatch empty check gracefully
	err := writer.WriteBatch(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected nil err on empty batch, got %v", err)
	}

	w := writer.New(nil, writer.Config{
		BatchSize:     5,
		FlushInterval: 50 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w.Start(ctx)

	// Enqueue less than batchSize
	w.Enqueue(model.CheckResult{
		TargetID:     uuid.New(),
		LocationID:   uuid.New(),
		ScheduledFor: time.Now(),
		ExecutedAt:   time.Now(),
		Class:        model.ClassOK,
	})

	w.Stop()
}
