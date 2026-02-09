package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebOfTrustMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/weboftrust", nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWebOfTrustInvalidNpub(t *testing.T) {
	req := httptest.NewRequest("GET", "/weboftrust?pubkey=npub1invalid", nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestWebOfTrustUnknownPubkey(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	pk := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+pk, nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp WoTGraphResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Pubkey != pk {
		t.Fatalf("expected pubkey %s, got %s", pk, resp.Pubkey)
	}
	// Unknown pubkey should have only the center node
	if resp.NodeCount != 1 {
		t.Fatalf("expected 1 node (center), got %d", resp.NodeCount)
	}
	if resp.Nodes[0].Group != "center" {
		t.Fatalf("expected center group, got %s", resp.Nodes[0].Group)
	}
}

func TestWebOfTrustWithFollows(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	center := padHex(5000)
	// Center follows 3 people
	for i := 0; i < 3; i++ {
		graph.AddFollow(center, padHex(5100+i))
	}
	// 2 people follow center
	for i := 0; i < 2; i++ {
		graph.AddFollow(padHex(5200+i), center)
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+center, nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	var resp WoTGraphResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// 1 center + 3 follows + 2 followers = 6 nodes
	if resp.NodeCount != 6 {
		t.Fatalf("expected 6 nodes, got %d", resp.NodeCount)
	}
	// 3 "follows" links + 2 "followed_by" links = 5
	if resp.LinkCount != 5 {
		t.Fatalf("expected 5 links, got %d", resp.LinkCount)
	}

	// Center should be first node
	if resp.Nodes[0].Group != "center" {
		t.Fatalf("expected center node first, got group %s", resp.Nodes[0].Group)
	}
	if resp.Nodes[0].Follows != 3 {
		t.Fatalf("expected center to follow 3, got %d", resp.Nodes[0].Follows)
	}
}

func TestWebOfTrustMutualConnections(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	a := padHex(6000)
	b := padHex(6001)

	// Mutual follow
	graph.AddFollow(a, b)
	graph.AddFollow(b, a)
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+a, nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	var resp WoTGraphResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.NodeCount != 2 {
		t.Fatalf("expected 2 nodes, got %d", resp.NodeCount)
	}

	// Find the non-center node — should be "mutual"
	for _, n := range resp.Nodes {
		if n.ID == b {
			if n.Group != "mutual" {
				t.Fatalf("expected mutual group for %s, got %s", b, n.Group)
			}
		}
	}
}

func TestWebOfTrustLimit(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	center := padHex(7000)
	// Center follows 100 people
	for i := 0; i < 100; i++ {
		graph.AddFollow(center, padHex(7100+i))
	}
	graph.ComputePageRank(20, 0.85)

	// Request with limit=5
	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+center+"&limit=5", nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	var resp WoTGraphResponse
	json.NewDecoder(w.Body).Decode(&resp)

	// 1 center + 5 follows = 6 (limit applies to follow/follower count separately)
	if resp.NodeCount != 6 {
		t.Fatalf("expected 6 nodes with limit=5, got %d", resp.NodeCount)
	}
}

func TestWebOfTrustLinkTypes(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	center := padHex(8000)
	followed := padHex(8001)
	follower := padHex(8002)

	graph.AddFollow(center, followed)
	graph.AddFollow(follower, center)
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+center, nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	var resp WoTGraphResponse
	json.NewDecoder(w.Body).Decode(&resp)

	followsLinks := 0
	followedByLinks := 0
	for _, l := range resp.Links {
		switch l.Type {
		case "follows":
			followsLinks++
			if l.Source != center {
				t.Fatalf("follows link source should be center, got %s", l.Source)
			}
		case "followed_by":
			followedByLinks++
			if l.Target != center {
				t.Fatalf("followed_by link target should be center, got %s", l.Target)
			}
		}
	}

	if followsLinks != 1 {
		t.Fatalf("expected 1 follows link, got %d", followsLinks)
	}
	if followedByLinks != 1 {
		t.Fatalf("expected 1 followed_by link, got %d", followedByLinks)
	}
}

func TestWebOfTrustResponseStructure(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	center := padHex(9000)
	graph.AddFollow(center, padHex(9001))
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/weboftrust?pubkey="+center, nil)
	w := httptest.NewRecorder()
	handleWebOfTrust(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	// Verify all top-level fields exist
	for _, field := range []string{"pubkey", "score", "rank", "nodes", "links", "node_count", "link_count", "graph_size"} {
		if _, ok := resp[field]; !ok {
			t.Fatalf("missing field: %s", field)
		}
	}
}
