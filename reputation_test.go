package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func buildReputationTestGraph() (*Graph, *CommunityDetector) {
	g := NewGraph()

	// Create a well-connected hub with 20 followers
	hub := padHex(10)
	for i := 11; i <= 30; i++ {
		pk := padHex(i)
		g.AddFollow(pk, hub)  // 20 followers
		g.AddFollow(hub, pk)  // hub follows back
		// Give followers their own connections for substance
		for j := i + 20; j <= i+25; j++ {
			other := padHex(j)
			g.AddFollow(pk, other)
			g.AddFollow(other, pk)
		}
	}

	g.ComputePageRank(20, 0.85)

	cd := NewCommunityDetector()
	cd.DetectCommunities(g, 10)

	return g, cd
}

func withReputationTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g, cd := buildReputationTestGraph()
	oldGraph := graph
	oldCommunities := communities
	graph = g
	communities = cd
	defer func() {
		graph = oldGraph
		communities = oldCommunities
	}()
	fn()
}

func TestReputation_MissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/reputation", nil)
	rr := httptest.NewRecorder()
	handleReputation(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestReputation_UnknownPubkey(t *testing.T) {
	oldGraph := graph
	oldCommunities := communities
	graph = NewGraph()
	graph.ComputePageRank(20, 0.85)
	communities = NewCommunityDetector()
	defer func() {
		graph = oldGraph
		communities = oldCommunities
	}()

	pk := padHex(99)
	req := httptest.NewRequest("GET", "/reputation?pubkey="+pk, nil)
	rr := httptest.NewRecorder()
	handleReputation(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for unknown pubkey, got %d", rr.Code)
	}

	var resp ReputationResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if resp.Pubkey != pk {
		t.Errorf("expected pubkey %s, got %s", pk, resp.Pubkey)
	}

	if resp.ReputationScore > 30 {
		t.Errorf("expected low reputation for unknown pubkey, got %d", resp.ReputationScore)
	}

	if resp.Confidence > 0.5 {
		t.Errorf("expected low confidence for unknown pubkey, got %f", resp.Confidence)
	}
}

func TestReputation_WellConnectedNode(t *testing.T) {
	withReputationTestGraph(t, func() {
		hub := padHex(10)
		req := httptest.NewRequest("GET", "/reputation?pubkey="+hub, nil)
		rr := httptest.NewRecorder()
		handleReputation(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp ReputationResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if resp.Pubkey != hub {
			t.Errorf("expected pubkey %s, got %s", hub, resp.Pubkey)
		}

		if resp.ReputationScore < 20 {
			t.Errorf("expected reasonable reputation for well-connected node, got %d", resp.ReputationScore)
		}

		if len(resp.Components) != 5 {
			t.Errorf("expected 5 components, got %d", len(resp.Components))
		}

		validGrades := map[string]bool{"A": true, "B": true, "C": true, "D": true, "F": true}
		if !validGrades[resp.Grade] {
			t.Errorf("unexpected grade: %s", resp.Grade)
		}

		validClassifications := map[string]bool{"excellent": true, "good": true, "fair": true, "poor": true, "untrusted": true}
		if !validClassifications[resp.Classification] {
			t.Errorf("unexpected classification: %s", resp.Classification)
		}

		if resp.Summary == "" {
			t.Error("expected non-empty summary")
		}

		if resp.Followers == 0 {
			t.Error("expected non-zero followers")
		}
		if resp.GraphSize == 0 {
			t.Error("expected non-zero graph size")
		}
	})
}

func TestReputation_ComponentWeightsSum(t *testing.T) {
	withReputationTestGraph(t, func() {
		pk := padHex(10)
		req := httptest.NewRequest("GET", "/reputation?pubkey="+pk, nil)
		rr := httptest.NewRecorder()
		handleReputation(rr, req)

		var resp ReputationResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		totalWeight := 0.0
		for _, c := range resp.Components {
			totalWeight += c.Weight
		}

		if totalWeight < 0.99 || totalWeight > 1.01 {
			t.Errorf("component weights should sum to ~1.0, got %f", totalWeight)
		}
	})
}

func TestReputation_ComponentScoresInRange(t *testing.T) {
	withReputationTestGraph(t, func() {
		pk := padHex(10)
		req := httptest.NewRequest("GET", "/reputation?pubkey="+pk, nil)
		rr := httptest.NewRecorder()
		handleReputation(rr, req)

		var resp ReputationResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		for _, c := range resp.Components {
			if c.Score < 0 || c.Score > 1.0 {
				t.Errorf("component %s score out of range: %f", c.Name, c.Score)
			}
		}

		if resp.ReputationScore < 0 || resp.ReputationScore > 100 {
			t.Errorf("reputation score out of range: %d", resp.ReputationScore)
		}

		if resp.Confidence < 0 || resp.Confidence > 1.0 {
			t.Errorf("confidence out of range: %f", resp.Confidence)
		}
	})
}

func TestReputation_GradeConsistency(t *testing.T) {
	tests := []struct {
		score int
		grade string
		class string
	}{
		{90, "A", "excellent"},
		{80, "A", "excellent"},
		{70, "B", "good"},
		{60, "B", "good"},
		{50, "C", "fair"},
		{40, "C", "fair"},
		{30, "D", "poor"},
		{20, "D", "poor"},
		{10, "F", "untrusted"},
		{0, "F", "untrusted"},
	}

	for _, tt := range tests {
		grade := gradeFromScoreInt(tt.score)
		class := classifyReputation(tt.score)

		if grade != tt.grade {
			t.Errorf("score %d: expected grade %s, got %s", tt.score, tt.grade, grade)
		}
		if class != tt.class {
			t.Errorf("score %d: expected class %s, got %s", tt.score, tt.class, class)
		}
	}
}

func TestReputation_GradeFromFloat(t *testing.T) {
	tests := []struct {
		score float64
		grade string
	}{
		{0.95, "A"},
		{0.80, "A"},
		{0.75, "B"},
		{0.60, "B"},
		{0.55, "C"},
		{0.40, "C"},
		{0.35, "D"},
		{0.20, "D"},
		{0.15, "F"},
		{0.0, "F"},
	}

	for _, tt := range tests {
		grade := gradeFromScore(tt.score)
		if grade != tt.grade {
			t.Errorf("score %f: expected grade %s, got %s", tt.score, tt.grade, grade)
		}
	}
}

func TestReputation_Summary(t *testing.T) {
	summary := buildReputationSummary("abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890", 75, "B", 50, 1, 25)
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if !strings.Contains(summary, "Grade B") {
		t.Errorf("expected 'Grade B' in summary, got: %s", summary)
	}
	if !strings.Contains(summary, "75/100") {
		t.Errorf("expected '75/100' in summary, got: %s", summary)
	}
}

func TestReputation_AnomalyCountZero(t *testing.T) {
	g := NewGraph()
	pk := padHex(10)
	for i := 11; i <= 20; i++ {
		other := padHex(i)
		g.AddFollow(pk, other)
		g.AddFollow(other, pk)
	}
	g.ComputePageRank(20, 0.85)

	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()

	follows := g.GetFollows(pk)
	followers := g.GetFollowers(pk)
	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	count := computeAnomalyCount(pk, follows, followers, followSet, g.Stats().Nodes, 0.5)
	if count != 0 {
		t.Errorf("expected 0 anomalies for normal node, got %d", count)
	}
}

func TestReputation_AnomalyCountExcessiveFollowing(t *testing.T) {
	g := NewGraph()
	pk := padHex(10)
	for i := 11; i <= 2012; i++ {
		other := padHex(i)
		g.AddFollow(pk, other)
	}
	g.ComputePageRank(20, 0.85)

	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()

	follows := g.GetFollows(pk)
	followers := g.GetFollowers(pk)
	followSet := make(map[string]bool, len(follows))
	for _, f := range follows {
		followSet[f] = true
	}

	count := computeAnomalyCount(pk, follows, followers, followSet, g.Stats().Nodes, 0.5)
	if count < 1 {
		t.Errorf("expected at least 1 anomaly for excessive following, got %d", count)
	}
}

func TestReputation_ResponseFields(t *testing.T) {
	withReputationTestGraph(t, func() {
		pk := padHex(10)
		req := httptest.NewRequest("GET", "/reputation?pubkey="+pk, nil)
		rr := httptest.NewRecorder()
		handleReputation(rr, req)

		var raw map[string]interface{}
		if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		requiredFields := []string{
			"pubkey", "reputation_score", "grade", "classification",
			"confidence", "components", "summary",
			"trust_score", "sybil_score", "anomaly_count",
			"community_size", "followers", "follows",
			"mutual_count", "percentile", "graph_size",
		}

		for _, field := range requiredFields {
			if _, ok := raw[field]; !ok {
				t.Errorf("missing required field: %s", field)
			}
		}
	})
}

func TestReputation_ComponentNames(t *testing.T) {
	withReputationTestGraph(t, func() {
		pk := padHex(10)
		req := httptest.NewRequest("GET", "/reputation?pubkey="+pk, nil)
		rr := httptest.NewRecorder()
		handleReputation(rr, req)

		var resp ReputationResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		expectedNames := map[string]bool{
			"wot_standing":          true,
			"sybil_resistance":     true,
			"community_integration": true,
			"anomaly_cleanliness":  true,
			"network_diversity":    true,
		}

		for _, c := range resp.Components {
			if !expectedNames[c.Name] {
				t.Errorf("unexpected component name: %s", c.Name)
			}
			delete(expectedNames, c.Name)
		}

		for name := range expectedNames {
			t.Errorf("missing expected component: %s", name)
		}
	})
}
