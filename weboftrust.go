package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
)

// WoTNode is a node in the trust graph visualization.
type WoTNode struct {
	ID        string `json:"id"`
	Score     int    `json:"score"`
	Followers int    `json:"followers"`
	Follows   int    `json:"follows"`
	Group     string `json:"group"` // "center", "follow", "follower", "mutual"
}

// WoTLink is an edge in the trust graph.
type WoTLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // "follows", "followed_by"
}

// WoTGraphResponse is the D3.js-compatible graph response.
type WoTGraphResponse struct {
	Pubkey     string    `json:"pubkey"`
	Score      int       `json:"score"`
	Rank       int       `json:"rank"`
	Nodes      []WoTNode `json:"nodes"`
	Links      []WoTLink `json:"links"`
	NodeCount  int       `json:"node_count"`
	LinkCount  int       `json:"link_count"`
	GraphSize  int       `json:"graph_size"`
}

// handleWebOfTrust returns a D3.js-compatible graph centered on a pubkey.
// GET /weboftrust?pubkey=<hex|npub>&depth=1&limit=50
func handleWebOfTrust(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	stats := graph.Stats()
	rawScore, _ := graph.GetScore(pubkey)
	centerScore := normalizeScore(rawScore, stats.Nodes)
	rank := graph.Rank(pubkey)

	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)

	// Identify mutual connections
	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}
	followerSet := make(map[string]bool, len(followers))
	for _, f := range followers {
		followerSet[f] = true
	}

	// Sort follows and followers by WoT score (highest first) so we keep the most interesting
	type scored struct {
		pk    string
		score int
	}

	scoreAndSort := func(pks []string) []scored {
		s := make([]scored, len(pks))
		for i, pk := range pks {
			raw, _ := graph.GetScore(pk)
			s[i] = scored{pk, normalizeScore(raw, stats.Nodes)}
		}
		sort.Slice(s, func(i, j int) bool { return s[i].score > s[j].score })
		return s
	}

	sortedFollows := scoreAndSort(follows)
	sortedFollowers := scoreAndSort(followers)

	// Build node and link sets
	nodeMap := make(map[string]*WoTNode)
	var links []WoTLink

	// Add center node
	nodeMap[pubkey] = &WoTNode{
		ID:        pubkey,
		Score:     centerScore,
		Followers: len(followers),
		Follows:   len(follows),
		Group:     "center",
	}

	// Add follows (up to limit)
	added := 0
	for _, s := range sortedFollows {
		if added >= limit {
			break
		}
		pk := s.pk
		if pk == pubkey {
			continue
		}

		group := "follow"
		if followerSet[pk] {
			group = "mutual"
		}

		if _, exists := nodeMap[pk]; !exists {
			fFollowers := graph.GetFollowers(pk)
			fFollows := graph.GetFollows(pk)
			nodeMap[pk] = &WoTNode{
				ID:        pk,
				Score:     s.score,
				Followers: len(fFollowers),
				Follows:   len(fFollows),
				Group:     group,
			}
			added++
		}
		links = append(links, WoTLink{
			Source: pubkey,
			Target: pk,
			Type:   "follows",
		})
	}

	// Add followers (up to limit, skip if already in nodeMap)
	added = 0
	for _, s := range sortedFollowers {
		if added >= limit {
			break
		}
		pk := s.pk
		if pk == pubkey {
			continue
		}

		if _, exists := nodeMap[pk]; !exists {
			group := "follower"
			if followSet[pk] {
				group = "mutual"
			}
			fFollowers := graph.GetFollowers(pk)
			fFollows := graph.GetFollows(pk)
			nodeMap[pk] = &WoTNode{
				ID:        pk,
				Score:     s.score,
				Followers: len(fFollowers),
				Follows:   len(fFollows),
				Group:     group,
			}
			added++
		}
		links = append(links, WoTLink{
			Source: pk,
			Target: pubkey,
			Type:   "followed_by",
		})
	}

	// Convert map to slice
	nodes := make([]WoTNode, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, *n)
	}

	// Sort: center first, then by score descending
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Group == "center" {
			return true
		}
		if nodes[j].Group == "center" {
			return false
		}
		return nodes[i].Score > nodes[j].Score
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(WoTGraphResponse{
		Pubkey:    pubkey,
		Score:     centerScore,
		Rank:      rank,
		Nodes:     nodes,
		Links:     links,
		NodeCount: len(nodes),
		LinkCount: len(links),
		GraphSize: stats.Nodes,
	})
}
