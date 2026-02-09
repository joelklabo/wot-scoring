package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildInfluenceTestGraph() *Graph {
	g := NewGraph()

	// Create a small but connected graph.
	// Hub node 'h' has many followers -> high PageRank.
	h := padHex(500)
	// Followers of h
	for i := 510; i <= 520; i++ {
		pk := padHex(i)
		g.AddFollow(pk, h)
		g.AddFollow(h, pk)
	}

	// Secondary node 's' with fewer connections
	s := padHex(501)
	for i := 530; i <= 534; i++ {
		pk := padHex(i)
		g.AddFollow(pk, s)
		g.AddFollow(s, pk)
	}

	// Connect s to h's neighborhood
	g.AddFollow(s, padHex(510))
	g.AddFollow(padHex(510), s)

	// Isolated node 'iso'
	iso := padHex(502)
	g.AddFollow(iso, padHex(540))
	g.AddFollow(padHex(540), iso)

	g.ComputePageRank(20, 0.85)
	return g
}

func withInfluenceTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildInfluenceTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func TestInfluence_MissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/influence?other=abc", nil)
	rr := httptest.NewRecorder()
	handleInfluence(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluence_MissingOther(t *testing.T) {
	pk := padHex(500)
	req := httptest.NewRequest("GET", "/influence?pubkey="+pk, nil)
	rr := httptest.NewRecorder()
	handleInfluence(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluence_SamePubkeys(t *testing.T) {
	pk := padHex(500)
	req := httptest.NewRequest("GET", "/influence?pubkey="+pk+"&other="+pk, nil)
	rr := httptest.NewRecorder()
	handleInfluence(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluence_InvalidAction(t *testing.T) {
	pk := padHex(500)
	other := padHex(502)
	req := httptest.NewRequest("GET", "/influence?pubkey="+pk+"&other="+other+"&action=delete", nil)
	rr := httptest.NewRecorder()
	handleInfluence(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid action, got %d", rr.Code)
	}
}

func TestInfluence_FollowAction(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		// Simulate iso following h
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp InfluenceResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if resp.Action != "follow" {
			t.Errorf("expected action=follow, got %s", resp.Action)
		}
		if resp.Pubkey != h {
			t.Errorf("expected pubkey=%s, got %s", h, resp.Pubkey)
		}
		if resp.Other != iso {
			t.Errorf("expected other=%s, got %s", iso, resp.Other)
		}
	})
}

func TestInfluence_DefaultAction(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		// No action param defaults to follow
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso, nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Action != "follow" {
			t.Errorf("expected default action=follow, got %s", resp.Action)
		}
	})
}

func TestInfluence_UnfollowAction(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		follower := padHex(510) // already follows h
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+follower+"&action=unfollow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Action != "unfollow" {
			t.Errorf("expected action=unfollow, got %s", resp.Action)
		}
	})
}

func TestInfluence_AffectedCountNonNegative(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.AffectedCount < 0 {
			t.Errorf("affected_count should be >= 0, got %d", resp.AffectedCount)
		}
	})
}

func TestInfluence_TopAffectedCapped(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.TopAffected) > 20 {
			t.Errorf("top_affected should be capped at 20, got %d", len(resp.TopAffected))
		}
	})
}

func TestInfluence_ResponseFields(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var raw map[string]interface{}
		json.NewDecoder(rr.Body).Decode(&raw)

		requiredFields := []string{
			"pubkey", "action", "other", "current_score",
			"simulated_score", "score_delta", "affected_count",
			"max_delta", "top_affected", "summary", "graph_size",
		}
		for _, f := range requiredFields {
			if _, ok := raw[f]; !ok {
				t.Errorf("missing required field: %s", f)
			}
		}
	})
}

func TestInfluence_SummaryFields(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		validRadii := map[string]bool{
			"none": true, "local": true, "moderate": true, "wide": true, "global": true,
		}
		if !validRadii[resp.Summary.InfluenceRadius] {
			t.Errorf("invalid influence_radius: %s", resp.Summary.InfluenceRadius)
		}

		validClassifications := map[string]bool{
			"negligible": true, "minimal": true, "moderate": true, "significant": true, "transformative": true,
		}
		if !validClassifications[resp.Summary.Classification] {
			t.Errorf("invalid classification: %s", resp.Summary.Classification)
		}
	})
}

func TestInfluence_GraphSizeNonZero(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso, nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.GraphSize == 0 {
			t.Error("graph_size should be non-zero")
		}
	})
}

func TestInfluence_FollowIncreasesScore(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502) // isolated, adding its follow to h should increase h's score
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// h gains a new follower, score should increase or stay same
		if resp.SimulatedScore < resp.CurrentScore {
			t.Errorf("expected follow to increase score, current=%d simulated=%d",
				resp.CurrentScore, resp.SimulatedScore)
		}
	})
}

func TestInfluence_DirectionField(t *testing.T) {
	withInfluenceTestGraph(t, func() {
		h := padHex(500)
		iso := padHex(502)
		req := httptest.NewRequest("GET", "/influence?pubkey="+h+"&other="+iso+"&action=follow", nil)
		rr := httptest.NewRecorder()
		handleInfluence(rr, req)

		var resp InfluenceResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for _, a := range resp.TopAffected {
			if a.Direction != "increase" && a.Direction != "decrease" {
				t.Errorf("invalid direction: %s for pubkey %s", a.Direction, a.Pubkey)
			}
		}
	})
}

func TestClassifyRadius(t *testing.T) {
	tests := []struct {
		affected int
		total    int
		want     string
	}{
		{0, 100, "none"},
		{1, 100, "moderate"},    // 1% = moderate threshold
		{5, 100, "moderate"},    // 5% = moderate
		{10, 100, "wide"},       // 10% = wide threshold
		{50, 100, "global"},     // 50% = global threshold
		{100, 100, "global"},
		{0, 0, "none"},
	}
	for _, tc := range tests {
		got := classifyRadius(tc.affected, tc.total)
		if got != tc.want {
			t.Errorf("classifyRadius(%d, %d) = %s, want %s", tc.affected, tc.total, got, tc.want)
		}
	}
}

func TestClassifyInfluence(t *testing.T) {
	tests := []struct {
		affected int
		maxDelta float64
		total    int
		want     string
	}{
		{0, 0, 100, "negligible"},
		{1, 1e-8, 100, "moderate"},      // 1% ratio = moderate
		{5, 1e-7, 100, "moderate"},      // 5% ratio, low delta
		{10, 1e-6, 100, "significant"},  // 10% ratio + delta > 1e-7 = significant
		{30, 1e-6, 100, "significant"},  // 30% ratio, moderate delta
		{50, 1e-5, 100, "transformative"}, // 50% ratio, high delta
	}
	for _, tc := range tests {
		got := classifyInfluence(tc.affected, tc.maxDelta, tc.total)
		if got != tc.want {
			t.Errorf("classifyInfluence(%d, %e, %d) = %s, want %s",
				tc.affected, tc.maxDelta, tc.total, got, tc.want)
		}
	}
}

func TestComputePageRankOnSnapshot(t *testing.T) {
	follows := map[string][]string{
		"a": {"b", "c"},
		"b": {"c"},
		"c": {"a"},
	}
	followers := map[string][]string{
		"a": {"c"},
		"b": {"a"},
		"c": {"a", "b"},
	}

	scores := computePageRankOnSnapshot(follows, followers, 20, 0.85)

	// All three nodes should have positive scores
	for _, node := range []string{"a", "b", "c"} {
		if scores[node] <= 0 {
			t.Errorf("expected positive score for %s, got %f", node, scores[node])
		}
	}

	// Scores should sum to ~1.0
	total := 0.0
	for _, s := range scores {
		total += s
	}
	if math.Abs(total-1.0) > 0.01 {
		t.Errorf("PageRank scores should sum to ~1.0, got %f", total)
	}
}

func TestComputePageRankOnSnapshot_Empty(t *testing.T) {
	scores := computePageRankOnSnapshot(
		map[string][]string{},
		map[string][]string{},
		20, 0.85,
	)
	if len(scores) != 0 {
		t.Errorf("expected empty scores for empty graph, got %d entries", len(scores))
	}
}

func TestRemoveFromSlice(t *testing.T) {
	tests := []struct {
		input []string
		val   string
		want  int
	}{
		{[]string{"a", "b", "c"}, "b", 2},
		{[]string{"a", "b", "c"}, "d", 3},
		{[]string{"a"}, "a", 0},
		{[]string{}, "a", 0},
	}
	for _, tc := range tests {
		got := removeFromSlice(tc.input, tc.val)
		if len(got) != tc.want {
			t.Errorf("removeFromSlice(%v, %s) length = %d, want %d", tc.input, tc.val, len(got), tc.want)
		}
		for _, v := range got {
			if v == tc.val {
				t.Errorf("removeFromSlice should have removed %s", tc.val)
			}
		}
	}
}
