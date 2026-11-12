package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

// levelRank orders attribution levels from weakest to strongest evidence.
var levelRank = map[string]int{
	store.LevelRetrieved:      1,
	store.LevelInContext:      2,
	store.LevelValueFlow:      3,
	store.LevelCounterfactual: 4,
}

type explainResponse struct {
	Decision decisionView    `json:"decision"`
	Run      store.Run       `json:"run"`
	LLMCall  *llmCallView    `json:"llm_call"`
	Alerts   []alertView     `json:"alerts"`
	Memories []influenceView `json:"memories"`
	Sources  []influenceView `json:"sources"`
	Replays  []replayView    `json:"replays"`
	DemoMode bool            `json:"demo_mode"`
}

type decisionView struct {
	store.Decision
	Tier      string    `json:"tier"`
	ArgLeaves []argLeaf `json:"arg_leaves"`
}

type argLeaf struct {
	Path  string `json:"path"`
	Value string `json:"value"`
}

type llmCallView struct {
	ID         string `json:"id"`
	Model      string `json:"model"`
	OccurredAt string `json:"occurred_at"`
}

// influenceView is one thing that influenced the decision, with every
// attribution level that was established for it and the exact label the
// dashboard shows.
type influenceView struct {
	Kind        string      `json:"kind"` // memory | source
	ID          string      `json:"id"`
	MemoryID    string      `json:"memory_id,omitempty"`
	MemoryKey   string      `json:"memory_key,omitempty"`
	Version     int         `json:"version,omitempty"`
	Origin      string      `json:"origin,omitempty"`
	SourceKind  string      `json:"source_kind,omitempty"`
	Content     string      `json:"content"`
	TrustMin    int16       `json:"trust_min"`
	Tier        string      `json:"tier"`
	TrustReason string      `json:"trust_reason,omitempty"`
	Level       string      `json:"level"`
	Levels      []string    `json:"levels"`
	Labels      []string    `json:"labels"`
	ArgPaths    []string    `json:"arg_paths,omitempty"`
	Spans       [][3]int    `json:"spans,omitempty"`
	TaintPath   []pathNode  `json:"taint_path,omitempty"`
	Replay      *replayView `json:"replay,omitempty"`
}

func (s *Server) explain(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "decision id must be a uuid")
		return
	}
	d, err := store.DecisionByID(ctx, s.db.Pool, id)
	if err != nil {
		s.fail(w, err, "decision")
		return
	}
	run, err := store.RunByID(ctx, s.db.Pool, d.RunID)
	if err != nil && err != store.ErrNotFound {
		s.fail(w, err, "run")
		return
	}

	resp := explainResponse{
		Decision: decisionView{Decision: d, Tier: store.TierName(d.TrustMin), ArgLeaves: flattenArgs(d.Args)},
		Run:      run,
		DemoMode: s.cfg.DemoMode,
		// Every list in the response is a list, never null. A nil slice
		// marshals to JSON null, and a client that reasonably trusts the
		// declared type crashes on the first decision that has no alerts.
		Alerts:   []alertView{},
		Memories: []influenceView{},
		Sources:  []influenceView{},
		Replays:  []replayView{},
	}
	if d.LLMCallID != nil {
		if c, err := store.LLMCallByID(ctx, s.db.Pool, *d.LLMCallID); err == nil {
			resp.LLMCall = &llmCallView{ID: c.ID.String(), Model: c.Model,
				OccurredAt: c.OccurredAt.Format("2006-01-02T15:04:05Z07:00")}
		}
	}

	alerts, err := store.AlertsForSubjects(ctx, s.db.Pool, []uuid.UUID{d.ID})
	if err != nil {
		s.fail(w, err, "alerts")
		return
	}
	for _, a := range alerts {
		resp.Alerts = append(resp.Alerts, alertView{Alert: a, Target: targetOf(a)})
	}

	replays, err := store.ReplaysForDecision(ctx, s.db.Pool, d.ID)
	if err != nil {
		s.fail(w, err, "replays")
		return
	}
	byVersion := map[uuid.UUID]*replayView{}
	for _, e := range replays {
		view, err := s.replayView(ctx, e)
		if err != nil {
			s.fail(w, err, "replay samples")
			return
		}
		resp.Replays = append(resp.Replays, view)
		if _, seen := byVersion[e.TargetMemoryVersionID]; !seen {
			v := view
			byVersion[e.TargetMemoryVersionID] = &v
		}
	}

	attrs, err := store.AttributionsOf(ctx, s.db.Pool, d.ID)
	if err != nil {
		s.fail(w, err, "attributions")
		return
	}

	memories := map[uuid.UUID]*influenceView{}
	sources := map[uuid.UUID]*influenceView{}
	for _, a := range attrs {
		var detail map[string]any
		_ = json.Unmarshal(a.Detail, &detail)

		switch {
		case a.MemoryVersionID != nil:
			view, ok := memories[*a.MemoryVersionID]
			if !ok {
				v, err := store.MemoryVersionByID(ctx, s.db.Pool, *a.MemoryVersionID)
				if err != nil {
					continue
				}
				key := ""
				memID := ""
				if m, err := store.MemoryByID(ctx, s.db.Pool, v.MemoryID); err == nil {
					key, memID = m.Key, m.ID.String()
				}
				path, err := taintPath(ctx, s.db.Pool, v.ID)
				if err != nil {
					s.fail(w, err, "taint path")
					return
				}
				view = &influenceView{
					Kind: "memory", ID: v.ID.String(), MemoryID: memID, MemoryKey: key,
					Version: v.Version, Content: v.Content, TrustMin: v.TrustMin,
					Tier: store.TierName(v.TrustMin), TaintPath: path,
					Replay: byVersion[v.ID],
				}
				memories[*a.MemoryVersionID] = view
			}
			applyLevel(view, a.Level, detail)
		case a.SourceID != nil:
			view, ok := sources[*a.SourceID]
			if !ok {
				src, err := store.SourceByID(ctx, s.db.Pool, *a.SourceID)
				if err != nil {
					continue
				}
				view = &influenceView{
					Kind: "source", ID: src.ID.String(), Origin: src.Origin, SourceKind: src.Kind,
					Content: src.Content, TrustMin: src.Trust, Tier: store.TierName(src.Trust),
					TrustReason: src.TrustReason, TaintPath: []pathNode{sourceNode(src)},
				}
				sources[*a.SourceID] = view
			}
			applyLevel(view, a.Level, detail)
		}
	}

	for _, v := range memories {
		finishInfluence(v)
		resp.Memories = append(resp.Memories, *v)
	}
	for _, v := range sources {
		finishInfluence(v)
		resp.Sources = append(resp.Sources, *v)
	}
	sortInfluences(resp.Memories)
	sortInfluences(resp.Sources)

	writeJSON(w, http.StatusOK, resp)
}

// applyLevel records one attribution level on an influence and collects the
// details that level carries.
func applyLevel(v *influenceView, level string, detail map[string]any) {
	for _, existing := range v.Levels {
		if existing == level {
			level = ""
			break
		}
	}
	if level != "" {
		v.Levels = append(v.Levels, level)
	}
	if path, ok := detail["arg_path"].(string); ok && path != "" {
		known := false
		for _, p := range v.ArgPaths {
			if p == path {
				known = true
				break
			}
		}
		if !known {
			v.ArgPaths = append(v.ArgPaths, path)
		}
	}
	if raw, ok := detail["spans"].([]any); ok {
		for _, s := range raw {
			triple, ok := s.([]any)
			if !ok || len(triple) != 3 {
				continue
			}
			var span [3]int
			valid := true
			for i, part := range triple {
				n, ok := part.(float64)
				if !ok {
					valid = false
					break
				}
				span[i] = int(n)
			}
			if valid {
				v.Spans = append(v.Spans, span)
			}
		}
	}
}

// finishInfluence sorts the levels, picks the strongest, and writes the exact
// label text for each level. A proven counterfactual is a level of its own,
// derived from the replay rather than from an attribution row.
func finishInfluence(v *influenceView) {
	if v.Replay != nil && v.Replay.Verdict != nil &&
		(*v.Replay.Verdict == store.VerdictStrong || *v.Replay.Verdict == store.VerdictModerate) {
		has := false
		for _, l := range v.Levels {
			if l == store.LevelCounterfactual {
				has = true
			}
		}
		if !has {
			v.Levels = append(v.Levels, store.LevelCounterfactual)
		}
	}
	sort.Slice(v.Levels, func(i, j int) bool { return levelRank[v.Levels[i]] < levelRank[v.Levels[j]] })
	if len(v.Levels) > 0 {
		v.Level = v.Levels[len(v.Levels)-1]
	}
	v.Labels = make([]string, 0, len(v.Levels))
	for _, l := range v.Levels {
		v.Labels = append(v.Labels, labelFor(l, v))
	}
}

// labelFor returns the wording the decision page uses for an attribution level.
func labelFor(level string, v *influenceView) string {
	switch level {
	case store.LevelRetrieved:
		return "Retrieved"
	case store.LevelInContext:
		return "Was in the prompt"
	case store.LevelValueFlow:
		if len(v.ArgPaths) > 0 {
			return fmt.Sprintf("Supplied a value to `%s`", v.ArgPaths[0])
		}
		return "Supplied a value"
	case store.LevelCounterfactual:
		if v.Replay != nil {
			return fmt.Sprintf("Removing it changed the decision in %d of %d samples",
				v.Replay.Changed, v.Replay.Total)
		}
		return "Removing it changed the decision"
	}
	return level
}

func sortInfluences(list []influenceView) {
	sort.SliceStable(list, func(i, j int) bool {
		if levelRank[list[i].Level] != levelRank[list[j].Level] {
			return levelRank[list[i].Level] > levelRank[list[j].Level]
		}
		return list[i].TrustMin < list[j].TrustMin
	})
}

// flattenArgs turns the tool arguments into path/value pairs so the dashboard
// can show each value as its own chip.
func flattenArgs(raw json.RawMessage) []argLeaf {
	var root any
	if len(raw) == 0 || json.Unmarshal(raw, &root) != nil {
		return nil
	}
	var out []argLeaf
	var walk func(prefix string, node any)
	walk = func(prefix string, node any) {
		switch n := node.(type) {
		case map[string]any:
			keys := make([]string, 0, len(n))
			for k := range n {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(join(prefix, k), n[k])
			}
		case []any:
			for i, item := range n {
				walk(fmt.Sprintf("%s[%d]", prefix, i), item)
			}
		case nil:
			out = append(out, argLeaf{Path: prefix, Value: "null"})
		case string:
			out = append(out, argLeaf{Path: prefix, Value: n})
		default:
			out = append(out, argLeaf{Path: prefix, Value: fmt.Sprint(n)})
		}
	}
	walk("", root)
	return out
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}
