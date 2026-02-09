package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildCompareTestGraph creates a graph with two centers that have overlapping trust circles.
// Structure:
//   - user1 (padHex(300)): follows alice, bob, carol, dave — they follow back (4 mutuals)
//   - user2 (padHex(301)): follows alice, bob, eve, frank — they follow back (4 mutuals)
//   - Overlap: alice (padHex(302)), bob (padHex(303)) — in both circles
//   - Unique to user1: carol (padHex(304)), dave (padHex(305))
//   - Unique to user2: eve (padHex(306)), frank (padHex(307))
//   - ghost (padHex(308)): no connections
//   - Additional edges for non-trivial PageRank
func buildCompareTestGraph() *Graph {
	g := NewGraph()

	user1 := padHex(300)
	user2 := padHex(301)
	alice := padHex(302)
	bob := padHex(303)
	carol := padHex(304)
	dave := padHex(305)
	eve := padHex(306)
	frank := padHex(307)

	// User1's circle: alice, bob, carol, dave
	g.AddFollow(user1, alice)
	g.AddFollow(user1, bob)
	g.AddFollow(user1, carol)
	g.AddFollow(user1, dave)
	g.AddFollow(alice, user1)
	g.AddFollow(bob, user1)
	g.AddFollow(carol, user1)
	g.AddFollow(dave, user1)

	// User2's circle: alice, bob, eve, frank
	g.AddFollow(user2, alice)
	g.AddFollow(user2, bob)
	g.AddFollow(user2, eve)
	g.AddFollow(user2, frank)
	g.AddFollow(alice, user2)
	g.AddFollow(bob, user2)
	g.AddFollow(eve, user2)
	g.AddFollow(frank, user2)

	// Shared follows (alice follows bob, both users follow alice and bob)
	g.AddFollow(alice, bob)
	g.AddFollow(bob, alice)

	// Some additional edges for graph texture
	g.AddFollow(carol, alice)
	g.AddFollow(dave, bob)
	g.AddFollow(eve, alice)
	g.AddFollow(frank, bob)

	// Chain for non-trivial PageRank
	for i := 400; i < 450; i++ {
		g.AddFollow(padHex(i), padHex(i+1))
	}

	g.ComputePageRank(20, 0.85)
	return g
}

func withCompareTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildCompareTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func getCompare(pubkey1, pubkey2 string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/trust-circle/compare?pubkey1="+pubkey1+"&pubkey2="+pubkey2, nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	return rr
}

func TestCompare_MissingPubkeys(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle/compare", nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCompare_MissingPubkey2(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle/compare?pubkey1="+padHex(300), nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCompare_InvalidPubkey1(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle/compare?pubkey1=npub1invalid&pubkey2="+padHex(301), nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCompare_InvalidPubkey2(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle/compare?pubkey1="+padHex(300)+"&pubkey2=npub1invalid", nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestCompare_SamePubkey(t *testing.T) {
	pk := padHex(300)
	req := httptest.NewRequest("GET", "/trust-circle/compare?pubkey1="+pk+"&pubkey2="+pk, nil)
	rr := httptest.NewRecorder()
	handleTrustCircleCompare(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for same pubkey, got %d", rr.Code)
	}
}

func TestCompare_BasicResponse(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Pubkey1 != user1 {
			t.Fatalf("expected pubkey1 %s, got %s", user1, resp.Pubkey1)
		}
		if resp.Pubkey2 != user2 {
			t.Fatalf("expected pubkey2 %s, got %s", user2, resp.Pubkey2)
		}
		if resp.GraphSize == 0 {
			t.Fatal("graph_size should be > 0")
		}
	})
}

func TestCompare_CircleSizes(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// User1 has 4 mutuals: alice, bob, carol, dave
		if resp.CircleSize1 != 4 {
			t.Fatalf("expected circle_size_1 = 4, got %d", resp.CircleSize1)
		}
		// User2 has 4 mutuals: alice, bob, eve, frank
		if resp.CircleSize2 != 4 {
			t.Fatalf("expected circle_size_2 = 4, got %d", resp.CircleSize2)
		}
	})
}

func TestCompare_OverlapMembers(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Overlap should be alice and bob
		if len(resp.Overlap) != 2 {
			t.Fatalf("expected 2 overlap members, got %d", len(resp.Overlap))
		}

		overlapSet := make(map[string]bool)
		for _, m := range resp.Overlap {
			overlapSet[m.Pubkey] = true
		}
		alice := padHex(302)
		bob := padHex(303)
		if !overlapSet[alice] {
			t.Fatal("alice should be in overlap")
		}
		if !overlapSet[bob] {
			t.Fatal("bob should be in overlap")
		}
	})
}

func TestCompare_UniqueMembers(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Unique to user1: carol, dave
		if len(resp.Unique1) != 2 {
			t.Fatalf("expected 2 unique_to_1 members, got %d", len(resp.Unique1))
		}
		// Unique to user2: eve, frank
		if len(resp.Unique2) != 2 {
			t.Fatalf("expected 2 unique_to_2 members, got %d", len(resp.Unique2))
		}

		carol := padHex(304)
		dave := padHex(305)
		eve := padHex(306)
		frank := padHex(307)

		unique1Set := make(map[string]bool)
		for _, m := range resp.Unique1 {
			unique1Set[m.Pubkey] = true
		}
		if !unique1Set[carol] || !unique1Set[dave] {
			t.Fatal("carol and dave should be unique to user1")
		}

		unique2Set := make(map[string]bool)
		for _, m := range resp.Unique2 {
			unique2Set[m.Pubkey] = true
		}
		if !unique2Set[eve] || !unique2Set[frank] {
			t.Fatal("eve and frank should be unique to user2")
		}
	})
}

func TestCompare_CompatibilityScore(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		c := resp.Compatibility
		if c.Score < 0 || c.Score > 100 {
			t.Fatalf("compatibility score should be 0-100, got %d", c.Score)
		}
		// With 2/6 overlap (Jaccard = 2/6 = 0.333), should be moderate or low
		if c.Classification == "" {
			t.Fatal("classification should not be empty")
		}
	})
}

func TestCompare_OverlapCount(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Compatibility.OverlapCount != 2 {
			t.Fatalf("expected overlap_count 2, got %d", resp.Compatibility.OverlapCount)
		}
	})
}

func TestCompare_OverlapRatioBounded(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		r := resp.Compatibility.OverlapRatio
		if r < 0 || r > 1 {
			t.Fatalf("overlap_ratio should be 0-1, got %f", r)
		}
		// Jaccard = 2 / (4 + 4 - 2) = 2/6 ≈ 0.333
		if r < 0.3 || r > 0.4 {
			t.Fatalf("expected overlap_ratio ≈ 0.333, got %f", r)
		}
	})
}

func TestCompare_SharedFollows(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Both follow alice and bob = at least 2 shared follows
		if resp.Compatibility.SharedFollows < 2 {
			t.Fatalf("expected at least 2 shared_follows, got %d", resp.Compatibility.SharedFollows)
		}
	})
}

func TestCompare_SharedRatioBounded(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		r := resp.Compatibility.SharedRatio
		if r < 0 || r > 1 {
			t.Fatalf("shared_ratio should be 0-1, got %f", r)
		}
	})
}

func TestCompare_OverlapSortedByScore(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for i := 1; i < len(resp.Overlap); i++ {
			if resp.Overlap[i].TrustScore > resp.Overlap[i-1].TrustScore {
				t.Fatalf("overlap not sorted by trust_score descending")
			}
		}
	})
}

func TestCompare_OverlapMemberFields(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for _, m := range resp.Overlap {
			if m.Pubkey == "" {
				t.Fatal("overlap member pubkey should not be empty")
			}
			if m.Strength1 < 0 || m.Strength1 > 1 {
				t.Fatalf("strength_with_1 should be 0-1, got %f", m.Strength1)
			}
			if m.Strength2 < 0 || m.Strength2 > 1 {
				t.Fatalf("strength_with_2 should be 0-1, got %f", m.Strength2)
			}
		}
	})
}

func TestCompare_NoOverlap(t *testing.T) {
	withCompareTestGraph(t, func() {
		// carol (user1's unique) and eve (user2's unique) have no overlap
		carol := padHex(304)
		eve := padHex(306)
		rr := getCompare(carol, eve)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Compatibility.OverlapCount != 0 {
			t.Fatalf("expected 0 overlap between carol and eve, got %d", resp.Compatibility.OverlapCount)
		}
		if len(resp.Overlap) != 0 {
			t.Fatalf("expected empty overlap, got %d members", len(resp.Overlap))
		}
	})
}

func TestCompare_Classification(t *testing.T) {
	tests := []struct {
		score    int
		expected string
	}{
		{80, "high"},
		{60, "high"},
		{45, "moderate"},
		{30, "moderate"},
		{15, "low"},
		{10, "low"},
		{5, "none"},
		{0, "none"},
	}

	for _, tt := range tests {
		got := classifyCompatibility(tt.score)
		if got != tt.expected {
			t.Errorf("classifyCompatibility(%d) = %s, want %s", tt.score, got, tt.expected)
		}
	}
}

func TestCompare_TrustScores(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Both users are well-connected, should have positive scores
		if resp.Trust1 < 0 || resp.Trust1 > 100 {
			t.Fatalf("trust_score_1 should be 0-100, got %d", resp.Trust1)
		}
		if resp.Trust2 < 0 || resp.Trust2 > 100 {
			t.Fatalf("trust_score_2 should be 0-100, got %d", resp.Trust2)
		}
	})
}

func TestCompare_AvgOverlapWot(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		rr := getCompare(user1, user2)

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Compatibility.AvgOverlapWot < 0 || resp.Compatibility.AvgOverlapWot > 100 {
			t.Fatalf("avg_overlap_wot should be 0-100, got %f", resp.Compatibility.AvgOverlapWot)
		}
	})
}

func TestCompare_UnknownPubkeys(t *testing.T) {
	withCompareTestGraph(t, func() {
		unknown1 := padHex(998)
		unknown2 := padHex(999)
		rr := getCompare(unknown1, unknown2)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 for unknown pubkeys, got %d", rr.Code)
		}

		var resp CircleCompareResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.CircleSize1 != 0 || resp.CircleSize2 != 0 {
			t.Fatalf("expected 0 circle sizes for unknown pubkeys, got %d and %d", resp.CircleSize1, resp.CircleSize2)
		}
		if resp.Compatibility.OverlapCount != 0 {
			t.Fatalf("expected 0 overlap for unknown pubkeys, got %d", resp.Compatibility.OverlapCount)
		}
		if resp.Compatibility.Classification != "none" {
			t.Fatalf("expected 'none' classification for unknown pubkeys, got %s", resp.Compatibility.Classification)
		}
	})
}

func TestCompare_Symmetry(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)

		rr1 := getCompare(user1, user2)
		rr2 := getCompare(user2, user1)

		var resp1, resp2 CircleCompareResponse
		json.NewDecoder(rr1.Body).Decode(&resp1)
		json.NewDecoder(rr2.Body).Decode(&resp2)

		// Compatibility score should be the same regardless of order
		if resp1.Compatibility.Score != resp2.Compatibility.Score {
			t.Fatalf("compatibility score should be symmetric: %d vs %d",
				resp1.Compatibility.Score, resp2.Compatibility.Score)
		}
		if resp1.Compatibility.OverlapCount != resp2.Compatibility.OverlapCount {
			t.Fatalf("overlap_count should be symmetric: %d vs %d",
				resp1.Compatibility.OverlapCount, resp2.Compatibility.OverlapCount)
		}
	})
}

func TestCompare_GetMutualSet(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		circle := getMutualSet(user1)
		if len(circle) != 4 {
			t.Fatalf("expected 4 mutuals for user1, got %d", len(circle))
		}
	})
}

func TestCompare_CountSharedFollows(t *testing.T) {
	withCompareTestGraph(t, func() {
		user1 := padHex(300)
		user2 := padHex(301)
		shared := countSharedFollows(user1, user2)
		// Both follow alice and bob = 2 shared
		if shared < 2 {
			t.Fatalf("expected at least 2 shared follows, got %d", shared)
		}
	})
}

func TestCompare_ExcludesQueriedPubkeys(t *testing.T) {
	// Build a graph where user1 and user2 are mutual follows (so they appear in each other's circles)
	g := NewGraph()
	user1 := padHex(500)
	user2 := padHex(501)
	shared := padHex(502)

	// user1 <-> user2 (mutual)
	g.AddFollow(user1, user2)
	g.AddFollow(user2, user1)
	// user1 <-> shared (mutual)
	g.AddFollow(user1, shared)
	g.AddFollow(shared, user1)
	// user2 <-> shared (mutual)
	g.AddFollow(user2, shared)
	g.AddFollow(shared, user2)

	for i := 600; i < 620; i++ {
		g.AddFollow(padHex(i), padHex(i+1))
	}
	g.ComputePageRank(20, 0.85)

	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()

	rr := getCompare(user1, user2)
	var resp CircleCompareResponse
	json.NewDecoder(rr.Body).Decode(&resp)

	// user1's circle: user2, shared. user2's circle: user1, shared.
	// But overlap should exclude user1 and user2 themselves.
	// Only "shared" should be in overlap.
	if len(resp.Overlap) != 1 {
		t.Fatalf("expected 1 overlap member (excluding queried pubkeys), got %d", len(resp.Overlap))
	}
	if resp.Overlap[0].Pubkey != shared {
		t.Fatalf("expected shared pubkey in overlap, got %s", resp.Overlap[0].Pubkey)
	}
	// Unique lists should also exclude queried pubkeys
	for _, m := range resp.Unique1 {
		if m.Pubkey == user1 || m.Pubkey == user2 {
			t.Fatalf("queried pubkey %s should not appear in unique_to_1", m.Pubkey)
		}
	}
	for _, m := range resp.Unique2 {
		if m.Pubkey == user1 || m.Pubkey == user2 {
			t.Fatalf("queried pubkey %s should not appear in unique_to_2", m.Pubkey)
		}
	}
}

func TestCompare_MutualStrength(t *testing.T) {
	// Test the strength calculation
	s := mutualStrength(50, 50, 0)
	if s < 0.4 || s > 0.6 {
		t.Fatalf("expected ~0.5 strength for equal 50 scores, got %f", s)
	}

	s = mutualStrength(0, 50, 0)
	if s != 0 {
		t.Fatalf("expected 0 strength when one score is 0, got %f", s)
	}

	s = mutualStrength(50, 50, 100)
	if s <= mutualStrength(50, 50, 0) {
		t.Fatal("shared follows should boost mutual strength")
	}
}
