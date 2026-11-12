package lineage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/store"
)

// ref is one resolved influence on a decision: either a memory version or a
// source, at one attribution level.
type ref struct {
	version *store.MemoryVersion
	source  *store.Source
	level   string
	detail  map[string]any
}

func (w *Worker) applyDecision(ctx context.Context, tx pgx.Tx, ev store.StoredEvent, force bool) error {
	id, err := uuid.Parse(ev.Str(event.AttrDecisionID))
	if err != nil {
		return fmt.Errorf("decision id: %w", err)
	}

	var callID *uuid.UUID
	var call store.LLMCall
	if raw := ev.Str(event.AttrLLMCallID); raw != "" {
		parsed, perr := uuid.Parse(raw)
		if perr != nil {
			return fmt.Errorf("decision llm call id: %w", perr)
		}
		call, err = store.LLMCallByID(ctx, tx, parsed)
		switch {
		case err == nil:
			callID = &parsed
		case errors.Is(err, store.ErrNotFound):
			// The decision span can reach ingest before the llm.call span it
			// belongs to; wait for it, then give up and record the decision
			// without the prompt link.
			if !force {
				return errDeferred
			}
			w.log.Warn("decision kept without its llm call", "decision_id", id, "llm_call_id", parsed)
		default:
			return err
		}
	}

	refs, err := w.collectRefs(ctx, tx, ev, call, force)
	if err != nil {
		return err
	}
	trustMin := decisionTrust(refs)
	taint := decisionTaint(refs)

	d := store.Decision{
		ID:             id,
		RunID:          ev.RunID,
		LLMCallID:      callID,
		Tool:           ev.Str(event.AttrTool),
		SideEffect:     ev.Bool(event.AttrSideEffect),
		Args:           ev.RawJSON(event.AttrArgs),
		TrustMin:       trustMin,
		TaintSourceIDs: taint,
		OccurredAt:     ev.OccurredAt,
	}
	if err := store.InsertDecision(ctx, tx, d); err != nil {
		return err
	}
	for _, r := range refs {
		detail, err := json.Marshal(r.detail)
		if err != nil {
			return err
		}
		a := store.Attribution{DecisionID: id, Level: r.level, Detail: detail}
		if r.version != nil {
			vid := r.version.ID
			a.MemoryVersionID = &vid
		}
		if r.source != nil {
			sid := r.source.ID
			a.SourceID = &sid
		}
		if err := store.InsertAttribution(ctx, tx, a); err != nil {
			return err
		}
	}
	return w.alertSideEffect(ctx, tx, d, refs)
}

// collectRefs builds every attribution for one decision:
//
//	retrieved   memories read earlier in the run
//	in_context  refs the SDK matched inside the prompt of this decision's call
//	value_flow  refs whose text supplied an argument value
func (w *Worker) collectRefs(ctx context.Context, tx pgx.Tx, ev store.StoredEvent,
	call store.LLMCall, force bool) ([]ref, error) {
	var refs []ref

	reads, err := store.MemoryReadsInRun(ctx, tx, ev.RunID)
	if err != nil {
		return nil, err
	}
	seenRetrieved := map[uuid.UUID]bool{}
	for _, rd := range reads {
		if rd.OccurredAt.After(ev.OccurredAt) {
			continue
		}
		var results []resolvedRead
		if err := json.Unmarshal(rd.Results, &results); err != nil {
			continue
		}
		for _, r := range results {
			if r.MemoryVersionID == nil || seenRetrieved[*r.MemoryVersionID] {
				continue
			}
			v, err := store.MemoryVersionByID(ctx, tx, *r.MemoryVersionID)
			if err != nil {
				continue
			}
			seenRetrieved[v.ID] = true
			version := v
			refs = append(refs, ref{
				version: &version,
				level:   store.LevelRetrieved,
				detail:  map[string]any{"rank": r.Rank, "read_at": rd.OccurredAt},
			})
		}
	}

	var inContext []event.InContextRef
	if len(call.InContext) > 0 {
		if err := json.Unmarshal(call.InContext, &inContext); err != nil {
			w.log.Warn("unreadable in_context", "llm_call_id", call.ID, "err", err)
		}
	}
	for _, r := range inContext {
		resolved, err := w.resolveRef(ctx, tx, r.RefType, r.RefID, r.HMAC, force)
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			continue
		}
		resolved.level = store.LevelInContext
		resolved.detail = map[string]any{"match": r.Match, "spans": r.Spans}
		refs = append(refs, *resolved)
	}

	var flow []event.ValueFlowRef
	if err := ev.JSON(event.AttrValueFlow, &flow); err != nil {
		w.log.Warn("unreadable value_flow", "event_id", ev.EventID, "err", err)
	}
	for _, r := range flow {
		resolved, err := w.resolveRef(ctx, tx, r.RefType, r.RefID, r.HMAC, force)
		if err != nil {
			return nil, err
		}
		if resolved == nil {
			continue
		}
		resolved.level = store.LevelValueFlow
		resolved.detail = map[string]any{"arg_path": r.ArgPath, "match": r.Match}
		refs = append(refs, *resolved)
	}
	return refs, nil
}

// resolveRef looks up one in-context or value-flow reference. A reference to
// something that has not arrived defers the event; past the grace window it is
// dropped and nil is returned.
func (w *Worker) resolveRef(ctx context.Context, tx pgx.Tx, refType, refID, hmac string, force bool) (*ref, error) {
	switch refType {
	case "memory":
		v, err := w.resolveMemoryRef(ctx, tx, refID, hmac)
		switch {
		case err == nil:
			version := v
			return &ref{version: &version}, nil
		case errors.Is(err, store.ErrNotFound):
			if !force {
				return nil, errDeferred
			}
			w.log.Warn("decision referred to an unknown memory", "ref_id", refID)
			return nil, nil
		default:
			return nil, err
		}
	case "source":
		id, err := uuid.Parse(refID)
		if err != nil {
			return nil, nil
		}
		s, err := store.SourceByID(ctx, tx, id)
		switch {
		case err == nil:
			source := s
			return &ref{source: &source}, nil
		case errors.Is(err, store.ErrNotFound):
			if !force {
				return nil, errDeferred
			}
			w.log.Warn("decision referred to an unknown source", "ref_id", refID)
			return nil, nil
		default:
			return nil, err
		}
	}
	return nil, nil
}

// decisionTrust is the minimum trust over everything that was in the prompt or
// supplied a value. A memory that was only retrieved does not count: it never
// reached the model.
func decisionTrust(refs []ref) int16 {
	min := int16(store.TrustSystem)
	counted := false
	for _, r := range refs {
		if r.level != store.LevelInContext && r.level != store.LevelValueFlow {
			continue
		}
		counted = true
		switch {
		case r.version != nil && r.version.TrustMin < min:
			min = r.version.TrustMin
		case r.source != nil && r.source.Trust < min:
			min = r.source.Trust
		}
	}
	if !counted {
		return store.TrustSystem
	}
	return min
}

func decisionTaint(refs []ref) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, r := range refs {
		if r.level != store.LevelInContext && r.level != store.LevelValueFlow {
			continue
		}
		if r.version != nil {
			for _, id := range r.version.TaintSourceIDs {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
		}
		if r.source != nil && r.source.Trust <= store.TrustExternal && !seen[r.source.ID] {
			seen[r.source.ID] = true
			out = append(out, r.source.ID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
