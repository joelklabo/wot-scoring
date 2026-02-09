package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"
)

// SpamSignal describes one component of the spam analysis.
type SpamSignal struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`  // raw signal value
	Weight float64 `json:"weight"` // how much this contributes to final score
	Score  float64 `json:"score"`  // weighted contribution (0=human, 1=spam)
	Reason string  `json:"reason"` // human-readable explanation
}

// SpamResponse is the full response for the /spam endpoint.
type SpamResponse struct {
	Pubkey         string       `json:"pubkey"`
	SpamProbability float64     `json:"spam_probability"` // 0.0 (human) to 1.0 (spam)
	Classification string       `json:"classification"`   // "likely_human", "suspicious", "likely_spam"
	Signals        []SpamSignal `json:"signals"`
	Summary        string       `json:"summary"`
	GraphSize      int          `json:"graph_size"`
}

// computeSpam analyzes a pubkey for spam indicators and returns a SpamResponse.
func computeSpam(pubkey string, graphSize int) SpamResponse {
	rawScore, found := graph.GetScore(pubkey)
	score := normalizeScore(rawScore, graphSize)
	percentile := graph.Percentile(pubkey)
	followers := graph.GetFollowers(pubkey)
	follows := graph.GetFollows(pubkey)
	m := meta.Get(pubkey)

	var signals []SpamSignal

	signals = append(signals, spamSignalWoT(score, found, percentile))
	signals = append(signals, spamSignalFollowRatio(len(followers), len(follows)))
	signals = append(signals, spamSignalAge(m.FirstCreated))
	signals = append(signals, spamSignalEngagement(m.ReactionsRecd, m.ZapCntRecd, m.PostCount))
	signals = append(signals, spamSignalReports(m.ReportsRecd))
	signals = append(signals, spamSignalActivity(m.PostCount, m.ReplyCount, m.ReactionsSent))

	var spamProb float64
	for _, s := range signals {
		spamProb += s.Score
	}
	if spamProb > 1.0 {
		spamProb = 1.0
	}
	if spamProb < 0.0 {
		spamProb = 0.0
	}
	spamProb = math.Round(spamProb*1000) / 1000

	classification := classifySpam(spamProb)
	summary := spamSummary(classification, score, len(followers), m.ReportsRecd)

	return SpamResponse{
		Pubkey:          pubkey,
		SpamProbability: spamProb,
		Classification:  classification,
		Signals:         signals,
		Summary:         summary,
		GraphSize:       graphSize,
	}
}

// handleSpam analyzes a pubkey for spam indicators using WoT graph signals.
func handleSpam(w http.ResponseWriter, r *http.Request) {
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
	resp := computeSpam(pubkey, stats.Nodes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// SpamBatchResult is a compact result for batch spam checking.
type SpamBatchResult struct {
	Pubkey          string  `json:"pubkey"`
	SpamProbability float64 `json:"spam_probability"`
	Classification  string  `json:"classification"`
	Summary         string  `json:"summary"`
}

// handleSpamBatch checks up to 100 pubkeys for spam in one request.
// POST /spam/batch with JSON body: {"pubkeys": ["hex1", "hex2", ...]}
func handleSpamBatch(w http.ResponseWriter, r *http.Request) {
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
	if len(req.Pubkeys) > 100 {
		http.Error(w, `{"error":"max 100 pubkeys per request"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()
	results := make([]interface{}, len(req.Pubkeys))

	for i, raw := range req.Pubkeys {
		pubkey, err := resolvePubkey(raw)
		if err != nil {
			results[i] = map[string]interface{}{
				"pubkey": raw,
				"error":  err.Error(),
			}
			continue
		}

		full := computeSpam(pubkey, stats.Nodes)
		results[i] = SpamBatchResult{
			Pubkey:          pubkey,
			SpamProbability: full.SpamProbability,
			Classification:  full.Classification,
			Summary:         full.Summary,
		}
	}

	// Count classifications
	var human, suspicious, spam, errors int
	for _, r := range results {
		switch v := r.(type) {
		case SpamBatchResult:
			switch v.Classification {
			case "likely_human":
				human++
			case "suspicious":
				suspicious++
			case "likely_spam":
				spam++
			}
		default:
			errors++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":    results,
		"count":      len(results),
		"graph_size": stats.Nodes,
		"summary": map[string]int{
			"likely_human": human,
			"suspicious":   suspicious,
			"likely_spam":  spam,
			"errors":       errors,
		},
	})
}

func spamSignalWoT(score int, found bool, percentile float64) SpamSignal {
	weight := 0.30
	var raw, spamScore float64
	var reason string

	if !found {
		raw = 0
		spamScore = weight
		reason = "Pubkey not found in WoT graph — no trust data available"
	} else if score == 0 {
		raw = 0
		spamScore = weight
		reason = "WoT score is 0 — no measurable trust"
	} else {
		raw = float64(score)
		// Higher score = less spam. Score 50+ is very trustworthy.
		// Map: score 0 -> 1.0 spam, score 30 -> 0.3, score 60+ -> 0.0
		spamFactor := 1.0 - math.Min(float64(score)/50.0, 1.0)
		spamScore = math.Round(spamFactor*weight*1000) / 1000
		if percentile > 0.9 {
			reason = fmt.Sprintf("WoT score %d (top %.0f%%) — highly trusted", score, (1-percentile)*100)
		} else if percentile > 0.5 {
			reason = fmt.Sprintf("WoT score %d (top %.0f%%) — moderate trust", score, (1-percentile)*100)
		} else {
			reason = fmt.Sprintf("WoT score %d (bottom %.0f%%) — low trust", score, percentile*100)
		}
	}

	return SpamSignal{
		Name:   "wot_score",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func spamSignalFollowRatio(followerCount, followCount int) SpamSignal {
	weight := 0.15
	var raw, spamScore float64
	var reason string

	if followCount == 0 && followerCount == 0 {
		raw = 0
		spamScore = weight * 0.5
		reason = "No follow data — unknown"
	} else if followCount == 0 {
		raw = float64(followerCount)
		spamScore = 0
		reason = fmt.Sprintf("%d followers, follows nobody — passive but not spammy", followerCount)
	} else {
		ratio := float64(followerCount) / float64(followCount)
		raw = math.Round(ratio*100) / 100

		if ratio >= 1.0 {
			spamScore = 0
			reason = fmt.Sprintf("Ratio %.2f (%d followers / %d following) — healthy", ratio, followerCount, followCount)
		} else if ratio >= 0.1 {
			spamFactor := 1.0 - ratio
			spamScore = math.Round(spamFactor*weight*1000) / 1000
			reason = fmt.Sprintf("Ratio %.2f (%d followers / %d following) — slightly imbalanced", ratio, followerCount, followCount)
		} else {
			spamScore = weight
			reason = fmt.Sprintf("Ratio %.2f (%d followers / %d following) — follows many, few follow back", ratio, followerCount, followCount)
		}
	}

	return SpamSignal{
		Name:   "follow_ratio",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func spamSignalAge(firstCreated int64) SpamSignal {
	weight := 0.15
	var raw, spamScore float64
	var reason string

	if firstCreated == 0 {
		raw = 0
		spamScore = weight * 0.5
		reason = "No event history — account age unknown"
	} else {
		age := time.Since(time.Unix(firstCreated, 0))
		days := age.Hours() / 24
		raw = math.Round(days*10) / 10

		if days >= 365 {
			spamScore = 0
			reason = fmt.Sprintf("Account %.0f days old — established", days)
		} else if days >= 90 {
			spamFactor := 1.0 - (days / 365.0)
			spamScore = math.Round(spamFactor*weight*1000) / 1000
			reason = fmt.Sprintf("Account %.0f days old — moderate age", days)
		} else if days >= 7 {
			spamFactor := 0.7
			spamScore = math.Round(spamFactor*weight*1000) / 1000
			reason = fmt.Sprintf("Account %.0f days old — relatively new", days)
		} else {
			spamScore = weight
			reason = fmt.Sprintf("Account %.0f days old — very new", days)
		}
	}

	return SpamSignal{
		Name:   "account_age_days",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func spamSignalEngagement(reactionsRecd, zapCntRecd, postCount int) SpamSignal {
	weight := 0.15
	var raw, spamScore float64
	var reason string

	totalEngagement := reactionsRecd + zapCntRecd
	raw = float64(totalEngagement)

	if postCount == 0 && totalEngagement == 0 {
		spamScore = weight * 0.3
		reason = "No posts or engagement — lurker (not necessarily spam)"
	} else if postCount > 0 && totalEngagement == 0 {
		spamScore = weight * 0.8
		reason = fmt.Sprintf("%d posts but 0 engagement received — one-way broadcasting", postCount)
	} else if totalEngagement > 0 {
		// More engagement per post = more human
		engagementRate := float64(totalEngagement) / math.Max(float64(postCount), 1)
		if engagementRate >= 1.0 {
			spamScore = 0
			reason = fmt.Sprintf("%d reactions + %d zaps received — well-engaged", reactionsRecd, zapCntRecd)
		} else {
			spamFactor := 1.0 - engagementRate
			spamScore = math.Round(spamFactor*weight*1000) / 1000
			reason = fmt.Sprintf("%d reactions + %d zaps on %d posts — some engagement", reactionsRecd, zapCntRecd, postCount)
		}
	}

	return SpamSignal{
		Name:   "engagement_received",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func spamSignalReports(reportsRecd int) SpamSignal {
	weight := 0.15
	var raw, spamScore float64
	var reason string

	raw = float64(reportsRecd)
	if reportsRecd == 0 {
		spamScore = 0
		reason = "No reports received"
	} else if reportsRecd <= 2 {
		spamScore = weight * 0.5
		reason = fmt.Sprintf("%d report(s) received — minor flag", reportsRecd)
	} else {
		spamScore = weight
		reason = fmt.Sprintf("%d reports received — significant spam signal", reportsRecd)
	}

	return SpamSignal{
		Name:   "reports_received",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func spamSignalActivity(postCount, replyCount, reactionsSent int) SpamSignal {
	weight := 0.10
	var raw, spamScore float64
	var reason string

	totalActivity := postCount + replyCount + reactionsSent
	raw = float64(totalActivity)

	if totalActivity == 0 {
		spamScore = weight * 0.3
		reason = "No activity — inactive account"
	} else if replyCount == 0 && reactionsSent == 0 && postCount > 5 {
		spamScore = weight
		reason = fmt.Sprintf("%d posts but no replies or reactions sent — broadcast-only pattern", postCount)
	} else {
		interactionRate := float64(replyCount+reactionsSent) / float64(totalActivity)
		if interactionRate >= 0.3 {
			spamScore = 0
			reason = fmt.Sprintf("%.0f%% interaction rate (%d replies, %d reactions sent) — healthy mix", interactionRate*100, replyCount, reactionsSent)
		} else {
			spamFactor := 1.0 - (interactionRate / 0.3)
			spamScore = math.Round(spamFactor*weight*1000) / 1000
			reason = fmt.Sprintf("%.0f%% interaction rate — mostly posting, limited engagement", interactionRate*100)
		}
	}

	return SpamSignal{
		Name:   "activity_pattern",
		Value:  raw,
		Weight: weight,
		Score:  spamScore,
		Reason: reason,
	}
}

func classifySpam(prob float64) string {
	if prob >= 0.7 {
		return "likely_spam"
	}
	if prob >= 0.4 {
		return "suspicious"
	}
	return "likely_human"
}

func spamSummary(classification string, score, followers, reports int) string {
	switch classification {
	case "likely_spam":
		return fmt.Sprintf("High spam probability. WoT score %d, %d followers, %d reports. This pubkey shows multiple spam indicators.", score, followers, reports)
	case "suspicious":
		return fmt.Sprintf("Moderate spam risk. WoT score %d, %d followers. Some indicators suggest this may not be a genuine account.", score, followers)
	default:
		return fmt.Sprintf("Likely human. WoT score %d, %d followers. Trust signals are consistent with a real user.", score, followers)
	}
}
