package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// SybilSignal is a single component of the Sybil resistance score.
type SybilSignal struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`       // 0.0-1.0 (1.0 = most genuine)
	Weight      float64 `json:"weight"`      // weight in final score
	Description string  `json:"description"` // human-readable explanation
	Value       float64 `json:"value"`       // underlying metric value
}

// SybilResponse is the response for the /sybil endpoint.
type SybilResponse struct {
	Pubkey           string        `json:"pubkey"`
	SybilScore       int           `json:"sybil_score"`       // 0-100 (100 = most genuine)
	Classification   string        `json:"classification"`    // "genuine", "likely_genuine", "suspicious", "likely_sybil"
	Confidence       float64       `json:"confidence"`        // 0.0-1.0 how confident we are
	Signals          []SybilSignal `json:"signals"`           // breakdown of scoring components
	TrustScore       int           `json:"trust_score"`       // existing WoT score for reference
	Rank             int           `json:"rank"`
	Followers        int           `json:"followers"`
	Follows          int           `json:"follows"`
	MutualCount      int           `json:"mutual_count"`      // mutual follows with scored accounts
	HighValueMutuals int           `json:"high_value_mutuals"` // mutuals with score > 50
	GraphSize        int           `json:"graph_size"`
}

// handleSybil computes a Sybil resistance score for a pubkey.
// GET /sybil?pubkey=<hex|npub>
func handleSybil(w http.ResponseWriter, r *http.Request) {
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
	rank := graph.Rank(pubkey)
	percentile := graph.Percentile(pubkey)

	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)

	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	// --- Signal 1: Follower Quality ---
	// Average normalized WoT score of followers. Genuine accounts attract high-quality followers.
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
	followerQuality := math.Min(avgFollowerScore/30.0, 1.0) // cap at score 30 = perfect

	// --- Signal 2: Mutual Trust ---
	// Mutual follows with scored accounts indicate organic relationships.
	mutualCount := 0
	highValueMutuals := 0
	for _, f := range followers {
		if followSet[f] {
			mutualCount++
			fRaw, ok := graph.GetScore(f)
			if ok {
				ns := normalizeScore(fRaw, stats.Nodes)
				if ns > 50 {
					highValueMutuals++
				}
			}
		}
	}
	mutualTrust := 0.0
	if len(followers) > 0 {
		mutualRatio := float64(mutualCount) / float64(len(followers))
		// Sweet spot: 10-60% mutual rate is organic. Too high (>90%) = farming. Too low (<5%) = passive.
		if mutualRatio >= 0.10 && mutualRatio <= 0.60 {
			mutualTrust = 0.8
		} else if mutualRatio > 0.60 && mutualRatio <= 0.90 {
			mutualTrust = 0.5
		} else if mutualRatio > 0.90 {
			mutualTrust = 0.2 // likely farming
		} else {
			mutualTrust = 0.4 // very few mutuals
		}
		// Bonus for high-value mutuals
		if highValueMutuals > 3 {
			mutualTrust = math.Min(mutualTrust+0.2, 1.0)
		}
	}

	// --- Signal 3: Score-Rank Consistency ---
	// Does PageRank align with follower count? Sybils inflate followers without real PageRank.
	scoreRankConsistency := 0.5 // default: neutral
	if len(followers) > 0 && found {
		expectedPercentile := math.Min(float64(len(followers))/1000.0, 0.99)
		deviation := math.Abs(percentile - expectedPercentile)
		scoreRankConsistency = math.Max(0, 1.0-deviation*2)
	}

	// --- Signal 4: Follower Diversity ---
	// Are followers spread across the graph or clustered? Check how many distinct "neighborhoods" followers come from.
	followerDiversity := computeFollowerDiversity(followers, stats.Nodes)

	// --- Signal 5: Account Substance ---
	// Does this account have substance beyond just following? Check outgoing follows ratio, score, etc.
	accountSubstance := 0.0
	if found {
		// Base substance from having a nonzero score
		accountSubstance = math.Min(float64(score)/50.0, 0.6)
		// Bonus for having follows (active participant)
		if len(follows) > 5 && len(follows) < 2000 {
			accountSubstance += 0.2
		}
		// Bonus for having followers
		if len(followers) > 5 {
			accountSubstance += 0.2
		}
		accountSubstance = math.Min(accountSubstance, 1.0)
	}

	// --- Combine signals ---
	signals := []SybilSignal{
		{
			Name:        "follower_quality",
			Score:       round3(followerQuality),
			Weight:      0.30,
			Description: fmt.Sprintf("Average follower WoT score: %.1f (higher = more genuine followers)", avgFollowerScore),
			Value:       round3(avgFollowerScore),
		},
		{
			Name:        "mutual_trust",
			Score:       round3(mutualTrust),
			Weight:      0.25,
			Description: fmt.Sprintf("%d mutual follows (%d high-value) — organic relationships indicate genuine account", mutualCount, highValueMutuals),
			Value:       float64(mutualCount),
		},
		{
			Name:        "score_consistency",
			Score:       round3(scoreRankConsistency),
			Weight:      0.15,
			Description: fmt.Sprintf("PageRank percentile %.0f%% vs expected from %d followers — alignment indicates genuine influence", percentile*100, len(followers)),
			Value:       round3(percentile),
		},
		{
			Name:        "follower_diversity",
			Score:       round3(followerDiversity),
			Weight:      0.15,
			Description: "Follower neighborhood diversity — genuine accounts attract followers from diverse graph regions",
			Value:       round3(followerDiversity),
		},
		{
			Name:        "account_substance",
			Score:       round3(accountSubstance),
			Weight:      0.15,
			Description: fmt.Sprintf("Account substance: score %d, %d follows, %d followers", score, len(follows), len(followers)),
			Value:       float64(score),
		},
	}

	// Weighted sum
	finalScore := 0.0
	for _, s := range signals {
		finalScore += s.Score * s.Weight
	}
	sybilScore := int(math.Round(finalScore * 100))
	if sybilScore > 100 {
		sybilScore = 100
	}

	// Classification
	classification := classifySybilScore(sybilScore)

	// Confidence: higher when we have more data
	confidence := computeConfidence(len(followers), len(follows), found, scoredFollowers)

	resp := SybilResponse{
		Pubkey:           pubkey,
		SybilScore:       sybilScore,
		Classification:   classification,
		Confidence:       round3(confidence),
		Signals:          signals,
		TrustScore:       score,
		Rank:             rank,
		Followers:        len(followers),
		Follows:          len(follows),
		MutualCount:      mutualCount,
		HighValueMutuals: highValueMutuals,
		GraphSize:        stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// computeFollowerDiversity measures how spread out followers are across the graph.
// Uses a sampling approach: for each follower, check their other follows and see
// how many unique "neighborhoods" they represent.
func computeFollowerDiversity(followers []string, graphSize int) float64 {
	if len(followers) < 3 {
		return 0.3 // too few followers to judge
	}

	// Sample up to 50 followers for performance
	sample := followers
	if len(sample) > 50 {
		// Take evenly spaced samples
		step := len(followers) / 50
		sampled := make([]string, 0, 50)
		for i := 0; i < len(followers) && len(sampled) < 50; i += step {
			sampled = append(sampled, followers[i])
		}
		sample = sampled
	}

	// For each sampled follower, get their follows (their "neighborhood")
	// Count how many unique pubkeys appear across all follower neighborhoods
	neighborhoodPubkeys := make(map[string]int) // pubkey -> how many followers follow them
	for _, f := range sample {
		fFollows := graph.GetFollows(f)
		for _, ff := range fFollows {
			neighborhoodPubkeys[ff]++
		}
	}

	if len(neighborhoodPubkeys) == 0 {
		return 0.2
	}

	// Diversity = how spread out the neighborhood is
	// If all followers follow the same accounts, diversity is low (Sybil cluster)
	// If followers follow very different accounts, diversity is high (genuine)

	// Count pubkeys that appear in >50% of follower neighborhoods (overlap)
	overlapCount := 0
	threshold := len(sample) / 2
	if threshold < 2 {
		threshold = 2
	}
	for _, count := range neighborhoodPubkeys {
		if count >= threshold {
			overlapCount++
		}
	}

	// High overlap ratio = low diversity = suspicious
	overlapRatio := float64(overlapCount) / float64(len(neighborhoodPubkeys))

	// Invert: low overlap = high diversity score
	diversity := 1.0 - overlapRatio

	// Also factor in raw breadth: more unique neighborhoods = more diverse
	breadth := math.Min(float64(len(neighborhoodPubkeys))/500.0, 1.0)

	return diversity*0.6 + breadth*0.4
}

func classifySybilScore(score int) string {
	switch {
	case score >= 75:
		return "genuine"
	case score >= 50:
		return "likely_genuine"
	case score >= 25:
		return "suspicious"
	default:
		return "likely_sybil"
	}
}

func computeConfidence(followers, follows int, found bool, scoredFollowers int) float64 {
	if !found {
		return 0.1 // very low confidence if not in graph at all
	}
	confidence := 0.3 // base confidence for being in graph
	if followers > 10 {
		confidence += 0.2
	}
	if followers > 50 {
		confidence += 0.1
	}
	if follows > 5 {
		confidence += 0.1
	}
	if scoredFollowers > 10 {
		confidence += 0.2
	}
	if scoredFollowers > 50 {
		confidence += 0.1
	}
	if confidence > 1.0 {
		confidence = 1.0
	}
	return confidence
}

// round3 rounds to 3 decimal places.
func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

// handleSybilBatch scores multiple pubkeys for Sybil resistance.
// POST /sybil/batch with JSON body {"pubkeys": ["hex1", "hex2", ...]}
func handleSybilBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Pubkeys []string `json:"pubkeys"`
	}
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

	type batchEntry struct {
		Pubkey         string `json:"pubkey"`
		SybilScore     int    `json:"sybil_score"`
		Classification string `json:"classification"`
		TrustScore     int    `json:"trust_score"`
		Followers      int    `json:"followers"`
		Error          string `json:"error,omitempty"`
	}

	stats := graph.Stats()
	results := make([]batchEntry, 0, len(req.Pubkeys))

	for _, raw := range req.Pubkeys {
		pubkey, err := resolvePubkey(raw)
		if err != nil {
			results = append(results, batchEntry{Pubkey: raw, Error: err.Error()})
			continue
		}

		rawScore, found := graph.GetScore(pubkey)
		score := normalizeScore(rawScore, stats.Nodes)
		followers := graph.GetFollowers(pubkey)
		follows := graph.GetFollows(pubkey)

		followSet := make(map[string]bool, len(follows))
		for _, f := range follows {
			followSet[f] = true
		}

		// Quick scoring: use simplified signals for batch
		batchFollowerSum := 0
		scoredFollowers := 0
		mutualCount := 0
		highValueMutuals := 0
		for _, f := range followers {
			fRaw, ok := graph.GetScore(f)
			if ok {
				ns := normalizeScore(fRaw, stats.Nodes)
				batchFollowerSum += ns
				scoredFollowers++
				if followSet[f] {
					mutualCount++
					if ns > 50 {
						highValueMutuals++
					}
				}
			}
		}
		avgFollowerScore := 0.0
		if scoredFollowers > 0 {
			avgFollowerScore = float64(batchFollowerSum) / float64(scoredFollowers)
		}

		fq := math.Min(avgFollowerScore/30.0, 1.0)
		mt := 0.5
		if mutualCount > 3 && highValueMutuals > 1 {
			mt = 0.8
		}
		as := 0.0
		if found {
			as = math.Min(float64(score)/50.0, 1.0)
		}

		sybilScore := int(math.Round((fq*0.40 + mt*0.30 + as*0.30) * 100))
		if sybilScore > 100 {
			sybilScore = 100
		}

		results = append(results, batchEntry{
			Pubkey:         pubkey,
			SybilScore:     sybilScore,
			Classification: classifySybilScore(sybilScore),
			TrustScore:     score,
			Followers:      len(followers),
		})
	}

	// Sort by sybil score ascending (most suspicious first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].SybilScore < results[j].SybilScore
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":    results,
		"count":      len(results),
		"graph_size": stats.Nodes,
	})
}
