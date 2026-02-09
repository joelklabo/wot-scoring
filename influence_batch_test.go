package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildInfluenceBatchTestGraph() *Graph {
	g := NewGraph()

	// Hub: many followers, high PageRank
	hub := padHex(600)
	for i := 610; i <= 625; i++ {
		pk := padHex(i)
		g.AddFollow(pk, hub)
		g.AddFollow(hub, pk) // mutual
	}

	// Authority: fewer followers but still significant
	auth := padHex(601)
	for i := 630; i <= 640; i++ {
		pk := padHex(i)
		g.AddFollow(pk, auth)
	}
	g.AddFollow(auth, hub)
	g.AddFollow(hub, auth)

	// Connector: high mutual ratio
	conn := padHex(602)
	for i := 650; i <= 655; i++ {
		pk := padHex(i)
		g.AddFollow(pk, conn)
		g.AddFollow(conn, pk) // mutual
	}
	g.AddFollow(conn, hub)

	// Consumer: follows many, few followers
	consumer := padHex(603)
	for i := 610; i <= 625; i++ {
		g.AddFollow(consumer, padHex(i))
	}
	// Only one follower
	g.AddFollow(padHex(660), consumer)

	// Isolated: no connections
	// padHex(604) — not added to graph

	// Observer: follows others but no followers
	observer := padHex(605)
	g.AddFollow(observer, hub)
	g.AddFollow(observer, auth)
	g.AddFollow(observer, conn)

	g.ComputePageRank(20, 0.85)
	return g
}

func withInfluenceBatchTestGraph(t *testing.T, fn func()) {
	t.Helper()
	g := buildInfluenceBatchTestGraph()
	oldGraph := graph
	graph = g
	defer func() { graph = oldGraph }()
	fn()
}

func postInfluenceBatch(pubkeys []string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(InfluenceBatchRequest{Pubkeys: pubkeys})
	req := httptest.NewRequest("POST", "/influence/batch", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handleInfluenceBatch(rr, req)
	return rr
}

func TestInfluenceBatch_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/influence/batch", nil)
	rr := httptest.NewRecorder()
	handleInfluenceBatch(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestInfluenceBatch_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/influence/batch", bytes.NewReader([]byte("not json")))
	rr := httptest.NewRecorder()
	handleInfluenceBatch(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluenceBatch_EmptyPubkeys(t *testing.T) {
	rr := postInfluenceBatch([]string{})
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluenceBatch_TooManyPubkeys(t *testing.T) {
	pks := make([]string, 51)
	for i := range pks {
		pks[i] = padHex(700 + i)
	}
	rr := postInfluenceBatch(pks)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestInfluenceBatch_SinglePubkey(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
		}

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(resp.Results))
		}
		if resp.Results[0].Pubkey != hub {
			t.Errorf("expected pubkey %s, got %s", hub, resp.Results[0].Pubkey)
		}
		if resp.Results[0].TrustScore <= 0 {
			t.Errorf("hub should have positive trust score, got %d", resp.Results[0].TrustScore)
		}
		if resp.GraphSize <= 0 {
			t.Errorf("graph_size should be positive, got %d", resp.GraphSize)
		}
	})
}

func TestInfluenceBatch_MultiplePubkeys(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		auth := padHex(601)
		conn := padHex(602)
		rr := postInfluenceBatch([]string{hub, auth, conn})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(resp.Results))
		}
	})
}

func TestInfluenceBatch_SortedByScore(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		consumer := padHex(603)
		rr := postInfluenceBatch([]string{consumer, hub})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Results) < 2 {
			t.Fatalf("expected 2+ results, got %d", len(resp.Results))
		}
		// Hub should be sorted first (higher score)
		if resp.Results[0].TrustScore < resp.Results[1].TrustScore {
			t.Errorf("results should be sorted by trust_score descending, got %d then %d",
				resp.Results[0].TrustScore, resp.Results[1].TrustScore)
		}
	})
}

func TestInfluenceBatch_InvalidPubkeyInBatch(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub, "npub1invalid"})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200 (partial results), got %d", rr.Code)
		}

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(resp.Results))
		}

		// Find the error entry
		hasError := false
		for _, r := range resp.Results {
			if r.Error != "" {
				hasError = true
			}
		}
		if !hasError {
			t.Error("expected one result with error for invalid pubkey")
		}
	})
}

func TestInfluenceBatch_ResponseFields(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})

		var raw map[string]interface{}
		json.NewDecoder(rr.Body).Decode(&raw)

		if _, ok := raw["results"]; !ok {
			t.Error("missing 'results' field")
		}
		if _, ok := raw["graph_size"]; !ok {
			t.Error("missing 'graph_size' field")
		}
	})
}

func TestInfluenceBatch_EntryFields(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		entry := resp.Results[0]
		if entry.Pubkey == "" {
			t.Error("missing pubkey")
		}
		if entry.Followers <= 0 {
			t.Errorf("hub should have followers, got %d", entry.Followers)
		}
		if entry.Follows <= 0 {
			t.Errorf("hub should have follows, got %d", entry.Follows)
		}
		if entry.Percentile < 0 || entry.Percentile > 1 {
			t.Errorf("percentile should be 0-1, got %f", entry.Percentile)
		}
		if entry.Rank <= 0 {
			t.Errorf("rank should be positive, got %d", entry.Rank)
		}
		if entry.Classification == "" {
			t.Error("missing classification")
		}
	})
}

func TestInfluenceBatch_HubHasHighFollowers(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		consumer := padHex(603)
		rr := postInfluenceBatch([]string{hub, consumer})

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		var hubEntry, consumerEntry InfluenceEntry
		for _, r := range resp.Results {
			if r.Pubkey == hub {
				hubEntry = r
			}
			if r.Pubkey == consumer {
				consumerEntry = r
			}
		}

		if hubEntry.Followers <= consumerEntry.Followers {
			t.Errorf("hub should have more followers (%d) than consumer (%d)",
				hubEntry.Followers, consumerEntry.Followers)
		}
	})
}

func TestInfluenceBatch_ReachEstimate(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		entry := resp.Results[0]
		// Hub's reach should be >= its follower count
		if entry.ReachEstimate < entry.Followers {
			t.Errorf("reach (%d) should be >= followers (%d)", entry.ReachEstimate, entry.Followers)
		}
	})
}

func TestInfluenceBatch_MutualCount(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		entry := resp.Results[0]
		if entry.MutualCount <= 0 {
			t.Errorf("hub should have mutuals, got %d", entry.MutualCount)
		}
	})
}

func TestInfluenceBatch_AvgFollowerQuality(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		hub := padHex(600)
		rr := postInfluenceBatch([]string{hub})

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		entry := resp.Results[0]
		if entry.AvgFollowerQuality < 0 {
			t.Errorf("avg_follower_quality should be non-negative, got %f", entry.AvgFollowerQuality)
		}
	})
}

func TestInfluenceBatch_NotInGraph(t *testing.T) {
	withInfluenceBatchTestGraph(t, func() {
		unknown := padHex(999) // not in graph
		rr := postInfluenceBatch([]string{unknown})
		if rr.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rr.Code)
		}

		var resp InfluenceBatchResponse
		json.NewDecoder(rr.Body).Decode(&resp)

		if len(resp.Results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(resp.Results))
		}
		if resp.Results[0].TrustScore != 0 {
			t.Errorf("unknown pubkey should have 0 trust score, got %d", resp.Results[0].TrustScore)
		}
		if resp.Results[0].Classification != "isolated" {
			t.Errorf("unknown pubkey should be 'isolated', got %s", resp.Results[0].Classification)
		}
	})
}

// --- classifyInfluenceRole tests ---

func TestClassifyInfluenceRole_Isolated(t *testing.T) {
	got := classifyInfluenceRole(0, 0, 0, 0, 0)
	if got != "isolated" {
		t.Errorf("expected 'isolated', got %s", got)
	}
}

func TestClassifyInfluenceRole_Observer(t *testing.T) {
	got := classifyInfluenceRole(0, 0, 10, 0, 0)
	if got != "observer" {
		t.Errorf("expected 'observer', got %s", got)
	}
}

func TestClassifyInfluenceRole_Hub(t *testing.T) {
	got := classifyInfluenceRole(95, 100, 50, 30, 0.99)
	if got != "hub" {
		t.Errorf("expected 'hub', got %s", got)
	}
}

func TestClassifyInfluenceRole_Authority(t *testing.T) {
	got := classifyInfluenceRole(80, 30, 10, 5, 0.92)
	if got != "authority" {
		t.Errorf("expected 'authority', got %s", got)
	}
}

func TestClassifyInfluenceRole_Connector(t *testing.T) {
	// 10 followers, 8 mutuals = 80% mutual ratio, but below top 10%
	got := classifyInfluenceRole(40, 10, 15, 8, 0.50)
	if got != "connector" {
		t.Errorf("expected 'connector', got %s", got)
	}
}

func TestClassifyInfluenceRole_Consumer(t *testing.T) {
	got := classifyInfluenceRole(5, 3, 50, 0, 0.10)
	if got != "consumer" {
		t.Errorf("expected 'consumer', got %s", got)
	}
}

func TestClassifyInfluenceRole_Participant(t *testing.T) {
	got := classifyInfluenceRole(30, 15, 20, 3, 0.50)
	if got != "participant" {
		t.Errorf("expected 'participant', got %s", got)
	}
}
