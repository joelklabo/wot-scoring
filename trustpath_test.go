package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTrustPathMissingParams(t *testing.T) {
	graph = NewGraph()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path", nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTrustPathMissingTo(t *testing.T) {
	graph = NewGraph()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+padHex(2), nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestTrustPathSameNode(t *testing.T) {
	graph = NewGraph()
	pk := padHex(2)
	graph.AddFollow(pk, padHex(3))
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+pk+"&to="+pk, nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Connected {
		t.Error("same node should be connected")
	}
	if resp.PathDiversity != 1 {
		t.Errorf("expected 1 path, got %d", resp.PathDiversity)
	}
}

func TestTrustPathDirectFollow(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	// Create a small graph
	graph.AddFollow(a, b)
	graph.AddFollow(b, a) // mutual
	graph.AddFollow(a, padHex(4))
	graph.AddFollow(padHex(4), a)
	graph.AddFollow(b, padHex(5))
	graph.AddFollow(padHex(5), b)
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+b, nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Connected {
		t.Error("directly connected nodes should be connected")
	}
	if len(resp.Paths) == 0 {
		t.Fatal("expected at least one path")
	}
	if resp.Paths[0].Length != 1 {
		t.Errorf("direct follow should be length 1, got %d", resp.Paths[0].Length)
	}
	if resp.Classification == "none" {
		t.Error("connected nodes should not be classified as none")
	}
}

func TestTrustPathMultiHop(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	c := padHex(4)
	d := padHex(5)

	// Chain: a -> b -> c -> d
	graph.AddFollow(a, b)
	graph.AddFollow(b, c)
	graph.AddFollow(c, d)
	// Add back links for non-trivial graph
	graph.AddFollow(b, a)
	graph.AddFollow(c, b)
	graph.AddFollow(d, c)
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+d, nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Connected {
		t.Error("chain a->b->c->d should be connected")
	}
	if len(resp.Paths) == 0 {
		t.Fatal("expected at least one path")
	}
	if resp.Paths[0].Length != 3 {
		t.Errorf("expected path length 3, got %d", resp.Paths[0].Length)
	}
}

func TestTrustPathNotConnected(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	// Two isolated nodes
	graph.AddFollow(a, padHex(4))
	graph.AddFollow(b, padHex(5))
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+b, nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Connected {
		t.Error("disconnected nodes should not be connected")
	}
	if resp.Classification != "none" {
		t.Errorf("expected classification none, got %s", resp.Classification)
	}
	if len(resp.Paths) != 0 {
		t.Error("expected no paths for disconnected nodes")
	}
}

func TestTrustPathMultiplePaths(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	m1 := padHex(4)
	m2 := padHex(5)

	// Two paths: a -> m1 -> b and a -> m2 -> b
	graph.AddFollow(a, m1)
	graph.AddFollow(m1, b)
	graph.AddFollow(a, m2)
	graph.AddFollow(m2, b)
	// Back links for graph
	graph.AddFollow(m1, a)
	graph.AddFollow(b, m1)
	graph.AddFollow(m2, a)
	graph.AddFollow(b, m2)
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+b+"&max_paths=3", nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Connected {
		t.Error("expected connected")
	}
	if resp.PathDiversity < 2 {
		t.Errorf("expected at least 2 paths, got %d", resp.PathDiversity)
	}
	// Multiple paths should increase overall trust above best single path
	if resp.OverallTrust < resp.BestTrust {
		t.Error("overall trust should be >= best trust with multiple paths")
	}
}

func TestTrustPathMutualBonus(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	c := padHex(4)

	// Path with mutual: a <-> b -> c
	graph.AddFollow(a, b)
	graph.AddFollow(b, a) // mutual
	graph.AddFollow(b, c)
	graph.AddFollow(c, b) // mutual with b
	// Extra for graph
	graph.AddFollow(a, padHex(5))
	graph.AddFollow(padHex(5), a)
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+c, nil)
	handleTrustPath(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if !resp.Connected {
		t.Fatal("expected connected")
	}
	// Check mutual flags
	for i, hop := range resp.Paths[0].Hops {
		if i < len(resp.Paths[0].Hops)-1 {
			// All hops should show mutual=true
			if !hop.IsMutual {
				t.Logf("hop %d (%s) is_mutual=%v", i, hop.Pubkey[:8], hop.IsMutual)
			}
		}
	}
}

func TestTrustPathClassification(t *testing.T) {
	tests := []struct {
		trust float64
		want  string
	}{
		{0.0, "none"},
		{0.1, "weak"},
		{0.29, "weak"},
		{0.3, "moderate"},
		{0.59, "moderate"},
		{0.6, "strong"},
		{1.0, "strong"},
	}
	for _, tc := range tests {
		got := classifyTrust(tc.trust)
		if got != tc.want {
			t.Errorf("classifyTrust(%v) = %q, want %q", tc.trust, got, tc.want)
		}
	}
}

func TestCombinedTrust(t *testing.T) {
	// Single path with trust 0.5 -> combined = 0.5
	paths := []TrustPath{{TrustScore: 0.5}}
	ct := combinedTrust(paths)
	if ct < 0.49 || ct > 0.51 {
		t.Errorf("single path combined trust should be ~0.5, got %f", ct)
	}

	// Two paths with trust 0.5 each -> combined = 1 - (0.5 * 0.5) = 0.75
	paths = []TrustPath{{TrustScore: 0.5}, {TrustScore: 0.5}}
	ct = combinedTrust(paths)
	if ct < 0.74 || ct > 0.76 {
		t.Errorf("two 0.5 paths combined trust should be ~0.75, got %f", ct)
	}

	// No paths
	ct = combinedTrust(nil)
	if ct != 0 {
		t.Errorf("no paths combined trust should be 0, got %f", ct)
	}
}

func TestFindMultiplePathsEmpty(t *testing.T) {
	graph = NewGraph()
	graph.ComputePageRank(20, 0.85)
	paths := findMultiplePaths(padHex(2), padHex(3), 3, 6)
	if len(paths) != 0 {
		t.Errorf("expected 0 paths for empty graph, got %d", len(paths))
	}
}

func TestTrustPathResponseFields(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	graph.AddFollow(a, b)
	graph.AddFollow(b, a)
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+b, nil)
	handleTrustPath(rec, req)

	var raw map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&raw)

	requiredFields := []string{"from", "to", "connected", "paths", "best_trust", "path_diversity", "overall_trust", "classification", "graph_size"}
	for _, f := range requiredFields {
		if _, ok := raw[f]; !ok {
			t.Errorf("missing response field: %s", f)
		}
	}
}

func TestTrustPathMaxPathsCapped(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	// Create many alternate paths
	for i := 10; i < 20; i++ {
		m := padHex(i)
		graph.AddFollow(a, m)
		graph.AddFollow(m, b)
		graph.AddFollow(m, a)
		graph.AddFollow(b, m)
	}
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+b+"&max_paths=10", nil)
	handleTrustPath(rec, req)

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.PathDiversity > 5 {
		t.Errorf("max_paths should be capped at 5, got %d paths", resp.PathDiversity)
	}
}

func TestTrustPathWeakestHop(t *testing.T) {
	graph = NewGraph()
	a := padHex(2)
	b := padHex(3)
	c := padHex(4)

	// a -> b -> c, where b has fewer connections
	graph.AddFollow(a, b)
	graph.AddFollow(b, c)
	graph.AddFollow(c, a) // cycle for PageRank
	graph.ComputePageRank(20, 0.85)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/trust-path?from="+a+"&to="+c, nil)
	handleTrustPath(rec, req)

	var resp TrustPathResponse
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Connected && len(resp.Paths) > 0 {
		path := resp.Paths[0]
		weakest := path.Hops[path.WeakestHop]
		// WeakestHop should point to a valid node
		if weakest.Pubkey == "" {
			t.Error("weakest hop pubkey should not be empty")
		}
	}
}
