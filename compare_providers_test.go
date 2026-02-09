package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareProvidersMissingParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/compare-providers", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCompareProvidersInvalidNpub(t *testing.T) {
	req := httptest.NewRequest("GET", "/compare-providers?pubkey=npub1invalid", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCompareProvidersOwnScoreOnly(t *testing.T) {
	oldGraph := graph
	oldStore := externalAssertions
	graph = NewGraph()
	externalAssertions = NewAssertionStore()
	defer func() {
		graph = oldGraph
		externalAssertions = oldStore
	}()

	graph.AddFollow("aaa", "bbb")
	graph.AddFollow("bbb", "aaa")
	graph.AddFollow("ccc", "aaa")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/compare-providers?pubkey=aaa", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}

	var resp CompareProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if resp.Pubkey != "aaa" {
		t.Errorf("pubkey = %q, want aaa", resp.Pubkey)
	}
	if !resp.InGraph {
		t.Error("expected in_graph = true")
	}
	if len(resp.Providers) != 1 {
		t.Fatalf("expected 1 provider (self), got %d", len(resp.Providers))
	}
	if !resp.Providers[0].IsOurs {
		t.Error("expected first provider to be ours")
	}
	if resp.Consensus != nil {
		t.Error("expected no consensus with 1 provider")
	}
}

func TestCompareProvidersWithExternals(t *testing.T) {
	oldGraph := graph
	oldStore := externalAssertions
	graph = NewGraph()
	externalAssertions = NewAssertionStore()
	defer func() {
		graph = oldGraph
		externalAssertions = oldStore
	}()

	graph.AddFollow("aaa", "bbb")
	graph.AddFollow("bbb", "aaa")
	graph.AddFollow("ccc", "aaa")
	graph.ComputePageRank(20, 0.85)

	// Add external assertions from two providers
	externalAssertions.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "aaa",
		Rank:           75,
		Followers:      100,
		CreatedAt:      1700000000,
	})
	externalAssertions.Add(&ExternalAssertion{
		ProviderPubkey: "provider2",
		SubjectPubkey:  "aaa",
		Rank:           80,
		Followers:      95,
		CreatedAt:      1700000000,
	})

	req := httptest.NewRequest("GET", "/compare-providers?pubkey=aaa", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}

	var resp CompareProvidersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	if len(resp.Providers) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(resp.Providers))
	}

	// First should be ours
	if !resp.Providers[0].IsOurs {
		t.Error("first provider should be ours")
	}

	// Verify external providers present
	foundP1, foundP2 := false, false
	for _, p := range resp.Providers {
		if p.ProviderPubkey == "provider1" {
			foundP1 = true
			if p.NormalizedRank != 75 {
				t.Errorf("provider1 normalized_rank = %d, want 75", p.NormalizedRank)
			}
		}
		if p.ProviderPubkey == "provider2" {
			foundP2 = true
			if p.NormalizedRank != 80 {
				t.Errorf("provider2 normalized_rank = %d, want 80", p.NormalizedRank)
			}
		}
	}
	if !foundP1 || !foundP2 {
		t.Error("missing external providers in response")
	}

	// Should have consensus
	if resp.Consensus == nil {
		t.Fatal("expected consensus metrics")
	}
	if resp.Consensus.ProviderCount != 3 {
		t.Errorf("provider_count = %d, want 3", resp.Consensus.ProviderCount)
	}
}

func TestCompareProvidersConsensusStrong(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 50},
		{NormalizedRank: 52},
		{NormalizedRank: 48},
	}
	c := calculateConsensus(providers)
	if c.Agreement != "strong" {
		t.Errorf("agreement = %q, want strong (stddev=%v)", c.Agreement, c.StdDev)
	}
	if c.Mean != 50 {
		t.Errorf("mean = %v, want 50", c.Mean)
	}
	if c.Median != 50 {
		t.Errorf("median = %v, want 50", c.Median)
	}
}

func TestCompareProvidersConsensusModerate(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 30},
		{NormalizedRank: 50},
		{NormalizedRank: 60},
	}
	c := calculateConsensus(providers)
	if c.Agreement != "moderate" {
		t.Errorf("agreement = %q, want moderate (stddev=%v)", c.Agreement, c.StdDev)
	}
}

func TestCompareProvidersConsensusWeak(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 10},
		{NormalizedRank: 50},
		{NormalizedRank: 80},
	}
	c := calculateConsensus(providers)
	if c.Agreement != "weak" {
		t.Errorf("agreement = %q, want weak (stddev=%v)", c.Agreement, c.StdDev)
	}
}

func TestCompareProvidersConsensusNone(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 0},
		{NormalizedRank: 50},
		{NormalizedRank: 100},
	}
	c := calculateConsensus(providers)
	if c.Agreement != "no_consensus" {
		t.Errorf("agreement = %q, want no_consensus (stddev=%v)", c.Agreement, c.StdDev)
	}
	if c.Spread != 100 {
		t.Errorf("spread = %d, want 100", c.Spread)
	}
}

func TestCompareProvidersMinMax(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 20},
		{NormalizedRank: 80},
	}
	c := calculateConsensus(providers)
	if c.Min != 20 {
		t.Errorf("min = %d, want 20", c.Min)
	}
	if c.Max != 80 {
		t.Errorf("max = %d, want 80", c.Max)
	}
	if c.Spread != 60 {
		t.Errorf("spread = %d, want 60", c.Spread)
	}
}

func TestCompareProvidersMedianEven(t *testing.T) {
	providers := []ProviderScore{
		{NormalizedRank: 10},
		{NormalizedRank: 20},
		{NormalizedRank: 30},
		{NormalizedRank: 40},
	}
	c := calculateConsensus(providers)
	if c.Median != 25 {
		t.Errorf("median = %v, want 25", c.Median)
	}
}

func TestCompareProvidersNotInGraph(t *testing.T) {
	oldGraph := graph
	oldStore := externalAssertions
	graph = NewGraph()
	externalAssertions = NewAssertionStore()
	defer func() {
		graph = oldGraph
		externalAssertions = oldStore
	}()

	graph.AddFollow("bbb", "ccc")
	graph.ComputePageRank(20, 0.85)

	// Add external assertion for pubkey not in our graph
	externalAssertions.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "zzz",
		Rank:           60,
		CreatedAt:      1700000000,
	})

	req := httptest.NewRequest("GET", "/compare-providers?pubkey=zzz", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200. body: %s", rec.Code, rec.Body.String())
	}

	var resp CompareProvidersResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.InGraph {
		t.Error("expected in_graph = false for unknown pubkey")
	}
	if len(resp.Providers) != 2 {
		t.Fatalf("expected 2 providers (self + 1 external), got %d", len(resp.Providers))
	}
}

func TestCompareProvidersGraphSize(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	graph.AddFollow("aaa", "bbb")
	graph.AddFollow("ccc", "ddd")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/compare-providers?pubkey=aaa", nil)
	rec := httptest.NewRecorder()
	handleCompareProviders(rec, req)

	var resp CompareProvidersResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)

	if resp.GraphSize != 4 {
		t.Errorf("graph_size = %d, want 4", resp.GraphSize)
	}
}
