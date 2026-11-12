// Package event is the wire model shared by ingest and the lineage worker:
// one OTLP span becomes one Event, and the attribute names of the wire format
// are read through typed accessors.
package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Op names an event kind. It travels in the totem.op span attribute.
type Op string

const (
	OpRun         Op = "run"
	OpSource      Op = "source"
	OpMemoryWrite Op = "memory.write"
	OpMemoryRead  Op = "memory.read"
	OpLLMCall     Op = "llm.call"
	OpDecision    Op = "decision"
)

// Attribute keys.
const (
	AttrOp          = "totem.op"
	AttrEventID     = "totem.event.id"
	AttrAgentID     = "totem.agent.id"
	AttrRunID       = "totem.run.id"
	AttrRunScope    = "totem.run.scope"
	AttrOverheadUs  = "totem.sdk.overhead_us"
	AttrSourceID    = "totem.source.id"
	AttrSourceKind  = "totem.source.kind"
	AttrOrigin      = "totem.source.origin"
	AttrTrust       = "totem.source.trust"
	AttrHMAC        = "totem.hmac"
	AttrContent     = "totem.content"
	AttrMemoryID    = "totem.memory.id"
	AttrDeleted     = "totem.memory.deleted"
	AttrSourceIDs   = "totem.source.ids"
	AttrDerivedFrom = "totem.memory.derived_from"
	AttrPrecision   = "totem.lineage.precision"
	AttrReadResults = "totem.read.results"
	AttrLLMCallID   = "totem.llm.call_id"
	AttrModel       = "gen_ai.request.model"
	AttrInContext   = "totem.llm.in_context"
	AttrPayload     = "totem.llm.payload"
	AttrDecisionID  = "totem.decision.id"
	AttrTool        = "totem.decision.tool"
	AttrSideEffect  = "totem.decision.side_effect"
	AttrArgs        = "totem.decision.args"
	AttrValueFlow   = "totem.decision.value_flow"
)

// Event is one decoded span.
type Event struct {
	EventID    uuid.UUID      `json:"event_id"`
	RunID      string         `json:"run_id"`
	Op         Op             `json:"op"`
	OccurredAt time.Time      `json:"occurred_at"`
	Attrs      map[string]any `json:"attrs"`
}

// Str returns a string attribute, or "" when it is absent or not a string.
func (e Event) Str(key string) string {
	s, _ := e.Attrs[key].(string)
	return s
}

// Bool returns a boolean attribute. OTLP senders may encode it as a string.
func (e Event) Bool(key string) bool {
	switch v := e.Attrs[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// Int returns an integer attribute and whether it was present.
func (e Event) Int(key string) (int, bool) {
	switch v := e.Attrs[key].(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		return int(i), err == nil
	}
	return 0, false
}

// StrSlice returns a string-array attribute. A lone string counts as one item.
func (e Event) StrSlice(key string) []string {
	switch v := e.Attrs[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, i := range v {
			if s, ok := i.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
}

// JSON decodes a JSON-valued attribute into dst. A missing or empty attribute
// leaves dst untouched and returns nil, because every JSON attribute is
// optional on the wire.
func (e Event) JSON(key string, dst any) error {
	raw, ok := e.Attrs[key]
	if !ok || raw == nil {
		return nil
	}
	var b []byte
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		b = []byte(v)
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			return fmt.Errorf("re-encode %s: %w", key, err)
		}
	}
	if err := json.Unmarshal(b, dst); err != nil {
		return fmt.Errorf("decode %s: %w", key, err)
	}
	return nil
}

// RawJSON returns a JSON-valued attribute as bytes, or nil when absent.
func (e Event) RawJSON(key string) []byte {
	raw, ok := e.Attrs[key]
	if !ok || raw == nil {
		return nil
	}
	if s, ok := raw.(string); ok {
		if s == "" {
			return nil
		}
		if json.Valid([]byte(s)) {
			return []byte(s)
		}
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return b
}

// InContextRef is one entry of totem.llm.in_context. A span is
// [message index, start code point, end code point) into the messages array of
// the recorded request, which is what lets replay cut the text back out.
type InContextRef struct {
	RefType string   `json:"ref_type"` // memory | source
	RefID   string   `json:"ref_id"`
	HMAC    string   `json:"hmac"`
	Match   string   `json:"match"` // exact | partial
	Spans   [][3]int `json:"spans"`
}

// ValueFlowRef is one entry of totem.decision.value_flow.
type ValueFlowRef struct {
	RefType string `json:"ref_type"`
	RefID   string `json:"ref_id"`
	HMAC    string `json:"hmac"`
	ArgPath string `json:"arg_path"`
	Match   string `json:"match"`
}

// ReadResult is one entry of totem.read.results.
type ReadResult struct {
	MemoryID string  `json:"memory_id"`
	HMAC     string  `json:"hmac"`
	Rank     float64 `json:"rank"`
}
