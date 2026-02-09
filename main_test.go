package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGraphGetFollowsAndFollowers(t *testing.T) {
	g := NewGraph()
	g.AddFollow("alice", "bob")
	g.AddFollow("alice", "carol")
	g.AddFollow("bob", "carol")

	follows := g.GetFollows("alice")
	if len(follows) != 2 {
		t.Fatalf("expected alice to follow 2, got %d", len(follows))
	}

	followers := g.GetFollowers("carol")
	if len(followers) != 2 {
		t.Fatalf("expected carol to have 2 followers, got %d", len(followers))
	}

	// Non-existent pubkey
	empty := g.GetFollows("nobody")
	if len(empty) != 0 {
		t.Errorf("expected 0 follows for unknown pubkey, got %d", len(empty))
	}
}

func TestHandleBatch(t *testing.T) {
	// Set up graph with known scores
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.AddFollow("a", "b")
	graph.AddFollow("a", "c")
	graph.AddFollow("b", "c")
	graph.ComputePageRank(20, 0.85)

	body := `{"pubkeys":["a","b","c"]}`
	req := httptest.NewRequest(http.MethodPost, "/batch", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleBatch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	results, ok := resp["results"].([]interface{})
	if !ok {
		t.Fatal("expected results array in response")
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Each result should have pubkey, score, found
	for _, r := range results {
		entry := r.(map[string]interface{})
		if _, ok := entry["pubkey"]; !ok {
			t.Error("missing pubkey in batch result")
		}
		if _, ok := entry["found"]; !ok {
			t.Error("missing found in batch result")
		}
	}
}

func TestHandleBatchRejectsGET(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/batch", nil)
	w := httptest.NewRecorder()
	handleBatch(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleBatchRejectsOverLimit(t *testing.T) {
	pubkeys := make([]string, 101)
	for i := range pubkeys {
		pubkeys[i] = "hex"
	}
	body, _ := json.Marshal(map[string][]string{"pubkeys": pubkeys})
	req := httptest.NewRequest(http.MethodPost, "/batch", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleBatch(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandlePersonalized(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	// alice follows bob, bob follows alice (mutual)
	// carol follows bob
	// dave follows nobody
	graph.AddFollow("alice", "bob")
	graph.AddFollow("alice", "carol")
	graph.AddFollow("bob", "alice")
	graph.AddFollow("carol", "bob")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/personalized?viewer=alice&target=bob", nil)
	w := httptest.NewRecorder()

	handlePersonalized(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["viewer_follows_target"] != true {
		t.Error("expected viewer_follows_target = true")
	}
	if resp["target_follows_viewer"] != true {
		t.Error("expected target_follows_viewer = true")
	}
	if resp["mutual_follow"] != true {
		t.Error("expected mutual_follow = true")
	}

	personalizedScore, ok := resp["personalized_score"].(float64)
	if !ok {
		t.Fatal("missing personalized_score")
	}
	if personalizedScore <= 0 {
		t.Errorf("expected positive personalized_score, got %v", personalizedScore)
	}

	// Test non-follower: alice -> dave (no relationship)
	req2 := httptest.NewRequest(http.MethodGet, "/personalized?viewer=alice&target=dave", nil)
	w2 := httptest.NewRecorder()
	handlePersonalized(w2, req2)

	var resp2 map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if resp2["viewer_follows_target"] != false {
		t.Error("expected viewer_follows_target = false for dave")
	}
}

func TestHandlePersonalizedMissingParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/personalized?viewer=alice", nil)
	w := httptest.NewRecorder()
	handlePersonalized(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
