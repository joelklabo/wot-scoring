package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestVerifyMethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest("GET", "/verify", nil)
	w := httptest.NewRecorder()
	handleVerify(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestVerifyInvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/verify", bytes.NewBufferString("not json"))
	w := httptest.NewRecorder()
	handleVerify(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestVerifyWrongKind(t *testing.T) {
	ev := nostr.Event{Kind: 1}
	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestVerifyConsistentAssertion(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	f1 := "1111111111111111111111111111111111111111111111111111111111111111"
	f2 := "2222222222222222222222222222222222222222222222222222222222222222"
	f3 := "3333333333333333333333333333333333333333333333333333333333333333"
	graph.AddFollow(f1, target)
	graph.AddFollow(f2, target)
	graph.AddFollow(f3, target)
	graph.ComputePageRank(20, 0.85)

	// Create a signed event with claims matching our graph
	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      30382,
		Tags: nostr.Tags{
			{"d", target},
			{"rank", "0"}, // small graph = low score, we just test it doesn't crash
			{"followers", "3"},
		},
	}
	ev.Sign(sk)

	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp VerifyResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if !resp.Valid {
		t.Fatal("expected Valid=true for properly signed event")
	}
	if resp.SubjectPubkey != target {
		t.Fatalf("expected subject %s, got %s", target, resp.SubjectPubkey)
	}
	if resp.ProviderPubkey != pub {
		t.Fatalf("expected provider %s, got %s", pub, resp.ProviderPubkey)
	}

	// Followers should match exactly
	foundFollowers := false
	for _, c := range resp.Checks {
		if c.Field == "followers" {
			foundFollowers = true
			if c.Status != "match" {
				t.Fatalf("followers check: expected match, got %s (claimed=%v, observed=%v)", c.Status, c.Claimed, c.Observed)
			}
		}
	}
	if !foundFollowers {
		t.Fatal("no followers check found in response")
	}
}

func TestVerifyDivergentAssertion(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	f1 := "1111111111111111111111111111111111111111111111111111111111111111"
	graph.AddFollow(f1, target)
	graph.ComputePageRank(20, 0.85)

	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      30382,
		Tags: nostr.Tags{
			{"d", target},
			{"followers", "10000"}, // wildly wrong
		},
	}
	ev.Sign(sk)

	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp VerifyResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if !resp.Valid {
		t.Fatal("expected Valid=true (signature is fine, claims are divergent)")
	}
	if resp.Verdict != "divergent" {
		t.Fatalf("expected verdict=divergent, got %s", resp.Verdict)
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	ev := nostr.Event{
		PubKey:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CreatedAt: nostr.Now(),
		Kind:      30382,
		Tags:      nostr.Tags{{"d", "test"}},
		Sig:       "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
	}
	// Compute the correct ID so CheckID passes
	ev.ID = ev.GetID()

	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp VerifyResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Valid {
		t.Fatal("expected Valid=false for bad signature")
	}
	if resp.Verdict != "invalid" {
		t.Fatalf("expected verdict=invalid, got %s", resp.Verdict)
	}
}

func TestVerifyMissingDTag(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      30382,
		Tags:      nostr.Tags{{"rank", "50"}}, // no d tag
	}
	ev.Sign(sk)

	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)

	var resp VerifyResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Verdict != "invalid" {
		t.Fatalf("expected verdict=invalid for missing d tag, got %s", resp.Verdict)
	}
}

func TestVerifyUnverifiableAssertion(t *testing.T) {
	sk := nostr.GeneratePrivateKey()
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      30382,
		Tags: nostr.Tags{
			{"d", "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"},
			// no rank or followers tags — nothing verifiable
		},
	}
	ev.Sign(sk)

	body, _ := json.Marshal(ev)
	req := httptest.NewRequest("POST", "/verify", bytes.NewBuffer(body))
	w := httptest.NewRecorder()
	handleVerify(w, req)

	var resp VerifyResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.Verdict != "unverifiable" {
		t.Fatalf("expected verdict=unverifiable, got %s", resp.Verdict)
	}
}

func TestVerifyNumericField(t *testing.T) {
	tests := []struct {
		name     string
		claimed  int
		observed int
		tol      int
		want     string
	}{
		{"exact match", 50, 50, 15, "match"},
		{"both zero", 0, 0, 15, "match"},
		{"close within tolerance", 100, 110, 15, "close"},
		{"divergent beyond tolerance", 100, 200, 15, "divergent"},
		{"close at boundary", 100, 115, 15, "close"},
		{"divergent just past boundary", 100, 120, 15, "divergent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := verifyNumericField("test", tt.claimed, tt.observed, tt.tol)
			if check.Status != tt.want {
				t.Fatalf("claimed=%d observed=%d tol=%d%%: got %s, want %s",
					tt.claimed, tt.observed, tt.tol, check.Status, tt.want)
			}
		})
	}
}
