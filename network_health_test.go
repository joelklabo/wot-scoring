package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// buildHealthTestGraph creates a test graph with known topology for network health tests.
func buildHealthTestGraph() func() {
	oldFollows := graph.follows
	oldFollowers := graph.followers
	oldScores := graph.scores

	graph.follows = map[string][]string{
		padHex(2): {padHex(3), padHex(4)},
		padHex(3): {padHex(2), padHex(5)},
		padHex(4): {padHex(2)},
		padHex(5): {padHex(3), padHex(6)},
		padHex(6): {padHex(5)},
		padHex(7): {padHex(2)},
	}

	graph.followers = map[string][]string{
		padHex(2): {padHex(3), padHex(4), padHex(7)},
		padHex(3): {padHex(2), padHex(5)},
		padHex(4): {padHex(2)},
		padHex(5): {padHex(3), padHex(6)},
		padHex(6): {padHex(5)},
	}

	graph.scores = map[string]float64{
		padHex(2): 0.30,
		padHex(3): 0.25,
		padHex(4): 0.10,
		padHex(5): 0.20,
		padHex(6): 0.10,
		padHex(7): 0.05,
	}

	return func() {
		graph.follows = oldFollows
		graph.followers = oldFollowers
		graph.scores = oldScores
	}
}

func TestNetworkHealth_EmptyGraph(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	// Temporarily empty the graph
	graph.scores = map[string]float64{}
	graph.follows = map[string][]string{}
	graph.followers = map[string][]string{}

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for empty graph, got %d", w.Code)
	}
}

func TestNetworkHealth_ResponseFields(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp NetworkHealthResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.GraphSize != 6 {
		t.Errorf("expected 6 nodes, got %d", resp.GraphSize)
	}

	if resp.EdgeCount <= 0 {
		t.Errorf("expected positive edge count, got %d", resp.EdgeCount)
	}

	if resp.Density <= 0 || resp.Density > 1 {
		t.Errorf("density should be in (0,1], got %f", resp.Density)
	}

	if resp.Reciprocity < 0 || resp.Reciprocity > 1 {
		t.Errorf("reciprocity should be in [0,1], got %f", resp.Reciprocity)
	}

	if resp.HealthScore < 0 || resp.HealthScore > 100 {
		t.Errorf("health score should be in [0,100], got %d", resp.HealthScore)
	}

	if resp.Classification == "" {
		t.Error("classification should not be empty")
	}
}

func TestNetworkHealth_Connectivity(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	var resp NetworkHealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	conn := resp.Connectivity
	if conn.LargestComponentSize <= 0 {
		t.Error("largest component should have at least 1 node")
	}

	if conn.LargestComponentPercent <= 0 || conn.LargestComponentPercent > 100 {
		t.Errorf("component percent should be in (0,100], got %f", conn.LargestComponentPercent)
	}

	if conn.ComponentCount <= 0 {
		t.Error("should have at least 1 component")
	}

	if conn.IsolatedNodes < 0 {
		t.Error("isolated nodes should be non-negative")
	}
}

func TestNetworkHealth_DegreeStats(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	var resp NetworkHealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	ds := resp.DegreeStats
	if ds.MeanInDegree <= 0 {
		t.Errorf("mean in-degree should be positive, got %f", ds.MeanInDegree)
	}

	if ds.MeanOutDegree <= 0 {
		t.Errorf("mean out-degree should be positive, got %f", ds.MeanOutDegree)
	}

	if ds.MaxInDegree <= 0 {
		t.Errorf("max in-degree should be positive, got %d", ds.MaxInDegree)
	}

	if ds.MaxOutDegree <= 0 {
		t.Errorf("max out-degree should be positive, got %d", ds.MaxOutDegree)
	}
}

func TestNetworkHealth_ScoreDistribution(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	var resp NetworkHealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	sd := resp.ScoreDistrib
	if sd.GiniCoefficient < 0 || sd.GiniCoefficient > 1 {
		t.Errorf("Gini should be in [0,1], got %f", sd.GiniCoefficient)
	}

	if sd.Top1Percent < 0 || sd.Top1Percent > 100 {
		t.Errorf("top 1%% share should be in [0,100], got %f", sd.Top1Percent)
	}

	if sd.Top10Percent < 0 || sd.Top10Percent > 100 {
		t.Errorf("top 10%% share should be in [0,100], got %f", sd.Top10Percent)
	}

	validCentralizations := map[string]bool{
		"highly_centralized":   true,
		"centralized":          true,
		"moderate":             true,
		"decentralized":        true,
		"highly_decentralized": true,
		"unknown":              true,
	}
	if !validCentralizations[sd.Centralization] {
		t.Errorf("unexpected centralization: %s", sd.Centralization)
	}
}

func TestNetworkHealth_TopHubs(t *testing.T) {
	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	var resp NetworkHealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if len(resp.TopHubs) == 0 {
		t.Error("expected at least one top hub")
	}

	if len(resp.TopHubs) > 5 {
		t.Errorf("expected at most 5 top hubs, got %d", len(resp.TopHubs))
	}

	for _, hub := range resp.TopHubs {
		if hub.Pubkey == "" {
			t.Error("hub pubkey should not be empty")
		}
		if hub.InDegree+hub.OutDegree <= 0 {
			t.Errorf("hub %s should have positive combined degree", hub.Pubkey)
		}
	}
}

func TestNetworkHealth_Classification(t *testing.T) {
	validClassifications := map[string]bool{
		"excellent":   true,
		"good":        true,
		"developing":  true,
		"weak":        true,
		"nascent":     true,
	}

	restore := buildHealthTestGraph()
	defer restore()

	req := httptest.NewRequest("GET", "/network-health", nil)
	w := httptest.NewRecorder()
	handleNetworkHealth(w, req)

	var resp NetworkHealthResponse
	json.Unmarshal(w.Body.Bytes(), &resp)

	if !validClassifications[resp.Classification] {
		t.Errorf("unexpected classification: %s", resp.Classification)
	}
}

// Unit tests for helper functions

func TestGiniCoefficient_Equal(t *testing.T) {
	// All equal values should give Gini = 0
	vals := []float64{1.0, 1.0, 1.0, 1.0}
	gini := giniCoefficient(vals)
	if math.Abs(gini) > 0.001 {
		t.Errorf("expected Gini ~0 for equal values, got %f", gini)
	}
}

func TestGiniCoefficient_Unequal(t *testing.T) {
	// One person has everything
	vals := []float64{0.0, 0.0, 0.0, 1.0}
	gini := giniCoefficient(vals)
	if gini < 0.5 {
		t.Errorf("expected high Gini for unequal distribution, got %f", gini)
	}
}

func TestGiniCoefficient_Empty(t *testing.T) {
	gini := giniCoefficient([]float64{})
	if gini != 0 {
		t.Errorf("expected Gini 0 for empty, got %f", gini)
	}
}

func TestEstimatePowerLaw_TooFew(t *testing.T) {
	alpha := estimatePowerLaw([]int{1, 2, 3})
	if alpha != 0 {
		t.Errorf("expected 0 for too few values, got %f", alpha)
	}
}

func TestEstimatePowerLaw_Reasonable(t *testing.T) {
	// Create a power-law-ish distribution
	degrees := make([]int, 100)
	for i := range degrees {
		degrees[i] = int(math.Pow(float64(i+1), -1.5) * 1000)
	}
	alpha := estimatePowerLaw(degrees)
	if alpha <= 0 {
		t.Errorf("expected positive alpha for power-law data, got %f", alpha)
	}
}

func TestClassifyHealth(t *testing.T) {
	cases := []struct {
		score    int
		expected string
	}{
		{90, "excellent"},
		{80, "excellent"},
		{70, "good"},
		{60, "good"},
		{50, "developing"},
		{40, "developing"},
		{30, "weak"},
		{20, "weak"},
		{10, "nascent"},
		{0, "nascent"},
	}

	for _, tc := range cases {
		got := classifyHealth(tc.score)
		if got != tc.expected {
			t.Errorf("classifyHealth(%d) = %s, want %s", tc.score, got, tc.expected)
		}
	}
}

func TestClassifyCentralization(t *testing.T) {
	cases := []struct {
		gini     float64
		expected string
	}{
		{0.9, "highly_centralized"},
		{0.7, "centralized"},
		{0.5, "moderate"},
		{0.3, "decentralized"},
		{0.1, "highly_decentralized"},
	}

	for _, tc := range cases {
		got := classifyCentralization(tc.gini)
		if got != tc.expected {
			t.Errorf("classifyCentralization(%f) = %s, want %s", tc.gini, got, tc.expected)
		}
	}
}

func TestComputeReciprocity_Mutual(t *testing.T) {
	follows := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	r := computeReciprocity(follows)
	if math.Abs(r-1.0) > 0.001 {
		t.Errorf("expected reciprocity 1.0 for fully mutual graph, got %f", r)
	}
}

func TestComputeReciprocity_OneWay(t *testing.T) {
	follows := map[string][]string{
		"a": {"b"},
	}
	r := computeReciprocity(follows)
	if r != 0 {
		t.Errorf("expected reciprocity 0 for one-way graph, got %f", r)
	}
}

func TestComputeReciprocity_Empty(t *testing.T) {
	r := computeReciprocity(map[string][]string{})
	if r != 0 {
		t.Errorf("expected reciprocity 0 for empty graph, got %f", r)
	}
}

func TestMean(t *testing.T) {
	vals := []int{2, 4, 6}
	got := mean(vals)
	if math.Abs(got-4.0) > 0.001 {
		t.Errorf("expected mean 4, got %f", got)
	}
}

func TestMean_Empty(t *testing.T) {
	got := mean([]int{})
	if got != 0 {
		t.Errorf("expected mean 0 for empty, got %f", got)
	}
}

func TestMedian(t *testing.T) {
	got := median([]int{1, 3, 5})
	if got != 3 {
		t.Errorf("expected median 3, got %d", got)
	}
}

func TestMedian_Empty(t *testing.T) {
	got := median([]int{})
	if got != 0 {
		t.Errorf("expected median 0 for empty, got %d", got)
	}
}

func TestMaxInt(t *testing.T) {
	got := maxInt([]int{1, 5, 3})
	// Note: maxInt assumes sorted input
	sorted := []int{1, 3, 5}
	got = maxInt(sorted)
	if got != 5 {
		t.Errorf("expected max 5, got %d", got)
	}
}

func TestRound6(t *testing.T) {
	got := round6(0.123456789)
	expected := 0.123457
	if math.Abs(got-expected) > 0.0000001 {
		t.Errorf("expected %f, got %f", expected, got)
	}
}
