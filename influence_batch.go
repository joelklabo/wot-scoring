package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
)

// InfluenceBatchRequest is the POST body for /influence/batch.
type InfluenceBatchRequest struct {
	Pubkeys []string `json:"pubkeys"`
}

// InfluenceBatchResponse is the top-level response.
type InfluenceBatchResponse struct {
	Results   []InfluenceEntry `json:"results"`
	GraphSize int              `json:"graph_size"`
}

// InfluenceEntry describes a single pubkey's static influence in the graph.
type InfluenceEntry struct {
	Pubkey             string  `json:"pubkey"`
	TrustScore         int     `json:"trust_score"`
	Percentile         float64 `json:"percentile"`
	Rank               int     `json:"rank"`
	Followers          int     `json:"followers"`
	Follows            int     `json:"follows"`
	AvgFollowerQuality float64 `json:"avg_follower_quality"`
	MutualCount        int     `json:"mutual_count"`
	ReachEstimate      int     `json:"reach_estimate"`
	Classification     string  `json:"classification"`
	Error              string  `json:"error,omitempty"`
}

func handleInfluenceBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req InfluenceBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Pubkeys) == 0 {
		http.Error(w, `{"error":"pubkeys array required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Pubkeys) > 50 {
		http.Error(w, `{"error":"maximum 50 pubkeys per batch"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()
	results := make([]InfluenceEntry, 0, len(req.Pubkeys))

	for _, raw := range req.Pubkeys {
		pubkey, err := resolvePubkey(raw)
		if err != nil {
			results = append(results, InfluenceEntry{Pubkey: raw, Error: err.Error()})
			continue
		}

		rawScore, _ := graph.GetScore(pubkey)
		score := normalizeScore(rawScore, stats.Nodes)
		followers := graph.GetFollowers(pubkey)
		follows := graph.GetFollows(pubkey)
		percentile := graph.Percentile(pubkey)
		rank := graph.Rank(pubkey)

		// Build follows set for mutual detection
		followSet := make(map[string]bool, len(follows))
		for _, f := range follows {
			followSet[f] = true
		}

		// Compute follower quality and mutual count
		followerScoreSum := 0.0
		scoredFollowers := 0
		mutualCount := 0
		for _, f := range followers {
			fRaw, ok := graph.GetScore(f)
			if ok {
				followerScoreSum += float64(normalizeScore(fRaw, stats.Nodes))
				scoredFollowers++
			}
			if followSet[f] {
				mutualCount++
			}
		}

		avgFollowerQuality := 0.0
		if scoredFollowers > 0 {
			avgFollowerQuality = math.Round(followerScoreSum/float64(scoredFollowers)*10) / 10
		}

		// Reach estimate: followers + unique followers-of-followers
		reachSet := make(map[string]bool, len(followers)*5)
		for _, f := range followers {
			reachSet[f] = true
			for _, ff := range graph.GetFollowers(f) {
				reachSet[ff] = true
			}
		}
		// Remove self from reach
		delete(reachSet, pubkey)
		reachEstimate := len(reachSet)

		classification := classifyInfluenceRole(score, len(followers), len(follows), mutualCount, percentile)

		results = append(results, InfluenceEntry{
			Pubkey:             pubkey,
			TrustScore:         score,
			Percentile:         math.Round(percentile*1000) / 1000,
			Rank:               rank,
			Followers:          len(followers),
			Follows:            len(follows),
			AvgFollowerQuality: avgFollowerQuality,
			MutualCount:        mutualCount,
			ReachEstimate:      reachEstimate,
			Classification:     classification,
		})
	}

	// Sort by trust score descending
	sort.Slice(results, func(i, j int) bool {
		if results[i].Error != "" && results[j].Error == "" {
			return false
		}
		if results[i].Error == "" && results[j].Error != "" {
			return true
		}
		return results[i].TrustScore > results[j].TrustScore
	})

	resp := InfluenceBatchResponse{
		Results:   results,
		GraphSize: stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

// classifyInfluenceRole determines the role of a pubkey in the trust network.
func classifyInfluenceRole(score, followers, follows, mutuals int, percentile float64) string {
	if followers == 0 && follows == 0 {
		return "isolated"
	}
	if followers == 0 {
		return "observer" // follows others but nobody follows them
	}
	if percentile >= 0.99 && followers > 50 {
		return "hub" // top 1% with many followers
	}
	if percentile >= 0.90 && followers > 20 {
		return "authority" // top 10% with significant followers
	}
	if mutuals > 0 && float64(mutuals)/float64(followers) > 0.5 {
		return "connector" // high mutual ratio — bridges communities
	}
	if follows > followers*3 && followers < 10 {
		return "consumer" // follows many, few followers
	}
	return "participant" // average network member
}
