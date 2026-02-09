package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAnomaliesMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/anomalies", nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestAnomaliesCleanProfile(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	// Create a normal-looking profile: 10 followers, follows 5, no anomalies
	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	followers := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
		"4444444444444444444444444444444444444444444444444444444444444444",
		"5555555555555555555555555555555555555555555555555555555555555555",
		"6666666666666666666666666666666666666666666666666666666666666666",
		"7777777777777777777777777777777777777777777777777777777777777777",
		"8888888888888888888888888888888888888888888888888888888888888888",
		"9999999999999999999999999999999999999999999999999999999999999999",
		"0000000000000000000000000000000000000000000000000000000000000001",
	}

	// Target follows 5 of them (not all — normal behavior)
	for _, f := range followers {
		graph.AddFollow(f, target)
	}
	for _, f := range followers[:5] {
		graph.AddFollow(target, f)
	}
	// Build a chain to give non-zero scores
	for i := 0; i < len(followers)-1; i++ {
		graph.AddFollow(followers[i], followers[i+1])
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/anomalies?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp AnomaliesResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Pubkey != target {
		t.Fatalf("expected pubkey %s, got %s", target, resp.Pubkey)
	}
	if resp.Followers != 10 {
		t.Fatalf("expected 10 followers, got %d", resp.Followers)
	}
	if resp.RiskLevel != "clean" {
		t.Fatalf("expected clean risk level for normal profile, got %s", resp.RiskLevel)
	}
	if resp.AnomalyCount != 0 {
		t.Fatalf("expected 0 anomalies, got %d", resp.AnomalyCount)
	}
}

func TestAnomaliesFollowFarming(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Create 60 followers, target follows all of them back (100% follow-back ratio)
	for i := 0; i < 60; i++ {
		follower := padHex(i)
		graph.AddFollow(follower, target)
		graph.AddFollow(target, follower)
		// Chain follows for non-zero scores
		if i > 0 {
			graph.AddFollow(follower, padHex(i-1))
		}
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/anomalies?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)

	var resp AnomaliesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.FollowBackRatio < 0.95 {
		t.Fatalf("expected high follow-back ratio, got %f", resp.FollowBackRatio)
	}

	found := false
	for _, a := range resp.Anomalies {
		if a.Type == "follow_farming" {
			found = true
			if a.Severity != "high" {
				t.Fatalf("expected high severity for 100%% follow-back, got %s", a.Severity)
			}
		}
	}
	if !found {
		t.Fatal("expected follow_farming anomaly flag")
	}
}

func TestAnomaliesGhostFollowers(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Create 30 followers that only follow target and nothing else (ghost-like)
	for i := 0; i < 30; i++ {
		follower := padHex(i)
		graph.AddFollow(follower, target)
		// These followers follow nobody else and have no followers — ghost profiles
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/anomalies?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)

	var resp AnomaliesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.GhostRatio < 0.5 {
		t.Fatalf("expected high ghost ratio, got %f", resp.GhostRatio)
	}

	found := false
	for _, a := range resp.Anomalies {
		if a.Type == "ghost_followers" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected ghost_followers anomaly flag")
	}
}

func TestAnomaliesTrustConcentration(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bigFollower := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Big follower has many followers of their own (high PageRank) and follows only target
	for i := 0; i < 100; i++ {
		graph.AddFollow(padHex(i), bigFollower)
	}
	graph.AddFollow(bigFollower, target)

	// Add a few tiny followers to target
	for i := 100; i < 110; i++ {
		f := padHex(i)
		graph.AddFollow(f, target)
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/anomalies?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)

	var resp AnomaliesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.TopFollowerShare < 0.50 {
		t.Fatalf("expected high top-follower share, got %f", resp.TopFollowerShare)
	}

	found := false
	for _, a := range resp.Anomalies {
		if a.Type == "trust_concentration" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected trust_concentration anomaly flag")
	}
}

func TestAnomaliesResponseFields(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	graph.AddFollow("1111111111111111111111111111111111111111111111111111111111111111", target)
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/anomalies?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleAnomalies(w, req)

	var resp AnomaliesResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.GraphSize == 0 {
		t.Fatal("expected non-zero graph_size")
	}
	if resp.Followers != 1 {
		t.Fatalf("expected 1 follower, got %d", resp.Followers)
	}
}

func TestSeverityRank(t *testing.T) {
	if severityRank("high") != 3 {
		t.Fatal("high should be 3")
	}
	if severityRank("medium") != 2 {
		t.Fatal("medium should be 2")
	}
	if severityRank("low") != 1 {
		t.Fatal("low should be 1")
	}
	if severityRank("unknown") != 0 {
		t.Fatal("unknown should be 0")
	}
}

// padHex is defined in spam_test.go — reused here.
