package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildFollowQualityTestGraph creates a graph with varied follow quality.
// Structure:
//   - user (fqPad(100)): follows highTrust, medTrust, lowTrust, ghost, mutual1, mutual2
//   - highTrust (fqPad(101)): high PageRank (many followers), follows user back
//   - medTrust (fqPad(102)): moderate PageRank, follows user back
//   - lowTrust (fqPad(103)): low PageRank, does NOT follow user back
//   - ghost (fqPad(104)): no followers, no outgoing follows except from user
//   - mutual1 (fqPad(105)): follows user back, moderate score
//   - mutual2 (fqPad(106)): follows user back, high score
//   - hub (fqPad(600)): followed by 100 nodes, gives highTrust its PageRank
func buildFollowQualityTestGraph() *Graph {
	g := NewGraph()

	user := fqPad(100)
	highTrust := fqPad(101)
	medTrust := fqPad(102)
	lowTrust := fqPad(103)
	ghost := fqPad(104)
	mutual1 := fqPad(105)
	mutual2 := fqPad(106)
	hub := fqPad(600)

	// User follows 6 accounts
	g.AddFollow(user, highTrust)
	g.AddFollow(user, medTrust)
	g.AddFollow(user, lowTrust)
	g.AddFollow(user, ghost)
	g.AddFollow(user, mutual1)
	g.AddFollow(user, mutual2)

	// Reciprocal follows (4 out of 6 follow back)
	g.AddFollow(highTrust, user)
	g.AddFollow(medTrust, user)
	g.AddFollow(mutual1, user)
	g.AddFollow(mutual2, user)

	// Give highTrust many followers for high PageRank
	for i := 200; i < 250; i++ {
		g.AddFollow(fqPad(i), highTrust)
		g.AddFollow(fqPad(i), fqPad(i+1)) // chain
	}

	// Give medTrust some followers
	for i := 300; i < 320; i++ {
		g.AddFollow(fqPad(i), medTrust)
		g.AddFollow(fqPad(i), fqPad(i+1))
	}

	// Give mutual1 some followers
	for i := 400; i < 415; i++ {
		g.AddFollow(fqPad(i), mutual1)
		g.AddFollow(fqPad(i), fqPad(i+1))
	}

	// Give mutual2 many followers
	for i := 500; i < 545; i++ {
		g.AddFollow(fqPad(i), mutual2)
		g.AddFollow(fqPad(i), fqPad(i+1))
	}

	// lowTrust: only user follows them + a couple others
	g.AddFollow(fqPad(700), lowTrust)
	g.AddFollow(fqPad(701), lowTrust)

	// ghost: only user follows them, they follow nobody
	// (no extra edges)

	// Hub for graph structure
	g.AddFollow(hub, highTrust)
	for i := 800; i < 900; i++ {
		g.AddFollow(fqPad(i), hub)
		g.AddFollow(fqPad(i), fqPad(i+1))
	}

	g.ComputePageRank(20, 0.85)
	return g
}

// fqPad generates a 64-char hex pubkey from an integer.
func fqPad(n int) string {
	s := make([]byte, 64)
	for i := range s {
		s[i] = '0'
	}
	digits := "0123456789abcdef"
	pos := 63
	val := n
	if val == 0 {
		s[pos] = '1'
	}
	for val > 0 && pos >= 0 {
		s[pos] = digits[val%16]
		val /= 16
		pos--
	}
	return string(s)
}

func withFollowQualityTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildFollowQualityTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func getFollowQuality(pubkey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/follow-quality?pubkey="+pubkey, nil)
	rr := httptest.NewRecorder()
	handleFollowQuality(rr, req)
	return rr
}

func getFollowQualityWithSuggestions(pubkey string, n int) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", fmt.Sprintf("/follow-quality?pubkey=%s&suggestions=%d", pubkey, n), nil)
	rr := httptest.NewRecorder()
	handleFollowQuality(rr, req)
	return rr
}

// --- Tests ---

func TestFollowQualityMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/follow-quality", nil)
	rr := httptest.NewRecorder()
	handleFollowQuality(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestFollowQualityInvalidPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/follow-quality?pubkey=npub1invalid", nil)
	rr := httptest.NewRecorder()
	handleFollowQuality(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestFollowQualityNoFollows(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		// ghost has no outgoing follows
		rr := getFollowQuality(fqPad(104))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp.FollowCount != 0 {
			t.Errorf("expected 0 follows, got %d", resp.FollowCount)
		}
		if resp.Classification != "insufficient_data" {
			t.Errorf("expected insufficient_data, got %s", resp.Classification)
		}
	})
}

func TestFollowQualityBasicResponse(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Pubkey != fqPad(100) {
			t.Errorf("expected pubkey %s, got %s", fqPad(100), resp.Pubkey)
		}
		if resp.FollowCount != 6 {
			t.Errorf("expected 6 follows, got %d", resp.FollowCount)
		}
		if resp.GraphSize == 0 {
			t.Error("expected non-zero graph size")
		}
	})
}

func TestFollowQualityScoreBounded(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.QualityScore < 0 || resp.QualityScore > 100 {
			t.Errorf("quality score %d out of bounds [0,100]", resp.QualityScore)
		}
	})
}

func TestFollowQualityClassificationValues(t *testing.T) {
	valid := map[string]bool{
		"excellent":         true,
		"good":              true,
		"moderate":          true,
		"poor":              true,
		"insufficient_data": true,
	}
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if !valid[resp.Classification] {
			t.Errorf("unexpected classification: %s", resp.Classification)
		}
	})
}

func TestFollowQualityCategoriesSum(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		sum := resp.Categories.Strong + resp.Categories.Moderate + resp.Categories.Weak + resp.Categories.Unknown
		if sum != resp.FollowCount {
			t.Errorf("categories sum %d != follow count %d", sum, resp.FollowCount)
		}
	})
}

func TestFollowQualityReciprocity(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// 4 out of 6 follow back
		expected := 4.0 / 6.0
		if diff := resp.Breakdown.Reciprocity - expected; diff < -0.01 || diff > 0.01 {
			t.Errorf("expected reciprocity ~%.3f, got %.3f", expected, resp.Breakdown.Reciprocity)
		}
	})
}

func TestFollowQualityBreakdownBounds(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		b := resp.Breakdown
		if b.Diversity < 0 || b.Diversity > 1 {
			t.Errorf("diversity %.3f out of bounds [0,1]", b.Diversity)
		}
		if b.Reciprocity < 0 || b.Reciprocity > 1 {
			t.Errorf("reciprocity %.3f out of bounds [0,1]", b.Reciprocity)
		}
		if b.SignalRatio < 0 || b.SignalRatio > 1 {
			t.Errorf("signal ratio %.3f out of bounds [0,1]", b.SignalRatio)
		}
		if b.AvgTrustScore < 0 {
			t.Errorf("avg trust score %.1f should be non-negative", b.AvgTrustScore)
		}
		if b.MedianTrustScore < 0 {
			t.Errorf("median trust score %d should be non-negative", b.MedianTrustScore)
		}
	})
}

func TestFollowQualitySignalRatio(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// At least some follows should have scores (highTrust, medTrust, etc.)
		if resp.Breakdown.SignalRatio == 0 {
			t.Error("expected non-zero signal ratio — some follows have PageRank scores")
		}
	})
}

func TestFollowQualitySuggestionsPresent(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// ghost (fqPad(104)) should appear in suggestions (zero/low trust, no follow back)
		if resp.Suggestions == nil {
			t.Fatal("expected non-nil suggestions")
		}
		// Suggestions should have pubkey and reason
		for _, s := range resp.Suggestions {
			if s.Pubkey == "" {
				t.Error("suggestion has empty pubkey")
			}
			if s.Reason == "" {
				t.Error("suggestion has empty reason")
			}
		}
	})
}

func TestFollowQualitySuggestionsLimit(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQualityWithSuggestions(fqPad(100), 2)
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Suggestions) > 2 {
			t.Errorf("expected at most 2 suggestions, got %d", len(resp.Suggestions))
		}
	})
}

func TestFollowQualityZeroSuggestions(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQualityWithSuggestions(fqPad(100), 0)
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Suggestions) != 0 {
			t.Errorf("expected 0 suggestions, got %d", len(resp.Suggestions))
		}
	})
}

func TestFollowQualitySuggestionsOrderedByScore(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for i := 1; i < len(resp.Suggestions); i++ {
			if resp.Suggestions[i].TrustScore < resp.Suggestions[i-1].TrustScore {
				t.Errorf("suggestions not ordered: score[%d]=%d < score[%d]=%d",
					i, resp.Suggestions[i].TrustScore, i-1, resp.Suggestions[i-1].TrustScore)
			}
		}
	})
}

func TestFollowQualityDiversityPositive(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		// User follows high, med, low, and ghost — should have diversity > 0
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Breakdown.Diversity <= 0 {
			t.Errorf("expected positive diversity for varied follow list, got %.3f", resp.Breakdown.Diversity)
		}
	})
}

func TestFollowQualityAvgTrustPositive(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Breakdown.AvgTrustScore <= 0 {
			t.Errorf("expected positive avg trust for user following scored accounts, got %.1f", resp.Breakdown.AvgTrustScore)
		}
	})
}

func TestFollowQualityGhostInSuggestions(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		ghost := fqPad(104)
		found := false
		for _, s := range resp.Suggestions {
			if s.Pubkey == ghost {
				found = true
				break
			}
		}
		if !found {
			t.Error("expected ghost account (fqPad(104)) in suggestions — it has no followers and no outgoing follows")
		}
	})
}

func TestFollowQualityStrongFollowsCount(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		rr := getFollowQuality(fqPad(100))
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// highTrust and mutual2 have many followers, should have high scores
		// At minimum we expect some non-zero strong or moderate follows
		if resp.Categories.Strong+resp.Categories.Moderate == 0 {
			t.Error("expected at least some strong or moderate quality follows")
		}
	})
}

func TestFollowQualityUnknownPubkey(t *testing.T) {
	withFollowQualityTestGraph(t, func() {
		// A pubkey not in the graph should return insufficient_data
		rr := getFollowQuality(fqPad(9999))
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}
		var resp FollowQualityResponse
		json.NewDecoder(rr.Body).Decode(&resp)
		if resp.Classification != "insufficient_data" {
			t.Errorf("expected insufficient_data for unknown pubkey, got %s", resp.Classification)
		}
	})
}

func TestFollowQualityClassifyFunction(t *testing.T) {
	tests := []struct {
		score    int
		expected string
	}{
		{100, "excellent"},
		{75, "excellent"},
		{74, "good"},
		{50, "good"},
		{49, "moderate"},
		{25, "moderate"},
		{24, "poor"},
		{0, "poor"},
	}
	for _, tt := range tests {
		got := classifyFollowQuality(tt.score)
		if got != tt.expected {
			t.Errorf("classifyFollowQuality(%d) = %s, want %s", tt.score, got, tt.expected)
		}
	}
}
