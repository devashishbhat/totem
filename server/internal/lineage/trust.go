package lineage

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

// trustOf returns the tier for a source and the reason for it.
// An explicit totem.source.trust attribute from the SDK overrides the default.
func (w *Worker) trustOf(kind, origin string, explicit int, hasExplicit bool) (int16, string) {
	if hasExplicit {
		t := int16(explicit)
		if t < store.TrustUnknown {
			t = store.TrustUnknown
		}
		if t > store.TrustSystem {
			t = store.TrustSystem
		}
		return t, "set by the SDK"
	}
	switch kind {
	case "system_config":
		return store.TrustSystem, "system configuration"
	case "user_message":
		return store.TrustUser, "message from the user"
	case "tool_output":
		if w.cfg.TrustedTools[origin] {
			return store.TrustTool, fmt.Sprintf("output of %s, a tool listed in TM_TRUSTED_TOOLS", origin)
		}
		return store.TrustExternal, fmt.Sprintf("output of %s, a tool that is not listed as trusted", origin)
	case "external_document":
		return store.TrustExternal, fmt.Sprintf("document fetched from %s", origin)
	default:
		return store.TrustUnknown, "provenance unknown"
	}
}

// parents is the resolved DERIVED_FROM set of a memory version.
type parents struct {
	sources  []store.Source
	versions []store.MemoryVersion
}

// trustMin is the minimum trust over the parents. A version with no recorded
// parent is T0: nothing is known about where its content came from.
func (p parents) trustMin() (int16, string) {
	if len(p.sources) == 0 && len(p.versions) == 0 {
		return store.TrustUnknown, "written with no recorded provenance"
	}
	min := int16(store.TrustSystem)
	reason := ""
	for _, s := range p.sources {
		if s.Trust <= min {
			min, reason = s.Trust, s.TrustReason
		}
	}
	for _, v := range p.versions {
		if v.TrustMin <= min {
			min, reason = v.TrustMin, "inherited from an earlier memory version"
		}
	}
	return min, reason
}

// taint is the set of T0/T1 source ids this version descends from.
func (p parents) taint() []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(id uuid.UUID) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, s := range p.sources {
		if s.Trust <= store.TrustExternal {
			add(s.ID)
		}
	}
	for _, v := range p.versions {
		for _, id := range v.TaintSourceIDs {
			add(id)
		}
	}
	return out
}
