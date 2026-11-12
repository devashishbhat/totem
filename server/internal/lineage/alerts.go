package lineage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/totemtext"
)

// flowEvidence is one value-flow attribution as the alert records it: which
// argument was supplied, from what, and the untrusted sources behind it.
type flowEvidence struct {
	ArgPath   string      `json:"arg_path"`
	Match     string      `json:"match"`
	MemoryKey string      `json:"memory_key,omitempty"`
	Origin    string      `json:"origin,omitempty"`
	TaintFrom []uuid.UUID `json:"taint_from,omitempty"`
}

// alertSideEffect raises taint.side_effect_from_untrusted: a tool call that
// changes the world, driven by text the agent should not have trusted.
// It is critical when untrusted text supplied an argument value, because then
// the attacker chose the value, not just the context.
func (w *Worker) alertSideEffect(ctx context.Context, tx pgx.Tx, d store.Decision, refs []ref) error {
	if !d.SideEffect || d.TrustMin > store.TrustExternal {
		return nil
	}
	severity := "high"
	var flows []flowEvidence
	for _, r := range refs {
		if r.level != store.LevelValueFlow {
			continue
		}
		fe := flowEvidence{
			ArgPath: str(r.detail["arg_path"]),
			Match:   str(r.detail["match"]),
		}
		tainted := false
		if r.version != nil {
			fe.TaintFrom = r.version.TaintSourceIDs
			if m, err := store.MemoryByID(ctx, tx, r.version.MemoryID); err == nil {
				fe.MemoryKey = m.Key
			}
			// Where the memory's content entered the agent. Without it the
			// alert can say a value was not the user's but not where it came
			// from, which is the first thing anyone asks.
			fe.Origin = taintOrigin(ctx, tx, r.version.TaintSourceIDs)
			tainted = len(r.version.TaintSourceIDs) > 0 || r.version.TrustMin <= store.TrustExternal
		}
		if r.source != nil {
			fe.Origin = r.source.Origin
			tainted = r.source.Trust <= store.TrustExternal
		}
		if tainted {
			severity = "critical"
		}
		flows = append(flows, fe)
	}

	title := sideEffectTitle(d, refs, flows, severity)
	evidence, err := json.Marshal(map[string]any{
		"decision_id": d.ID,
		"run_id":      d.RunID,
		"tool":        d.Tool,
		"trust_min":   d.TrustMin,
		"tier":        store.TierName(d.TrustMin),
		"value_flow":  flows,
		"taint_from":  d.TaintSourceIDs,
	})
	if err != nil {
		return err
	}
	return store.UpsertAlert(ctx, tx, store.Alert{
		Kind:      store.AlertSideEffectFromUntrusted,
		Severity:  severity,
		SubjectID: d.ID,
		RunID:     d.RunID,
		Title:     title,
		Evidence:  evidence,
		CreatedAt: d.OccurredAt,
	})
}

// taintOrigin names where untrusted content entered, following a memory
// version's recorded taint sources. It returns "" when nothing is recorded.
func taintOrigin(ctx context.Context, tx pgx.Tx, ids []uuid.UUID) string {
	if len(ids) == 0 {
		return ""
	}
	sources, err := store.SourcesByIDs(ctx, tx, ids)
	if err != nil {
		return ""
	}
	// A fetched document names a place; other kinds name a tool, which is
	// less useful but better than nothing.
	for _, s := range sources {
		if s.Kind == "external_document" && s.Origin != "" {
			return s.Origin
		}
	}
	for _, s := range sources {
		if s.Origin != "" {
			return s.Origin
		}
	}
	return ""
}

// sideEffectTitle says what actually made the decision untrusted.
//
// It is not always a memory. An untrusted page fetched earlier in the same
// conversation stays in the prompt, and that is enough on its own to pull the
// decision's trust down, with no memory involved. Saying "ran on untrusted
// memory" there would send whoever reads the alert looking for a memory that
// does not exist.
func sideEffectTitle(d store.Decision, refs []ref, flows []flowEvidence, severity string) string {
	tool := toolName(d.Tool)
	if severity == "critical" {
		path := "an argument"
		if len(flows) > 0 && flows[0].ArgPath != "" {
			path = "`" + flows[0].ArgPath + "`"
		}
		// A value can flow from a source directly, without being remembered.
		if flowedFromMemory(refs) {
			return fmt.Sprintf("Untrusted memory chose %s of %s", path, tool)
		}
		return fmt.Sprintf("Untrusted text chose %s of %s", path, tool)
	}
	if untrustedMemoryInContext(refs) {
		return fmt.Sprintf("%s ran on untrusted memory", tool)
	}
	if origin := untrustedSourceInContext(refs); origin != "" {
		return fmt.Sprintf("%s ran with untrusted text from %s in its prompt", tool, origin)
	}
	return fmt.Sprintf("%s ran on untrusted text", tool)
}

// counts returns whether a ref is one of the levels that decide trust. A
// memory that was only retrieved never reached the model, so it is not one.
func counts(r ref) bool {
	return r.level == store.LevelInContext || r.level == store.LevelValueFlow
}

func flowedFromMemory(refs []ref) bool {
	for _, r := range refs {
		if r.level == store.LevelValueFlow && r.version != nil &&
			(len(r.version.TaintSourceIDs) > 0 || r.version.TrustMin <= store.TrustExternal) {
			return true
		}
	}
	return false
}

func untrustedMemoryInContext(refs []ref) bool {
	for _, r := range refs {
		if counts(r) && r.version != nil && r.version.TrustMin <= store.TrustExternal {
			return true
		}
	}
	return false
}

// untrustedSourceInContext names where the untrusted text came from, which for
// a fetched document is its URL.
//
// Several untrusted sources can be in one prompt, and they are not equally
// worth naming: a fetched page is somebody else's words, while the output of
// an unvouched-for tool is often the agent's own. The page is named first, and
// a lower tier beats a higher one.
func untrustedSourceInContext(refs []ref) string {
	best := ""
	bestRank := -1
	for _, r := range refs {
		if !counts(r) || r.source == nil || r.source.Trust > store.TrustExternal {
			continue
		}
		rank := int(store.TrustExternal-r.source.Trust) * 2
		if r.source.Kind == "external_document" {
			rank++
		}
		if rank <= bestRank {
			continue
		}
		bestRank = rank
		if best = r.source.Origin; best == "" {
			best = "an untrusted source"
		}
	}
	return best
}

// alertUntrustedContact raises memory.untrusted_contact_value: an untrusted
// memory that holds a contact detail, which is the shape a payment-redirect
// poisoning takes.
func (w *Worker) alertUntrustedContact(ctx context.Context, tx pgx.Tx, runID string,
	mem store.Memory, v store.MemoryVersion, reason string) error {
	if v.Deleted || v.TrustMin > store.TrustExternal || !totemtext.HasContactPlaceholder(v.Content) {
		return nil
	}
	evidence, err := json.Marshal(map[string]any{
		"memory_id":         mem.ID,
		"memory_key":        mem.Key,
		"memory_version_id": v.ID,
		"version":           v.Version,
		"run_id":            runID,
		"trust_min":         v.TrustMin,
		"tier":              store.TierName(v.TrustMin),
		"trust_reason":      reason,
		"taint_from":        v.TaintSourceIDs,
	})
	if err != nil {
		return err
	}
	return store.UpsertAlert(ctx, tx, store.Alert{
		Kind:      store.AlertUntrustedContactValue,
		Severity:  "medium",
		SubjectID: v.ID,
		RunID:     runID,
		Title:     fmt.Sprintf("Untrusted memory %s holds a contact detail", mem.Key),
		Evidence:  evidence,
		CreatedAt: v.RecordedAt,
	})
}

func toolName(t string) string {
	if t == "" {
		return "A final answer"
	}
	return t
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
