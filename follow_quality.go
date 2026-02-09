package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// FollowQualityResponse is the top-level response for /follow-quality.
type FollowQualityResponse struct {
	Pubkey          string               `json:"pubkey"`
	TrustScore      int                  `json:"trust_score"`
	FollowCount     int                  `json:"follow_count"`
	QualityScore    int                  `json:"quality_score"`    // 0-100 overall quality
	Classification  string               `json:"classification"`  // "excellent", "good", "moderate", "poor"
	Breakdown       FollowQualityBreak   `json:"breakdown"`
	Categories      FollowCategories     `json:"categories"`
	Suggestions     []FollowSuggestion   `json:"suggestions"`     // lowest quality follows to reconsider
	GraphSize       int                  `json:"graph_size"`
}

// FollowQualityBreak breaks down the quality score into components.
type FollowQualityBreak struct {
	AvgTrustScore    float64 `json:"avg_trust_score"`    // mean trust score of follows
	MedianTrustScore int     `json:"median_trust_score"` // median trust score of follows
	Diversity        float64 `json:"diversity"`           // 0-1, how diverse the follow list is
	Reciprocity      float64 `json:"reciprocity"`        // 0-1, fraction that follow back
	SignalRatio      float64 `json:"signal_ratio"`       // 0-1, fraction with meaningful trust scores
}

// FollowCategories breaks follows into quality tiers.
type FollowCategories struct {
	Strong     int `json:"strong"`     // trust score >= 60
	Moderate   int `json:"moderate"`   // trust score 30-59
	Weak       int `json:"weak"`       // trust score 1-29
	Unknown    int `json:"unknown"`    // trust score 0 (not in graph or no score)
}

// FollowSuggestion suggests a follow to reconsider (lowest quality).
type FollowSuggestion struct {
	Pubkey     string `json:"pubkey"`
	TrustScore int    `json:"trust_score"`
	Reason     string `json:"reason"`
}

// handleFollowQuality analyzes the quality of a pubkey's follow list.
// GET /follow-quality?pubkey=<hex|npub>&suggestions=10
func handleFollowQuality(w http.ResponseWriter, r *http.Request) {
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

	suggLimit := 10
	if s := r.URL.Query().Get("suggestions"); s != "" {
		var n int
		if cnt, _ := fmt.Sscanf(s, "%d", &n); cnt == 1 && n >= 0 && n <= 50 {
			suggLimit = n
		}
	}

	stats := graph.Stats()
	rawScore, _ := graph.GetScore(pubkey)
	selfScore := normalizeScore(rawScore, stats.Nodes)

	follows := graph.GetFollows(pubkey)
	if len(follows) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		json.NewEncoder(w).Encode(FollowQualityResponse{
			Pubkey:         pubkey,
			TrustScore:     selfScore,
			FollowCount:    0,
			QualityScore:   0,
			Classification: "insufficient_data",
			Breakdown:      FollowQualityBreak{},
			Categories:     FollowCategories{},
			GraphSize:      stats.Nodes,
		})
		return
	}

	// Build follower set for reciprocity check
	followers := graph.GetFollowers(pubkey)
	followerSet := make(map[string]bool, len(followers))
	for _, f := range followers {
		followerSet[f] = true
	}

	// Analyze each follow
	type followInfo struct {
		pubkey     string
		trustScore int
		followsBack bool
		hasScore   bool
	}

	infos := make([]followInfo, len(follows))
	trustScores := make([]int, 0, len(follows))
	trustSum := 0
	reciprocalCount := 0
	scoredCount := 0
	var categories FollowCategories

	for i, f := range follows {
		fRaw, found := graph.GetScore(f)
		fScore := normalizeScore(fRaw, stats.Nodes)
		followsBack := followerSet[f]

		infos[i] = followInfo{
			pubkey:      f,
			trustScore:  fScore,
			followsBack: followsBack,
			hasScore:    found && fScore > 0,
		}

		if followsBack {
			reciprocalCount++
		}

		if found && fScore > 0 {
			scoredCount++
			trustSum += fScore
			trustScores = append(trustScores, fScore)
		}

		switch {
		case fScore >= 60:
			categories.Strong++
		case fScore >= 30:
			categories.Moderate++
		case fScore >= 1:
			categories.Weak++
		default:
			categories.Unknown++
		}
	}

	// Calculate metrics
	avgTrust := 0.0
	if scoredCount > 0 {
		avgTrust = float64(trustSum) / float64(scoredCount)
	}

	medianTrust := 0
	if len(trustScores) > 0 {
		sort.Ints(trustScores)
		mid := len(trustScores) / 2
		if len(trustScores)%2 == 0 {
			medianTrust = (trustScores[mid-1] + trustScores[mid]) / 2
		} else {
			medianTrust = trustScores[mid]
		}
	}

	reciprocity := float64(reciprocalCount) / float64(len(follows))
	signalRatio := float64(scoredCount) / float64(len(follows))

	// Diversity: how many distinct "score buckets" are represented
	// Use the entropy of the category distribution as diversity measure
	total := float64(len(follows))
	diversity := 0.0
	for _, count := range []int{categories.Strong, categories.Moderate, categories.Weak, categories.Unknown} {
		if count > 0 {
			p := float64(count) / total
			diversity -= p * math.Log2(p)
		}
	}
	// Normalize by max entropy (log2(4) = 2.0)
	diversity /= 2.0
	if diversity > 1.0 {
		diversity = 1.0
	}

	// Composite quality score (0-100)
	// 40% avg trust, 20% reciprocity, 20% signal ratio, 20% diversity
	qualityRaw := (math.Min(avgTrust/40.0, 1.0))*0.40 +
		reciprocity*0.20 +
		signalRatio*0.20 +
		diversity*0.20
	qualityScore := int(math.Round(qualityRaw * 100))
	if qualityScore > 100 {
		qualityScore = 100
	}

	classification := classifyFollowQuality(qualityScore)

	// Build suggestions: lowest-quality follows to reconsider
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].trustScore < infos[j].trustScore
	})

	suggestions := make([]FollowSuggestion, 0)
	for _, info := range infos {
		if len(suggestions) >= suggLimit {
			break
		}
		if info.trustScore > 25 {
			break // only suggest below score 25 (weak tier and below)
		}
		reason := ""
		switch {
		case !info.hasScore:
			reason = "not found in trust graph — may be inactive or a ghost account"
		case info.trustScore == 0:
			reason = "zero trust score — no meaningful trust signal"
		default:
			reason = fmt.Sprintf("very low trust score (%d) — minimal network presence", info.trustScore)
		}
		if !info.followsBack {
			reason += "; does not follow you back"
		}
		suggestions = append(suggestions, FollowSuggestion{
			Pubkey:     info.pubkey,
			TrustScore: info.trustScore,
			Reason:     reason,
		})
	}

	resp := FollowQualityResponse{
		Pubkey:         pubkey,
		TrustScore:     selfScore,
		FollowCount:    len(follows),
		QualityScore:   qualityScore,
		Classification: classification,
		Breakdown: FollowQualityBreak{
			AvgTrustScore:    math.Round(avgTrust*10) / 10,
			MedianTrustScore: medianTrust,
			Diversity:        math.Round(diversity*1000) / 1000,
			Reciprocity:      math.Round(reciprocity*1000) / 1000,
			SignalRatio:      math.Round(signalRatio*1000) / 1000,
		},
		Categories:  categories,
		Suggestions: suggestions,
		GraphSize:   stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func classifyFollowQuality(score int) string {
	switch {
	case score >= 75:
		return "excellent"
	case score >= 50:
		return "good"
	case score >= 25:
		return "moderate"
	default:
		return "poor"
	}
}
