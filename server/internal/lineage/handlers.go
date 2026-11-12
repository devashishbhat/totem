package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/store"
)

func (w *Worker) applyRun(ctx context.Context, tx pgx.Tx, ev store.StoredEvent) error {
	return store.UpsertRun(ctx, tx, store.Run{
		ID:        ev.RunID,
		AgentID:   ev.Str(event.AttrAgentID),
		Scope:     ev.Str(event.AttrRunScope),
		StartedAt: ev.OccurredAt,
	})
}

func (w *Worker) applySource(ctx context.Context, tx pgx.Tx, ev store.StoredEvent) error {
	id, err := uuid.Parse(ev.Str(event.AttrSourceID))
	if err != nil {
		return fmt.Errorf("source id: %w", err)
	}
	kind := ev.Str(event.AttrSourceKind)
	origin := ev.Str(event.AttrOrigin)
	explicit, hasExplicit := ev.Int(event.AttrTrust)
	trust, reason := w.trustOf(kind, origin, explicit, hasExplicit)
	return store.InsertSource(ctx, tx, store.Source{
		ID:          id,
		RunID:       ev.RunID,
		Kind:        kind,
		Origin:      origin,
		HMAC:        ev.Str(event.AttrHMAC),
		Content:     ev.Str(event.AttrContent),
		Trust:       trust,
		TrustReason: reason,
		ObservedAt:  ev.OccurredAt,
	})
}

func (w *Worker) applyMemoryWrite(ctx context.Context, tx pgx.Tx, ev store.StoredEvent, force bool) error {
	key := ev.Str(event.AttrMemoryID)
	if key == "" {
		return errors.New("memory.write without a memory id")
	}
	hmac := ev.Str(event.AttrHMAC)
	deleted := ev.Bool(event.AttrDeleted)

	mem, err := store.EnsureMemory(ctx, tx, key)
	if err != nil {
		return err
	}
	cur, curErr := store.CurrentMemoryVersion(ctx, tx, mem.ID)
	if curErr != nil && !errors.Is(curErr, store.ErrNotFound) {
		return curErr
	}
	hasCurrent := curErr == nil
	// An unchanged write is not a new version.
	if hasCurrent && cur.HMAC == hmac && cur.Deleted == deleted {
		return nil
	}

	par, err := w.resolveParents(ctx, tx, ev, force)
	if err != nil {
		return err
	}
	trustMin, reason := par.trustMin()

	version := 1
	if hasCurrent {
		version = cur.Version + 1
	}
	nv := store.MemoryVersion{
		ID:               uuid.New(),
		MemoryID:         mem.ID,
		Version:          version,
		HMAC:             hmac,
		Content:          ev.Str(event.AttrContent),
		Deleted:          deleted,
		LineagePrecision: ev.Str(event.AttrPrecision),
		TrustMin:         trustMin,
		TaintSourceIDs:   par.taint(),
		RecordedAt:       ev.OccurredAt,
	}
	if err := store.InsertMemoryVersion(ctx, tx, nv); err != nil {
		return err
	}
	for _, s := range par.sources {
		if err := store.InsertEdge(ctx, tx, store.Edge{
			Kind: "DERIVED_FROM", FromID: nv.ID, ToID: s.ID, ToType: "source"}); err != nil {
			return err
		}
	}
	for _, v := range par.versions {
		if err := store.InsertEdge(ctx, tx, store.Edge{
			Kind: "DERIVED_FROM", FromID: nv.ID, ToID: v.ID, ToType: "memory_version"}); err != nil {
			return err
		}
	}
	if hasCurrent {
		if err := store.SupersedeMemoryVersion(ctx, tx, cur.ID, ev.OccurredAt); err != nil {
			return err
		}
		if err := store.InsertEdge(ctx, tx, store.Edge{
			Kind: "SUPERSEDES", FromID: nv.ID, ToID: cur.ID, ToType: "memory_version"}); err != nil {
			return err
		}
	}
	status := "active"
	if deleted {
		status = "deleted"
	}
	if err := store.SetMemoryHead(ctx, tx, mem.ID, version, status); err != nil {
		return err
	}
	return w.alertUntrustedContact(ctx, tx, ev.RunID, mem, nv, reason)
}

// resolveParents turns the DERIVED_FROM attributes of a memory.write into the
// rows they name. A name that has not arrived yet defers the event, unless the
// grace window has passed, in which case the link is skipped.
func (w *Worker) resolveParents(ctx context.Context, tx pgx.Tx, ev store.StoredEvent, force bool) (parents, error) {
	var par parents
	for _, raw := range ev.StrSlice(event.AttrSourceIDs) {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		s, err := store.SourceByID(ctx, tx, id)
		switch {
		case err == nil:
			par.sources = append(par.sources, s)
		case errors.Is(err, store.ErrNotFound):
			if !force {
				return par, errDeferred
			}
			w.log.Warn("memory.write skipped a missing source", "source_id", id, "event_id", ev.EventID)
		default:
			return par, err
		}
	}
	var derived []string
	if err := ev.JSON(event.AttrDerivedFrom, &derived); err != nil {
		w.log.Warn("unreadable derived_from", "event_id", ev.EventID, "err", err)
	}
	for _, parentKey := range derived {
		pm, err := store.MemoryByKey(ctx, tx, parentKey)
		if errors.Is(err, store.ErrNotFound) {
			if !force {
				return par, errDeferred
			}
			continue
		} else if err != nil {
			return par, err
		}
		pv, err := store.CurrentMemoryVersion(ctx, tx, pm.ID)
		if errors.Is(err, store.ErrNotFound) {
			if !force {
				return par, errDeferred
			}
			continue
		} else if err != nil {
			return par, err
		}
		par.versions = append(par.versions, pv)
	}
	return par, nil
}

// resolvedRead is a read result with the version it points at, so the API does
// not have to resolve fingerprints again.
type resolvedRead struct {
	MemoryID        string     `json:"memory_id"`
	HMAC            string     `json:"hmac"`
	Rank            float64    `json:"rank"`
	MemoryVersionID *uuid.UUID `json:"memory_version_id"`
	TrustMin        *int16     `json:"trust_min"`
	Content         string     `json:"content"`
}

func (w *Worker) applyMemoryRead(ctx context.Context, tx pgx.Tx, ev store.StoredEvent, force bool) error {
	var results []event.ReadResult
	if err := ev.JSON(event.AttrReadResults, &results); err != nil {
		return err
	}
	resolved := make([]resolvedRead, 0, len(results))
	for _, r := range results {
		out := resolvedRead{MemoryID: r.MemoryID, HMAC: r.HMAC, Rank: r.Rank}
		v, err := w.resolveMemoryRef(ctx, tx, r.MemoryID, r.HMAC)
		switch {
		case err == nil:
			id, trust := v.ID, v.TrustMin
			out.MemoryVersionID, out.TrustMin, out.Content = &id, &trust, v.Content
		case errors.Is(err, store.ErrNotFound):
			if !force {
				return errDeferred
			}
		default:
			return err
		}
		resolved = append(resolved, out)
	}
	payload, err := json.Marshal(resolved)
	if err != nil {
		return err
	}
	return store.InsertMemoryRead(ctx, tx, store.MemoryRead{
		ID:         ev.EventID,
		RunID:      ev.RunID,
		OccurredAt: ev.OccurredAt,
		Results:    payload,
	})
}

// resolveMemoryRef finds the version a (key, fingerprint) pair names, falling
// back to the fingerprint alone and then to the memory's current version.
func (w *Worker) resolveMemoryRef(ctx context.Context, tx pgx.Tx, key, hmac string) (store.MemoryVersion, error) {
	if key != "" && hmac != "" {
		if v, err := store.MemoryVersionByKeyHMAC(ctx, tx, key, hmac); err == nil {
			return v, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return v, err
		}
	}
	if hmac != "" {
		if v, err := store.MemoryVersionByHMAC(ctx, tx, hmac); err == nil {
			return v, nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return v, err
		}
	}
	if key != "" {
		m, err := store.MemoryByKey(ctx, tx, key)
		if err != nil {
			return store.MemoryVersion{}, err
		}
		return store.CurrentMemoryVersion(ctx, tx, m.ID)
	}
	return store.MemoryVersion{}, store.ErrNotFound
}

func (w *Worker) applyLLMCall(ctx context.Context, tx pgx.Tx, ev store.StoredEvent) error {
	id, err := uuid.Parse(ev.Str(event.AttrLLMCallID))
	if err != nil {
		return fmt.Errorf("llm call id: %w", err)
	}
	return store.InsertLLMCall(ctx, tx, store.LLMCall{
		ID:         id,
		RunID:      ev.RunID,
		Model:      ev.Str(event.AttrModel),
		InContext:  ev.RawJSON(event.AttrInContext),
		Payload:    ev.RawJSON(event.AttrPayload),
		OccurredAt: ev.OccurredAt,
	})
}
