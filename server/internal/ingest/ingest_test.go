package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/ingest"
	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/testsupport"
)

const (
	key  = "test-ingest-key"
	salt = "test-salt"
)

func handler(t *testing.T) (*ingest.Handler, *store.Store) {
	t.Helper()
	db := testsupport.DB(t)
	return &ingest.Handler{
		Store:          db,
		IngestKey:      key,
		Salt:           salt,
		RedactPayloads: true,
		Log:            slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}, db
}

func sv(v string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}
}

func body(t *testing.T, spans ...map[string]*commonpb.AnyValue) []byte {
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
			TraceId: traceID, SpanId: spanID, Name: "span",
			StartTimeUnixNano: uint64(time.Now().UnixNano()), Attributes: kvs,
		})
	}
	raw, err := proto.Marshal(&tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: out}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func post(h *ingest.Handler, bearer string, payload []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(payload))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func runSpan() map[string]*commonpb.AnyValue {
	return map[string]*commonpb.AnyValue{
		event.AttrOp:      sv("run"),
		event.AttrRunID:   sv("run-1"),
		event.AttrAgentID: sv("crm"),
	}
}

func TestAuthorization(t *testing.T) {
	h, _ := handler(t)
	payload := body(t, runSpan())
	cases := []struct {
		name   string
		bearer string
		want   int
	}{
		{"the right key", key, http.StatusAccepted},
		{"the wrong key", "nope", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := post(h, c.bearer, payload).Code; got != c.want {
				t.Fatalf("status %d, want %d", got, c.want)
			}
		})
	}
}

func TestANonPostIsRefused(t *testing.T) {
	h, _ := handler(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/traces", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestAnUndecodableBodyIsABadRequest(t *testing.T) {
	h, _ := handler(t)
	rec := post(h, key, []byte("definitely not protobuf, just some prose"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestSpansBecomeEvents(t *testing.T) {
	h, db := handler(t)
	rec := post(h, key, body(t, runSpan()))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Accepted int      `json:"accepted"`
		Rejected []string `json:"rejected"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Accepted != 1 || len(resp.Rejected) != 0 {
		t.Fatalf("response is %+v", resp)
	}
	total, pending, err := db.CountEvents(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 1 || pending != 1 {
		t.Fatalf("stored %d events, %d pending", total, pending)
	}
}

func TestAnInvalidSpanIsReportedWithoutLosingTheRest(t *testing.T) {
	h, db := handler(t)
	bad := map[string]*commonpb.AnyValue{
		event.AttrOp:    sv("source"),
		event.AttrRunID: sv("run-1"),
	}
	rec := post(h, key, body(t, runSpan(), bad))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "totem.source.id") {
		t.Fatalf("the response does not say what was wrong: %s", rec.Body.String())
	}
	total, _, _ := db.CountEvents(context.Background())
	if total != 1 {
		t.Fatalf("stored %d events, want 1", total)
	}
}

func TestResendingTheSameExportIsIdempotent(t *testing.T) {
	// The SDK retries, and a retry must not duplicate a run.
	h, db := handler(t)
	payload := body(t, runSpan())
	for i := 0; i < 3; i++ {
		if got := post(h, key, payload).Code; got != http.StatusAccepted {
			t.Fatalf("attempt %d: status %d", i, got)
		}
	}
	total, _, _ := db.CountEvents(context.Background())
	if total != 1 {
		t.Fatalf("stored %d events, want 1", total)
	}
}

func TestTheServerRedactsAgain(t *testing.T) {
	// An old or misconfigured SDK could send a raw value. Nothing reaches the
	// database unredacted.
	h, db := handler(t)
	span := map[string]*commonpb.AnyValue{
		event.AttrOp:         sv("source"),
		event.AttrRunID:      sv("run-1"),
		event.AttrAgentID:    sv("crm"),
		event.AttrSourceID:   sv(uuid.New().String()),
		event.AttrSourceKind: sv("external_document"),
		event.AttrHMAC:       sv("h"),
		event.AttrContent:    sv("invoices go to billing@acme-payments.example"),
	}
	if got := post(h, key, body(t, span)).Code; got != http.StatusAccepted {
		t.Fatalf("status %d", got)
	}
	var content string
	err := db.Pool.QueryRow(context.Background(),
		`select attrs ->> 'totem.content' from events limit 1`).Scan(&content)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if strings.Contains(content, "billing@acme-payments.example") {
		t.Fatalf("the raw address was stored: %q", content)
	}
	if !strings.Contains(content, "[EMAIL:") {
		t.Fatalf("no placeholder in %q", content)
	}
	var count int
	if err := db.Pool.QueryRow(context.Background(),
		`select (attrs ->> 'totem.redacted.totem.content')::int from events limit 1`).Scan(&count); err != nil {
		t.Fatalf("query redaction count: %v", err)
	}
	if count != 1 {
		t.Fatalf("redaction count is %d, want 1", count)
	}
}

func TestThePromptCanBeKeptVerbatimForReplay(t *testing.T) {
	// A redacted prompt no longer holds the value the agent acted on, so
	// replay cannot reproduce the decision. An operator can trade that
	// privacy for fidelity; everything else stays redacted either way.
	db := testsupport.DB(t)
	h := &ingest.Handler{
		Store: db, IngestKey: key, Salt: salt, RedactPayloads: false,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	span := map[string]*commonpb.AnyValue{
		event.AttrOp:        sv("llm.call"),
		event.AttrRunID:     sv("run-1"),
		event.AttrAgentID:   sv("crm"),
		event.AttrLLMCallID: sv(uuid.New().String()),
		event.AttrPayload:   sv(`{"messages":[{"content":"send to billing@acme-payments.example"}]}`),
		event.AttrContent:   sv("remember billing@acme-payments.example"),
	}
	if got := post(h, key, body(t, span)).Code; got != http.StatusAccepted {
		t.Fatalf("status %d", got)
	}
	var payload, content string
	err := db.Pool.QueryRow(context.Background(),
		`select attrs ->> 'totem.llm.payload', attrs ->> 'totem.content' from events limit 1`).
		Scan(&payload, &content)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(payload, "billing@acme-payments.example") {
		t.Fatalf("the prompt was redacted anyway: %q", payload)
	}
	if strings.Contains(content, "billing@acme-payments.example") {
		t.Fatalf("content should still be redacted: %q", content)
	}
}
