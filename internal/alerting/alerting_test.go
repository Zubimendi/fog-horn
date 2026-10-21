package alerting_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/alerting"
	"github.com/zubimendi/fog-horn/internal/model"
)

func TestDedupeKey(t *testing.T) {
	targetID := uuid.MustParse("00000000-0000-0000-0000-000000000042")
	onset := time.Date(2026, 5, 1, 14, 30, 0, 0, time.UTC)

	k1 := alerting.DedupeKey(targetID, model.AlertTargetDown, onset)
	k2 := alerting.DedupeKey(targetID, model.AlertTargetDown, onset)

	if k1 != k2 {
		t.Fatalf("expected deterministic dedupe key, got %q and %q", k1, k2)
	}

	expected := "00000000-0000-0000-0000-000000000042:TARGET_DOWN:2026-05-01T14:30:00Z"
	if k1 != expected {
		t.Errorf("got %q, want %q", k1, expected)
	}
}

func TestDispatcherClientDryRun(t *testing.T) {
	client := alerting.NewDispatcherClient("", "")
	err := client.Send(context.Background(), alerting.OutboxItem{
		ID:        uuid.New(),
		TenantID:  uuid.New(),
		Kind:      model.AlertTargetDown,
		DedupeKey: "key-123",
		Payload:   map[string]any{"test": "val"},
	})
	if err != nil {
		t.Errorf("expected no error in dry-run mode, got %v", err)
	}
}
