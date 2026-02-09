package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"
)

// TimelinePoint represents a single point in a pubkey's trust timeline.
type TimelinePoint struct {
	Date              string  `json:"date"`               // YYYY-MM
	CumulativeFollows int     `json:"cumulative_follows"`  // total followers up to this date
	NewFollows        int     `json:"new_follows"`         // new followers in this period
	EstimatedScore    int     `json:"estimated_score"`     // approximate normalized score at this point
	Velocity          float64 `json:"velocity"`            // followers per day in this period
}

// TimelineResponse is the full response for the /timeline endpoint.
type TimelineResponse struct {
	Pubkey             string          `json:"pubkey"`
	Points             []TimelinePoint `json:"points"`
	CurrentScore       int             `json:"current_score"`
	CurrentFollowers   int             `json:"current_followers"`
	FollowersWithDates int             `json:"followers_with_dates"`
	TotalFollowers     int             `json:"total_followers"`
	FirstFollow        string          `json:"first_follow,omitempty"`
	LatestFollow       string          `json:"latest_follow,omitempty"`
	GraphSize          int             `json:"graph_size"`
}

// handleTimeline returns a time-series of follower growth and estimated trust
// for a given pubkey, reconstructed from follow timestamps.
func handleTimeline(w http.ResponseWriter, r *http.Request) {
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

	if len(pubkey) != 64 {
		http.Error(w, `{"error":"pubkey must be 64 hex characters"}`, http.StatusBadRequest)
		return
	}
	for _, c := range pubkey {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			http.Error(w, `{"error":"pubkey must be lowercase hex"}`, http.StatusBadRequest)
			return
		}
	}

	followers := graph.GetFollowers(pubkey)
	if len(followers) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TimelineResponse{
			Pubkey:           pubkey,
			Points:           []TimelinePoint{},
			CurrentFollowers: 0,
			GraphSize:        graph.Stats().Nodes,
		})
		return
	}

	// Collect follow timestamps for this pubkey's followers
	type followEvent struct {
		from string
		at   time.Time
	}
	var dated []followEvent
	for _, f := range followers {
		t := graph.GetFollowTime(f, pubkey)
		if !t.IsZero() {
			dated = append(dated, followEvent{from: f, at: t})
		}
	}

	// Sort by time
	sort.Slice(dated, func(i, j int) bool {
		return dated[i].at.Before(dated[j].at)
	})

	stats := graph.Stats()
	withoutDates := len(followers) - len(dated)

	if len(dated) == 0 {
		// No timestamp data — return current state only
		rawScore, _ := graph.GetScore(pubkey)
		score := normalizeScore(rawScore, stats.Nodes)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TimelineResponse{
			Pubkey:             pubkey,
			Points:             []TimelinePoint{},
			CurrentScore:       score,
			CurrentFollowers:   len(followers),
			FollowersWithDates: 0,
			TotalFollowers:     len(followers),
			GraphSize:          stats.Nodes,
		})
		return
	}

	// Bucket by month
	buckets := make(map[string][]followEvent) // "YYYY-MM" -> events
	for _, fe := range dated {
		key := fe.at.UTC().Format("2006-01")
		buckets[key] = append(buckets[key], fe)
	}

	// Get sorted month keys
	months := make([]string, 0, len(buckets))
	for m := range buckets {
		months = append(months, m)
	}
	sort.Strings(months)

	// Build timeline points
	points := make([]TimelinePoint, 0, len(months))
	cumulative := withoutDates // followers without dates are assumed to pre-exist
	for _, month := range months {
		events := buckets[month]
		newCount := len(events)
		cumulative += newCount

		// Estimate score: approximate using follower ratio vs graph size
		// This is a simplified model: score ~ log10(followers/avg + 1) * 25
		estScore := estimateScoreFromFollowers(cumulative, stats.Nodes)

		// Calculate velocity (followers per day in this month)
		var velocity float64
		t, _ := time.Parse("2006-01", month)
		daysInMonth := float64(t.AddDate(0, 1, 0).Sub(t).Hours() / 24)
		if daysInMonth > 0 {
			velocity = float64(newCount) / daysInMonth
		}

		points = append(points, TimelinePoint{
			Date:              month,
			CumulativeFollows: cumulative,
			NewFollows:        newCount,
			EstimatedScore:    estScore,
			Velocity:          math.Round(velocity*100) / 100,
		})
	}

	rawScore, _ := graph.GetScore(pubkey)
	currentScore := normalizeScore(rawScore, stats.Nodes)

	resp := TimelineResponse{
		Pubkey:             pubkey,
		Points:             points,
		CurrentScore:       currentScore,
		CurrentFollowers:   len(followers),
		FollowersWithDates: len(dated),
		TotalFollowers:     len(followers),
		FirstFollow:        dated[0].at.UTC().Format(time.RFC3339),
		LatestFollow:       dated[len(dated)-1].at.UTC().Format(time.RFC3339),
		GraphSize:          stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// estimateScoreFromFollowers approximates a normalized trust score based on
// follower count relative to graph size. This is a simplified model that
// approximates PageRank behavior: nodes with more followers tend to score higher.
func estimateScoreFromFollowers(followers, graphSize int) int {
	if graphSize == 0 || followers == 0 {
		return 0
	}
	// Average node has ~(totalEdges/totalNodes) followers
	// For estimation, use followers relative to average
	avgFollowers := 12.0 // typical average in a social graph
	ratio := float64(followers) / avgFollowers
	score := math.Log10(ratio+1) * 25
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return int(math.Round(score))
}
