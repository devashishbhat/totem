package api

import (
	"encoding/json"
	"net/http"

	"github.com/devashishbhat/totem/server/internal/store"
)

type overviewResponse struct {
	DemoMode  bool              `json:"demo_mode"`
	Runs      int               `json:"runs"`
	Decisions int               `json:"decisions"`
	Memories  int               `json:"memories"`
	Alerts    []alertView       `json:"alerts"`
	Featured  *featuredIncident `json:"featured"`
}

type alertView struct {
	store.Alert
	// Target is where the dashboard should send a click.
	Target alertTarget `json:"target"`
}

type alertTarget struct {
	Kind string `json:"kind"` // decision | memory
	ID   string `json:"id"`
}

type featuredIncident struct {
	AlertID    string `json:"alert_id"`
	DecisionID string `json:"decision_id"`
	Title      string `json:"title"`
	Severity   string `json:"severity"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	runs, decisions, memories, err := s.db.Counts(ctx)
	if err != nil {
		s.fail(w, err, "counts")
		return
	}
	alerts, err := store.AlertsNewest(ctx, s.db.Pool, queryInt(r, "limit", 50, 1, 200))
	if err != nil {
		s.fail(w, err, "alerts")
		return
	}
	resp := overviewResponse{
		DemoMode:  s.cfg.DemoMode,
		Runs:      runs,
		Decisions: decisions,
		Memories:  memories,
		Alerts:    make([]alertView, 0, len(alerts)),
	}
	for _, a := range alerts {
		resp.Alerts = append(resp.Alerts, alertView{Alert: a, Target: targetOf(a)})
		if resp.Featured == nil && a.Kind == store.AlertSideEffectFromUntrusted && a.Severity == "critical" {
			resp.Featured = &featuredIncident{
				AlertID:    a.ID.String(),
				DecisionID: a.SubjectID.String(),
				Title:      a.Title,
				Severity:   a.Severity,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// targetOf says which page an alert leads to. The subject of a side-effect
// alert is the decision itself; the subject of a memory alert is a version, so
// the click goes to its memory.
func targetOf(a store.Alert) alertTarget {
	if a.Kind == store.AlertSideEffectFromUntrusted {
		return alertTarget{Kind: "decision", ID: a.SubjectID.String()}
	}
	var ev struct {
		MemoryID string `json:"memory_id"`
	}
	if err := json.Unmarshal(a.Evidence, &ev); err == nil && ev.MemoryID != "" {
		return alertTarget{Kind: "memory", ID: ev.MemoryID}
	}
	return alertTarget{Kind: "memory", ID: a.SubjectID.String()}
}
