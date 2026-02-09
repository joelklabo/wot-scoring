package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

// followEdge stores the timestamp of a follow relationship.
type followEdge struct {
	From      string
	To        string
	CreatedAt time.Time
}

// AddFollowWithTime records a follow relationship with a timestamp.
// If the timestamp is zero, falls back to AddFollow (no time data).
func (g *Graph) AddFollowWithTime(from, to string, createdAt time.Time) {
	g.AddFollow(from, to)
	if createdAt.IsZero() {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.followTimes == nil {
		g.followTimes = make(map[string]time.Time)
	}
	key := from + ":" + to
	g.followTimes[key] = createdAt
}

// GetFollowTime returns the timestamp of a follow, or zero if unknown.
func (g *Graph) GetFollowTime(from, to string) time.Time {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.followTimes == nil {
		return time.Time{}
	}
	return g.followTimes[from+":"+to]
}

// decayWeight computes an exponential decay weight for an edge.
// halfLifeDays controls how fast old follows lose weight.
// Returns a value in (0, 1] where 1.0 = just created, 0.5 = halfLifeDays ago.
func decayWeight(createdAt time.Time, now time.Time, halfLifeDays float64) float64 {
	if createdAt.IsZero() || halfLifeDays <= 0 {
		return 1.0 // no time data = full weight
	}
	ageDays := now.Sub(createdAt).Hours() / 24.0
	if ageDays < 0 {
		ageDays = 0
	}
	lambda := math.Ln2 / halfLifeDays
	return math.Exp(-lambda * ageDays)
}

// ComputeDecayedPageRank runs PageRank with time-decayed edge weights.
// Newer follows contribute more to a node's score than older ones.
func (g *Graph) ComputeDecayedPageRank(iterations int, damping float64, halfLifeDays float64) map[string]float64 {
	g.mu.RLock()

	now := time.Now()

	// Collect all nodes
	nodes := make(map[string]bool)
	for k, vs := range g.follows {
		nodes[k] = true
		for _, v := range vs {
			nodes[v] = true
		}
	}

	n := float64(len(nodes))
	if n == 0 {
		g.mu.RUnlock()
		return make(map[string]float64)
	}

	// Pre-compute decay weights for all edges
	type weightedEdge struct {
		weight float64
	}
	edgeWeights := make(map[string]float64) // "from:to" -> weight
	outWeightSum := make(map[string]float64) // from -> sum of outgoing weights

	for from, tos := range g.follows {
		for _, to := range tos {
			key := from + ":" + to
			var w float64
			if g.followTimes != nil {
				w = decayWeight(g.followTimes[key], now, halfLifeDays)
			} else {
				w = 1.0
			}
			edgeWeights[key] = w
			outWeightSum[from] += w
		}
	}

	// Copy followers map for iteration
	followersCopy := make(map[string][]string, len(g.followers))
	for k, v := range g.followers {
		followersCopy[k] = v
	}

	g.mu.RUnlock()

	// Initialize scores uniformly
	scores := make(map[string]float64)
	for node := range nodes {
		scores[node] = 1.0 / n
	}

	for i := 0; i < iterations; i++ {
		newScores := make(map[string]float64)
		for node := range nodes {
			sum := 0.0
			for _, follower := range followersCopy[node] {
				key := follower + ":" + node
				w := edgeWeights[key]
				totalOut := outWeightSum[follower]
				if totalOut > 0 {
					sum += scores[follower] * w / totalOut
				}
			}
			newScores[node] = (1-damping)/n + damping*sum
		}
		scores = newScores
	}

	return scores
}

// DecayScoreEntry represents a single decay score result.
type DecayScoreEntry struct {
	Pubkey      string  `json:"pubkey"`
	DecayScore  int     `json:"decay_score"`
	StaticScore int     `json:"static_score"`
	Delta       int     `json:"delta"`
	OldestFollow string `json:"oldest_follow,omitempty"`
	NewestFollow string `json:"newest_follow,omitempty"`
}

func handleDecay(w http.ResponseWriter, r *http.Request) {
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

	halfLifeStr := r.URL.Query().Get("half_life")
	halfLifeDays := 365.0 // default: 1 year half-life
	if halfLifeStr != "" {
		if n, err := fmt.Sscanf(halfLifeStr, "%f", &halfLifeDays); n != 1 || err != nil || halfLifeDays < 1 {
			halfLifeDays = 365.0
		}
		if halfLifeDays > 3650 {
			halfLifeDays = 3650 // cap at 10 years
		}
	}

	stats := graph.Stats()

	// Static score (standard PageRank)
	staticRaw, found := graph.GetScore(pubkey)
	staticScore := normalizeScore(staticRaw, stats.Nodes)

	// Decay-adjusted score
	decayScores := graph.ComputeDecayedPageRank(20, 0.85, halfLifeDays)
	decayRaw := decayScores[pubkey]
	decayScore := normalizeScore(decayRaw, stats.Nodes)

	// Find oldest and newest follow times for this pubkey's followers
	followers := graph.GetFollowers(pubkey)
	var oldest, newest time.Time
	for _, f := range followers {
		t := graph.GetFollowTime(f, pubkey)
		if t.IsZero() {
			continue
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
		if newest.IsZero() || t.After(newest) {
			newest = t
		}
	}

	resp := map[string]interface{}{
		"pubkey":         pubkey,
		"decay_score":    decayScore,
		"static_score":   staticScore,
		"delta":          decayScore - staticScore,
		"half_life_days": halfLifeDays,
		"found":          found,
		"follower_count": len(followers),
		"graph_size":     stats.Nodes,
	}

	if !oldest.IsZero() {
		resp["oldest_follow"] = oldest.UTC().Format(time.RFC3339)
	}
	if !newest.IsZero() {
		resp["newest_follow"] = newest.UTC().Format(time.RFC3339)
	}

	// Count follows with time data
	withTime := 0
	for _, f := range followers {
		if !graph.GetFollowTime(f, pubkey).IsZero() {
			withTime++
		}
	}
	resp["followers_with_time_data"] = withTime

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleDecayTop returns the top N pubkeys by decay-adjusted score, showing
// who gains and loses rank when temporal freshness is factored in.
func handleDecayTop(w http.ResponseWriter, r *http.Request) {
	halfLifeStr := r.URL.Query().Get("half_life")
	halfLifeDays := 365.0
	if halfLifeStr != "" {
		if n, err := fmt.Sscanf(halfLifeStr, "%f", &halfLifeDays); n != 1 || err != nil || halfLifeDays < 1 {
			halfLifeDays = 365.0
		}
		if halfLifeDays > 3650 {
			halfLifeDays = 3650
		}
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 {
			limit = 50
		}
		if limit > 200 {
			limit = 200
		}
	}

	stats := graph.Stats()
	decayScores := graph.ComputeDecayedPageRank(20, 0.85, halfLifeDays)

	type entry struct {
		Pubkey      string  `json:"pubkey"`
		DecayScore  int     `json:"decay_score"`
		StaticScore int     `json:"static_score"`
		Delta       int     `json:"delta"`
		DecayRank   int     `json:"decay_rank"`
		StaticRank  int     `json:"static_rank"`
		RankChange  int     `json:"rank_change"`
	}

	// Build sorted list by decay score
	entries := make([]entry, 0, len(decayScores))
	for pk, decayRaw := range decayScores {
		staticRaw, _ := graph.GetScore(pk)
		ds := normalizeScore(decayRaw, stats.Nodes)
		ss := normalizeScore(staticRaw, stats.Nodes)
		entries = append(entries, entry{
			Pubkey:      pk,
			DecayScore:  ds,
			StaticScore: ss,
			Delta:       ds - ss,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].DecayScore > entries[j].DecayScore
	})

	// Assign decay ranks
	for i := range entries {
		entries[i].DecayRank = i + 1
	}

	// Build static rank lookup
	staticRanks := make(map[string]int)
	staticSorted := make([]entry, len(entries))
	copy(staticSorted, entries)
	sort.Slice(staticSorted, func(i, j int) bool {
		return staticSorted[i].StaticScore > staticSorted[j].StaticScore
	})
	for i, e := range staticSorted {
		staticRanks[e.Pubkey] = i + 1
	}

	// Assign static ranks and rank changes
	for i := range entries {
		entries[i].StaticRank = staticRanks[entries[i].Pubkey]
		entries[i].RankChange = entries[i].StaticRank - entries[i].DecayRank // positive = improved with decay
	}

	if len(entries) > limit {
		entries = entries[:limit]
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"entries":        entries,
		"half_life_days": halfLifeDays,
		"graph_size":     stats.Nodes,
		"algorithm":      "PageRank with exponential time decay",
	})
}
