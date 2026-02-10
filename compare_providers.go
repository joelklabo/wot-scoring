package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

// ProviderScore represents one provider's score for a pubkey.
type ProviderScore struct {
	ProviderPubkey string `json:"provider_pubkey"`
	RawRank        int    `json:"raw_rank"`
	NormalizedRank int    `json:"normalized_rank"`
	Followers      int    `json:"followers,omitempty"`
	IsOurs         bool   `json:"is_ours"`
	AssertionCount int    `json:"assertion_count,omitempty"`
	AgeSecs        int64  `json:"age_seconds,omitempty"`
}

// ConsensusMetrics summarizes agreement across providers.
type ConsensusMetrics struct {
	ProviderCount int     `json:"provider_count"`
	Mean          float64 `json:"mean"`
	Median        float64 `json:"median"`
	StdDev        float64 `json:"std_dev"`
	Min           int     `json:"min"`
	Max           int     `json:"max"`
	Spread        int     `json:"spread"`
	Agreement     string  `json:"agreement"` // "strong", "moderate", "weak", "no_consensus"
}

// CompareProvidersResponse is the response for /compare-providers.
type CompareProvidersResponse struct {
	Pubkey           string            `json:"pubkey"`
	InGraph          bool              `json:"in_graph"`
	Providers        []ProviderScore   `json:"providers"`
	Consensus        *ConsensusMetrics `json:"consensus,omitempty"`
	ConsensusNonZero *ConsensusMetrics `json:"consensus_nonzero,omitempty"`
	GraphSize        int               `json:"graph_size"`
}

// handleCompareProviders returns WoT scores from multiple NIP-85 providers for a pubkey.
// GET /compare-providers?pubkey=<hex|npub>
func handleCompareProviders(w http.ResponseWriter, r *http.Request) {
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

	// Our own score
	rawScore, inGraph := graph.GetScore(pubkey)
	ourNorm := normalizeScore(rawScore, stats.Nodes)

	providers := []ProviderScore{{
		ProviderPubkey: "self",
		RawRank:        ourNorm,
		NormalizedRank: ourNorm,
		Followers:      len(graph.GetFollowers(pubkey)),
		IsOurs:         true,
	}}

	// External provider scores
	now := time.Now().Unix()
	externals := externalAssertions.GetForSubject(pubkey)
	for _, a := range externals {
		provInfo := externalAssertions.GetProvider(a.ProviderPubkey)
		norm := NormalizeRank(a.Rank, provInfo)
		ps := ProviderScore{
			ProviderPubkey: a.ProviderPubkey,
			RawRank:        a.Rank,
			NormalizedRank: norm,
			Followers:      a.Followers,
			IsOurs:         false,
			AgeSecs:        now - a.CreatedAt,
		}
		if provInfo != nil {
			ps.AssertionCount = provInfo.AssertionCnt
		}
		providers = append(providers, ps)
	}

	resp := CompareProvidersResponse{
		Pubkey:    pubkey,
		InGraph:   inGraph,
		Providers: providers,
		GraphSize: stats.Nodes,
	}

	// Calculate consensus if we have 2+ providers
	if len(providers) >= 2 {
		resp.Consensus = calculateConsensus(providers)

		// "0" ranks are sometimes used by providers as "no opinion/unranked".
		// Keep the original consensus (for backwards compatibility) and also
		// provide a non-zero-only view that's often more informative.
		nonZero := make([]ProviderScore, 0, len(providers))
		for _, p := range providers {
			if p.NormalizedRank > 0 {
				nonZero = append(nonZero, p)
			}
		}
		if len(nonZero) >= 2 {
			resp.ConsensusNonZero = calculateConsensus(nonZero)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func calculateConsensus(providers []ProviderScore) *ConsensusMetrics {
	scores := make([]int, len(providers))
	for i, p := range providers {
		scores[i] = p.NormalizedRank
	}
	sort.Ints(scores)

	n := len(scores)
	sum := 0
	for _, s := range scores {
		sum += s
	}
	mean := float64(sum) / float64(n)

	var median float64
	if n%2 == 0 {
		median = float64(scores[n/2-1]+scores[n/2]) / 2.0
	} else {
		median = float64(scores[n/2])
	}

	varSum := 0.0
	for _, s := range scores {
		diff := float64(s) - mean
		varSum += diff * diff
	}
	stdDev := math.Sqrt(varSum / float64(n))

	spread := scores[n-1] - scores[0]

	agreement := "no_consensus"
	if stdDev <= 5 {
		agreement = "strong"
	} else if stdDev <= 15 {
		agreement = "moderate"
	} else if stdDev <= 30 {
		agreement = "weak"
	}

	return &ConsensusMetrics{
		ProviderCount: n,
		Mean:          math.Round(mean*10) / 10,
		Median:        math.Round(median*10) / 10,
		StdDev:        math.Round(stdDev*10) / 10,
		Min:           scores[0],
		Max:           scores[n-1],
		Spread:        spread,
		Agreement:     agreement,
	}
}
