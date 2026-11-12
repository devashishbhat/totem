package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

type replayView struct {
	store.ReplayExperiment
	Changed  int           `json:"changed"`
	Total    int           `json:"total"`
	Baseline []sampleView  `json:"baseline"`
	Variant  []sampleView  `json:"variant"`
	Sentence string        `json:"sentence"`
	Target   *replayTarget `json:"target,omitempty"`
}

type replayTarget struct {
	MemoryVersionID string `json:"memory_version_id"`
	MemoryID        string `json:"memory_id"`
	MemoryKey       string `json:"memory_key"`
	Version         int    `json:"version"`
}

type sampleView struct {
	Idx             int             `json:"idx"`
	Output          json.RawMessage `json:"output"`
	MatchesBaseline bool            `json:"matches_baseline"`
	Summary         string          `json:"summary"`
}

// replayView loads the samples of an experiment and writes the verdict
// sentence the replay card shows.
func (s *Server) replayView(ctx context.Context, e store.ReplayExperiment) (replayView, error) {
	view := replayView{ReplayExperiment: e, Baseline: []sampleView{}, Variant: []sampleView{}}
	samples, err := store.ReplaySamples(ctx, s.db.Pool, e.ID)
	if err != nil {
		return view, err
	}
	for _, smp := range samples {
		sv := sampleView{Idx: smp.Idx, Output: smp.Output, MatchesBaseline: smp.MatchesBaseline,
			Summary: summarizeOutput(smp.Output)}
		if smp.Arm == "baseline" {
			view.Baseline = append(view.Baseline, sv)
		} else {
			view.Variant = append(view.Variant, sv)
			view.Total++
			if !smp.MatchesBaseline {
				view.Changed++
			}
		}
	}
	if view.Total == 0 {
		view.Total = e.SamplesPerArm
	}
	if v, err := store.MemoryVersionByID(ctx, s.db.Pool, e.TargetMemoryVersionID); err == nil {
		t := replayTarget{MemoryVersionID: v.ID.String(), MemoryID: v.MemoryID.String(), Version: v.Version}
		if m, err := store.MemoryByID(ctx, s.db.Pool, v.MemoryID); err == nil {
			t.MemoryKey = m.Key
		}
		view.Target = &t
	}
	view.Sentence = verdictSentence(view)
	return view, nil
}

// verdictSentence states the result in one plain sentence.
func verdictSentence(v replayView) string {
	switch v.Status {
	case store.ReplayPending, store.ReplayRunning:
		return fmt.Sprintf("Replaying the decision %d times with this memory removed.", v.SamplesPerArm)
	case store.ReplayFailed:
		return "The replay could not be run."
	}
	if v.Verdict == nil || v.Effect == nil || v.CILow == nil || v.CIHigh == nil {
		return "The replay produced no verdict."
	}
	return fmt.Sprintf(
		"Removing this memory changed the decision in %d of %d samples: effect %+.2f, 95%% CI %+.2f to %+.2f. Verdict: %s.",
		v.Changed, v.Total, *v.Effect, *v.CILow, *v.CIHigh, *v.Verdict)
}

// summarizeOutput reduces one sampled model output to a short line: the tool it
// called and the arguments it chose.
func summarizeOutput(raw json.RawMessage) string {
	var out struct {
		Tool    string          `json:"tool"`
		Args    json.RawMessage `json:"args"`
		Content string          `json:"content"`
		Error   string          `json:"error"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return ""
	}
	switch {
	case out.Error != "":
		return "error: " + out.Error
	case out.Tool != "":
		return fmt.Sprintf("%s(%s)", out.Tool, string(out.Args))
	case out.Content != "":
		return out.Content
	}
	return "no tool call"
}

type createReplayRequest struct {
	DecisionID      string `json:"decision_id"`
	MemoryVersionID string `json:"memory_version_id"`
	SamplesPerArm   int    `json:"samples_per_arm"`
}

func (s *Server) createReplay(w http.ResponseWriter, r *http.Request) {
	var req createReplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "body must be JSON")
		return
	}
	decisionID, err := uuid.Parse(req.DecisionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "decision_id must be a uuid")
		return
	}
	versionID, err := uuid.Parse(req.MemoryVersionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "memory_version_id must be a uuid")
		return
	}
	id, err := s.runner.Start(r.Context(), decisionID, versionID, req.SamplesPerArm)
	if err != nil {
		s.log.Warn("replay rejected", "err", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id.String()})
}

func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "replay id must be a uuid")
		return
	}
	e, err := store.ReplayByID(r.Context(), s.db.Pool, id)
	if err != nil {
		s.fail(w, err, "replay")
		return
	}
	view, err := s.replayView(r.Context(), e)
	if err != nil {
		s.fail(w, err, "replay samples")
		return
	}
	writeJSON(w, http.StatusOK, view)
}
