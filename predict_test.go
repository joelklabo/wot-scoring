package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildPredictTestGraph() *Graph {
	g := NewGraph()

	// Create a cluster where A and B share many connections
	a := padHex(100)
	b := padHex(101)

	// Shared connections (common neighbors)
	for i := 200; i <= 210; i++ {
		pk := padHex(i)
		g.AddFollow(a, pk)
		g.AddFollow(b, pk)
		g.AddFollow(pk, a)
		g.AddFollow(pk, b)
		// Give each shared connection some followers for PageRank substance
		for j := i + 100; j <= i+105; j++ {
			other := padHex(j)
			g.AddFollow(other, pk)
			g.AddFollow(pk, other)
		}
	}

	// A-only connections
	for i := 220; i <= 225; i++ {
		pk := padHex(i)
		g.AddFollow(a, pk)
		g.AddFollow(pk, a)
	}

	// B-only connections
	for i := 230; i <= 235; i++ {
		pk := padHex(i)
		g.AddFollow(b, pk)
		g.AddFollow(pk, b)
	}

	// Isolated node C with no connections to A or B
	c := padHex(102)
	for i := 250; i <= 252; i++ {
		pk := padHex(i)
		g.AddFollow(c, pk)
		g.AddFollow(pk, c)
	}

	g.ComputePageRank(20, 0.85)
	return g
}

func withPredictTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildPredictTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func TestPredict_MissingSource(t *testing.T) {
	req := httptest.NewRequest("GET", "/predict?target=abc", nil)
	rr := httptest.NewRecorder()
	handlePredict(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestPredict_MissingTarget(t *testing.T) {
	req := httptest.NewRequest("GET", "/predict?source=abc", nil)
	rr := httptest.NewRecorder()
	handlePredict(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestPredict_MissingBoth(t *testing.T) {
	req := httptest.NewRequest("GET", "/predict", nil)
	rr := httptest.NewRecorder()
	handlePredict(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestPredict_SameSourceTarget(t *testing.T) {
	pk := padHex(100)
	req := httptest.NewRequest("GET", "/predict?source="+pk+"&target="+pk, nil)
	rr := httptest.NewRecorder()
	handlePredict(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for same source/target, got %d", rr.Code)
	}
}

func TestPredict_HighPrediction_SharedConnections(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp PredictResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if resp.Prediction < 0.3 {
			t.Errorf("expected high prediction for well-connected pair, got %.3f", resp.Prediction)
		}
		if resp.AlreadyFollows {
			t.Error("A and B don't directly follow each other in test graph")
		}
	})
}

func TestPredict_LowPrediction_IsolatedNodes(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		c := padHex(102)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+c, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp PredictResponse
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode error: %v", err)
		}

		if resp.Prediction > 0.5 {
			t.Errorf("expected low prediction for disconnected pair, got %.3f", resp.Prediction)
		}
	})
}

func TestPredict_SignalCount(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Signals) != 5 {
			t.Errorf("expected 5 signals, got %d", len(resp.Signals))
		}

		expectedNames := map[string]bool{
			"common_neighbors":        false,
			"adamic_adar":             false,
			"preferential_attachment":  false,
			"jaccard_coefficient":      false,
			"wot_proximity":           false,
		}
		for _, s := range resp.Signals {
			if _, ok := expectedNames[s.Name]; !ok {
				t.Errorf("unexpected signal name: %s", s.Name)
			}
			expectedNames[s.Name] = true
		}
		for name, found := range expectedNames {
			if !found {
				t.Errorf("missing signal: %s", name)
			}
		}
	})
}

func TestPredict_SignalWeightsSum(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		totalWeight := 0.0
		for _, s := range resp.Signals {
			totalWeight += s.Weight
		}
		if totalWeight < 0.99 || totalWeight > 1.01 {
			t.Errorf("signal weights should sum to ~1.0, got %.2f", totalWeight)
		}
	})
}

func TestPredict_PredictionRange(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.Prediction < 0 || resp.Prediction > 1 {
			t.Errorf("prediction should be 0-1, got %.3f", resp.Prediction)
		}
		if resp.Confidence < 0 || resp.Confidence > 1 {
			t.Errorf("confidence should be 0-1, got %.2f", resp.Confidence)
		}
	})
}

func TestPredict_Classification(t *testing.T) {
	validClassifications := map[string]bool{
		"very_likely":   true,
		"likely":        true,
		"possible":      true,
		"unlikely":      true,
		"very_unlikely": true,
	}

	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if !validClassifications[resp.Classification] {
			t.Errorf("invalid classification: %s", resp.Classification)
		}
	})
}

func TestPredict_TopMutuals(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.TopMutuals) == 0 {
			t.Error("expected non-empty top_mutuals for well-connected pair")
		}
		if len(resp.TopMutuals) > 10 {
			t.Errorf("top_mutuals should be capped at 10, got %d", len(resp.TopMutuals))
		}

		// Verify sorted descending by WoT score
		for i := 1; i < len(resp.TopMutuals); i++ {
			if resp.TopMutuals[i].WotScore > resp.TopMutuals[i-1].WotScore {
				t.Error("top_mutuals should be sorted by WoT score descending")
				break
			}
		}
	})
}

func TestPredict_CommonNeighborsSignal(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		for _, s := range resp.Signals {
			if s.Name == "common_neighbors" {
				if s.RawValue < 5 {
					t.Errorf("expected many common neighbors for A-B, got %.0f", s.RawValue)
				}
				return
			}
		}
		t.Error("common_neighbors signal not found")
	})
}

func TestPredict_ResponseFields(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var raw map[string]interface{}
		json.NewDecoder(rr.Body).Decode(&raw)

		requiredFields := []string{
			"source", "target", "already_follows", "prediction",
			"confidence", "classification", "signals", "graph_size",
		}
		for _, f := range requiredFields {
			if _, ok := raw[f]; !ok {
				t.Errorf("missing required field: %s", f)
			}
		}
	})
}

func TestPredict_GraphSizeNonZero(t *testing.T) {
	withPredictTestGraph(t, func() {
		a := padHex(100)
		b := padHex(101)
		req := httptest.NewRequest("GET", "/predict?source="+a+"&target="+b, nil)
		rr := httptest.NewRecorder()
		handlePredict(rr, req)

		var resp PredictResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if resp.GraphSize == 0 {
			t.Error("graph_size should be non-zero")
		}
	})
}

func TestClassifyPrediction(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.8, "very_likely"},
		{0.7, "very_likely"},
		{0.6, "likely"},
		{0.5, "likely"},
		{0.4, "possible"},
		{0.3, "possible"},
		{0.2, "unlikely"},
		{0.1, "unlikely"},
		{0.05, "very_unlikely"},
		{0.0, "very_unlikely"},
	}
	for _, tc := range tests {
		got := classifyPrediction(tc.score)
		if got != tc.want {
			t.Errorf("classifyPrediction(%.2f) = %s, want %s", tc.score, got, tc.want)
		}
	}
}
