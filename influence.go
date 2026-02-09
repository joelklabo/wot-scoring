package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// InfluenceResponse represents the influence propagation analysis.
type InfluenceResponse struct {
	Pubkey        string            `json:"pubkey"`
	Action        string            `json:"action"`
	Other         string            `json:"other"`
	CurrentScore  int               `json:"current_score"`
	SimulatedScore int              `json:"simulated_score"`
	ScoreDelta    int               `json:"score_delta"`
	AffectedCount int               `json:"affected_count"`
	MaxDelta      float64           `json:"max_delta"`
	TopAffected   []AffectedPubkey  `json:"top_affected"`
	Summary       InfluenceSummary  `json:"summary"`
	GraphSize     int               `json:"graph_size"`
}

// AffectedPubkey represents a pubkey whose score changed in the simulation.
type AffectedPubkey struct {
	Pubkey       string  `json:"pubkey"`
	CurrentScore int     `json:"current_score"`
	NewScore     int     `json:"new_score"`
	Delta        int     `json:"delta"`
	RawDelta     float64 `json:"raw_delta"`
	Direction    string  `json:"direction"` // "increase" or "decrease"
}

// InfluenceSummary provides aggregate metrics about the influence propagation.
type InfluenceSummary struct {
	TotalPositive    int     `json:"total_positive"`
	TotalNegative    int     `json:"total_negative"`
	AvgDelta         float64 `json:"avg_delta"`
	InfluenceRadius  string  `json:"influence_radius"`
	Classification   string  `json:"classification"`
}

func handleInfluence(w http.ResponseWriter, r *http.Request) {
	pubkeyRaw := r.URL.Query().Get("pubkey")
	if pubkeyRaw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(pubkeyRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	action := r.URL.Query().Get("action")
	if action == "" {
		action = "follow"
	}
	if action != "follow" && action != "unfollow" {
		http.Error(w, `{"error":"action must be 'follow' or 'unfollow'"}`, http.StatusBadRequest)
		return
	}

	otherRaw := r.URL.Query().Get("other")
	if otherRaw == "" {
		http.Error(w, `{"error":"other parameter required (the pubkey that follows/unfollows)"}`, http.StatusBadRequest)
		return
	}

	other, err := resolvePubkey(otherRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid other: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if pubkey == other {
		http.Error(w, `{"error":"pubkey and other must be different"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()

	// Get current scores snapshot
	currentScores := graph.ScoresSnapshot()

	// Build a simulated graph with the hypothetical change
	simFollows, simFollowers := graph.FollowsSnapshot()

	if action == "follow" {
		// Add other -> pubkey follow
		simFollows[other] = append(simFollows[other], pubkey)
		simFollowers[pubkey] = append(simFollowers[pubkey], other)
	} else {
		// Remove other -> pubkey follow
		simFollows[other] = removeFromSlice(simFollows[other], pubkey)
		simFollowers[pubkey] = removeFromSlice(simFollowers[pubkey], other)
	}

	// Run PageRank on the simulated graph
	simScores := computePageRankOnSnapshot(simFollows, simFollowers, 20, 0.85)

	// Compare scores and find affected pubkeys
	var affected []AffectedPubkey
	totalPositive := 0
	totalNegative := 0
	maxRawDelta := 0.0
	sumAbsDelta := 0.0
	affectedCount := 0

	for pk, newRaw := range simScores {
		oldRaw := currentScores[pk]
		rawDelta := newRaw - oldRaw

		if math.Abs(rawDelta) < 1e-12 {
			continue
		}

		oldNorm := normalizeScore(oldRaw, stats.Nodes)
		newNorm := normalizeScore(newRaw, stats.Nodes)
		normDelta := newNorm - oldNorm

		affectedCount++
		sumAbsDelta += math.Abs(rawDelta)

		if math.Abs(rawDelta) > maxRawDelta {
			maxRawDelta = math.Abs(rawDelta)
		}

		if rawDelta > 0 {
			totalPositive++
		} else {
			totalNegative++
		}

		// Only include in top_affected if the normalized score changed
		if normDelta != 0 || pk == pubkey {
			direction := "increase"
			if rawDelta < 0 {
				direction = "decrease"
			}
			affected = append(affected, AffectedPubkey{
				Pubkey:       pk,
				CurrentScore: oldNorm,
				NewScore:     newNorm,
				Delta:        normDelta,
				RawDelta:     math.Round(rawDelta*1e9) / 1e9,
				Direction:    direction,
			})
		}
	}

	// Sort by absolute delta descending
	sort.Slice(affected, func(i, j int) bool {
		return math.Abs(float64(affected[i].Delta))+math.Abs(affected[i].RawDelta) >
			math.Abs(float64(affected[j].Delta))+math.Abs(affected[j].RawDelta)
	})

	// Limit to top 20
	if len(affected) > 20 {
		affected = affected[:20]
	}

	// Compute summary
	avgDelta := 0.0
	if affectedCount > 0 {
		avgDelta = sumAbsDelta / float64(affectedCount)
	}

	currentNorm := normalizeScore(currentScores[pubkey], stats.Nodes)
	simNorm := normalizeScore(simScores[pubkey], stats.Nodes)

	resp := InfluenceResponse{
		Pubkey:         pubkey,
		Action:         action,
		Other:          other,
		CurrentScore:   currentNorm,
		SimulatedScore: simNorm,
		ScoreDelta:     simNorm - currentNorm,
		AffectedCount:  affectedCount,
		MaxDelta:       math.Round(maxRawDelta*1e9) / 1e9,
		TopAffected:    affected,
		Summary: InfluenceSummary{
			TotalPositive:   totalPositive,
			TotalNegative:   totalNegative,
			AvgDelta:        math.Round(avgDelta*1e12) / 1e12,
			InfluenceRadius: classifyRadius(affectedCount, stats.Nodes),
			Classification:  classifyInfluence(affectedCount, maxRawDelta, stats.Nodes),
		},
		GraphSize: stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

// computePageRankOnSnapshot runs PageRank on a copy of the graph data.
func computePageRankOnSnapshot(follows, followers map[string][]string, iterations int, damping float64) map[string]float64 {
	// Collect all nodes
	nodes := make(map[string]bool)
	for k, vs := range follows {
		nodes[k] = true
		for _, v := range vs {
			nodes[v] = true
		}
	}
	for k := range followers {
		nodes[k] = true
	}

	n := float64(len(nodes))
	if n == 0 {
		return make(map[string]float64)
	}

	scores := make(map[string]float64, len(nodes))
	for node := range nodes {
		scores[node] = 1.0 / n
	}

	for i := 0; i < iterations; i++ {
		newScores := make(map[string]float64, len(nodes))
		for node := range nodes {
			sum := 0.0
			for _, follower := range followers[node] {
				outDegree := len(follows[follower])
				if outDegree > 0 {
					sum += scores[follower] / float64(outDegree)
				}
			}
			newScores[node] = (1-damping)/n + damping*sum
		}
		scores = newScores
	}

	return scores
}

func removeFromSlice(s []string, val string) []string {
	result := make([]string, 0, len(s))
	for _, v := range s {
		if v != val {
			result = append(result, v)
		}
	}
	return result
}

func classifyRadius(affected, total int) string {
	if total == 0 {
		return "none"
	}
	ratio := float64(affected) / float64(total)
	switch {
	case ratio >= 0.5:
		return "global"
	case ratio >= 0.1:
		return "wide"
	case ratio >= 0.01:
		return "moderate"
	case affected > 0:
		return "local"
	default:
		return "none"
	}
}

func classifyInfluence(affected int, maxDelta float64, total int) string {
	if total == 0 {
		return "negligible"
	}
	ratio := float64(affected) / float64(total)
	switch {
	case ratio >= 0.3 && maxDelta > 1e-6:
		return "transformative"
	case ratio >= 0.1 && maxDelta > 1e-7:
		return "significant"
	case ratio >= 0.01:
		return "moderate"
	case affected > 0:
		return "minimal"
	default:
		return "negligible"
	}
}
