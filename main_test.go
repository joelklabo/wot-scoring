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

func TestHandleSimilar(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	// alice follows: bob, carol, dave
	// eve follows: bob, carol, frank
	// mallory follows: zara (no overlap)
	graph.AddFollow("alice", "bob")
	graph.AddFollow("alice", "carol")
	graph.AddFollow("alice", "dave")
	graph.AddFollow("eve", "bob")
	graph.AddFollow("eve", "carol")
	graph.AddFollow("eve", "frank")
	graph.AddFollow("mallory", "zara")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/similar?pubkey=alice", nil)
	w := httptest.NewRecorder()
	handleSimilar(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	similar, ok := resp["similar"].([]interface{})
	if !ok {
		t.Fatal("expected similar array in response")
	}

	// eve should be the most similar (shares bob + carol)
	if len(similar) == 0 {
		t.Fatal("expected at least 1 similar result")
	}

	first := similar[0].(map[string]interface{})
	if first["pubkey"] != "eve" {
		t.Errorf("expected eve as most similar, got %s", first["pubkey"])
	}
	if first["shared_follows"].(float64) != 2 {
		t.Errorf("expected 2 shared follows, got %v", first["shared_follows"])
	}

	// mallory should NOT appear (only 1 follow = below min threshold of 3)
	for _, s := range similar {
		entry := s.(map[string]interface{})
		if entry["pubkey"] == "mallory" {
			t.Error("mallory should not appear (< 3 follows)")
		}
	}
}

func TestHandleSimilarMissingPubkey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/similar", nil)
	w := httptest.NewRecorder()
	handleSimilar(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSimilarNonexistentPubkey(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/similar?pubkey=nonexistent", nil)
	w := httptest.NewRecorder()
	handleSimilar(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "pubkey has no follows in graph" {
		t.Errorf("expected error message for nonexistent pubkey, got %v", resp["error"])
	}
}

func TestGraphAllFollowers(t *testing.T) {
	g := NewGraph()
	g.AddFollow("alice", "bob")
	g.AddFollow("carol", "dave")

	all := g.AllFollowers()
	if len(all) != 2 {
		t.Fatalf("expected 2, got %d", len(all))
	}

	// Both alice and carol should be in the list (they have follows)
	found := make(map[string]bool)
	for _, pk := range all {
		found[pk] = true
	}
	if !found["alice"] {
		t.Error("expected alice in AllFollowers")
	}
	if !found["carol"] {
		t.Error("expected carol in AllFollowers")
	}
}

func TestHandleRecommend(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	// alice follows: bob, carol, dave
	// bob follows: eve, frank, greg
	// carol follows: eve, hank
	// dave follows: frank, ivan
	// So eve is followed by bob + carol (2 of alice's 3 follows) — strong recommendation
	// frank is followed by bob + dave (2 of alice's 3 follows) — strong recommendation
	// greg is followed only by bob (1 of 3) — below threshold
	// hank is followed only by carol (1 of 3) — below threshold
	// ivan is followed only by dave (1 of 3) — below threshold
	graph.AddFollow("alice", "bob")
	graph.AddFollow("alice", "carol")
	graph.AddFollow("alice", "dave")
	graph.AddFollow("bob", "eve")
	graph.AddFollow("bob", "frank")
	graph.AddFollow("bob", "greg")
	graph.AddFollow("carol", "eve")
	graph.AddFollow("carol", "hank")
	graph.AddFollow("dave", "frank")
	graph.AddFollow("dave", "ivan")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/recommend?pubkey=alice", nil)
	w := httptest.NewRecorder()
	handleRecommend(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	recs, ok := resp["recommendations"].([]interface{})
	if !ok {
		t.Fatal("expected recommendations array in response")
	}

	// eve and frank should be recommended (each followed by 2 of alice's 3 follows)
	if len(recs) < 2 {
		t.Fatalf("expected at least 2 recommendations, got %d", len(recs))
	}

	// Collect recommended pubkeys
	recPubkeys := make(map[string]bool)
	for _, r := range recs {
		entry := r.(map[string]interface{})
		recPubkeys[entry["pubkey"].(string)] = true
		// mutual_follows should be 2 for eve and frank
		if entry["pubkey"] == "eve" || entry["pubkey"] == "frank" {
			if entry["mutual_follows"].(float64) != 2 {
				t.Errorf("expected 2 mutual follows for %s, got %v", entry["pubkey"], entry["mutual_follows"])
			}
		}
	}

	if !recPubkeys["eve"] {
		t.Error("expected eve in recommendations")
	}
	if !recPubkeys["frank"] {
		t.Error("expected frank in recommendations")
	}

	// greg, hank, ivan should NOT appear (only 1 mutual follow, below threshold of 2)
	for _, excluded := range []string{"greg", "hank", "ivan"} {
		if recPubkeys[excluded] {
			t.Errorf("%s should not be in recommendations (only 1 mutual follow)", excluded)
		}
	}

	// alice should NOT appear in her own recommendations
	if recPubkeys["alice"] {
		t.Error("alice should not appear in her own recommendations")
	}

	// bob, carol, dave should NOT appear (already followed by alice)
	for _, followed := range []string{"bob", "carol", "dave"} {
		if recPubkeys[followed] {
			t.Errorf("%s should not be in recommendations (already followed)", followed)
		}
	}

	// Check follows_count
	if resp["follows_count"].(float64) != 3 {
		t.Errorf("expected follows_count 3, got %v", resp["follows_count"])
	}
}

func TestHandleRecommendMissingPubkey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/recommend", nil)
	w := httptest.NewRecorder()
	handleRecommend(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRecommendNoFollows(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/recommend?pubkey=nonexistent", nil)
	w := httptest.NewRecorder()
	handleRecommend(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "pubkey has no follows in graph" {
		t.Errorf("expected error message for nonexistent pubkey, got %v", resp["error"])
	}
}

func TestHandleGraphPath(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	// alice -> bob -> carol -> dave
	graph.AddFollow("alice", "bob")
	graph.AddFollow("bob", "carol")
	graph.AddFollow("carol", "dave")
	// Also add a longer path: alice -> eve -> frank -> dave
	graph.AddFollow("alice", "eve")
	graph.AddFollow("eve", "frank")
	graph.AddFollow("frank", "dave")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/graph?from=alice&to=dave", nil)
	w := httptest.NewRecorder()
	handleGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["found"] != true {
		t.Fatal("expected path to be found")
	}

	// BFS should find the 3-hop path: alice -> bob -> carol -> dave
	hops := resp["hops"].(float64)
	if hops != 3 {
		t.Errorf("expected 3 hops, got %v", hops)
	}

	path := resp["path"].([]interface{})
	if len(path) != 4 {
		t.Fatalf("expected path length 4, got %d", len(path))
	}
	if path[0].(map[string]interface{})["pubkey"] != "alice" {
		t.Error("expected path to start with alice")
	}
	if path[3].(map[string]interface{})["pubkey"] != "dave" {
		t.Error("expected path to end with dave")
	}
}

func TestHandleGraphPathNotFound(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.AddFollow("alice", "bob")
	graph.AddFollow("carol", "dave") // disconnected from alice
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/graph?from=alice&to=dave", nil)
	w := httptest.NewRecorder()
	handleGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["found"] != false {
		t.Error("expected path not to be found between disconnected nodes")
	}
}

func TestHandleGraphNeighborhood(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.AddFollow("alice", "bob")
	graph.AddFollow("alice", "carol")
	graph.AddFollow("bob", "alice") // mutual
	graph.AddFollow("dave", "alice") // follower only
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest(http.MethodGet, "/graph?pubkey=alice", nil)
	w := httptest.NewRecorder()
	handleGraph(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp["pubkey"] != "alice" {
		t.Errorf("expected pubkey alice, got %v", resp["pubkey"])
	}
	if resp["follows_count"].(float64) != 2 {
		t.Errorf("expected 2 follows, got %v", resp["follows_count"])
	}
	if resp["followers_count"].(float64) != 2 {
		t.Errorf("expected 2 followers, got %v", resp["followers_count"])
	}

	neighbors := resp["neighbors"].([]interface{})
	if len(neighbors) != 3 { // bob (mutual), carol (follows), dave (follower)
		t.Fatalf("expected 3 neighbors, got %d", len(neighbors))
	}

	// Check relation types
	relations := make(map[string]string)
	for _, n := range neighbors {
		entry := n.(map[string]interface{})
		relations[entry["pubkey"].(string)] = entry["relation"].(string)
	}
	if relations["bob"] != "mutual" {
		t.Errorf("expected bob to be mutual, got %s", relations["bob"])
	}
	if relations["carol"] != "follows" {
		t.Errorf("expected carol to be follows, got %s", relations["carol"])
	}
	if relations["dave"] != "follower" {
		t.Errorf("expected dave to be follower, got %s", relations["dave"])
	}
}

func TestHandleGraphMissingParams(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/graph", nil)
	w := httptest.NewRecorder()
	handleGraph(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleGraphSamePubkey(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/graph?from=alice&to=alice", nil)
	w := httptest.NewRecorder()
	handleGraph(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for same pubkey, got %d", w.Code)
	}
}

func TestBfsPath(t *testing.T) {
	oldGraph := graph
	defer func() { graph = oldGraph }()

	graph = NewGraph()
	graph.AddFollow("a", "b")
	graph.AddFollow("b", "c")
	graph.AddFollow("c", "d")

	// Direct path: a -> b -> c -> d
	path, found := bfsPath("a", "d", 6)
	if !found {
		t.Fatal("expected path a->d to be found")
	}
	if len(path) != 4 {
		t.Fatalf("expected path length 4, got %d", len(path))
	}
	if path[0] != "a" || path[3] != "d" {
		t.Errorf("expected path from a to d, got %v", path)
	}

	// No reverse path (follows are directional)
	_, found = bfsPath("d", "a", 6)
	if found {
		t.Error("expected no path from d to a (follows are directional)")
	}

	// Max depth limit
	_, found = bfsPath("a", "d", 2)
	if found {
		t.Error("expected path not found within depth 2")
	}
}
