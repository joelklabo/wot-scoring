package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RelayTrustResponse is the API response for /relay endpoint.
type RelayTrustResponse struct {
	URL            string            `json:"url"`
	Name           string            `json:"name,omitempty"`
	OperatorPubkey string            `json:"operator_pubkey,omitempty"`
	RelayTrust     *RelayTrustScores `json:"relay_trust,omitempty"`
	OperatorWoT    *OperatorWoTScore `json:"operator_wot,omitempty"`
	CombinedScore  int               `json:"combined_score"`
	Source         string            `json:"source"`
}

type RelayTrustScores struct {
	Overall       int     `json:"overall"`
	Reliability   int     `json:"reliability"`
	Quality       int     `json:"quality"`
	Accessibility int     `json:"accessibility"`
	OperatorTrust int     `json:"operator_trust"`
	UptimePercent float64 `json:"uptime_percent"`
	Confidence    string  `json:"confidence"`
	Observations  int     `json:"observations"`
}

type OperatorWoTScore struct {
	Pubkey    string `json:"pubkey"`
	WoTScore  int    `json:"wot_score"`
	Followers int    `json:"followers"`
	InGraph   bool   `json:"in_graph"`
}

// trustedRelaysCache caches relay data to avoid hammering the API.
var trustedRelaysCache struct {
	mu    sync.RWMutex
	data  map[string]*trustedRelaysCacheEntry
	inited bool
}

type trustedRelaysCacheEntry struct {
	response *trustedRelaysAPIResponse
	fetched  time.Time
}

const trustedRelaysCacheTTL = 30 * time.Minute

func init() {
	trustedRelaysCache.data = make(map[string]*trustedRelaysCacheEntry)
	trustedRelaysCache.inited = true
}

// trustedRelaysAPIResponse matches the trustedrelays.xyz /api/relay response.
type trustedRelaysAPIResponse struct {
	Success bool `json:"success"`
	Data    struct {
		URL       string `json:"url"`
		RelayType string `json:"relayType"`
		Reachable bool   `json:"reachable"`
		NIP11     struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Pubkey      string `json:"pubkey"`
			Contact     string `json:"contact"`
		} `json:"nip11"`
		Scores struct {
			Overall       int `json:"overall"`
			Reliability   int `json:"reliability"`
			Quality       int `json:"quality"`
			Accessibility int `json:"accessibility"`
			Latency       struct {
				ConnectMs float64 `json:"connectMs"`
				ReadMs    float64 `json:"readMs"`
			} `json:"latency"`
			UptimePercent float64 `json:"uptimePercent"`
		} `json:"scores"`
		Operator struct {
			Pubkey          string `json:"pubkey"`
			TrustScore      int    `json:"trustScore"`
			TrustConfidence string `json:"trustConfidence"`
		} `json:"operator"`
		History []struct {
			Observations int `json:"observations"`
			Confidence   string `json:"confidence"`
		} `json:"history"`
	} `json:"data"`
}

func fetchTrustedRelayData(relayURL string) (*trustedRelaysAPIResponse, error) {
	// Check cache
	trustedRelaysCache.mu.RLock()
	if entry, ok := trustedRelaysCache.data[relayURL]; ok && time.Since(entry.fetched) < trustedRelaysCacheTTL {
		trustedRelaysCache.mu.RUnlock()
		return entry.response, nil
	}
	trustedRelaysCache.mu.RUnlock()

	apiURL := fmt.Sprintf("https://trustedrelays.xyz/api/relay?url=%s", relayURL)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("trustedrelays.xyz fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("trustedrelays.xyz returned %d: %s", resp.StatusCode, string(body))
	}

	var result trustedRelaysAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode trustedrelays response: %w", err)
	}

	// Cache the result
	trustedRelaysCache.mu.Lock()
	trustedRelaysCache.data[relayURL] = &trustedRelaysCacheEntry{
		response: &result,
		fetched:  time.Now(),
	}
	trustedRelaysCache.mu.Unlock()

	return &result, nil
}

func handleRelay(w http.ResponseWriter, r *http.Request) {
	relayURL := r.URL.Query().Get("url")
	if relayURL == "" {
		http.Error(w, `{"error":"url parameter required (e.g., wss://relay.damus.io)"}`, http.StatusBadRequest)
		return
	}

	// Normalize: ensure wss:// prefix
	if !strings.HasPrefix(relayURL, "wss://") && !strings.HasPrefix(relayURL, "ws://") {
		relayURL = "wss://" + relayURL
	}

	resp := RelayTrustResponse{
		URL:    relayURL,
		Source: "wot.klabo.world + trustedrelays.xyz",
	}

	// Fetch from trustedrelays.xyz
	trData, err := fetchTrustedRelayData(relayURL)
	if err != nil {
		log.Printf("trustedrelays.xyz error for %s: %v", relayURL, err)
		// Still return what we can (just WoT data for operator if known)
	}

	if trData != nil && trData.Success {
		resp.Name = trData.Data.NIP11.Name
		resp.OperatorPubkey = trData.Data.Operator.Pubkey

		observations := 0
		confidence := "unknown"
		if len(trData.Data.History) > 0 {
			latest := trData.Data.History[len(trData.Data.History)-1]
			observations = latest.Observations
			confidence = latest.Confidence
		}

		resp.RelayTrust = &RelayTrustScores{
			Overall:       trData.Data.Scores.Overall,
			Reliability:   trData.Data.Scores.Reliability,
			Quality:       trData.Data.Scores.Quality,
			Accessibility: trData.Data.Scores.Accessibility,
			OperatorTrust: trData.Data.Operator.TrustScore,
			UptimePercent: trData.Data.Scores.UptimePercent,
			Confidence:    confidence,
			Observations:  observations,
		}

		// Cross-reference operator pubkey with our WoT graph
		if trData.Data.Operator.Pubkey != "" {
			operatorPub := trData.Data.Operator.Pubkey
			rawScore, inGraph := graph.GetScore(operatorPub)
			stats := graph.Stats()
			wotScore := normalizeScore(rawScore, stats.Nodes)

			followers := 0
			m := meta.Get(operatorPub)
			if m != nil {
				followers = m.Followers
			}

			resp.OperatorWoT = &OperatorWoTScore{
				Pubkey:    operatorPub,
				WoTScore:  wotScore,
				Followers: followers,
				InGraph:   inGraph,
			}
		}
	}

	// Compute combined score
	resp.CombinedScore = computeCombinedScore(resp.RelayTrust, resp.OperatorWoT)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// computeCombinedScore blends relay technical trust with operator WoT reputation.
// Formula: 70% relay trust + 30% operator WoT score.
func computeCombinedScore(relay *RelayTrustScores, wot *OperatorWoTScore) int {
	if relay == nil && wot == nil {
		return 0
	}
	if relay == nil {
		return wot.WoTScore
	}
	if wot == nil || !wot.InGraph {
		return relay.Overall
	}

	// Blend: 70% relay infrastructure trust + 30% operator social trust
	combined := float64(relay.Overall)*0.7 + float64(wot.WoTScore)*0.3
	score := int(combined + 0.5)
	if score > 100 {
		score = 100
	}
	return score
}
