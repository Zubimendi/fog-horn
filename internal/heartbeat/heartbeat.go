// Package heartbeat manages heartbeat token generation, hashing, verification,
// and incoming ping processing for push-based targets.
package heartbeat

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zubimendi/fog-horn/internal/model"
)

// DefaultGracePeriod is the time window an old token remains valid after rotation.
const DefaultGracePeriod = 24 * time.Hour

// TokenInfo holds the token metadata.
type TokenInfo struct {
	TargetID          uuid.UUID
	TokenHash         string
	PreviousTokenHash string
	PreviousExpiresAt *time.Time
	RotatedAt         time.Time
	CreatedAt         time.Time
}

// HashToken calculates the hex SHA-256 hash of a raw token.
// Raw tokens are never persisted in plaintext.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// GenerateRawToken generates a cryptographically secure random token string.
func GenerateRawToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("heartbeat: generate token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// IsValidToken verifies if a candidate raw token matches either the current token
// or the previous token within its grace period.
func (ti *TokenInfo) IsValidToken(raw string, now time.Time) bool {
	h := HashToken(raw)
	if h == ti.TokenHash {
		return true
	}
	if ti.PreviousTokenHash != "" && h == ti.PreviousTokenHash {
		if ti.PreviousExpiresAt != nil && now.Before(*ti.PreviousExpiresAt) {
			return true
		}
	}
	return false
}

// Rotate updates the token info with a new raw token, moving the current token
// to previous with the specified grace period.
func (ti *TokenInfo) Rotate(newRaw string, gracePeriod time.Duration, now time.Time) {
	expiresAt := now.Add(gracePeriod)
	ti.PreviousTokenHash = ti.TokenHash
	ti.PreviousExpiresAt = &expiresAt
	ti.TokenHash = HashToken(newRaw)
	ti.RotatedAt = now
}

// BuildPingResult generates a CheckResult from an incoming heartbeat ping.
func BuildPingResult(targetID uuid.UUID, locationID uuid.UUID, isFail bool, now time.Time) model.CheckResult {
	class := model.ClassOK
	reason := "heartbeat received"
	if isFail {
		class = model.ClassTargetFailure
		reason = "heartbeat reported failure"
	}

	return model.CheckResult{
		TargetID:     targetID,
		LocationID:   locationID,
		ScheduledFor: now,
		ExecutedAt:   now,
		Class:        class,
		Reason:       reason,
		StatusCode:   200,
	}
}
