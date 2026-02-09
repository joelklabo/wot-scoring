package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// TrustPathHop is a single node along a trust path.
type TrustPathHop struct {
	Pubkey   string  `json:"pubkey"`
	WotScore int     `json:"wot_score"`
	IsMutual bool    `json:"is_mutual"` // mutual follow with next hop
}

// TrustPath is one path between two pubkeys with its trust score.
type TrustPath struct {
	Hops       []TrustPathHop `json:"hops"`
	Length     int            `json:"length"`      // number of edges
	TrustScore float64       `json:"trust_score"` // 0.0-1.0 product of hop trust
	WeakestHop int           `json:"weakest_hop"` // index of lowest-scored node in path
}

// TrustPathResponse is the response for the /trust-path endpoint.
type TrustPathResponse struct {
	From           string      `json:"from"`
	To             string      `json:"to"`
	Connected      bool        `json:"connected"`
	Paths          []TrustPath `json:"paths"`
	BestTrust      float64     `json:"best_trust"`      // highest trust_score across paths
	PathDiversity  int         `json:"path_diversity"`   // number of distinct paths found
	OverallTrust   float64     `json:"overall_trust"`    // combined trust from all paths
	Classification string     `json:"classification"`   // "strong", "moderate", "weak", "none"
	GraphSize      int         `json:"graph_size"`
}

// handleTrustPath finds and scores trust paths between two pubkeys.
// GET /trust-path?from=<hex|npub>&to=<hex|npub>&max_paths=5
func handleTrustPath(w http.ResponseWriter, r *http.Request) {
	fromRaw := r.URL.Query().Get("from")
	toRaw := r.URL.Query().Get("to")
	if fromRaw == "" || toRaw == "" {
		http.Error(w, `{"error":"from and to parameters required"}`, http.StatusBadRequest)
		return
	}

	fromHex, err := resolvePubkey(fromRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid from: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	toHex, err := resolvePubkey(toRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid to: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	maxPathsStr := r.URL.Query().Get("max_paths")
	maxPaths := 3
	if maxPathsStr != "" {
		if n, err := fmt.Sscanf(maxPathsStr, "%d", &maxPaths); n != 1 || err != nil || maxPaths < 1 {
			maxPaths = 3
		}
		if maxPaths > 5 {
			maxPaths = 5
		}
	}

	stats := graph.Stats()

	// Find multiple paths using iterative BFS with node exclusion
	paths := findMultiplePaths(fromHex, toHex, maxPaths, 6)

	if len(paths) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TrustPathResponse{
			From:           fromHex,
			To:             toHex,
			Connected:      false,
			Paths:          []TrustPath{},
			BestTrust:      0,
			PathDiversity:  0,
			OverallTrust:   0,
			Classification: "none",
			GraphSize:      stats.Nodes,
		})
		return
	}

	// Score each path
	scoredPaths := make([]TrustPath, 0, len(paths))
	for _, rawPath := range paths {
		tp := scoreTrustPath(rawPath, stats.Nodes)
		scoredPaths = append(scoredPaths, tp)
	}

	// Sort by trust_score descending
	sort.Slice(scoredPaths, func(i, j int) bool {
		return scoredPaths[i].TrustScore > scoredPaths[j].TrustScore
	})

	bestTrust := scoredPaths[0].TrustScore

	// Overall trust: combine paths using 1 - product(1 - trust_i)
	// Multiple independent paths increase confidence
	overallTrust := combinedTrust(scoredPaths)

	classification := classifyTrust(overallTrust)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TrustPathResponse{
		From:           fromHex,
		To:             toHex,
		Connected:      true,
		Paths:          scoredPaths,
		BestTrust:      round3(bestTrust),
		PathDiversity:  len(scoredPaths),
		OverallTrust:   round3(overallTrust),
		Classification: classification,
		GraphSize:      stats.Nodes,
	})
}

// findMultiplePaths finds up to maxPaths distinct shortest paths between source and target.
// Uses iterative BFS: after finding a path, exclude intermediate nodes and search again.
func findMultiplePaths(source, target string, maxPaths, maxDepth int) [][]string {
	if source == target {
		return [][]string{{source}}
	}

	var results [][]string
	excludeNodes := make(map[string]bool) // intermediate nodes to exclude in subsequent searches
	seenPaths := make(map[string]bool)    // deduplicate identical paths

	for i := 0; i < maxPaths; i++ {
		path := bfsPathExcluding(source, target, maxDepth, excludeNodes)
		if path == nil {
			break
		}

		// Deduplicate: skip if we've seen this exact path before
		pathKey := fmt.Sprintf("%v", path)
		if seenPaths[pathKey] {
			break // no new paths possible
		}
		seenPaths[pathKey] = true

		results = append(results, path)

		// Exclude intermediate nodes (not source/target) for path diversity
		intermediates := path[1 : len(path)-1]
		if len(intermediates) == 0 {
			break // direct edge, no alternate paths through intermediates
		}
		for _, node := range intermediates {
			excludeNodes[node] = true
		}
	}

	return results
}

// bfsPathExcluding finds shortest path while excluding certain intermediate nodes.
func bfsPathExcluding(source, target string, maxDepth int, exclude map[string]bool) []string {
	if source == target {
		return []string{source}
	}

	type bfsEntry struct {
		pubkey string
		path   []string
	}

	visited := make(map[string]bool)
	visited[source] = true
	// Also mark excluded nodes as visited (but not source/target)
	for node := range exclude {
		if node != source && node != target {
			visited[node] = true
		}
	}

	queue := []bfsEntry{{pubkey: source, path: []string{source}}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if len(current.path) > maxDepth {
			break
		}

		follows := graph.GetFollows(current.pubkey)
		for _, next := range follows {
			if next == target {
				return append(current.path, target)
			}
			if !visited[next] {
				visited[next] = true
				newPath := make([]string, len(current.path)+1)
				copy(newPath, current.path)
				newPath[len(current.path)] = next
				queue = append(queue, bfsEntry{pubkey: next, path: newPath})
			}
		}
	}

	return nil
}

// scoreTrustPath computes trust metrics for a single path.
func scoreTrustPath(path []string, graphSize int) TrustPath {
	hops := make([]TrustPathHop, len(path))
	weakestIdx := 0
	weakestScore := 101

	for i, pk := range path {
		raw, _ := graph.GetScore(pk)
		score := normalizeScore(raw, graphSize)

		isMutual := false
		if i < len(path)-1 {
			nextPk := path[i+1]
			// Check if next node also follows back
			nextFollows := graph.GetFollows(nextPk)
			for _, f := range nextFollows {
				if f == pk {
					isMutual = true
					break
				}
			}
		}

		hops[i] = TrustPathHop{
			Pubkey:   pk,
			WotScore: score,
			IsMutual: isMutual,
		}

		if score < weakestScore {
			weakestScore = score
			weakestIdx = i
		}
	}

	// Trust score: product of normalized hop scores / 100
	// Mutual follows get a 20% trust bonus per hop
	trustProduct := 1.0
	for i := 0; i < len(hops)-1; i++ {
		hopScore := float64(hops[i].WotScore) / 100.0
		if hopScore < 0.01 {
			hopScore = 0.01 // floor to prevent zero-multiplication
		}
		if hops[i].IsMutual {
			hopScore = math.Min(hopScore*1.2, 1.0)
		}
		trustProduct *= hopScore
	}

	// Apply length penalty: longer paths = less trust
	lengthPenalty := 1.0 / math.Pow(1.3, float64(len(path)-2))
	trustScore := trustProduct * lengthPenalty
	if trustScore > 1.0 {
		trustScore = 1.0
	}

	return TrustPath{
		Hops:       hops,
		Length:     len(path) - 1,
		TrustScore: round3(trustScore),
		WeakestHop: weakestIdx,
	}
}

// combinedTrust combines multiple path trust scores.
// Uses 1 - product(1 - trust_i): more paths = higher combined trust.
func combinedTrust(paths []TrustPath) float64 {
	if len(paths) == 0 {
		return 0
	}
	product := 1.0
	for _, p := range paths {
		product *= (1.0 - p.TrustScore)
	}
	return 1.0 - product
}

// classifyTrust returns a human-readable classification.
func classifyTrust(overallTrust float64) string {
	switch {
	case overallTrust >= 0.6:
		return "strong"
	case overallTrust >= 0.3:
		return "moderate"
	case overallTrust > 0:
		return "weak"
	default:
		return "none"
	}
}
