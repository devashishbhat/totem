// Package otlp turns an OTLP/HTTP protobuf trace export into Totem events and
// validates that each one carries the attributes its op requires.
package otlp

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/devashishbhat/totem/server/internal/event"
)

// eventNamespace makes the fallback event id a deterministic function of the
// span identity, so a retried export dedupes instead of duplicating.
var eventNamespace = uuid.MustParse("6f1b0f9e-6a5e-4b2f-9a0e-0b7c1d2e3f40")

// Decode parses the request body and returns the events it contains along with
// one rejection per span that failed validation. A body that cannot be decoded
// at all is an error.
func Decode(body []byte) (events []event.Event, rejected []string, err error) {
	// TracesData is wire-compatible with ExportTraceServiceRequest (both carry
	// repeated ResourceSpans in field 1) and lives in the proto-only package,
	// so decoding it keeps grpc and grpc-gateway out of the build.
	var req tracepb.TracesData
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, nil, fmt.Errorf("unmarshal OTLP request: %w", err)
	}
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := attrsToMap(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				ev, verr := spanToEvent(span, resourceAttrs)
				if verr != nil {
					rejected = append(rejected, verr.Error())
					continue
				}
				events = append(events, ev)
			}
		}
	}
	return events, rejected, nil
}

func spanToEvent(span *tracepb.Span, resourceAttrs map[string]any) (event.Event, error) {
	attrs := make(map[string]any, len(resourceAttrs)+len(span.GetAttributes()))
	for k, v := range resourceAttrs {
		attrs[k] = v
	}
	for k, v := range attrsToMap(span.GetAttributes()) {
		attrs[k] = v
	}

	ev := event.Event{
		Op:         event.Op(str(attrs[event.AttrOp])),
		RunID:      str(attrs[event.AttrRunID]),
		OccurredAt: time.Unix(0, int64(span.GetStartTimeUnixNano())).UTC(),
		Attrs:      attrs,
	}
	if ev.OccurredAt.IsZero() || span.GetStartTimeUnixNano() == 0 {
		ev.OccurredAt = time.Now().UTC()
	}
	if id := str(attrs[event.AttrEventID]); id != "" {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return ev, fmt.Errorf("span %q: %s is not a uuid", span.GetName(), event.AttrEventID)
		}
		ev.EventID = parsed
	} else {
		ev.EventID = uuid.NewSHA1(eventNamespace, append(span.GetTraceId(), span.GetSpanId()...))
	}
	if err := Validate(ev); err != nil {
		return ev, fmt.Errorf("span %q: %w", span.GetName(), err)
	}
	return ev, nil
}

// Validate checks the op and the attributes that op cannot be processed
// without.
func Validate(ev event.Event) error {
	if ev.RunID == "" {
		return fmt.Errorf("missing %s", event.AttrRunID)
	}
	required := map[string]struct{ keys []string }{
		string(event.OpRun):         {[]string{event.AttrAgentID}},
		string(event.OpSource):      {[]string{event.AttrSourceID, event.AttrSourceKind, event.AttrHMAC}},
		string(event.OpMemoryWrite): {[]string{event.AttrMemoryID, event.AttrHMAC, event.AttrPrecision}},
		string(event.OpMemoryRead):  {[]string{event.AttrReadResults}},
		string(event.OpLLMCall):     {[]string{event.AttrLLMCallID}},
		string(event.OpDecision):    {[]string{event.AttrDecisionID}},
	}
	spec, ok := required[string(ev.Op)]
	if !ok {
		return fmt.Errorf("unknown %s %q", event.AttrOp, ev.Op)
	}
	for _, k := range spec.keys {
		if _, present := ev.Attrs[k]; !present {
			return fmt.Errorf("op %s: missing %s", ev.Op, k)
		}
	}
	switch ev.Op {
	case event.OpSource:
		if _, err := uuid.Parse(ev.Str(event.AttrSourceID)); err != nil {
			return fmt.Errorf("op source: %s is not a uuid", event.AttrSourceID)
		}
		switch ev.Str(event.AttrSourceKind) {
		case "user_message", "system_config", "tool_output", "external_document", "unknown":
		default:
			return fmt.Errorf("op source: unknown %s %q", event.AttrSourceKind, ev.Str(event.AttrSourceKind))
		}
	case event.OpMemoryWrite:
		switch ev.Str(event.AttrPrecision) {
		case "step", "run":
		default:
			return fmt.Errorf("op memory.write: %s must be step or run", event.AttrPrecision)
		}
	case event.OpLLMCall:
		if _, err := uuid.Parse(ev.Str(event.AttrLLMCallID)); err != nil {
			return fmt.Errorf("op llm.call: %s is not a uuid", event.AttrLLMCallID)
		}
	case event.OpDecision:
		if _, err := uuid.Parse(ev.Str(event.AttrDecisionID)); err != nil {
			return fmt.Errorf("op decision: %s is not a uuid", event.AttrDecisionID)
		}
	}
	return nil
}

func attrsToMap(kvs []*commonpb.KeyValue) map[string]any {
	out := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = anyValue(kv.GetValue())
	}
	return out
}

func anyValue(v *commonpb.AnyValue) any {
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		return val.BoolValue
	case *commonpb.AnyValue_IntValue:
		return val.IntValue
	case *commonpb.AnyValue_DoubleValue:
		return val.DoubleValue
	case *commonpb.AnyValue_ArrayValue:
		items := val.ArrayValue.GetValues()
		out := make([]any, 0, len(items))
		for _, i := range items {
			out = append(out, anyValue(i))
		}
		return out
	case *commonpb.AnyValue_KvlistValue:
		return attrsToMap(val.KvlistValue.GetValues())
	case *commonpb.AnyValue_BytesValue:
		return string(val.BytesValue)
	}
	return nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
