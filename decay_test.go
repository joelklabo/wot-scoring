package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDecayWeight(t *testing.T) {
	now := time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC)

	// Just created: weight should be 1.0
	w := decayWeight(now, now, 365)
	if math.Abs(w-1.0) > 0.001 {
		t.Errorf("expected weight ~1.0 for fresh follow, got %f", w)
	}

	// Exactly one half-life ago: weight should be ~0.5
	oneYearAgo := now.AddDate(-1, 0, 0)
	w = decayWeight(oneYearAgo, now, 365)
	if math.Abs(w-0.5) > 0.02 {
		t.Errorf("expected weight ~0.5 for 1-year-old follow (365d half-life), got %f", w)
	}

	// Two half-lives ago: weight should be ~0.25
	twoYearsAgo := now.AddDate(-2, 0, 0)
	w = decayWeight(twoYearsAgo, now, 365)
	if math.Abs(w-0.25) > 0.02 {
		t.Errorf("expected weight ~0.25 for 2-year-old follow, got %f", w)
	}

	// Zero time: full weight (no data)
	w = decayWeight(time.Time{}, now, 365)
	if w != 1.0 {
		t.Errorf("expected weight 1.0 for zero time, got %f", w)
	}

	// Zero half-life: full weight (decay disabled)
	w = decayWeight(oneYearAgo, now, 0)
	if w != 1.0 {
		t.Errorf("expected weight 1.0 for zero half-life, got %f", w)
	}

	// Future time: should clamp to 1.0
	future := now.Add(24 * time.Hour)
	w = decayWeight(future, now, 365)
	if math.Abs(w-1.0) > 0.001 {
		t.Errorf("expected weight ~1.0 for future follow, got %f", w)
	}
}

func TestAddFollowWithTime(t *testing.T) {
	g := NewGraph()
	ts := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)

	g.AddFollowWithTime("alice", "bob", ts)

	follows := g.GetFollows("alice")
	if len(follows) != 1 || follows[0] != "bob" {
		t.Errorf("expected alice->bob follow, got %v", follows)
	}

	ft := g.GetFollowTime("alice", "bob")
	if !ft.Equal(ts) {
		t.Errorf("expected follow time %v, got %v", ts, ft)
	}

	// Zero time should still add the follow but no time entry
	g.AddFollowWithTime("carol", "dave", time.Time{})
	if ft := g.GetFollowTime("carol", "dave"); !ft.IsZero() {
		t.Errorf("expected zero time for carol->dave, got %v", ft)
	}
}

func TestComputeDecayedPageRank(t *testing.T) {
	g := NewGraph()
	now := time.Now()
	recent := now.Add(-30 * 24 * time.Hour)  // 30 days ago
	old := now.Add(-730 * 24 * time.Hour)     // 2 years ago

	// bob gets a recent follow from alice, old follow from carol
	// dave gets recent follows from both alice and carol
	g.AddFollowWithTime("alice", "bob", recent)
	g.AddFollowWithTime("carol", "bob", old)
	g.AddFollowWithTime("alice", "dave", recent)
	g.AddFollowWithTime("carol", "dave", recent)

	g.ComputePageRank(20, 0.85)

	// Static: bob and dave should have similar scores (both have 2 followers)
	staticBob, _ := g.GetScore("bob")
	staticDave, _ := g.GetScore("dave")
	if math.Abs(staticBob-staticDave) > 0.001 {
		t.Errorf("expected similar static scores, bob=%f dave=%f", staticBob, staticDave)
	}

	// Decayed: dave should score higher (both followers are recent)
	// bob has one old follower (carol) which gets discounted
	decayScores := g.ComputeDecayedPageRank(20, 0.85, 365)
	decayBob := decayScores["bob"]
	decayDave := decayScores["dave"]

	if decayDave <= decayBob {
		t.Errorf("expected dave (all recent follows) to outscore bob (one old follow) in decay, bob=%f dave=%f", decayBob, decayDave)
	}
}

func TestComputeDecayedPageRankEmpty(t *testing.T) {
	g := NewGraph()
	scores := g.ComputeDecayedPageRank(20, 0.85, 365)
	if len(scores) != 0 {
		t.Errorf("expected empty scores for empty graph, got %d entries", len(scores))
	}
}

func TestComputeDecayedPageRankNoTimeData(t *testing.T) {
	g := NewGraph()
	// No time data — should behave identically to static PageRank
	g.AddFollow("alice", "bob")
	g.AddFollow("bob", "carol")
	g.ComputePageRank(20, 0.85)

	decayScores := g.ComputeDecayedPageRank(20, 0.85, 365)

	staticBob, _ := g.GetScore("bob")
	decayBob := decayScores["bob"]

	// Should be very close (not identical due to float precision, but close)
	if math.Abs(staticBob-decayBob) > 0.0001 {
		t.Errorf("expected similar scores without time data, static=%f decay=%f", staticBob, decayBob)
	}
}

func TestHandleDecay(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	now := time.Now()
	graph.AddFollowWithTime("alice", "bob", now.Add(-30*24*time.Hour))
	graph.AddFollowWithTime("carol", "bob", now.Add(-365*24*time.Hour))
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/decay?pubkey=bob", nil)
	w := httptest.NewRecorder()
	handleDecay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["pubkey"] != "bob" {
		t.Errorf("expected pubkey bob, got %v", resp["pubkey"])
	}
	if _, ok := resp["decay_score"]; !ok {
		t.Error("missing decay_score in response")
	}
	if _, ok := resp["static_score"]; !ok {
		t.Error("missing static_score in response")
	}
	if _, ok := resp["delta"]; !ok {
		t.Error("missing delta in response")
	}
	if resp["half_life_days"].(float64) != 365 {
		t.Errorf("expected half_life_days 365, got %v", resp["half_life_days"])
	}
	if resp["followers_with_time_data"].(float64) != 2 {
		t.Errorf("expected 2 followers with time data, got %v", resp["followers_with_time_data"])
	}
}

func TestHandleDecayCustomHalfLife(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	now := time.Now()
	graph.AddFollowWithTime("alice", "bob", now.Add(-30*24*time.Hour))
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/decay?pubkey=bob&half_life=30", nil)
	w := httptest.NewRecorder()
	handleDecay(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp["half_life_days"].(float64) != 30 {
		t.Errorf("expected half_life_days 30, got %v", resp["half_life_days"])
	}
}

func TestHandleDecayMissingPubkey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/decay", nil)
	w := httptest.NewRecorder()
	handleDecay(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleDecayTop(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	now := time.Now()
	// bob: 1 recent follower, 1 old follower
	graph.AddFollowWithTime("alice", "bob", now.Add(-30*24*time.Hour))
	graph.AddFollowWithTime("carol", "bob", now.Add(-730*24*time.Hour))
	// dave: 2 recent followers
	graph.AddFollowWithTime("alice", "dave", now.Add(-7*24*time.Hour))
	graph.AddFollowWithTime("carol", "dave", now.Add(-14*24*time.Hour))
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/decay/top?limit=10", nil)
	w := httptest.NewRecorder()
	handleDecayTop(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	entries, ok := resp["entries"].([]interface{})
	if !ok {
		t.Fatal("expected entries array in response")
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 entry")
	}

	// Verify entries have expected fields
	first := entries[0].(map[string]interface{})
	for _, field := range []string{"pubkey", "decay_score", "static_score", "delta", "decay_rank", "static_rank", "rank_change"} {
		if _, ok := first[field]; !ok {
			t.Errorf("missing field %s in decay/top entry", field)
		}
	}

	if resp["algorithm"] != "PageRank with exponential time decay" {
		t.Errorf("unexpected algorithm: %v", resp["algorithm"])
	}
}
