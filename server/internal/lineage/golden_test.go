package lineage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/config"
	"github.com/devashishbhat/totem/server/internal/event"
	"github.com/devashishbhat/totem/server/internal/lineage"
	"github.com/devashishbhat/totem/server/internal/store"
	"github.com/devashishbhat/totem/server/internal/testsupport"
)

// A golden file is a sequence of events plus the state the worker should reach.
// Each one is a scenario the product has to get right, written down so a change
// to the worker has to say which scenario it changes.
type golden struct {
	Name   string         `json:"name"`
	Note   string         `json:"note"`
	Config goldenConfig   `json:"config"`
	Events []goldenEvent  `json:"events"`
	Expect goldenExpected `json:"expect"`
}

type goldenConfig struct {
	TrustedTools []string `json:"trusted_tools"`
}

type goldenEvent struct {
	Op     string         `json:"op"`
	RunID  string         `json:"run_id"`
	AtSecs int            `json:"at"`
	Attrs  map[string]any `json:"attrs"`
}

type goldenExpected struct {
	Sources   []goldenSource   `json:"sources"`
	Memories  []goldenMemory   `json:"memories"`
	Decisions []goldenDecision `json:"decisions"`
	Alerts    []goldenAlert    `json:"alerts"`
}

type goldenSource struct {
	Kind  string `json:"kind"`
	Trust int16  `json:"trust"`
}

type goldenMemory struct {
	Key        string `json:"key"`
	Versions   int    `json:"versions"`
	Current    int    `json:"current_version"`
	Status     string `json:"status"`
	TrustMin   int16  `json:"trust_min"`
	TaintSize  int    `json:"taint_sources"`
	Content    string `json:"content"`
	Precision  string `json:"lineage_precision"`
	Parents    int    `json:"derived_from_edges"`
	Supersedes int    `json:"supersedes_edges"`
}

type goldenDecision struct {
	Tool       string   `json:"tool"`
	TrustMin   int16    `json:"trust_min"`
	TaintSize  int      `json:"taint_sources"`
	Levels     []string `json:"attribution_levels"`
	HasLLMCall bool     `json:"has_llm_call"`
}

type goldenAlert struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	// Title is checked only when a fixture states one, because most scenarios
	// are about which alerts fire rather than how they read.
	Title string `json:"title,omitempty"`
}

func TestGoldenSequences(t *testing.T) {
	paths, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no golden files")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var g golden
			if err := json.Unmarshal(raw, &g); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			runGolden(t, g)
		})
	}
}

func runGolden(t *testing.T, g golden) {
	t.Helper()
	db := testsupport.DB(t)
	ctx := context.Background()

	cfg := config.Config{TrustedTools: map[string]bool{}}
	for _, tool := range g.Config.TrustedTools {
		cfg.TrustedTools[tool] = true
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	worker := lineage.New(db, cfg, log)

	base := time.Date(2026, 11, 6, 20, 0, 0, 0, time.UTC)
	events := make([]event.Event, 0, len(g.Events))
	for i, e := range g.Events {
		attrs := map[string]any{
			event.AttrOp:      e.Op,
			event.AttrRunID:   e.RunID,
			event.AttrAgentID: "golden-agent",
		}
		for k, v := range e.Attrs {
			attrs[k] = v
		}
		events = append(events, event.Event{
			EventID:    uuid.NewSHA1(uuid.Nil, []byte(fmt.Sprintf("%s/%d", g.Name, i))),
			RunID:      e.RunID,
			Op:         event.Op(e.Op),
			OccurredAt: base.Add(time.Duration(e.AtSecs) * time.Second),
			Attrs:      attrs,
		})
	}
	if _, err := db.InsertEvents(ctx, events); err != nil {
		t.Fatalf("insert events: %v", err)
	}

	// Drain the queue the way the running worker does.
	for i := 0; i < 10; i++ {
		if _, err := worker.Tick(ctx); err != nil {
			t.Fatalf("tick: %v", err)
		}
		_, pending, err := db.CountEvents(ctx)
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if pending == 0 {
			break
		}
	}
	if _, pending, _ := db.CountEvents(ctx); pending != 0 {
		t.Fatalf("%d events were never processed", pending)
	}

	checkSources(t, ctx, db, g)
	checkMemories(t, ctx, db, g)
	checkDecisions(t, ctx, db, g)
	checkAlerts(t, ctx, db, g)
}

func checkSources(t *testing.T, ctx context.Context, db *store.Store, g golden) {
	t.Helper()
	var got []goldenSource
	rows, err := db.Pool.Query(ctx, `select kind, trust from sources order by observed_at, kind`)
	if err != nil {
		t.Fatalf("query sources: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s goldenSource
		if err := rows.Scan(&s.Kind, &s.Trust); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, s)
	}
	if len(got) != len(g.Expect.Sources) {
		t.Fatalf("got %d sources, want %d: %+v", len(got), len(g.Expect.Sources), got)
	}
	for i, want := range g.Expect.Sources {
		if got[i] != want {
			t.Errorf("source %d is %+v, want %+v", i, got[i], want)
		}
	}
}

func checkMemories(t *testing.T, ctx context.Context, db *store.Store, g golden) {
	t.Helper()
	for _, want := range g.Expect.Memories {
		mem, err := store.MemoryByKey(ctx, db.Pool, want.Key)
		if err != nil {
			t.Fatalf("memory %s: %v", want.Key, err)
		}
		if mem.Status != want.Status || mem.CurrentVersion != want.Current {
			t.Errorf("memory %s is %s at v%d, want %s at v%d",
				want.Key, mem.Status, mem.CurrentVersion, want.Status, want.Current)
		}
		versions, err := store.MemoryVersionsOf(ctx, db.Pool, mem.ID)
		if err != nil {
			t.Fatalf("versions of %s: %v", want.Key, err)
		}
		if len(versions) != want.Versions {
			t.Fatalf("memory %s has %d versions, want %d", want.Key, len(versions), want.Versions)
		}
		current := versions[len(versions)-1]
		if current.TrustMin != want.TrustMin {
			t.Errorf("memory %s trust_min is %d, want %d", want.Key, current.TrustMin, want.TrustMin)
		}
		if len(current.TaintSourceIDs) != want.TaintSize {
			t.Errorf("memory %s descends from %d untrusted sources, want %d",
				want.Key, len(current.TaintSourceIDs), want.TaintSize)
		}
		if want.Content != "" && current.Content != want.Content {
			t.Errorf("memory %s content is %q, want %q", want.Key, current.Content, want.Content)
		}
		if want.Precision != "" && current.LineagePrecision != want.Precision {
			t.Errorf("memory %s precision is %q, want %q",
				want.Key, current.LineagePrecision, want.Precision)
		}
		edges, err := store.EdgesFrom(ctx, db.Pool, current.ID)
		if err != nil {
			t.Fatalf("edges: %v", err)
		}
		derived, supersedes := 0, 0
		for _, e := range edges {
			if e.Kind == "DERIVED_FROM" {
				derived++
			} else {
				supersedes++
			}
		}
		if derived != want.Parents {
			t.Errorf("memory %s has %d DERIVED_FROM edges, want %d", want.Key, derived, want.Parents)
		}
		if supersedes != want.Supersedes {
			t.Errorf("memory %s has %d SUPERSEDES edges, want %d",
				want.Key, supersedes, want.Supersedes)
		}
		// Only the newest version may be live.
		for _, v := range versions[:len(versions)-1] {
			if v.SupersededAt == nil {
				t.Errorf("memory %s v%d was never superseded", want.Key, v.Version)
			}
		}
	}
}

func checkDecisions(t *testing.T, ctx context.Context, db *store.Store, g golden) {
	t.Helper()
	rows, err := db.Pool.Query(ctx, `select id, tool, trust_min, taint_source_ids,
		llm_call_id is not null from decisions order by occurred_at, tool`)
	if err != nil {
		t.Fatalf("query decisions: %v", err)
	}
	type row struct {
		id     uuid.UUID
		golden goldenDecision
	}
	var got []row
	for rows.Next() {
		var r row
		var taint []uuid.UUID
		if err := rows.Scan(&r.id, &r.golden.Tool, &r.golden.TrustMin, &taint,
			&r.golden.HasLLMCall); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		r.golden.TaintSize = len(taint)
		got = append(got, r)
	}
	rows.Close()

	if len(got) != len(g.Expect.Decisions) {
		t.Fatalf("got %d decisions, want %d", len(got), len(g.Expect.Decisions))
	}
	for i, want := range g.Expect.Decisions {
		attrs, err := store.AttributionsOf(ctx, db.Pool, got[i].id)
		if err != nil {
			t.Fatalf("attributions: %v", err)
		}
		levels := map[string]bool{}
		for _, a := range attrs {
			levels[a.Level] = true
		}
		var names []string
		for level := range levels {
			names = append(names, level)
		}
		sort.Strings(names)
		wantLevels := append([]string(nil), want.Levels...)
		sort.Strings(wantLevels)

		if got[i].golden.Tool != want.Tool || got[i].golden.TrustMin != want.TrustMin ||
			got[i].golden.TaintSize != want.TaintSize ||
			got[i].golden.HasLLMCall != want.HasLLMCall {
			t.Errorf("decision %d is %+v, want %+v", i, got[i].golden, want)
		}
		if fmt.Sprint(names) != fmt.Sprint(wantLevels) {
			t.Errorf("decision %d has levels %v, want %v", i, names, wantLevels)
		}
	}
}

func checkAlerts(t *testing.T, ctx context.Context, db *store.Store, g golden) {
	t.Helper()
	alerts, err := store.AlertsNewest(ctx, db.Pool, 50)
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	wantTitles := false
	for _, a := range g.Expect.Alerts {
		if a.Title != "" {
			wantTitles = true
		}
	}
	var got []goldenAlert
	for _, a := range alerts {
		row := goldenAlert{Kind: a.Kind, Severity: a.Severity}
		if wantTitles {
			row.Title = a.Title
		}
		got = append(got, row)
	}
	sortAlerts(got)
	want := append([]goldenAlert(nil), g.Expect.Alerts...)
	sortAlerts(want)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("alerts are %v, want %v", got, want)
	}
}

func sortAlerts(list []goldenAlert) {
	sort.Slice(list, func(i, j int) bool {
		if list[i].Kind != list[j].Kind {
			return list[i].Kind < list[j].Kind
		}
		return list[i].Severity < list[j].Severity
	})
}
