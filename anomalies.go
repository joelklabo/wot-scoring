package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// AnomalyFlag represents a single detected anomaly in a pubkey's trust graph.
type AnomalyFlag struct {
	Type        string  `json:"type"`        // e.g. "follow_farming", "bot_followers", "trust_concentration", "ghost_followers"
	Severity    string  `json:"severity"`    // "low", "medium", "high"
	Description string  `json:"description"` // human-readable explanation
	Value       float64 `json:"value"`       // the metric value that triggered this flag
	Threshold   float64 `json:"threshold"`   // the threshold it crossed
}

// AnomaliesResponse is the response for the /anomalies endpoint.
type AnomaliesResponse struct {
	Pubkey           string        `json:"pubkey"`
	Score            int           `json:"score"`
	Rank             int           `json:"rank"`
	Followers        int           `json:"followers"`
	Follows          int           `json:"follows"`
	FollowBackRatio  float64       `json:"follow_back_ratio"`  // fraction of followers followed back
	GhostFollowers   int           `json:"ghost_followers"`    // followers with 0 WoT score
	GhostRatio       float64       `json:"ghost_ratio"`        // ghost_followers / total followers
	TopFollowerShare float64       `json:"top_follower_share"` // fraction of PageRank from top follower
	ScorePercentile  float64       `json:"score_percentile"`   // 0.0-1.0
	Anomalies        []AnomalyFlag `json:"anomalies"`
	AnomalyCount     int           `json:"anomaly_count"`
	RiskLevel        string        `json:"risk_level"` // "clean", "low", "medium", "high"
	GraphSize        int           `json:"graph_size"`
}

// handleAnomalies detects trust anomalies for a pubkey.
// GET /anomalies?pubkey=<hex|npub>
func handleAnomalies(w http.ResponseWriter, r *http.Request) {
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
	rawScore, _ := graph.GetScore(pubkey)
	score := normalizeScore(rawScore, stats.Nodes)
	rank := graph.Rank(pubkey)
	percentile := graph.Percentile(pubkey)

	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)

	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	// 1. Follow-back ratio: what fraction of followers does this pubkey follow back?
	followBackCount := 0
	for _, f := range followers {
		if followSet[f] {
			followBackCount++
		}
	}
	followBackRatio := 0.0
	if len(followers) > 0 {
		followBackRatio = float64(followBackCount) / float64(len(followers))
	}

	// 2. Ghost followers: followers with 0 or negligible WoT score
	ghostCount := 0
	for _, f := range followers {
		fRaw, ok := graph.GetScore(f)
		if !ok || normalizeScore(fRaw, stats.Nodes) < 5 {
			ghostCount++
		}
	}
	ghostRatio := 0.0
	if len(followers) > 0 {
		ghostRatio = float64(ghostCount) / float64(len(followers))
	}

	// 3. Top follower share: how much of this pubkey's PageRank comes from their top follower?
	// We approximate this by checking what fraction of total follower score the top follower contributes.
	topFollowerShare := 0.0
	if len(followers) > 0 {
		type followerScore struct {
			score float64
		}
		totalFollowerScore := 0.0
		maxFollowerScore := 0.0
		for _, f := range followers {
			fRaw, _ := graph.GetScore(f)
			outDegree := len(graph.GetFollows(f))
			contribution := 0.0
			if outDegree > 0 {
				contribution = fRaw / float64(outDegree)
			}
			totalFollowerScore += contribution
			if contribution > maxFollowerScore {
				maxFollowerScore = contribution
			}
		}
		if totalFollowerScore > 0 {
			topFollowerShare = maxFollowerScore / totalFollowerScore
		}
	}

	// Build anomaly flags
	anomalies := make([]AnomalyFlag, 0)

	// Follow farming: follow-back ratio > 90% with > 50 followers
	if len(followers) > 50 && followBackRatio > 0.90 {
		severity := "medium"
		if followBackRatio > 0.95 {
			severity = "high"
		}
		anomalies = append(anomalies, AnomalyFlag{
			Type:        "follow_farming",
			Severity:    severity,
			Description: fmt.Sprintf("Follows back %.0f%% of %d followers, suggesting follow-for-follow farming", followBackRatio*100, len(followers)),
			Value:       followBackRatio,
			Threshold:   0.90,
		})
	}

	// Bot followers: ghost ratio > 70%
	// Skip for top-1% accounts — high ghost ratios are normal for very popular accounts
	if len(followers) > 20 && ghostRatio > 0.70 && percentile < 0.99 {
		severity := "medium"
		if ghostRatio > 0.85 {
			severity = "high"
		}
		anomalies = append(anomalies, AnomalyFlag{
			Type:        "ghost_followers",
			Severity:    severity,
			Description: fmt.Sprintf("%d of %d followers (%.0f%%) have zero WoT score, suggesting bot or inactive followers", ghostCount, len(followers), ghostRatio*100),
			Value:       ghostRatio,
			Threshold:   0.70,
		})
	}

	// Trust concentration: > 50% of PageRank from single follower
	if topFollowerShare > 0.50 && len(followers) > 5 {
		severity := "low"
		if topFollowerShare > 0.75 {
			severity = "medium"
		}
		if topFollowerShare > 0.90 {
			severity = "high"
		}
		anomalies = append(anomalies, AnomalyFlag{
			Type:        "trust_concentration",
			Severity:    severity,
			Description: fmt.Sprintf("%.0f%% of trust score derived from a single follower — fragile trust foundation", topFollowerShare*100),
			Value:       topFollowerShare,
			Threshold:   0.50,
		})
	}

	// Score-follower divergence: high followers but low percentile
	if len(followers) > 100 && percentile < 0.50 {
		severity := "low"
		if percentile < 0.30 {
			severity = "medium"
		}
		if percentile < 0.10 {
			severity = "high"
		}
		anomalies = append(anomalies, AnomalyFlag{
			Type:        "score_follower_divergence",
			Severity:    severity,
			Description: fmt.Sprintf("%d followers but only %s percentile WoT score — followers may lack trust themselves", len(followers), formatPercentile(percentile)),
			Value:       percentile,
			Threshold:   0.50,
		})
	}

	// Excessive following: follows > 5000 with low score
	if len(follows) > 5000 && score < 30 {
		severity := "medium"
		if len(follows) > 10000 {
			severity = "high"
		}
		anomalies = append(anomalies, AnomalyFlag{
			Type:        "excessive_following",
			Severity:    severity,
			Description: fmt.Sprintf("Follows %d accounts but has low trust score (%d) — may be a bot or spam account", len(follows), score),
			Value:       float64(len(follows)),
			Threshold:   5000,
		})
	}

	// Determine risk level from anomaly severities
	riskLevel := "clean"
	if len(anomalies) > 0 {
		// Sort by severity for deterministic output
		sort.Slice(anomalies, func(i, j int) bool {
			return severityRank(anomalies[i].Severity) > severityRank(anomalies[j].Severity)
		})
		riskLevel = anomalies[0].Severity // highest severity
	}

	resp := AnomaliesResponse{
		Pubkey:           pubkey,
		Score:            score,
		Rank:             rank,
		Followers:        len(followers),
		Follows:          len(follows),
		FollowBackRatio:  math.Round(followBackRatio*1000) / 1000,
		GhostFollowers:   ghostCount,
		GhostRatio:       math.Round(ghostRatio*1000) / 1000,
		TopFollowerShare: math.Round(topFollowerShare*1000) / 1000,
		ScorePercentile:  math.Round(percentile*1000) / 1000,
		Anomalies:        anomalies,
		AnomalyCount:     len(anomalies),
		RiskLevel:        riskLevel,
		GraphSize:        stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func severityRank(s string) int {
	switch s {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func formatPercentile(p float64) string {
	return fmt.Sprintf("%.0f%%", p*100)
}
