// Package model defines the shared domain types for Foghorn.
package model

import (
	"time"

	"github.com/google/uuid"
)

// TargetType enumerates the supported check protocols.
type TargetType string

const (
	TargetHTTP      TargetType = "HTTP"
	TargetTCP       TargetType = "TCP"
	TargetDNS       TargetType = "DNS"
	TargetTLS       TargetType = "TLS"
	TargetHeartbeat TargetType = "HEARTBEAT"
)

// CheckClass is the result classification for a single probe attempt.
type CheckClass string

const (
	ClassOK            CheckClass = "OK"
	ClassOKSlow        CheckClass = "OK_SLOW"
	ClassTargetFailure CheckClass = "TARGET_FAILURE"
	ClassProbeError    CheckClass = "PROBE_ERROR"
	ClassConfigError   CheckClass = "CONFIG_ERROR"
)

// IsTargetFailure returns true only for classes that count as target downtime.
func (c CheckClass) IsTargetFailure() bool {
	return c == ClassTargetFailure
}

// LocationStateValue is the per-(target,location) hysteresis state.
type LocationStateValue string

const (
	LocationUp      LocationStateValue = "UP"
	LocationDown    LocationStateValue = "DOWN"
	LocationUnknown LocationStateValue = "UNKNOWN"
)

// TargetStateValue is the overall judgment state for a target.
type TargetStateValue string

const (
	StateUp      TargetStateValue = "UP"
	StateDown    TargetStateValue = "DOWN"
	StatePartial TargetStateValue = "PARTIAL"
	StateDegraded TargetStateValue = "DEGRADED"
	StateUnknown TargetStateValue = "UNKNOWN"
	StatePaused  TargetStateValue = "PAUSED"
)

// ComponentStatus enumerates the derived status values.
type ComponentStatus string

const (
	StatusOperational         ComponentStatus = "operational"
	StatusUnderMaintenance    ComponentStatus = "under_maintenance"
	StatusDegradedPerformance ComponentStatus = "degraded_performance"
	StatusPartialOutage       ComponentStatus = "partial_outage"
	StatusMajorOutage         ComponentStatus = "major_outage"
)

// SeverityRank maps a status to an integer for max-severity comparison.
func SeverityRank(s ComponentStatus) int {
	switch s {
	case StatusMajorOutage:
		return 5
	case StatusPartialOutage:
		return 4
	case StatusDegradedPerformance:
		return 3
	case StatusUnderMaintenance:
		return 2
	case StatusOperational:
		return 1
	default:
		return 0
	}
}

// MaxSeverity returns the higher severity status.
func MaxSeverity(a, b ComponentStatus) ComponentStatus {
	if SeverityRank(a) >= SeverityRank(b) {
		return a
	}
	return b
}

// IncidentStatus enumerates valid incident lifecycle states.
type IncidentStatus string

const (
	IncidentInvestigating IncidentStatus = "INVESTIGATING"
	IncidentIdentified    IncidentStatus = "IDENTIFIED"
	IncidentMonitoring    IncidentStatus = "MONITORING"
	IncidentResolved      IncidentStatus = "RESOLVED"
	IncidentRetracted     IncidentStatus = "RETRACTED"
)

// AlertKind enumerates the types of alert events.
type AlertKind string

const (
	AlertTargetDown     AlertKind = "TARGET_DOWN"
	AlertTargetPartial  AlertKind = "TARGET_PARTIAL"
	AlertMonitoringGap  AlertKind = "MONITORING_GAP"
	AlertCertExpiring   AlertKind = "CERT_EXPIRING"
	AlertProbeUnhealthy AlertKind = "PROBE_UNHEALTHY"
)

// Target represents a single monitored endpoint.
type Target struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	Type             TargetType
	Config           map[string]any
	ExpectedStatus   int
	Assertion        map[string]any
	LatencyThreshold time.Duration
	NFail            int
	NRecover         int
	Quorum           float64
	Interval         time.Duration
	Paused           bool
}

// CheckResult is the raw output of a single probe attempt.
type CheckResult struct {
	TargetID     uuid.UUID
	LocationID   uuid.UUID
	ScheduledFor time.Time
	ExecutedAt   time.Time
	Class        CheckClass
	Reason       string
	StatusCode   int
	DNSMS        float64
	ConnectMS    float64
	TLSMS        float64
	TTFBMS       float64
	TotalMS      float64
}

// LocationState tracks per-(target,location) hysteresis.
type LocationState struct {
	TargetID                  uuid.UUID
	LocationID                uuid.UUID
	ConsecutiveFailures       int
	ConsecutiveSuccesses      int
	State                     LocationStateValue
	StreakStartedAt           time.Time
	LastEvaluatedScheduledFor time.Time
}

// StateInterval represents a contiguous period in a single target state.
type StateInterval struct {
	ID       uuid.UUID
	TargetID uuid.UUID
	State    TargetStateValue
	StartAt  time.Time
	EndAt    *time.Time // nil while open
}

// AlertEvent is an outbox row for an alert to be delivered.
type AlertEvent struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Kind        AlertKind
	DedupeKey   string
	Payload     map[string]any
	State       string
	CreatedAt   time.Time
	DeliveredAt *time.Time
}
