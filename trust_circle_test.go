package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildTrustCircleTestGraph creates a graph with known mutual relationships.
// Structure:
//   - center (padHex(100)): follows alice, bob, carol, dave, eve + they follow back (mutuals)
//   - alice (padHex(101)): follows center, bob (mutual with both)
//   - bob (padHex(102)): follows center, alice, carol
//   - carol (padHex(103)): follows center, bob
//   - dave (padHex(104)): follows center
//   - eve (padHex(105)): follows center, alice, bob, carol, dave
//   - frank (padHex(106)): follows center but center does NOT follow frank (not mutual)
//   - hub (padHex(600)): followed by many (100 nodes), follows center
func buildTrustCircleTestGraph() *Graph {
	g := NewGraph()

	center := padHex(100)
	alice := padHex(101)
	bob := padHex(102)
	carol := padHex(103)
	dave := padHex(104)
	eve := padHex(105)
	frank := padHex(106)
	hub := padHex(600)

	// Center follows alice, bob, carol, dave, eve (5 mutuals)
	g.AddFollow(center, alice)
	g.AddFollow(center, bob)
	g.AddFollow(center, carol)
	g.AddFollow(center, dave)
	g.AddFollow(center, eve)

	// They all follow center back (mutuals)
	g.AddFollow(alice, center)
	g.AddFollow(bob, center)
	g.AddFollow(carol, center)
	g.AddFollow(dave, center)
	g.AddFollow(eve, center)

	// Intra-circle connections
	g.AddFollow(alice, bob)
	g.AddFollow(bob, alice) // alice-bob mutual
	g.AddFollow(bob, carol)
	g.AddFollow(carol, bob) // bob-carol mutual
	g.AddFollow(eve, alice)
	g.AddFollow(eve, bob)
	g.AddFollow(eve, carol)
	g.AddFollow(eve, dave)

	// Frank follows center but center doesn't follow frank
	g.AddFollow(frank, center)

	// Hub: many followers
	g.AddFollow(hub, center)
	for i := 200; i < 300; i++ {
		g.AddFollow(padHex(i), hub)
		g.AddFollow(padHex(i), padHex(i+1)) // chain for non-trivial graph
	}

	g.ComputePageRank(20, 0.85)
	return g
}

func withTrustCircleTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildTrustCircleTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func getTrustCircle(pubkey string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/trust-circle?pubkey="+pubkey, nil)
	rr := httptest.NewRecorder()
	handleTrustCircle(rr, req)
	return rr
}

func TestTrustCircle_MissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle", nil)
	rr := httptest.NewRecorder()
	handleTrustCircle(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestTrustCircle_InvalidPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/trust-circle?pubkey=npub1invalid", nil)
	rr := httptest.NewRecorder()
	handleTrustCircle(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}

func TestTrustCircle_UnknownPubkey(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		unknown := padHex(999)
		rr := getTrustCircle(unknown)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.CircleSize != 0 {
			t.Fatalf("expected circle_size 0 for unknown pubkey, got %d", resp.CircleSize)
		}
		if len(resp.Members) != 0 {
			t.Fatalf("expected 0 members, got %d", len(resp.Members))
		}
	})
}

func TestTrustCircle_BasicCircle(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Center has 5 mutual follows: alice, bob, carol, dave, eve
		if resp.CircleSize != 5 {
			t.Fatalf("expected circle_size 5, got %d", resp.CircleSize)
		}
		if len(resp.Members) != 5 {
			t.Fatalf("expected 5 members, got %d", len(resp.Members))
		}
	})
}

func TestTrustCircle_ExcludesNonMutual(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Frank follows center but center doesn't follow frank — NOT in circle
		frank := padHex(106)
		for _, m := range resp.Members {
			if m.Pubkey == frank {
				t.Fatalf("frank should not be in trust circle (not mutual)")
			}
		}
	})
}

func TestTrustCircle_MembersSortedByScore(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for i := 1; i < len(resp.Members); i++ {
			if resp.Members[i].TrustScore > resp.Members[i-1].TrustScore {
				t.Fatalf("members not sorted by trust_score descending: %d > %d at index %d",
					resp.Members[i].TrustScore, resp.Members[i-1].TrustScore, i)
			}
		}
	})
}

func TestTrustCircle_InnerCircle(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Inner circle should be <= 10 and <= circle size
		if len(resp.InnerCircle) > 10 {
			t.Fatalf("inner circle should be at most 10, got %d", len(resp.InnerCircle))
		}
		if len(resp.InnerCircle) > resp.CircleSize {
			t.Fatalf("inner circle (%d) should not exceed circle size (%d)", len(resp.InnerCircle), resp.CircleSize)
		}
		// With 5 members, inner circle should be all 5
		if len(resp.InnerCircle) != 5 {
			t.Fatalf("expected inner_circle of 5 (all members), got %d", len(resp.InnerCircle))
		}
	})
}

func TestTrustCircle_MemberFields(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for _, m := range resp.Members {
			if m.Pubkey == "" {
				t.Fatal("member pubkey should not be empty")
			}
			if m.Classification == "" {
				t.Fatal("member classification should not be empty")
			}
			if m.MutualStrength < 0 || m.MutualStrength > 1 {
				t.Fatalf("mutual_strength should be 0-1, got %f for %s", m.MutualStrength, m.Pubkey)
			}
			if m.SharedFollows < 0 {
				t.Fatalf("shared_follows should be >= 0, got %d for %s", m.SharedFollows, m.Pubkey)
			}
		}
	})
}

func TestTrustCircle_SharedFollows(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Eve follows center, alice, bob, carol, dave — should share follows with center
		eve := padHex(105)
		for _, m := range resp.Members {
			if m.Pubkey == eve {
				// Eve follows alice, bob, carol, dave (all of which center also follows)
				// Center follows alice, bob, carol, dave, eve. Eve follows center, alice, bob, carol, dave.
				// Shared: alice, bob, carol, dave = 4
				if m.SharedFollows < 3 {
					t.Fatalf("eve should share at least 3 follows with center, got %d", m.SharedFollows)
				}
				return
			}
		}
		t.Fatal("eve not found in members")
	})
}

func TestTrustCircle_Metrics(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		m := resp.Metrics
		if m.AvgTrustScore < 0 {
			t.Fatalf("avg_trust_score should be >= 0, got %f", m.AvgTrustScore)
		}
		if m.MedianTrust < 0 {
			t.Fatalf("median_trust should be >= 0, got %d", m.MedianTrust)
		}
		if m.Cohesion < 0 || m.Cohesion > 1 {
			t.Fatalf("cohesion should be 0-1, got %f", m.Cohesion)
		}
		if m.Density < 0 || m.Density > 1 {
			t.Fatalf("density should be 0-1, got %f", m.Density)
		}
		if m.TopRole == "" {
			t.Fatal("top_role should not be empty")
		}
		if len(m.RoleCounts) == 0 {
			t.Fatal("role_counts should not be empty")
		}
	})
}

func TestTrustCircle_CohesionPositive(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// There are intra-circle mutuals (alice-bob, bob-carol), so cohesion > 0
		if resp.Metrics.Cohesion <= 0 {
			t.Fatalf("cohesion should be > 0 (intra-circle mutuals exist), got %f", resp.Metrics.Cohesion)
		}
	})
}

func TestTrustCircle_DensityPositive(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// There are intra-circle edges, so density > 0
		if resp.Metrics.Density <= 0 {
			t.Fatalf("density should be > 0 (intra-circle edges exist), got %f", resp.Metrics.Density)
		}
	})
}

func TestTrustCircle_EmptyCircle(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		// Frank follows center but center doesn't follow frank. Frank has 0 mutuals.
		frank := padHex(106)
		rr := getTrustCircle(frank)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.CircleSize != 0 {
			t.Fatalf("expected circle_size 0 for frank (no mutuals), got %d", resp.CircleSize)
		}
		if len(resp.InnerCircle) != 0 {
			t.Fatalf("expected empty inner_circle, got %d", len(resp.InnerCircle))
		}
		if resp.Metrics.Cohesion != 0 {
			t.Fatalf("expected cohesion 0 for empty circle, got %f", resp.Metrics.Cohesion)
		}
	})
}

func TestTrustCircle_ResponseHasGraphSize(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.GraphSize == 0 {
			t.Fatal("graph_size should be > 0")
		}
	})
}

func TestTrustCircle_SelfScore(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Pubkey != center {
			t.Fatalf("expected pubkey %s, got %s", center, resp.Pubkey)
		}
		// Center should have a positive trust score (it's well-connected)
		if resp.TrustScore <= 0 {
			t.Fatalf("expected positive trust_score for center, got %d", resp.TrustScore)
		}
	})
}

func TestTrustCircle_RoleCounts(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Sum of role counts should equal circle size
		totalRoles := 0
		for _, count := range resp.Metrics.RoleCounts {
			totalRoles += count
		}
		if totalRoles != resp.CircleSize {
			t.Fatalf("sum of role_counts (%d) should equal circle_size (%d)", totalRoles, resp.CircleSize)
		}
	})
}

func TestTrustCircle_MutualStrengthBounded(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		center := padHex(100)
		rr := getTrustCircle(center)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for _, m := range resp.Members {
			if m.MutualStrength > 1.0 {
				t.Fatalf("mutual_strength should be <= 1.0, got %f for %s", m.MutualStrength, m.Pubkey)
			}
		}
	})
}

func TestTrustCircle_AliceCircle(t *testing.T) {
	withTrustCircleTestGraph(t, func() {
		// Alice follows center and bob; center and bob follow alice back
		alice := padHex(101)
		rr := getTrustCircle(alice)

		var resp TrustCircleResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		// Alice's mutuals: center (mutual) and bob (mutual)
		if resp.CircleSize != 2 {
			t.Fatalf("expected alice circle_size 2 (center, bob), got %d", resp.CircleSize)
		}
	})
}
