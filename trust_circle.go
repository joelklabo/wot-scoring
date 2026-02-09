package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// TrustCircleResponse is the top-level response for /trust-circle.
type TrustCircleResponse struct {
	Pubkey      string              `json:"pubkey"`
	TrustScore  int                 `json:"trust_score"`
	CircleSize  int                 `json:"circle_size"`
	Members     []CircleMember      `json:"members"`
	InnerCircle []CircleMember      `json:"inner_circle"`
	Metrics     CircleMetrics       `json:"metrics"`
	GraphSize   int                 `json:"graph_size"`
}

// CircleMember describes a member of the trust circle (mutual follow with scoring).
type CircleMember struct {
	Pubkey         string  `json:"pubkey"`
	TrustScore     int     `json:"trust_score"`
	Percentile     float64 `json:"percentile"`
	Rank           int     `json:"rank"`
	MutualStrength float64 `json:"mutual_strength"`
	SharedFollows  int     `json:"shared_follows"`
	Classification string  `json:"classification"`
}

// CircleMetrics describes aggregate properties of the trust circle.
type CircleMetrics struct {
	AvgTrustScore float64 `json:"avg_trust_score"`
	MedianTrust   int     `json:"median_trust"`
	Cohesion      float64 `json:"cohesion"`
	Density       float64 `json:"density"`
	TopRole       string  `json:"top_role"`
	RoleCounts    map[string]int `json:"role_counts"`
}

func handleTrustCircle(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	stats := graph.Stats()
	rawScore, _ := graph.GetScore(pubkey)
	selfScore := normalizeScore(rawScore, stats.Nodes)

	followers := graph.GetFollowers(pubkey)
	follows := graph.GetFollows(pubkey)

	// Build follows set for mutual detection
	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	// Find mutuals (bidirectional follows = trust circle)
	var mutuals []string
	for _, f := range followers {
		if followSet[f] {
			mutuals = append(mutuals, f)
		}
	}

	// Build circle members with scoring
	members := make([]CircleMember, 0, len(mutuals))
	for _, m := range mutuals {
		mRaw, _ := graph.GetScore(m)
		mScore := normalizeScore(mRaw, stats.Nodes)
		mPercentile := graph.Percentile(m)
		mRank := graph.Rank(m)
		mFollowers := graph.GetFollowers(m)
		mFollows := graph.GetFollows(m)

		// Shared follows: how many pubkeys do both follow?
		mFollowSet := make(map[string]bool, len(mFollows))
		for _, f := range mFollows {
			mFollowSet[f] = true
		}
		shared := 0
		for _, f := range follows {
			if mFollowSet[f] {
				shared++
			}
		}

		// Mutual strength: geometric mean of normalized scores, scaled by shared follows
		strength := 0.0
		if selfScore > 0 && mScore > 0 {
			strength = math.Sqrt(float64(selfScore)*float64(mScore)) / 100.0
			if shared > 0 {
				strength *= (1.0 + math.Log10(float64(shared)+1)/3.0)
				if strength > 1.0 {
					strength = 1.0
				}
			}
		}

		mMutualCount := 0
		mFollowerSet := make(map[string]bool, len(mFollowers))
		for _, f := range mFollowers {
			mFollowerSet[f] = true
		}
		for _, f := range mFollows {
			if mFollowerSet[f] {
				mMutualCount++
			}
		}

		classification := classifyInfluenceRole(mScore, len(mFollowers), len(mFollows), mMutualCount, mPercentile)

		members = append(members, CircleMember{
			Pubkey:         m,
			TrustScore:     mScore,
			Percentile:     math.Round(mPercentile*1000) / 1000,
			Rank:           mRank,
			MutualStrength: math.Round(strength*1000) / 1000,
			SharedFollows:  shared,
			Classification: classification,
		})
	}

	// Sort by trust score descending
	sort.Slice(members, func(i, j int) bool {
		return members[i].TrustScore > members[j].TrustScore
	})

	// Inner circle: top 10 by trust score
	innerSize := 10
	if innerSize > len(members) {
		innerSize = len(members)
	}
	innerCircle := make([]CircleMember, innerSize)
	copy(innerCircle, members[:innerSize])

	// Compute circle metrics
	metrics := computeCircleMetrics(members, graph, stats.Nodes)

	resp := TrustCircleResponse{
		Pubkey:      pubkey,
		TrustScore:  selfScore,
		CircleSize:  len(members),
		Members:     members,
		InnerCircle: innerCircle,
		Metrics:     metrics,
		GraphSize:   stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func computeCircleMetrics(members []CircleMember, g *Graph, totalNodes int) CircleMetrics {
	if len(members) == 0 {
		return CircleMetrics{
			RoleCounts: map[string]int{},
		}
	}

	// Average and median trust
	sum := 0.0
	scores := make([]int, len(members))
	for i, m := range members {
		sum += float64(m.TrustScore)
		scores[i] = m.TrustScore
	}
	avg := sum / float64(len(members))

	sort.Ints(scores)
	median := scores[len(scores)/2]

	// Role distribution
	roleCounts := make(map[string]int)
	for _, m := range members {
		roleCounts[m.Classification]++
	}
	topRole := ""
	topRoleCount := 0
	for role, count := range roleCounts {
		if count > topRoleCount {
			topRoleCount = count
			topRole = role
		}
	}

	// Cohesion: fraction of circle members that follow each other
	// Build set of circle members for fast lookup
	circleSet := make(map[string]bool, len(members))
	for _, m := range members {
		circleSet[m.Pubkey] = true
	}

	// Count intra-circle edges
	intraEdges := 0
	for _, m := range members {
		mFollows := g.GetFollows(m.Pubkey)
		for _, f := range mFollows {
			if circleSet[f] {
				intraEdges++
			}
		}
	}

	// Density: actual intra-circle edges / max possible edges
	n := len(members)
	maxEdges := n * (n - 1) // directed graph
	density := 0.0
	if maxEdges > 0 {
		density = float64(intraEdges) / float64(maxEdges)
	}

	// Cohesion: fraction of possible mutual edges (bidirectional) realized
	// Each mutual pair contributes 2 directed edges
	possibleMutualPairs := n * (n - 1) / 2
	mutualPairs := 0
	memberSlice := make([]string, len(members))
	for i, m := range members {
		memberSlice[i] = m.Pubkey
	}
	for i := 0; i < len(memberSlice); i++ {
		iFollows := make(map[string]bool)
		for _, f := range g.GetFollows(memberSlice[i]) {
			iFollows[f] = true
		}
		for j := i + 1; j < len(memberSlice); j++ {
			if !iFollows[memberSlice[j]] {
				continue
			}
			// Check reverse
			jFollows := g.GetFollows(memberSlice[j])
			for _, f := range jFollows {
				if f == memberSlice[i] {
					mutualPairs++
					break
				}
			}
		}
	}

	cohesion := 0.0
	if possibleMutualPairs > 0 {
		cohesion = float64(mutualPairs) / float64(possibleMutualPairs)
	}

	return CircleMetrics{
		AvgTrustScore: math.Round(avg*10) / 10,
		MedianTrust:   median,
		Cohesion:      math.Round(cohesion*1000) / 1000,
		Density:       math.Round(density*1000) / 1000,
		TopRole:       topRole,
		RoleCounts:    roleCounts,
	}
}
