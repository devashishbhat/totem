package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Trust tiers. Higher is more trusted.
const (
	TrustUnknown  int16 = 0 // T0
	TrustExternal int16 = 1 // T1
	TrustTool     int16 = 2 // T2
	TrustUser     int16 = 3 // T3
	TrustSystem   int16 = 4 // T4
)

// Attribution levels, weakest to strongest.
const (
	LevelRetrieved      = "retrieved"
	LevelInContext      = "in_context"
	LevelValueFlow      = "value_flow"
	LevelCounterfactual = "counterfactual"
)

// Alert kinds.
const (
	AlertSideEffectFromUntrusted = "taint.side_effect_from_untrusted"
	AlertUntrustedContactValue   = "memory.untrusted_contact_value"
)

type Run struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Scope     string    `json:"scope"`
	StartedAt time.Time `json:"started_at"`
}

type Source struct {
	ID          uuid.UUID `json:"id"`
	RunID       string    `json:"run_id"`
	Kind        string    `json:"kind"`
	Origin      string    `json:"origin"`
	HMAC        string    `json:"hmac"`
	Content     string    `json:"content"`
	Trust       int16     `json:"trust"`
	TrustReason string    `json:"trust_reason"`
	ObservedAt  time.Time `json:"observed_at"`
}

type Memory struct {
	ID             uuid.UUID `json:"id"`
	Key            string    `json:"memory_key"`
	Status         string    `json:"status"`
	CurrentVersion int       `json:"current_version"`
}

type MemoryVersion struct {
	ID               uuid.UUID   `json:"id"`
	MemoryID         uuid.UUID   `json:"memory_id"`
	MemoryKey        string      `json:"memory_key,omitempty"`
	Version          int         `json:"version"`
	HMAC             string      `json:"hmac"`
	Content          string      `json:"content"`
	Deleted          bool        `json:"deleted"`
	LineagePrecision string      `json:"lineage_precision"`
	TrustMin         int16       `json:"trust_min"`
	TaintSourceIDs   []uuid.UUID `json:"taint_source_ids"`
	RecordedAt       time.Time   `json:"recorded_at"`
	SupersededAt     *time.Time  `json:"superseded_at"`
}

type Edge struct {
	ID     uuid.UUID `json:"id"`
	Kind   string    `json:"kind"`
	FromID uuid.UUID `json:"from_id"`
	ToID   uuid.UUID `json:"to_id"`
	ToType string    `json:"to_type"`
}

type MemoryRead struct {
	ID         uuid.UUID       `json:"id"`
	RunID      string          `json:"run_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Results    json.RawMessage `json:"results"`
}

type LLMCall struct {
	ID         uuid.UUID       `json:"id"`
	RunID      string          `json:"run_id"`
	Model      string          `json:"model"`
	InContext  json.RawMessage `json:"in_context"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	OccurredAt time.Time       `json:"occurred_at"`
}

type Decision struct {
	ID             uuid.UUID       `json:"id"`
	RunID          string          `json:"run_id"`
	LLMCallID      *uuid.UUID      `json:"llm_call_id"`
	Tool           string          `json:"tool"`
	SideEffect     bool            `json:"side_effect"`
	Args           json.RawMessage `json:"args"`
	TrustMin       int16           `json:"trust_min"`
	TaintSourceIDs []uuid.UUID     `json:"taint_source_ids"`
	OccurredAt     time.Time       `json:"occurred_at"`
}

type Attribution struct {
	DecisionID      uuid.UUID       `json:"decision_id"`
	MemoryVersionID *uuid.UUID      `json:"memory_version_id"`
	SourceID        *uuid.UUID      `json:"source_id"`
	Level           string          `json:"level"`
	Detail          json.RawMessage `json:"detail"`
}

type Alert struct {
	ID        uuid.UUID       `json:"id"`
	Kind      string          `json:"kind"`
	Severity  string          `json:"severity"`
	SubjectID uuid.UUID       `json:"subject_id"`
	RunID     string          `json:"run_id"`
	Title     string          `json:"title"`
	Evidence  json.RawMessage `json:"evidence"`
	CreatedAt time.Time       `json:"created_at"`
}

// Replay statuses.
const (
	ReplayPending = "pending"
	ReplayRunning = "running"
	ReplayDone    = "done"
	ReplayFailed  = "failed"
)

// Replay verdicts.
const (
	VerdictStrong       = "strong"
	VerdictModerate     = "moderate"
	VerdictNone         = "none"
	VerdictInconclusive = "inconclusive"
)

type ReplayExperiment struct {
	ID                    uuid.UUID  `json:"id"`
	DecisionID            uuid.UUID  `json:"decision_id"`
	TargetMemoryVersionID uuid.UUID  `json:"target_memory_version_id"`
	SamplesPerArm         int        `json:"samples_per_arm"`
	Model                 string     `json:"model"`
	BaselineFlip          *float64   `json:"baseline_flip"`
	VariantFlip           *float64   `json:"variant_flip"`
	Effect                *float64   `json:"effect"`
	CILow                 *float64   `json:"ci_low"`
	CIHigh                *float64   `json:"ci_high"`
	Verdict               *string    `json:"verdict"`
	Status                string     `json:"status"`
	Error                 string     `json:"error,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	FinishedAt            *time.Time `json:"finished_at"`
}

type ReplaySample struct {
	ExperimentID    uuid.UUID       `json:"experiment_id"`
	Arm             string          `json:"arm"`
	Idx             int             `json:"idx"`
	Output          json.RawMessage `json:"output"`
	MatchesBaseline bool            `json:"matches_baseline"`
}

// TierName is the trust badge text the dashboard shows.
func TierName(t int16) string {
	switch t {
	case TrustSystem:
		return "T4 System"
	case TrustUser:
		return "T3 User"
	case TrustTool:
		return "T2 Tool"
	case TrustExternal:
		return "T1 External"
	default:
		return "T0 Unknown"
	}
}
