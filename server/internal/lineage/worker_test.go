package lineage

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/testsupport"
)

// quiet keeps the expected warnings out of the test output.
func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestAnEventWaitsForWhatItRefersTo(t *testing.T) {
	db := testsupport.DB(t)
	ctx := context.Background()
	worker := New(db, config.Config{TrustedTools: map[string]bool{}}, quiet())

	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	// The clock sits just inside the grace window, so a missing reference
	// defers the event instead of being skipped.
	worker.now = func() time.Time { return at.Add(5 * time.Second) }

	sourceID := uuid.New()
	write := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-1",
		Op:         event.OpMemoryWrite,
		OccurredAt: at,
		Attrs: map[string]any{
			event.AttrOp:        "memory.write",
			event.AttrRunID:     "run-1",
			event.AttrMemoryID:  "crm/memories/acme.note",
			event.AttrHMAC:      "hmac-1",
			event.AttrContent:   "Invoices go to [EMAIL:123456]",
			event.AttrSourceIDs: []any{sourceID.String()},
			event.AttrPrecision: "step",
		},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{write}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := worker.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if _, err := store.MemoryByKey(ctx, db.Pool, "crm/memories/acme.note"); err == nil {
		t.Fatal("the write was applied before its source arrived")
	}
	stored, err := db.EventByID(ctx, write.EventID)
	if err != nil {
		t.Fatalf("event: %v", err)
	}
	if stored.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", stored.Attempts)
	}

	// The source arrives; the write now resolves and carries its trust.
	source := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-1",
		Op:         event.OpSource,
		OccurredAt: at.Add(-time.Second),
		Attrs: map[string]any{
			event.AttrOp:         "source",
			event.AttrRunID:      "run-1",
			event.AttrSourceID:   sourceID.String(),
			event.AttrSourceKind: "external_document",
			event.AttrOrigin:     "https://vendor.example/notice",
			event.AttrHMAC:       "src-hmac",
			event.AttrContent:    "Invoices go to [EMAIL:123456]",
		},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{source}); err != nil {
		t.Fatalf("insert source: %v", err)
	}
	if _, err := worker.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mem, err := store.MemoryByKey(ctx, db.Pool, "crm/memories/acme.note")
	if err != nil {
		t.Fatalf("memory: %v", err)
	}
	version, err := store.CurrentMemoryVersion(ctx, db.Pool, mem.ID)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version.TrustMin != store.TrustExternal || len(version.TaintSourceIDs) != 1 {
		t.Fatalf("trust_min = %d with %d tainted sources", version.TrustMin, len(version.TaintSourceIDs))
	}
	if _, pending, _ := db.CountEvents(ctx); pending != 0 {
		t.Fatalf("%d events still pending", pending)
	}
}

func TestAfterTheGraceWindowTheMissingLinkIsSkipped(t *testing.T) {
	db := testsupport.DB(t)
	ctx := context.Background()
	worker := New(db, config.Config{TrustedTools: map[string]bool{}}, quiet())

	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	// One lost span must not stall the pipeline for ever.
	worker.now = func() time.Time { return at.Add(graceWindow + time.Second) }

	write := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-1",
		Op:         event.OpMemoryWrite,
		OccurredAt: at,
		Attrs: map[string]any{
			event.AttrOp:        "memory.write",
			event.AttrRunID:     "run-1",
			event.AttrMemoryID:  "crm/memories/acme.note",
			event.AttrHMAC:      "hmac-1",
			event.AttrContent:   "Acme prefers morning deliveries.",
			event.AttrSourceIDs: []any{uuid.New().String()},
			event.AttrPrecision: "step",
		},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{write}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := worker.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	mem, err := store.MemoryByKey(ctx, db.Pool, "crm/memories/acme.note")
	if err != nil {
		t.Fatalf("the write was never applied: %v", err)
	}
	version, err := store.CurrentMemoryVersion(ctx, db.Pool, mem.ID)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	// With the source skipped, nothing is known about where this came from.
	if version.TrustMin != store.TrustUnknown {
		t.Fatalf("trust_min = %d, want %d", version.TrustMin, store.TrustUnknown)
	}
	if _, pending, _ := db.CountEvents(ctx); pending != 0 {
		t.Fatalf("%d events still pending", pending)
	}
}

func TestOneBadEventDoesNotTakeTheBatchWithIt(t *testing.T) {
	db := testsupport.DB(t)
	ctx := context.Background()
	worker := New(db, config.Config{TrustedTools: map[string]bool{}}, quiet())
	at := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	worker.now = func() time.Time { return at.Add(graceWindow + time.Second) }

	// A memory.write with no memory id cannot be applied; the source after it
	// must still land. Without a savepoint per event, the failed statement
	// would abort the whole batch.
	bad := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-1",
		Op:         event.OpMemoryWrite,
		OccurredAt: at,
		Attrs: map[string]any{
			event.AttrOp:        "memory.write",
			event.AttrRunID:     "run-1",
			event.AttrHMAC:      "hmac-1",
			event.AttrPrecision: "step",
		},
	}
	good := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-2",
		Op:         event.OpSource,
		OccurredAt: at.Add(time.Second),
		Attrs: map[string]any{
			event.AttrOp:         "source",
			event.AttrRunID:      "run-2",
			event.AttrSourceID:   uuid.New().String(),
			event.AttrSourceKind: "user_message",
			event.AttrHMAC:       "src-hmac",
			event.AttrContent:    "please send the invoice",
		},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{bad, good}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := worker.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	sources, err := store.SourcesInRun(ctx, db.Pool, "run-2")
	if err != nil {
		t.Fatalf("sources: %v", err)
	}
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
}

func TestAnEventThatKeepsFailingIsEventuallyDropped(t *testing.T) {
	db := testsupport.DB(t)
	ctx := context.Background()
	worker := New(db, config.Config{TrustedTools: map[string]bool{}}, quiet())
	at := time.Now().UTC()
	worker.now = func() time.Time { return at }

	// An op the worker does not know about fails every time. It must not sit
	// at the head of the queue for ever.
	bad := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-1",
		Op:         event.Op("teleport"),
		OccurredAt: at,
		Attrs:      map[string]any{event.AttrOp: "teleport", event.AttrRunID: "run-1"},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{bad}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	for i := 0; i < maxAttempts+1; i++ {
		if _, err := worker.Tick(ctx); err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if _, pending, _ := db.CountEvents(ctx); pending != 0 {
		t.Fatalf("%d events still pending after %d attempts", pending, maxAttempts+1)
	}
}

func TestEveryEventEnsuresItsRunExists(t *testing.T) {
	// A lost run span must not hide the run from the runs list.
	db := testsupport.DB(t)
	ctx := context.Background()
	worker := New(db, config.Config{TrustedTools: map[string]bool{}}, quiet())
	at := time.Now().UTC().Add(-time.Minute)
	worker.now = func() time.Time { return at.Add(time.Minute) }

	source := event.Event{
		EventID:    uuid.New(),
		RunID:      "run-orphan",
		Op:         event.OpSource,
		OccurredAt: at,
		Attrs: map[string]any{
			event.AttrOp:         "source",
			event.AttrRunID:      "run-orphan",
			event.AttrAgentID:    "crm",
			event.AttrSourceID:   uuid.New().String(),
			event.AttrSourceKind: "user_message",
			event.AttrHMAC:       "h",
			event.AttrContent:    "hello",
		},
	}
	if _, err := db.InsertEvents(ctx, []event.Event{source}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := worker.Tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if _, err := store.RunByID(ctx, db.Pool, "run-orphan"); err != nil {
		t.Fatalf("run was not created: %v", err)
	}
}
