package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// CircleCompareResponse is the response for /trust-circle/compare.
type CircleCompareResponse struct {
	Pubkey1       string                `json:"pubkey1"`
	Pubkey2       string                `json:"pubkey2"`
	Trust1        int                   `json:"trust_score_1"`
	Trust2        int                   `json:"trust_score_2"`
	CircleSize1   int                   `json:"circle_size_1"`
	CircleSize2   int                   `json:"circle_size_2"`
	Compatibility CircleCompatibility   `json:"compatibility"`
	Overlap       []CircleOverlapMember `json:"overlap"`
	Unique1       []CircleUniqueMember  `json:"unique_to_1"`
	Unique2       []CircleUniqueMember  `json:"unique_to_2"`
	GraphSize     int                   `json:"graph_size"`
}

// CircleCompatibility describes the compatibility between two trust circles.
type CircleCompatibility struct {
	Score          int     `json:"score"`           // 0-100 compatibility score
	Classification string  `json:"classification"`  // high, moderate, low, none
	OverlapCount   int     `json:"overlap_count"`   // number of shared mutual follows
	OverlapRatio   float64 `json:"overlap_ratio"`   // Jaccard similarity of circles
	AvgOverlapWot  float64 `json:"avg_overlap_wot"` // avg WoT score of overlapping members
	SharedFollows  int     `json:"shared_follows"`  // how many pubkeys both follow (not just mutuals)
	SharedRatio    float64 `json:"shared_ratio"`    // shared follows / union of follows
}

// CircleOverlapMember is a member present in both trust circles.
type CircleOverlapMember struct {
	Pubkey     string  `json:"pubkey"`
	TrustScore int     `json:"trust_score"`
	Strength1  float64 `json:"strength_with_1"` // mutual strength with pubkey1
	Strength2  float64 `json:"strength_with_2"` // mutual strength with pubkey2
}

// CircleUniqueMember is a member present in only one trust circle.
type CircleUniqueMember struct {
	Pubkey     string `json:"pubkey"`
	TrustScore int    `json:"trust_score"`
}

func handleTrustCircleCompare(w http.ResponseWriter, r *http.Request) {
	raw1 := r.URL.Query().Get("pubkey1")
	raw2 := r.URL.Query().Get("pubkey2")
	if raw1 == "" || raw2 == "" {
		http.Error(w, `{"error":"pubkey1 and pubkey2 parameters required"}`, http.StatusBadRequest)
		return
	}

	pubkey1, err := resolvePubkey(raw1)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey1: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	pubkey2, err := resolvePubkey(raw2)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey2: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if pubkey1 == pubkey2 {
		http.Error(w, `{"error":"pubkey1 and pubkey2 must be different"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()

	// Get trust circles for both pubkeys
	circle1 := getMutualSet(pubkey1)
	circle2 := getMutualSet(pubkey2)

	// Scores
	raw1Score, _ := graph.GetScore(pubkey1)
	raw2Score, _ := graph.GetScore(pubkey2)
	score1 := normalizeScore(raw1Score, stats.Nodes)
	score2 := normalizeScore(raw2Score, stats.Nodes)

	// Find overlap and unique members
	var overlap []CircleOverlapMember
	var unique1 []CircleUniqueMember
	var unique2 []CircleUniqueMember

	// Exclude the two queried pubkeys themselves from overlap/unique lists
	// (they may be in each other's circles but aren't "shared trusted third parties")
	for pk := range circle1 {
		if pk == pubkey1 || pk == pubkey2 {
			continue
		}
		rawS, _ := graph.GetScore(pk)
		wotScore := normalizeScore(rawS, stats.Nodes)
		if circle2[pk] {
			s1 := mutualStrength(score1, wotScore, countSharedFollows(pubkey1, pk))
			s2 := mutualStrength(score2, wotScore, countSharedFollows(pubkey2, pk))
			overlap = append(overlap, CircleOverlapMember{
				Pubkey:     pk,
				TrustScore: wotScore,
				Strength1:  math.Round(s1*1000) / 1000,
				Strength2:  math.Round(s2*1000) / 1000,
			})
		} else {
			unique1 = append(unique1, CircleUniqueMember{Pubkey: pk, TrustScore: wotScore})
		}
	}
	for pk := range circle2 {
		if pk == pubkey1 || pk == pubkey2 {
			continue
		}
		if !circle1[pk] {
			rawS, _ := graph.GetScore(pk)
			wotScore := normalizeScore(rawS, stats.Nodes)
			unique2 = append(unique2, CircleUniqueMember{Pubkey: pk, TrustScore: wotScore})
		}
	}

	// Sort all by trust score descending
	sort.Slice(overlap, func(i, j int) bool { return overlap[i].TrustScore > overlap[j].TrustScore })
	sort.Slice(unique1, func(i, j int) bool { return unique1[i].TrustScore > unique1[j].TrustScore })
	sort.Slice(unique2, func(i, j int) bool { return unique2[i].TrustScore > unique2[j].TrustScore })

	// Limit results to top 50 each
	if len(overlap) > 50 {
		overlap = overlap[:50]
	}
	if len(unique1) > 50 {
		unique1 = unique1[:50]
	}
	if len(unique2) > 50 {
		unique2 = unique2[:50]
	}

	// Compute compatibility
	compatibility := computeCircleCompatibility(pubkey1, pubkey2, circle1, circle2, len(overlap), stats.Nodes)

	resp := CircleCompareResponse{
		Pubkey1:       pubkey1,
		Pubkey2:       pubkey2,
		Trust1:        score1,
		Trust2:        score2,
		CircleSize1:   len(circle1),
		CircleSize2:   len(circle2),
		Compatibility: compatibility,
		Overlap:       overlap,
		Unique1:       unique1,
		Unique2:       unique2,
		GraphSize:     stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

// getMutualSet returns the set of mutual follows (trust circle) for a pubkey.
func getMutualSet(pubkey string) map[string]bool {
	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)

	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	mutuals := make(map[string]bool)
	for _, f := range followers {
		if followSet[f] {
			mutuals[f] = true
		}
	}
	return mutuals
}

// mutualStrength computes the strength of a mutual connection.
// Same formula as trust_circle.go: geometric mean of scores, boosted by shared follows.
func mutualStrength(selfScore, memberScore int, sharedFollows int) float64 {
	if selfScore <= 0 || memberScore <= 0 {
		return 0
	}
	strength := math.Sqrt(float64(selfScore)*float64(memberScore)) / 100.0
	if sharedFollows > 0 {
		strength *= (1.0 + math.Log10(float64(sharedFollows)+1)/3.0)
		if strength > 1.0 {
			strength = 1.0
		}
	}
	return strength
}

// countSharedFollows returns how many pubkeys both a and b follow.
func countSharedFollows(a, b string) int {
	aFollows := graph.GetFollows(a)
	bFollows := graph.GetFollows(b)

	bSet := make(map[string]bool, len(bFollows))
	for _, f := range bFollows {
		bSet[f] = true
	}

	count := 0
	for _, f := range aFollows {
		if bSet[f] {
			count++
		}
	}
	return count
}

func computeCircleCompatibility(pubkey1, pubkey2 string, circle1, circle2 map[string]bool, overlapCount, totalNodes int) CircleCompatibility {
	// Jaccard similarity of trust circles (excluding the two queried pubkeys)
	allKeys := make(map[string]bool)
	for k := range circle1 {
		if k != pubkey1 && k != pubkey2 {
			allKeys[k] = true
		}
	}
	for k := range circle2 {
		if k != pubkey1 && k != pubkey2 {
			allKeys[k] = true
		}
	}
	unionSize := len(allKeys)

	overlapRatio := 0.0
	if unionSize > 0 {
		overlapRatio = float64(overlapCount) / float64(unionSize)
	}

	// Average WoT score of overlapping members (excluding queried pubkeys)
	avgWot := 0.0
	if overlapCount > 0 {
		sum := 0.0
		for pk := range circle1 {
			if pk == pubkey1 || pk == pubkey2 {
				continue
			}
			if circle2[pk] {
				rawS, _ := graph.GetScore(pk)
				sum += float64(normalizeScore(rawS, totalNodes))
			}
		}
		avgWot = sum / float64(overlapCount)
	}

	// Shared follows (broader than mutual — just follows in common)
	sharedFollows := countSharedFollows(pubkey1, pubkey2)
	follows1 := graph.GetFollows(pubkey1)
	follows2 := graph.GetFollows(pubkey2)
	followUnion := make(map[string]bool)
	for _, f := range follows1 {
		followUnion[f] = true
	}
	for _, f := range follows2 {
		followUnion[f] = true
	}
	sharedRatio := 0.0
	if len(followUnion) > 0 {
		sharedRatio = float64(sharedFollows) / float64(len(followUnion))
	}

	// Compatibility score: weighted combination
	// 40% circle overlap + 30% shared follow ratio + 30% avg WoT of overlap
	circleComponent := overlapRatio * 100.0
	followComponent := sharedRatio * 100.0
	wotComponent := avgWot // already 0-100
	rawScore := circleComponent*0.40 + followComponent*0.30 + wotComponent*0.30

	score := int(math.Round(rawScore))
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}

	classification := classifyCompatibility(score)

	return CircleCompatibility{
		Score:          score,
		Classification: classification,
		OverlapCount:   overlapCount,
		OverlapRatio:   math.Round(overlapRatio*1000) / 1000,
		AvgOverlapWot:  math.Round(avgWot*10) / 10,
		SharedFollows:  sharedFollows,
		SharedRatio:    math.Round(sharedRatio*1000) / 1000,
	}
}

func classifyCompatibility(score int) string {
	switch {
	case score >= 60:
		return "high"
	case score >= 30:
		return "moderate"
	case score >= 10:
		return "low"
	default:
		return "none"
	}
}
