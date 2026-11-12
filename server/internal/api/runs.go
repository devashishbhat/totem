package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/devashishbhat/totem/server/internal/store"
)

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.RunsNewest(r.Context(), queryInt(r, "limit", 50, 1, 200))
	if err != nil {
		s.fail(w, err, "runs")
		return
	}
	if list == nil {
		list = []store.RunSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": list})
}

// timelineItem is one row of the run timeline. Text fields hold observed
// content and the dashboard renders them as plain text.
type timelineItem struct {
	Kind     string         `json:"kind"`
	ID       string         `json:"id"`
	At       time.Time      `json:"at"`
	Title    string         `json:"title"`
	Text     string         `json:"text,omitempty"`
	TrustMin *int16         `json:"trust_min,omitempty"`
	Tier     string         `json:"tier,omitempty"`
	Link     *alertTarget   `json:"link,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runID := r.PathValue("id")
	run, err := store.RunByID(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "run")
		return
	}

	var items []timelineItem

	sources, err := store.SourcesInRun(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "sources")
		return
	}
	// The first thing the user said is how a person recognises this run.
	opening := ""
	for _, src := range sources {
		if src.Kind == "user_message" && opening == "" {
			opening = src.Content
		}
		trust := src.Trust
		items = append(items, timelineItem{
			Kind:     "source",
			ID:       src.ID.String(),
			At:       src.ObservedAt,
			Title:    sourceTitle(src),
			Text:     src.Content,
			TrustMin: &trust,
			Tier:     store.TierName(src.Trust),
			Link:     &alertTarget{Kind: "source", ID: src.ID.String()},
			Meta:     map[string]any{"kind": src.Kind, "origin": src.Origin, "trust_reason": src.TrustReason},
		})
	}

	reads, err := store.MemoryReadsInRun(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "memory reads")
		return
	}
	for _, rd := range reads {
		var results []struct {
			MemoryID string  `json:"memory_id"`
			TrustMin *int16  `json:"trust_min"`
			Content  string  `json:"content"`
			Rank     float64 `json:"rank"`
		}
		_ = json.Unmarshal(rd.Results, &results)
		lowest := int16(store.TrustSystem)
		keys := make([]string, 0, len(results))
		for _, res := range results {
			keys = append(keys, res.MemoryID)
			if res.TrustMin != nil && *res.TrustMin < lowest {
				lowest = *res.TrustMin
			}
		}
		item := timelineItem{
			Kind:  "memory.read",
			ID:    rd.ID.String(),
			At:    rd.OccurredAt,
			Title: fmt.Sprintf("Looked at %s it remembers", plural(len(results), "1 thing", "things")),
			Meta:  map[string]any{"results": results, "keys": keys},
		}
		if len(results) > 0 {
			item.TrustMin, item.Tier = &lowest, store.TierName(lowest)
		}
		items = append(items, item)
	}

	calls, err := store.LLMCallsInRun(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "llm calls")
		return
	}
	for _, c := range calls {
		var inCtx []map[string]any
		_ = json.Unmarshal(c.InContext, &inCtx)
		title := "Asked the model"
		if c.Model != "" {
			title = fmt.Sprintf("Asked %s", c.Model)
		}
		items = append(items, timelineItem{
			Kind:  "llm.call",
			ID:    c.ID.String(),
			At:    c.OccurredAt,
			Title: title,
			Meta:  map[string]any{"in_context": inCtx, "in_context_count": len(inCtx)},
		})
	}

	decisions, err := store.DecisionsInRun(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "decisions")
		return
	}
	for _, d := range decisions {
		trust := d.TrustMin
		title := fmt.Sprintf("Used %s", d.Tool)
		if d.Tool == "" {
			title = "Replied"
		}
		items = append(items, timelineItem{
			Kind:     "decision",
			ID:       d.ID.String(),
			At:       d.OccurredAt,
			Title:    title,
			Text:     string(d.Args),
			TrustMin: &trust,
			Tier:     store.TierName(d.TrustMin),
			Link:     &alertTarget{Kind: "decision", ID: d.ID.String()},
			Meta:     map[string]any{"side_effect": d.SideEffect, "tool": d.Tool},
		})
	}

	writes, err := store.MemoryWritesInRun(ctx, s.db.Pool, runID)
	if err != nil {
		s.fail(w, err, "memory writes")
		return
	}
	for _, v := range writes {
		trust := v.TrustMin
		verb := "Remembered"
		if v.Deleted {
			verb = "Forgot"
		}
		items = append(items, timelineItem{
			Kind:     "memory.write",
			ID:       v.ID.String(),
			At:       v.RecordedAt,
			Title:    fmt.Sprintf("%s %s", verb, shortKey(v.MemoryKey)),
			Text:     v.Content,
			TrustMin: &trust,
			Tier:     store.TierName(v.TrustMin),
			Link:     &alertTarget{Kind: "memory", ID: v.MemoryID.String()},
			Meta: map[string]any{
				"memory_key":        v.MemoryKey,
				"version":           v.Version,
				"lineage_precision": v.LineagePrecision,
				"taint_from":        v.TaintSourceIDs,
			},
		})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].At.Before(items[j].At) })
	if items == nil {
		items = []timelineItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "items": items, "opening": opening})
}

func sourceTitle(src store.Source) string {
	switch src.Kind {
	case "user_message":
		return "You said"
	case "system_config":
		return "System configuration"
	case "tool_output":
		return fmt.Sprintf("The tool %s replied", orDash(src.Origin))
	case "external_document":
		return fmt.Sprintf("Read %s", orDash(src.Origin))
	}
	return "Something of unknown origin"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// shortKey is the last segment of a memory key. A key is namespaced, and the
// namespace is the same for every memory an agent has, so it is the segment
// that tells one memory from another.
func shortKey(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 && i+1 < len(key) {
		return key[i+1:]
	}
	return key
}

func orDash(s string) string {
	if s == "" {
		return "unnamed"
	}
	return s
}
