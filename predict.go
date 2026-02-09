package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// PredictResponse represents the link prediction response.
type PredictResponse struct {
	Source       string             `json:"source"`
	Target       string             `json:"target"`
	AlreadyFollows bool            `json:"already_follows"`
	Prediction   float64           `json:"prediction"`
	Confidence   float64           `json:"confidence"`
	Classification string          `json:"classification"`
	Signals      []PredictSignal   `json:"signals"`
	TopMutuals   []PredictMutual   `json:"top_mutuals,omitempty"`
	GraphSize    int               `json:"graph_size"`
}

// PredictSignal represents one link prediction signal.
type PredictSignal struct {
	Name        string  `json:"name"`
	RawValue    float64 `json:"raw_value"`
	Normalized  float64 `json:"normalized"`
	Weight      float64 `json:"weight"`
	Description string  `json:"description"`
}

// PredictMutual represents a shared connection relevant to the prediction.
type PredictMutual struct {
	Pubkey   string `json:"pubkey"`
	WotScore int    `json:"wot_score"`
}

func handlePredict(w http.ResponseWriter, r *http.Request) {
	sourceRaw := r.URL.Query().Get("source")
	targetRaw := r.URL.Query().Get("target")
	if sourceRaw == "" || targetRaw == "" {
		http.Error(w, `{"error":"source and target parameters required"}`, http.StatusBadRequest)
		return
	}

	source, err := resolvePubkey(sourceRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid source: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	target, err := resolvePubkey(targetRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid target: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if source == target {
		http.Error(w, `{"error":"source and target must be different pubkeys"}`, http.StatusBadRequest)
		return
	}

	sourceFollows := graph.GetFollows(source)
	targetFollows := graph.GetFollows(target)
	sourceFollowers := graph.GetFollowers(source)
	targetFollowers := graph.GetFollowers(target)
	stats := graph.Stats()

	// Check if source already follows target
	alreadyFollows := false
	sourceFollowSet := make(map[string]bool, len(sourceFollows))
	for _, f := range sourceFollows {
		sourceFollowSet[f] = true
	}
	alreadyFollows = sourceFollowSet[target]

	// Build target's neighbor set (follows + followers)
	targetNeighborSet := make(map[string]bool, len(targetFollows)+len(targetFollowers))
	for _, f := range targetFollows {
		targetNeighborSet[f] = true
	}
	for _, f := range targetFollowers {
		targetNeighborSet[f] = true
	}

	// Signal 1: Common Neighbors
	// Count how many of source's follows also follow/are followed by target
	commonNeighbors := 0
	var mutuals []PredictMutual
	for _, sf := range sourceFollows {
		if targetNeighborSet[sf] {
			commonNeighbors++
			score, _ := graph.GetScore(sf)
			ns := normalizeScore(score, stats.Nodes)
			mutuals = append(mutuals, PredictMutual{Pubkey: sf, WotScore: ns})
		}
	}

	// Signal 2: Adamic-Adar Index
	// Weight common neighbors by 1/log(degree) — rare connections matter more
	adamicAdar := 0.0
	for _, sf := range sourceFollows {
		if targetNeighborSet[sf] {
			sfFollowers := graph.GetFollowers(sf)
			degree := len(sfFollowers)
			if degree > 1 {
				adamicAdar += 1.0 / math.Log(float64(degree))
			}
		}
	}

	// Signal 3: Preferential Attachment
	// Product of degrees — popular nodes attract links
	sourceDegree := len(sourceFollows) + len(sourceFollowers)
	targetDegree := len(targetFollows) + len(targetFollowers)
	prefAttachment := float64(sourceDegree) * float64(targetDegree)

	// Signal 4: Jaccard Coefficient
	// Overlap of neighborhoods
	sourceNeighborSet := make(map[string]bool, len(sourceFollows)+len(sourceFollowers))
	for _, f := range sourceFollows {
		sourceNeighborSet[f] = true
	}
	for _, f := range sourceFollowers {
		sourceNeighborSet[f] = true
	}
	intersection := 0
	for k := range sourceNeighborSet {
		if targetNeighborSet[k] {
			intersection++
		}
	}
	unionSize := len(sourceNeighborSet) + len(targetNeighborSet) - intersection
	jaccard := 0.0
	if unionSize > 0 {
		jaccard = float64(intersection) / float64(unionSize)
	}

	// Signal 5: WoT Score Proximity
	// How close are their trust scores? Similar-ranked accounts follow each other.
	sourceScore, _ := graph.GetScore(source)
	targetScore, _ := graph.GetScore(target)
	sourceNorm := normalizeScore(sourceScore, stats.Nodes)
	targetNorm := normalizeScore(targetScore, stats.Nodes)
	scoreDiff := math.Abs(float64(sourceNorm) - float64(targetNorm))
	scoreProximity := 1.0 - (scoreDiff / 100.0)

	// Normalize signals to 0-1 range
	cnNorm := math.Min(float64(commonNeighbors)/20.0, 1.0) // 20+ common = max
	aaNorm := math.Min(adamicAdar/5.0, 1.0)                // 5.0+ AA = max
	paNorm := math.Min(math.Log10(prefAttachment+1)/8.0, 1.0) // log scale
	// jaccard is already 0-1
	// scoreProximity is already 0-1

	// Weighted combination
	weights := [5]float64{0.30, 0.25, 0.10, 0.20, 0.15}
	prediction := cnNorm*weights[0] + aaNorm*weights[1] + paNorm*weights[2] + jaccard*weights[3] + scoreProximity*weights[4]

	// Confidence based on data availability
	confidence := 0.0
	if len(sourceFollows) > 0 {
		confidence += 0.25
	}
	if len(targetFollows) > 0 {
		confidence += 0.25
	}
	if commonNeighbors > 0 {
		confidence += 0.30
	}
	if sourceDegree > 5 && targetDegree > 5 {
		confidence += 0.20
	}

	classification := classifyPrediction(prediction)

	// Sort mutuals by WoT score descending, limit to top 10
	sort.Slice(mutuals, func(i, j int) bool {
		return mutuals[i].WotScore > mutuals[j].WotScore
	})
	if len(mutuals) > 10 {
		mutuals = mutuals[:10]
	}

	signals := []PredictSignal{
		{
			Name:        "common_neighbors",
			RawValue:    float64(commonNeighbors),
			Normalized:  cnNorm,
			Weight:      weights[0],
			Description: fmt.Sprintf("%d shared connections between source and target neighborhoods", commonNeighbors),
		},
		{
			Name:        "adamic_adar",
			RawValue:    math.Round(adamicAdar*1000) / 1000,
			Normalized:  aaNorm,
			Weight:      weights[1],
			Description: "Weighted common neighbors — rare connections count more (1/log degree)",
		},
		{
			Name:        "preferential_attachment",
			RawValue:    prefAttachment,
			Normalized:  paNorm,
			Weight:      weights[2],
			Description: fmt.Sprintf("Degree product: %d × %d = %.0f", sourceDegree, targetDegree, prefAttachment),
		},
		{
			Name:        "jaccard_coefficient",
			RawValue:    math.Round(jaccard*1000) / 1000,
			Normalized:  jaccard,
			Weight:      weights[3],
			Description: fmt.Sprintf("Neighborhood overlap: %d / %d", intersection, unionSize),
		},
		{
			Name:        "wot_proximity",
			RawValue:    math.Round(scoreProximity*1000) / 1000,
			Normalized:  scoreProximity,
			Weight:      weights[4],
			Description: fmt.Sprintf("Trust score proximity: source=%d, target=%d", sourceNorm, targetNorm),
		},
	}

	resp := PredictResponse{
		Source:         source,
		Target:         target,
		AlreadyFollows: alreadyFollows,
		Prediction:     math.Round(prediction*1000) / 1000,
		Confidence:     math.Round(confidence*100) / 100,
		Classification: classification,
		Signals:        signals,
		TopMutuals:     mutuals,
		GraphSize:      stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func classifyPrediction(score float64) string {
	switch {
	case score >= 0.7:
		return "very_likely"
	case score >= 0.5:
		return "likely"
	case score >= 0.3:
		return "possible"
	case score >= 0.1:
		return "unlikely"
	default:
		return "very_unlikely"
	}
}
