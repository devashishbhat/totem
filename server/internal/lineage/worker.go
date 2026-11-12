// Package lineage turns raw events into the graph the product reasons about:
// memory versions, DERIVED_FROM and SUPERSEDES edges, trust and taint,
// decision attributions and alerts.
//
// The events table is the queue. The worker claims unprocessed rows in
// (occurred_at, seq) order with FOR UPDATE SKIP LOCKED and processes one batch
// per transaction, so a crash re-processes at most one batch and every write in
// a batch lands together.
package lineage

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/store"
)

// Tunables.
const (
	pollInterval = 500 * time.Millisecond
	batchSize    = 200
	// graceWindow is how long an event waits for something it refers to. After
	// that it is processed anyway with the missing link skipped, so one lost
	// span cannot stall the pipeline.
	graceWindow = 30 * time.Second
	// maxAttempts is how often an event that keeps failing for an unexpected
	// reason is retried before it is dropped, so one bad row cannot stall the
	// queue for ever.
	maxAttempts = 5
)

// errDeferred means the event refers to something that has not arrived yet.
var errDeferred = errors.New("event refers to a row that has not arrived yet")

// Worker is the lineage goroutine.
type Worker struct {
	db  *store.Store
	cfg config.Config
	log *slog.Logger
	now func() time.Time
}

// New builds a worker.
func New(db *store.Store, cfg config.Config, log *slog.Logger) *Worker {
	return &Worker{db: db, cfg: cfg, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// Run polls until the context is cancelled.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	w.log.Info("lineage worker started")
	for {
		select {
		case <-ctx.Done():
			w.log.Info("lineage worker stopped")
			return
		case <-t.C:
			for {
				n, err := w.Tick(ctx)
				if err != nil {
					if ctx.Err() == nil {
						w.log.Error("lineage batch failed", "err", err)
					}
					break
				}
				// Drain a backlog without waiting for the next tick.
				if n < batchSize {
					break
				}
			}
		}
	}
}

// Tick processes one batch and reports how many events it claimed.
func (w *Worker) Tick(ctx context.Context) (int, error) {
	tx, events, err := w.db.ClaimEvents(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if len(events) == 0 {
		return 0, tx.Commit(ctx)
	}

	var processed, deferred []int64
	// A run whose event was deferred must not have later events applied out of
	// order, so the rest of that run waits for the next batch.
	blocked := map[string]bool{}

	for _, ev := range events {
		if blocked[ev.RunID] {
			deferred = append(deferred, ev.Seq)
			continue
		}
		force := w.now().Sub(ev.OccurredAt) > graceWindow

		// Each event gets its own savepoint. Without one, a single failing
		// statement aborts the whole transaction and every later event in the
		// batch fails with it.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return 0, err
		}
		err = w.process(ctx, sp, ev, force)
		switch {
		case err == nil:
			if err := sp.Commit(ctx); err != nil {
				return 0, err
			}
			processed = append(processed, ev.Seq)
		case errors.Is(err, errDeferred):
			_ = sp.Rollback(ctx)
			deferred = append(deferred, ev.Seq)
			blocked[ev.RunID] = true
			w.log.Debug("event deferred", "event_id", ev.EventID, "op", ev.Op)
		default:
			_ = sp.Rollback(ctx)
			if ev.Attempts+1 >= maxAttempts {
				w.log.Error("event dropped after repeated failures",
					"event_id", ev.EventID, "op", ev.Op, "attempts", ev.Attempts+1, "err", err)
				processed = append(processed, ev.Seq)
				break
			}
			w.log.Warn("event will be retried",
				"event_id", ev.EventID, "op", ev.Op, "attempts", ev.Attempts+1, "err", err)
			deferred = append(deferred, ev.Seq)
			blocked[ev.RunID] = true
		}
	}

	if err := store.MarkProcessed(ctx, tx, processed, w.now()); err != nil {
		return 0, err
	}
	if err := store.BumpAttempts(ctx, tx, deferred); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	w.log.Debug("lineage batch", "claimed", len(events), "processed", len(processed), "deferred", len(deferred))
	return len(events), nil
}

func (w *Worker) process(ctx context.Context, tx pgx.Tx, ev store.StoredEvent, force bool) error {
	// Every event names its run, so the run row exists even if the root span
	// was lost. UpsertRun keeps whatever the run event itself recorded.
	if err := store.UpsertRun(ctx, tx, store.Run{
		ID:        ev.RunID,
		AgentID:   ev.Str(event.AttrAgentID),
		StartedAt: ev.OccurredAt,
	}); err != nil {
		return err
	}
	switch ev.Op {
	case "run":
		return w.applyRun(ctx, tx, ev)
	case "source":
		return w.applySource(ctx, tx, ev)
	case "memory.write":
		return w.applyMemoryWrite(ctx, tx, ev, force)
	case "memory.read":
		return w.applyMemoryRead(ctx, tx, ev, force)
	case "llm.call":
		return w.applyLLMCall(ctx, tx, ev)
	case "decision":
		return w.applyDecision(ctx, tx, ev, force)
	}
	return errors.New("unknown op")
}
