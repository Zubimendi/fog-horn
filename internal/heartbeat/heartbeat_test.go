package heartbeat_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/heartbeat"
	"github.com/zubimendi/fog-horn/internal/model"
)

func TestTokenGenerationAndHashing(t *testing.T) {
	raw, err := heartbeat.GenerateRawToken()
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	if len(raw) == 0 {
		t.Fatalf("empty token generated")
	}

	h := heartbeat.HashToken(raw)
	if len(h) != 64 { // SHA-256 hex string is 64 chars
		t.Errorf("expected 64 chars hex hash, got len %d", len(h))
	}
}

func TestTokenRotationAndGracePeriod(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	raw1 := "token-initial-12345"
	raw2 := "token-rotated-67890"

	info := heartbeat.TokenInfo{
		TargetID:  uuid.New(),
		TokenHash: heartbeat.HashToken(raw1),
		CreatedAt: t0,
		RotatedAt: t0,
	}

	// 1. Initial token valid
	if !info.IsValidToken(raw1, t0) {
		t.Fatalf("expected raw1 to be valid initially")
	}
	if info.IsValidToken("invalid-token", t0) {
		t.Fatalf("invalid token should not pass")
	}

	// 2. Rotate to raw2 with 24h grace period
	t1 := t0.Add(1 * time.Hour)
	info.Rotate(raw2, 24*time.Hour, t1)

	// Both raw2 and raw1 should be valid at t1 + 1h
	tCheckWithinGrace := t1.Add(1 * time.Hour)
	if !info.IsValidToken(raw2, tCheckWithinGrace) {
		t.Errorf("new token raw2 should be valid")
	}
	if !info.IsValidToken(raw1, tCheckWithinGrace) {
		t.Errorf("old token raw1 should still be valid within 24h grace period")
	}

	// After 25 hours from rotation, raw1 expires, raw2 remains valid
	tCheckExpired := t1.Add(25 * time.Hour)
	if !info.IsValidToken(raw2, tCheckExpired) {
		t.Errorf("new token raw2 should remain valid")
	}
	if info.IsValidToken(raw1, tCheckExpired) {
		t.Errorf("old token raw1 should expire after grace period")
	}
}

func TestBuildPingResult(t *testing.T) {
	targetID := uuid.New()
	locID := uuid.New()
	now := time.Now()

	okResult := heartbeat.BuildPingResult(targetID, locID, false, now)
	if okResult.Class != model.ClassOK {
		t.Errorf("want OK, got %v", okResult.Class)
	}

	failResult := heartbeat.BuildPingResult(targetID, locID, true, now)
	if failResult.Class != model.ClassTargetFailure {
		t.Errorf("want TARGET_FAILURE, got %v", failResult.Class)
	}
}
