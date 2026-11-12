package otlp

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/devashishbhat/totem/server/internal/event"
)

func sv(v string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
}

// export builds an OTLP body from attribute maps, one span each.
func export(t *testing.T, spans ...map[string]*commonpb.AnyValue) []byte {
	t.Helper()
	var out []*tracepb.Span
	for i, attrs := range spans {
		var kvs []*commonpb.KeyValue
		for k, v := range attrs {
			kvs = append(kvs, &commonpb.KeyValue{Key: k, Value: v})
		}
		traceID := make([]byte, 16)
		spanID := make([]byte, 8)
		copy(traceID, "tracetracetracet")
		spanID[0] = byte(i + 1)
		out = append(out, &tracepb.Span{
			TraceId:           traceID,
			SpanId:            spanID,
			Name:              "span",
			StartTimeUnixNano: uint64(time.Now().UnixNano()),
			Attributes:        kvs,
		})
	}
	body, err := proto.Marshal(&tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: out}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func TestDecodeRejectsAnUndecodableBody(t *testing.T) {
	if _, _, err := Decode([]byte("this is not protobuf at all, not even close")); err == nil {
		t.Fatal("expected an error")
	}
}

func TestDecodeAcceptsAnEmptyBody(t *testing.T) {
	events, rejected, err := Decode(nil)
	if err != nil || len(events) != 0 || len(rejected) != 0 {
		t.Fatalf("Decode(nil) = %v, %v, %v", events, rejected, err)
	}
}

func TestDecodeReadsTheAttributes(t *testing.T) {
	id := uuid.New().String()
	body := export(t, map[string]*commonpb.AnyValue{
		event.AttrOp:         sv("source"),
		event.AttrRunID:      sv("run-1"),
		event.AttrAgentID:    sv("crm"),
		event.AttrSourceID:   sv(id),
		event.AttrSourceKind: sv("external_document"),
		event.AttrHMAC:       sv("abc"),
		event.AttrContent:    sv("hello"),
	})
	events, rejected, err := Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(rejected) != 0 {
		t.Fatalf("unexpected rejections: %v", rejected)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	got := events[0]
	if got.Op != event.OpSource || got.RunID != "run-1" || got.Str(event.AttrContent) != "hello" {
		t.Fatalf("decoded wrongly: %+v", got)
	}
	if got.EventID == uuid.Nil {
		t.Fatal("no event id was derived")
	}
}

func TestTheSameSpanAlwaysGetsTheSameEventID(t *testing.T) {
	// An export retry must dedupe rather than duplicate.
	attrs := map[string]*commonpb.AnyValue{
		event.AttrOp:      sv("run"),
		event.AttrRunID:   sv("run-1"),
		event.AttrAgentID: sv("crm"),
	}
	first, _, _ := Decode(export(t, attrs))
	second, _, _ := Decode(export(t, attrs))
	if first[0].EventID != second[0].EventID {
		t.Fatalf("ids differ: %s and %s", first[0].EventID, second[0].EventID)
	}
}

func TestValidate(t *testing.T) {
	good := uuid.New().String()
	cases := []struct {
		name  string
		attrs map[string]any
		want  string // a substring of the expected error, "" for valid
	}{
		{
			name:  "run",
			attrs: map[string]any{event.AttrOp: "run", event.AttrRunID: "r", event.AttrAgentID: "a"},
		},
		{
			name:  "run without an agent",
			attrs: map[string]any{event.AttrOp: "run", event.AttrRunID: "r"},
			want:  event.AttrAgentID,
		},
		{
			name:  "no run id",
			attrs: map[string]any{event.AttrOp: "run", event.AttrAgentID: "a"},
			want:  event.AttrRunID,
		},
		{
			name:  "unknown op",
			attrs: map[string]any{event.AttrOp: "teleport", event.AttrRunID: "r"},
			want:  "unknown",
		},
		{
			name: "source",
			attrs: map[string]any{
				event.AttrOp: "source", event.AttrRunID: "r", event.AttrSourceID: good,
				event.AttrSourceKind: "tool_output", event.AttrHMAC: "h",
			},
		},
		{
			name: "source with a bad kind",
			attrs: map[string]any{
				event.AttrOp: "source", event.AttrRunID: "r", event.AttrSourceID: good,
				event.AttrSourceKind: "rumour", event.AttrHMAC: "h",
			},
			want: "unknown totem.source.kind",
		},
		{
			name: "source with a bad id",
			attrs: map[string]any{
				event.AttrOp: "source", event.AttrRunID: "r", event.AttrSourceID: "nope",
				event.AttrSourceKind: "tool_output", event.AttrHMAC: "h",
			},
			want: "not a uuid",
		},
		{
			name: "memory write",
			attrs: map[string]any{
				event.AttrOp: "memory.write", event.AttrRunID: "r", event.AttrMemoryID: "a/b",
				event.AttrHMAC: "h", event.AttrPrecision: "step",
			},
		},
		{
			name: "memory write with a bad precision",
			attrs: map[string]any{
				event.AttrOp: "memory.write", event.AttrRunID: "r", event.AttrMemoryID: "a/b",
				event.AttrHMAC: "h", event.AttrPrecision: "vibes",
			},
			want: "step or run",
		},
		{
			name: "memory read",
			attrs: map[string]any{
				event.AttrOp: "memory.read", event.AttrRunID: "r", event.AttrReadResults: "[]",
			},
		},
		{
			name:  "memory read without results",
			attrs: map[string]any{event.AttrOp: "memory.read", event.AttrRunID: "r"},
			want:  event.AttrReadResults,
		},
		{
			name: "llm call",
			attrs: map[string]any{
				event.AttrOp: "llm.call", event.AttrRunID: "r", event.AttrLLMCallID: good,
			},
		},
		{
			name: "llm call with a bad id",
			attrs: map[string]any{
				event.AttrOp: "llm.call", event.AttrRunID: "r", event.AttrLLMCallID: "x",
			},
			want: "not a uuid",
		},
		{
			name: "decision",
			attrs: map[string]any{
				event.AttrOp: "decision", event.AttrRunID: "r", event.AttrDecisionID: good,
			},
		},
		{
			name:  "decision without an id",
			attrs: map[string]any{event.AttrOp: "decision", event.AttrRunID: "r"},
			want:  event.AttrDecisionID,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			runID, _ := c.attrs[event.AttrRunID].(string)
			op, _ := c.attrs[event.AttrOp].(string)
			err := Validate(event.Event{Op: event.Op(op), RunID: runID, Attrs: c.attrs})
			switch {
			case c.want == "" && err != nil:
				t.Fatalf("valid event rejected: %v", err)
			case c.want != "" && err == nil:
				t.Fatalf("invalid event accepted, wanted %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Fatalf("error %q does not mention %q", err, c.want)
			}
		})
	}
}

func TestDecodeRejectsOneSpanWithoutLosingTheOthers(t *testing.T) {
	good := map[string]*commonpb.AnyValue{
		event.AttrOp:      sv("run"),
		event.AttrRunID:   sv("run-1"),
		event.AttrAgentID: sv("crm"),
	}
	bad := map[string]*commonpb.AnyValue{
		event.AttrOp:    sv("source"),
		event.AttrRunID: sv("run-1"),
	}
	events, rejected, err := Decode(export(t, good, bad))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(events) != 1 || len(rejected) != 1 {
		t.Fatalf("got %d events and %d rejections, want 1 and 1", len(events), len(rejected))
	}
}

func TestResourceAttributesFillInTheSpan(t *testing.T) {
	body, err := proto.Marshal(&tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				{Key: event.AttrAgentID, Value: sv("crm")},
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
				TraceId:           make([]byte, 16),
				SpanId:            make([]byte, 8),
				StartTimeUnixNano: uint64(time.Now().UnixNano()),
				Attributes: []*commonpb.KeyValue{
					{Key: event.AttrOp, Value: sv("run")},
					{Key: event.AttrRunID, Value: sv("run-1")},
				},
			}}}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	events, rejected, err := Decode(body)
	if err != nil || len(rejected) != 0 {
		t.Fatalf("Decode: %v %v", err, rejected)
	}
	if events[0].Str(event.AttrAgentID) != "crm" {
		t.Fatalf("resource attribute was not merged: %+v", events[0].Attrs)
	}
}
