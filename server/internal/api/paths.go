package api

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/devashishbhat/totem/server/internal/store"
)

// pathNode is one hop of the left-to-right taint path the decision page draws:
// decision → memory → the page the memory came from.
type pathNode struct {
	Kind      string `json:"kind"` // memory_version | source
	ID        string `json:"id"`
	Label     string `json:"label"`
	Text      string `json:"text,omitempty"`
	TrustMin  int16  `json:"trust_min"`
	Tier      string `json:"tier"`
	Origin    string `json:"origin,omitempty"`
	MemoryID  string `json:"memory_id,omitempty"`
	MemoryKey string `json:"memory_key,omitempty"`
	Version   int    `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// taintPath walks DERIVED_FROM from a memory version to the least trusted
// source it descends from, and returns the chain. The walk is breadth-first,
// so the chain is the shortest one that reaches that source.
func taintPath(ctx context.Context, q store.Queryer, versionID uuid.UUID) ([]pathNode, error) {
	start, err := store.MemoryVersionByID(ctx, q, versionID)
	if err != nil {
		return nil, err
	}

	type state struct {
		node   pathNode
		parent int // index into visited, -1 for the root
	}
	visited := []state{{node: versionNode(ctx, q, start), parent: -1}}
	queue := []int{0}
	seen := map[uuid.UUID]bool{start.ID: true}
	best := -1
	bestTrust := int16(store.TrustSystem + 1)

	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		cur := visited[i]
		if cur.node.Kind == "source" {
			if cur.node.TrustMin < bestTrust {
				best, bestTrust = i, cur.node.TrustMin
			}
			continue
		}
		id, err := uuid.Parse(cur.node.ID)
		if err != nil {
			continue
		}
		edges, err := store.EdgesFrom(ctx, q, id)
		if err != nil {
			return nil, err
		}
		for _, e := range edges {
			if e.Kind != "DERIVED_FROM" || seen[e.ToID] {
				continue
			}
			seen[e.ToID] = true
			var n pathNode
			switch e.ToType {
			case "source":
				src, err := store.SourceByID(ctx, q, e.ToID)
				if err != nil {
					continue
				}
				n = sourceNode(src)
			case "memory_version":
				v, err := store.MemoryVersionByID(ctx, q, e.ToID)
				if err != nil {
					continue
				}
				n = versionNode(ctx, q, v)
			default:
				continue
			}
			visited = append(visited, state{node: n, parent: i})
			queue = append(queue, len(visited)-1)
		}
	}

	if best < 0 {
		return []pathNode{visited[0].node}, nil
	}
	var chain []pathNode
	for i := best; i >= 0; i = visited[i].parent {
		chain = append(chain, visited[i].node)
	}
	// chain was built leaf-first; reverse it so it reads memory → source.
	for l, r := 0, len(chain)-1; l < r; l, r = l+1, r-1 {
		chain[l], chain[r] = chain[r], chain[l]
	}
	return chain, nil
}

func versionNode(ctx context.Context, q store.Queryer, v store.MemoryVersion) pathNode {
	key := v.MemoryKey
	if key == "" {
		if m, err := store.MemoryByID(ctx, q, v.MemoryID); err == nil {
			key = m.Key
		}
	}
	return pathNode{
		Kind:      "memory_version",
		ID:        v.ID.String(),
		Label:     fmt.Sprintf("memory %s v%d", key, v.Version),
		Text:      v.Content,
		TrustMin:  v.TrustMin,
		Tier:      store.TierName(v.TrustMin),
		MemoryID:  v.MemoryID.String(),
		MemoryKey: key,
		Version:   v.Version,
	}
}

func sourceNode(src store.Source) pathNode {
	return pathNode{
		Kind:     "source",
		ID:       src.ID.String(),
		Label:    sourceTitle(src),
		Text:     src.Content,
		TrustMin: src.Trust,
		Tier:     store.TierName(src.Trust),
		Origin:   src.Origin,
		Reason:   src.TrustReason,
	}
}
