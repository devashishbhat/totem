package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/devashishbhat/totem/server/internal/event"
)

// StoredEvent is an events row.
type StoredEvent struct {
	Seq      int64
	Attempts int
	event.Event
}

// InsertEvents writes raw spans. Rows whose event_id is already present are
// ignored, which makes an export retry idempotent. It returns how many rows
// were new.
func (s *Store) InsertEvents(ctx context.Context, evs []event.Event) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inserted := 0
	for _, ev := range evs {
		attrs, err := json.Marshal(ev.Attrs)
		if err != nil {
			return inserted, fmt.Errorf("encode attrs: %w", err)
		}
		tag, err := tx.Exec(ctx, `
			insert into events (event_id, run_id, op, occurred_at, attrs)
			values ($1, $2, $3, $4, $5)
			on conflict (event_id) do nothing`,
			ev.EventID, ev.RunID, string(ev.Op), ev.OccurredAt, attrs)
		if err != nil {
			return inserted, fmt.Errorf("insert event: %w", err)
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, tx.Commit(ctx)
}

// ClaimEvents locks the next batch of unprocessed events in causal order. The
// caller must commit or roll back the returned transaction.
func (s *Store) ClaimEvents(ctx context.Context, limit int) (pgx.Tx, []StoredEvent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `
		select seq, event_id, run_id, op, occurred_at, attrs, attempts
		from events
		where processed_at is null
		order by occurred_at, seq
		limit $1
		for update skip locked`, limit)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	defer rows.Close()

	var out []StoredEvent
	for rows.Next() {
		var e StoredEvent
		var op string
		var attrs []byte
		if err := rows.Scan(&e.Seq, &e.EventID, &e.RunID, &op, &e.OccurredAt, &attrs, &e.Attempts); err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, err
		}
		e.Op = event.Op(op)
		if err := json.Unmarshal(attrs, &e.Attrs); err != nil {
			_ = tx.Rollback(ctx)
			return nil, nil, fmt.Errorf("decode attrs of event %s: %w", e.EventID, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		_ = tx.Rollback(ctx)
		return nil, nil, err
	}
	return tx, out, nil
}

// MarkProcessed stamps events as done inside the worker's transaction.
func MarkProcessed(ctx context.Context, tx pgx.Tx, seqs []int64, at time.Time) error {
	if len(seqs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `update events set processed_at = $2 where seq = any($1)`, seqs, at)
	return err
}

// BumpAttempts records that an event was deferred because something it refers
// to has not arrived yet.
func BumpAttempts(ctx context.Context, tx pgx.Tx, seqs []int64) error {
	if len(seqs) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `update events set attempts = attempts + 1 where seq = any($1)`, seqs)
	return err
}

// CountEvents reports how many events are stored and how many still wait.
func (s *Store) CountEvents(ctx context.Context) (total, pending int, err error) {
	err = s.Pool.QueryRow(ctx,
		`select count(*), count(*) filter (where processed_at is null) from events`).Scan(&total, &pending)
	return
}

// EventByID is used by tests and by the ingest dedupe check.
func (s *Store) EventByID(ctx context.Context, id uuid.UUID) (StoredEvent, error) {
	var e StoredEvent
	var op string
	var attrs []byte
	err := s.Pool.QueryRow(ctx, `
		select seq, event_id, run_id, op, occurred_at, attrs, attempts
		from events where event_id = $1`, id).
		Scan(&e.Seq, &e.EventID, &e.RunID, &op, &e.OccurredAt, &attrs, &e.Attempts)
	if err != nil {
		return e, err
	}
	e.Op = event.Op(op)
	err = json.Unmarshal(attrs, &e.Attrs)
	return e, err
}
