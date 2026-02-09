package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// ReputationComponent is a scored dimension of the reputation profile.
type ReputationComponent struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`       // 0.0-1.0
	Weight      float64 `json:"weight"`      // contribution to final score
	Grade       string  `json:"grade"`       // A-F for this component
	Description string  `json:"description"` // human-readable summary
}

// ReputationResponse is the response for the /reputation endpoint.
type ReputationResponse struct {
	Pubkey          string                `json:"pubkey"`
	ReputationScore int                   `json:"reputation_score"` // 0-100
	Grade           string                `json:"grade"`            // A, B, C, D, F
	Classification  string                `json:"classification"`   // "excellent", "good", "fair", "poor", "untrusted"
	Confidence      float64               `json:"confidence"`       // 0.0-1.0
	Components      []ReputationComponent `json:"components"`       // breakdown
	Summary         string                `json:"summary"`          // one-line human-readable summary

	// Quick reference fields
	TrustScore       int     `json:"trust_score"`        // WoT PageRank normalized 0-100
	SybilScore       int     `json:"sybil_score"`        // Sybil resistance 0-100
	AnomalyCount     int     `json:"anomaly_count"`      // number of anomaly flags
	CommunitySize    int     `json:"community_size"`     // size of detected community
	Followers        int     `json:"followers"`
	Follows          int     `json:"follows"`
	MutualCount      int     `json:"mutual_count"`
	Percentile       float64 `json:"percentile"`         // 0.0-1.0 in the graph
	GraphSize        int     `json:"graph_size"`
}

// handleReputation computes a comprehensive reputation profile for a pubkey.
// GET /reputation?pubkey=<hex|npub>
func handleReputation(w http.ResponseWriter, r *http.Request) {
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

	stats := graph.Stats()
	rawScore, found := graph.GetScore(pubkey)
	score := normalizeScore(rawScore, stats.Nodes)
	percentile := graph.Percentile(pubkey)

	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)

	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	// --- Component 1: WoT Standing (PageRank position) ---
	wotStanding := 0.0
	if found {
		// Percentile-based: top 1% = 1.0, top 10% = 0.7, top 50% = 0.4
		wotStanding = math.Min(percentile*1.2, 1.0)
	}

	// --- Component 2: Sybil Resistance ---
	// Recompute key sybil signals inline for efficiency
	followerScoreSum := 0
	scoredFollowers := 0
	for _, f := range followers {
		fRaw, ok := graph.GetScore(f)
		if ok {
			followerScoreSum += normalizeScore(fRaw, stats.Nodes)
			scoredFollowers++
		}
	}
	avgFollowerScore := 0.0
	if scoredFollowers > 0 {
		avgFollowerScore = float64(followerScoreSum) / float64(scoredFollowers)
	}
	followerQuality := math.Min(avgFollowerScore/30.0, 1.0)

	mutualCount := 0
	highValueMutuals := 0
	for _, f := range followers {
		if followSet[f] {
			mutualCount++
			fRaw, ok := graph.GetScore(f)
			if ok && normalizeScore(fRaw, stats.Nodes) > 50 {
				highValueMutuals++
			}
		}
	}

	mutualTrust := 0.0
	if len(followers) > 0 {
		mutualRatio := float64(mutualCount) / float64(len(followers))
		if mutualRatio >= 0.10 && mutualRatio <= 0.60 {
			mutualTrust = 0.8
		} else if mutualRatio > 0.60 && mutualRatio <= 0.90 {
			mutualTrust = 0.5
		} else if mutualRatio > 0.90 {
			mutualTrust = 0.2
		} else {
			mutualTrust = 0.4
		}
		if highValueMutuals > 3 {
			mutualTrust = math.Min(mutualTrust+0.2, 1.0)
		}
	}

	sybilResistance := followerQuality*0.5 + mutualTrust*0.5
	sybilScoreInt := int(math.Round(sybilResistance * 100))
	if sybilScoreInt > 100 {
		sybilScoreInt = 100
	}

	// --- Component 3: Community Integration ---
	communityIntegration := 0.0
	communitySize := 0
	if label, ok := communities.GetCommunity(pubkey); ok {
		_ = label
		members := communities.GetCommunityMembers(pubkey)
		communitySize = len(members)

		// Larger community = better integration (up to 1.0 at 100+ members)
		sizeFactor := math.Min(float64(communitySize)/100.0, 1.0)

		// Check quality: what's the average score in this community?
		communityScoreSum := 0
		scoredMembers := 0
		for _, m := range members {
			if mRaw, ok := graph.GetScore(m); ok {
				communityScoreSum += normalizeScore(mRaw, stats.Nodes)
				scoredMembers++
			}
		}
		avgCommunityScore := 0.0
		if scoredMembers > 0 {
			avgCommunityScore = float64(communityScoreSum) / float64(scoredMembers)
		}
		qualityFactor := math.Min(avgCommunityScore/20.0, 1.0)

		communityIntegration = sizeFactor*0.4 + qualityFactor*0.6
	}

	// --- Component 4: Anomaly Cleanliness ---
	// Lower anomaly count = higher score
	anomalyCount := computeAnomalyCount(pubkey, follows, followers, followSet, stats.Nodes, percentile)
	anomalyCleanliness := 1.0
	switch {
	case anomalyCount == 0:
		anomalyCleanliness = 1.0
	case anomalyCount == 1:
		anomalyCleanliness = 0.7
	case anomalyCount == 2:
		anomalyCleanliness = 0.4
	case anomalyCount >= 3:
		anomalyCleanliness = 0.1
	}

	// --- Component 5: Network Diversity ---
	// Followers from diverse parts of the graph
	followerDiversity := computeFollowerDiversity(followers, stats.Nodes)

	// --- Combine into reputation score ---
	components := []ReputationComponent{
		{
			Name:        "wot_standing",
			Score:       round3(wotStanding),
			Weight:      0.30,
			Grade:       gradeFromScore(wotStanding),
			Description: fmt.Sprintf("WoT percentile: %.0f%% (score %d of %d nodes)", percentile*100, score, stats.Nodes),
		},
		{
			Name:        "sybil_resistance",
			Score:       round3(sybilResistance),
			Weight:      0.25,
			Grade:       gradeFromScore(sybilResistance),
			Description: fmt.Sprintf("Follower quality %.1f, %d mutuals (%d high-value)", avgFollowerScore, mutualCount, highValueMutuals),
		},
		{
			Name:        "community_integration",
			Score:       round3(communityIntegration),
			Weight:      0.15,
			Grade:       gradeFromScore(communityIntegration),
			Description: fmt.Sprintf("Community size: %d members", communitySize),
		},
		{
			Name:        "anomaly_cleanliness",
			Score:       round3(anomalyCleanliness),
			Weight:      0.15,
			Grade:       gradeFromScore(anomalyCleanliness),
			Description: fmt.Sprintf("%d anomaly flags detected", anomalyCount),
		},
		{
			Name:        "network_diversity",
			Score:       round3(followerDiversity),
			Weight:      0.15,
			Grade:       gradeFromScore(followerDiversity),
			Description: "Follower diversity across graph regions",
		},
	}

	// Sort by weight descending for display
	sort.Slice(components, func(i, j int) bool {
		return components[i].Weight > components[j].Weight
	})

	finalScore := 0.0
	for _, c := range components {
		finalScore += c.Score * c.Weight
	}
	reputationScore := int(math.Round(finalScore * 100))
	if reputationScore > 100 {
		reputationScore = 100
	}

	grade := gradeFromScoreInt(reputationScore)
	classification := classifyReputation(reputationScore)
	confidence := computeConfidence(len(followers), len(follows), found, scoredFollowers)
	summary := buildReputationSummary(pubkey, reputationScore, grade, score, anomalyCount, communitySize)

	resp := ReputationResponse{
		Pubkey:          pubkey,
		ReputationScore: reputationScore,
		Grade:           grade,
		Classification:  classification,
		Confidence:      round3(confidence),
		Components:      components,
		Summary:         summary,
		TrustScore:      score,
		SybilScore:      sybilScoreInt,
		AnomalyCount:    anomalyCount,
		CommunitySize:   communitySize,
		Followers:       len(followers),
		Follows:         len(follows),
		MutualCount:     mutualCount,
		Percentile:      round3(percentile),
		GraphSize:       stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// computeAnomalyCount returns the number of anomaly flags for a pubkey.
// Inline version of anomaly detection for efficiency.
func computeAnomalyCount(pubkey string, follows, followers []string, followSet map[string]bool, graphSize int, percentile float64) int {
	count := 0
	stats := graph.Stats()

	// 1. Follow farming: following >500 with follow-back ratio >80%
	followBackCount := 0
	for _, f := range followers {
		if followSet[f] {
			followBackCount++
		}
	}
	if len(follows) > 500 {
		followBackRatio := 0.0
		if len(followers) > 0 {
			followBackRatio = float64(followBackCount) / float64(len(followers))
		}
		if followBackRatio > 0.80 {
			count++
		}
	}

	// 2. Ghost followers: >50% followers with negligible score
	ghostCount := 0
	isTopPercentile := percentile > 0.99
	for _, f := range followers {
		fRaw, ok := graph.GetScore(f)
		if !ok {
			ghostCount++
		} else {
			ns := normalizeScore(fRaw, stats.Nodes)
			if ns < 5 {
				ghostCount++
			}
		}
	}
	if len(followers) > 10 && !isTopPercentile {
		ghostRatio := float64(ghostCount) / float64(len(followers))
		if ghostRatio > 0.50 {
			count++
		}
	}

	// 3. Trust concentration: >40% of score from a single follower
	if len(followers) > 5 {
		rawScore, _ := graph.GetScore(pubkey)
		if rawScore > 0 {
			maxContribution := 0.0
			for _, f := range followers {
				fRaw, _ := graph.GetScore(f)
				fFollows := graph.GetFollows(f)
				if len(fFollows) > 0 {
					contribution := fRaw / float64(len(fFollows))
					if contribution > maxContribution {
						maxContribution = contribution
					}
				}
			}
			if rawScore > 0 {
				topShare := maxContribution / rawScore
				if topShare > 0.40 {
					count++
				}
			}
		}
	}

	// 4. Excessive following: >2000 follows
	if len(follows) > 2000 {
		count++
	}

	// 5. Score-follower divergence: many followers but very low score
	if len(followers) > 100 {
		rawScore, _ := graph.GetScore(pubkey)
		ns := normalizeScore(rawScore, graphSize)
		if ns < 10 {
			count++
		}
	}

	return count
}

func gradeFromScore(score float64) string {
	switch {
	case score >= 0.8:
		return "A"
	case score >= 0.6:
		return "B"
	case score >= 0.4:
		return "C"
	case score >= 0.2:
		return "D"
	default:
		return "F"
	}
}

func gradeFromScoreInt(score int) string {
	switch {
	case score >= 80:
		return "A"
	case score >= 60:
		return "B"
	case score >= 40:
		return "C"
	case score >= 20:
		return "D"
	default:
		return "F"
	}
}

func classifyReputation(score int) string {
	switch {
	case score >= 80:
		return "excellent"
	case score >= 60:
		return "good"
	case score >= 40:
		return "fair"
	case score >= 20:
		return "poor"
	default:
		return "untrusted"
	}
}

func buildReputationSummary(pubkey string, score int, grade string, wotScore int, anomalies int, communitySize int) string {
	short := pubkey
	if len(short) > 12 {
		short = short[:8] + "..." + short[len(short)-4:]
	}

	anomalyStr := "no anomalies"
	if anomalies == 1 {
		anomalyStr = "1 anomaly flag"
	} else if anomalies > 1 {
		anomalyStr = fmt.Sprintf("%d anomaly flags", anomalies)
	}

	return fmt.Sprintf("%s: Grade %s (%d/100) — WoT score %d, %s, community of %d",
		short, grade, score, wotScore, anomalyStr, communitySize)
}
