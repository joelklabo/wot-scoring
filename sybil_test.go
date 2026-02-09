package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSybilMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/sybil", nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSybilGenuineAccount(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Build a realistic account: followers with diverse neighborhoods,
	// mutual follows, and substance
	for i := 0; i < 30; i++ {
		follower := padHex(i)
		graph.AddFollow(follower, target)
		// Target follows some back (organic mutual behavior)
		if i < 10 {
			graph.AddFollow(target, follower)
		}
		// Followers follow each other (creating interconnected but diverse neighborhoods)
		if i > 0 {
			graph.AddFollow(follower, padHex(i-1))
		}
		// Some cross-connections for diversity
		if i%3 == 0 && i > 3 {
			graph.AddFollow(follower, padHex(i-3))
		}
	}
	// Give followers their own followers (boosting their scores)
	for i := 100; i < 200; i++ {
		booster := padHex(i)
		targetFollower := padHex(i % 30)
		graph.AddFollow(booster, targetFollower)
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/sybil?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp SybilResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Pubkey != target {
		t.Fatalf("expected pubkey %s, got %s", target, resp.Pubkey)
	}
	if resp.SybilScore < 40 {
		t.Fatalf("genuine account should score >= 40, got %d", resp.SybilScore)
	}
	if resp.Classification != "genuine" && resp.Classification != "likely_genuine" {
		t.Fatalf("expected genuine or likely_genuine, got %s", resp.Classification)
	}
	if resp.MutualCount != 10 {
		t.Fatalf("expected 10 mutuals, got %d", resp.MutualCount)
	}
	if resp.Followers != 30 {
		t.Fatalf("expected 30 followers, got %d", resp.Followers)
	}
	if len(resp.Signals) != 5 {
		t.Fatalf("expected 5 signals, got %d", len(resp.Signals))
	}
}

func TestSybilSuspiciousAccount(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	// Sybil-like: many followers that are ghost accounts (no score, no other activity)
	for i := 0; i < 50; i++ {
		follower := padHex(i)
		graph.AddFollow(follower, target)
		// These followers follow nobody else and nobody follows them — ghost accounts
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/sybil?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)

	var resp SybilResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.SybilScore > 60 {
		t.Fatalf("suspicious account should score <= 60, got %d", resp.SybilScore)
	}
	if resp.MutualCount != 0 {
		t.Fatalf("expected 0 mutuals for one-way followers, got %d", resp.MutualCount)
	}
}

func TestSybilUnknownAccount(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	// Account not in graph at all
	graph.AddFollow("1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222")
	graph.ComputePageRank(20, 0.85)

	unknown := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := httptest.NewRequest("GET", "/sybil?pubkey="+unknown, nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)

	var resp SybilResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.SybilScore > 20 {
		t.Fatalf("unknown account should score very low, got %d", resp.SybilScore)
	}
	if resp.Confidence > 0.2 {
		t.Fatalf("unknown account should have low confidence, got %f", resp.Confidence)
	}
}

func TestSybilSignalWeights(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	graph.AddFollow("1111111111111111111111111111111111111111111111111111111111111111", target)
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/sybil?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)

	var resp SybilResponse
	json.NewDecoder(w.Body).Decode(&resp)

	totalWeight := 0.0
	for _, s := range resp.Signals {
		totalWeight += s.Weight
		if s.Score < 0 || s.Score > 1.0 {
			t.Fatalf("signal %s score out of range: %f", s.Name, s.Score)
		}
		if s.Weight <= 0 {
			t.Fatalf("signal %s has zero or negative weight", s.Name)
		}
		if s.Name == "" {
			t.Fatal("signal has empty name")
		}
		if s.Description == "" {
			t.Fatal("signal has empty description")
		}
	}

	// Weights should sum to ~1.0
	if totalWeight < 0.99 || totalWeight > 1.01 {
		t.Fatalf("signal weights should sum to 1.0, got %f", totalWeight)
	}
}

func TestSybilClassification(t *testing.T) {
	tests := []struct {
		score    int
		expected string
	}{
		{100, "genuine"},
		{75, "genuine"},
		{74, "likely_genuine"},
		{50, "likely_genuine"},
		{49, "suspicious"},
		{25, "suspicious"},
		{24, "likely_sybil"},
		{0, "likely_sybil"},
	}
	for _, tt := range tests {
		got := classifySybilScore(tt.score)
		if got != tt.expected {
			t.Errorf("classifySybilScore(%d) = %s, want %s", tt.score, got, tt.expected)
		}
	}
}

func TestSybilResponseFields(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// Use indices 2-6 to avoid padHex(0)==padHex(1) collision
	for i := 2; i < 7; i++ {
		graph.AddFollow(padHex(i), target)
	}
	graph.AddFollow(target, padHex(2))
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/sybil?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSybil(w, req)

	var resp SybilResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.GraphSize == 0 {
		t.Fatal("expected non-zero graph_size")
	}
	if resp.Followers != 5 {
		t.Fatalf("expected 5 followers, got %d", resp.Followers)
	}
	if resp.Follows != 1 {
		t.Fatalf("expected 1 follow, got %d", resp.Follows)
	}
	if resp.MutualCount != 1 {
		t.Fatalf("expected 1 mutual, got %d", resp.MutualCount)
	}
	if resp.Classification == "" {
		t.Fatal("classification should not be empty")
	}
	if resp.SybilScore < 0 || resp.SybilScore > 100 {
		t.Fatalf("sybil_score out of range: %d", resp.SybilScore)
	}
	if resp.Confidence < 0 || resp.Confidence > 1.0 {
		t.Fatalf("confidence out of range: %f", resp.Confidence)
	}
}

func TestSybilBatchMissingBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/sybil/batch", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	handleSybilBatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty pubkeys, got %d", w.Code)
	}
}

func TestSybilBatchWrongMethod(t *testing.T) {
	req := httptest.NewRequest("GET", "/sybil/batch", nil)
	w := httptest.NewRecorder()
	handleSybilBatch(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestSybilBatchSuccess(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	pk1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pk2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// pk1 has followers, pk2 is isolated
	for i := 0; i < 10; i++ {
		graph.AddFollow(padHex(i), pk1)
	}
	graph.ComputePageRank(20, 0.85)

	body, _ := json.Marshal(map[string][]string{"pubkeys": {pk1, pk2}})
	req := httptest.NewRequest("POST", "/sybil/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handleSybilBatch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Results []struct {
			Pubkey         string `json:"pubkey"`
			SybilScore     int    `json:"sybil_score"`
			Classification string `json:"classification"`
			TrustScore     int    `json:"trust_score"`
		} `json:"results"`
		Count     int `json:"count"`
		GraphSize int `json:"graph_size"`
	}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Count != 2 {
		t.Fatalf("expected 2 results, got %d", resp.Count)
	}
	// Results should be sorted by sybil score ascending (most suspicious first)
	if resp.Results[0].SybilScore > resp.Results[1].SybilScore {
		t.Fatal("expected results sorted by sybil_score ascending")
	}
}

func TestSybilBatchTooMany(t *testing.T) {
	pubkeys := make([]string, 51)
	for i := range pubkeys {
		pubkeys[i] = padHex(i)
	}
	body, _ := json.Marshal(map[string][]string{"pubkeys": pubkeys})
	req := httptest.NewRequest("POST", "/sybil/batch", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handleSybilBatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for >50 pubkeys, got %d", w.Code)
	}
}

func TestFollowerDiversityFewFollowers(t *testing.T) {
	diversity := computeFollowerDiversity([]string{"a", "b"}, 1000)
	if diversity < 0 || diversity > 1.0 {
		t.Fatalf("diversity out of range: %f", diversity)
	}
	// Few followers should return low-ish diversity (0.3)
	if diversity > 0.5 {
		t.Fatalf("expected low diversity for 2 followers, got %f", diversity)
	}
}

func TestRound3(t *testing.T) {
	tests := []struct {
		input    float64
		expected float64
	}{
		{0.12345, 0.123},
		{0.9999, 1.0},
		{0.5555, 0.556},
		{0.0, 0.0},
		{1.0, 1.0},
	}
	for _, tt := range tests {
		got := round3(tt.input)
		if got != tt.expected {
			t.Errorf("round3(%f) = %f, want %f", tt.input, got, tt.expected)
		}
	}
}

func TestComputeConfidence(t *testing.T) {
	// Not found = very low confidence
	conf := computeConfidence(0, 0, false, 0)
	if conf > 0.2 {
		t.Fatalf("expected low confidence for not-found, got %f", conf)
	}

	// Found with lots of followers = high confidence
	conf = computeConfidence(100, 50, true, 80)
	if conf < 0.8 {
		t.Fatalf("expected high confidence for well-connected account, got %f", conf)
	}
}
