// Package ingest accepts OTLP trace exports from the SDK, redacts them a
// second time server-side, and appends them to the events table.
package ingest

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/otlp"
	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/totemtext"
)

// maxBody caps an export at 8 MiB, which is far above a batch of 256 spans.
const maxBody = 8 << 20

// Handler serves POST /v1/traces.
type Handler struct {
	Store     *store.Store
	IngestKey string
	Salt      string
	Log       *slog.Logger
	// RedactPayloads redacts the recorded prompt as well as everything else.
	// Turning it off is what makes replay faithful, at the cost of storing
	// the prompt as the agent sent it: see docs/architecture.md.
	RedactPayloads bool
}

type response struct {
	Accepted int      `json:"accepted"`
	Rejected []string `json:"rejected,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}
	events, rejected, err := otlp.Decode(body)
	if err != nil {
		h.Log.Warn("undecodable export", "err", err)
		http.Error(w, "cannot decode OTLP body", http.StatusBadRequest)
		return
	}
	for i := range events {
		h.redact(&events[i])
	}
	inserted, err := h.Store.InsertEvents(r.Context(), events)
	if err != nil {
		h.Log.Error("insert events", "err", err)
		http.Error(w, "cannot store events", http.StatusInternalServerError)
		return
	}
	if len(rejected) > 0 {
		h.Log.Warn("rejected spans", "count", len(rejected), "first", rejected[0])
	}
	h.Log.Debug("ingested", "spans", len(events), "new", inserted, "rejected", len(rejected))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response{Accepted: len(events), Rejected: rejected})
}

func (h *Handler) authorized(r *http.Request) bool {
	const prefix = "Bearer "
	got := r.Header.Get("Authorization")
	if !strings.HasPrefix(got, prefix) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(got, prefix)), []byte(h.IngestKey)) == 1
}

// textAttrs are the attributes that can hold observed text and are therefore
// redacted again on arrival. The SDK already redacted them; this pass is what
// guarantees the database never holds a raw secret even if an SDK is old or
// misconfigured.
var textAttrs = []string{
	event.AttrContent,
	event.AttrArgs,
	event.AttrOrigin,
}

func (h *Handler) redact(ev *event.Event) {
	keys := textAttrs
	if h.RedactPayloads {
		keys = append(append([]string{}, textAttrs...), event.AttrPayload)
	}
	for _, key := range keys {
		raw, ok := ev.Attrs[key]
		if !ok {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			continue
		}
		red, findings := totemtext.Redact(h.Salt, s)
		if len(findings) > 0 {
			ev.Attrs[key] = red
			ev.Attrs[fmt.Sprintf("totem.redacted.%s", key)] = len(findings)
		}
	}
}
