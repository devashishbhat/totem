package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

type memoryResponse struct {
	Memory   store.Memory  `json:"memory"`
	Versions []versionView `json:"versions"`
	Graph    graph         `json:"graph"`
	Alerts   []alertView   `json:"alerts"`
}

type versionView struct {
	store.MemoryVersion
	Tier string `json:"tier"`
}

// graph is the lineage picture the memory page draws with @xyflow/react.
type graph struct {
	Nodes []pathNode  `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

type graphEdge struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	From string `json:"from"`
	To   string `json:"to"`
}

func (s *Server) memory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "memory id must be a uuid")
		return
	}
	mem, err := store.MemoryByID(ctx, s.db.Pool, id)
	if err != nil {
		s.fail(w, err, "memory")
		return
	}
	versions, err := store.MemoryVersionsOf(ctx, s.db.Pool, mem.ID)
	if err != nil {
		s.fail(w, err, "memory versions")
		return
	}

	resp := memoryResponse{Memory: mem, Versions: []versionView{}, Alerts: []alertView{}}
	nodes := map[string]pathNode{}
	edgeSet := map[string]graphEdge{}
	var frontier []uuid.UUID
	var versionIDs []uuid.UUID

	for _, v := range versions {
		v.MemoryKey = mem.Key
		resp.Versions = append(resp.Versions, versionView{MemoryVersion: v, Tier: store.TierName(v.TrustMin)})
		n := versionNode(ctx, s.db.Pool, v)
		nodes[n.ID] = n
		frontier = append(frontier, v.ID)
		versionIDs = append(versionIDs, v.ID)
	}

	// Walk every ancestor so the graph shows the whole provenance, not just the
	// direct parents.
	seen := map[uuid.UUID]bool{}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		edges, err := store.EdgesFrom(ctx, s.db.Pool, cur)
		if err != nil {
			s.fail(w, err, "edges")
			return
		}
		for _, e := range edges {
			switch e.ToType {
			case "source":
				src, err := store.SourceByID(ctx, s.db.Pool, e.ToID)
				if err != nil {
					continue
				}
				n := sourceNode(src)
				nodes[n.ID] = n
			case "memory_version":
				v, err := store.MemoryVersionByID(ctx, s.db.Pool, e.ToID)
				if err != nil {
					continue
				}
				n := versionNode(ctx, s.db.Pool, v)
				nodes[n.ID] = n
				frontier = append(frontier, v.ID)
			default:
				continue
			}
			edgeSet[e.ID.String()] = graphEdge{
				ID: e.ID.String(), Kind: e.Kind, From: e.FromID.String(), To: e.ToID.String()}
		}
	}

	resp.Graph = graph{Nodes: make([]pathNode, 0, len(nodes)), Edges: make([]graphEdge, 0, len(edgeSet))}
	for _, n := range nodes {
		resp.Graph.Nodes = append(resp.Graph.Nodes, n)
	}
	for _, e := range edgeSet {
		resp.Graph.Edges = append(resp.Graph.Edges, e)
	}

	alerts, err := store.AlertsForSubjects(ctx, s.db.Pool, versionIDs)
	if err != nil {
		s.fail(w, err, "alerts")
		return
	}
	for _, a := range alerts {
		resp.Alerts = append(resp.Alerts, alertView{Alert: a, Target: targetOf(a)})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) source(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "source id must be a uuid")
		return
	}
	src, err := store.SourceByID(r.Context(), s.db.Pool, id)
	if err != nil {
		s.fail(w, err, "source")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": src,
		"tier":   store.TierName(src.Trust),
	})
}
