package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestMuteStoreAddAndGet(t *testing.T) {
	store := NewMuteStore()

	author := padHex(5001)
	target1 := padHex(5002)
	target2 := padHex(5003)

	store.Add(author, []string{target1, target2})

	mutes := store.GetMutes(author)
	if len(mutes) != 2 {
		t.Fatalf("expected 2 mutes, got %d", len(mutes))
	}

	mutedBy := store.GetMutedBy(target1)
	if len(mutedBy) != 1 {
		t.Fatalf("expected 1 muter, got %d", len(mutedBy))
	}
	if mutedBy[0] != author {
		t.Fatalf("expected muter %s, got %s", author, mutedBy[0])
	}
}

func TestMuteStoreReplacesOldList(t *testing.T) {
	store := NewMuteStore()

	author := padHex(5010)
	old := padHex(5011)
	new1 := padHex(5012)

	store.Add(author, []string{old})
	store.Add(author, []string{new1})

	mutes := store.GetMutes(author)
	if len(mutes) != 1 {
		t.Fatalf("expected 1 mute after replace, got %d", len(mutes))
	}
	if mutes[0] != new1 {
		t.Fatalf("expected mute %s, got %s", new1, mutes[0])
	}

	// Old target should no longer be muted
	mutedBy := store.GetMutedBy(old)
	if len(mutedBy) != 0 {
		t.Fatalf("expected old target to have 0 muters, got %d", len(mutedBy))
	}
}

func TestMuteStoreReverseIndex(t *testing.T) {
	store := NewMuteStore()

	target := padHex(5020)
	blocker1 := padHex(5021)
	blocker2 := padHex(5022)
	blocker3 := padHex(5023)

	store.Add(blocker1, []string{target})
	store.Add(blocker2, []string{target, padHex(5024)})
	store.Add(blocker3, []string{target})

	mutedBy := store.GetMutedBy(target)
	if len(mutedBy) != 3 {
		t.Fatalf("expected 3 muters, got %d", len(mutedBy))
	}
}

func TestMuteStoreCounts(t *testing.T) {
	store := NewMuteStore()

	store.Add(padHex(5030), []string{padHex(5031), padHex(5032)})
	store.Add(padHex(5033), []string{padHex(5031)})

	if store.TotalMuters() != 2 {
		t.Fatalf("expected 2 muters, got %d", store.TotalMuters())
	}
	if store.TotalMuted() != 2 {
		t.Fatalf("expected 2 muted, got %d", store.TotalMuted())
	}
}

func TestMuteStoreEmptyLookup(t *testing.T) {
	store := NewMuteStore()

	mutes := store.GetMutes(padHex(9999))
	if mutes != nil {
		t.Fatalf("expected nil for unknown pubkey, got %v", mutes)
	}

	mutedBy := store.GetMutedBy(padHex(9999))
	if mutedBy != nil {
		t.Fatalf("expected nil for unknown target, got %v", mutedBy)
	}
}

func TestParseMuteList(t *testing.T) {
	target1 := padHex(6001)
	target2 := padHex(6002)

	ev := &nostr.Event{
		Kind: 10000,
		Tags: nostr.Tags{
			{"p", target1},
			{"p", target2},
			{"e", "someeventhash"}, // non-p tags should be ignored
			{"p", "short"},          // invalid pubkey (not 64 chars) should be ignored
		},
	}

	muted := parseMuteList(ev)
	if len(muted) != 2 {
		t.Fatalf("expected 2 muted pubkeys, got %d", len(muted))
	}
}

func TestParseMuteListWrongKind(t *testing.T) {
	ev := &nostr.Event{
		Kind: 3, // kind 3 is follow list, not mute list
		Tags: nostr.Tags{
			{"p", padHex(6010)},
		},
	}

	muted := parseMuteList(ev)
	if muted != nil {
		t.Fatalf("expected nil for wrong kind, got %v", muted)
	}
}

func TestBlockedEndpointMissingParams(t *testing.T) {
	req := httptest.NewRequest("GET", "/blocked", nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBlockedEndpointInvalidPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/blocked?pubkey=npub1invalid", nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestBlockedEndpointMutesMode(t *testing.T) {
	oldMuteStore := muteStore
	oldGraph := graph
	muteStore = NewMuteStore()
	graph = NewGraph()
	defer func() {
		muteStore = oldMuteStore
		graph = oldGraph
	}()

	author := padHex(7001)
	target1 := padHex(7002)
	target2 := padHex(7003)

	muteStore.Add(author, []string{target1, target2})

	req := httptest.NewRequest("GET", "/blocked?pubkey="+author, nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp BlockedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Mode != "mutes" {
		t.Fatalf("expected mode 'mutes', got %q", resp.Mode)
	}
	if resp.MutedCount != 2 {
		t.Fatalf("expected muted_count 2, got %d", resp.MutedCount)
	}
	if len(resp.MutedPubkeys) != 2 {
		t.Fatalf("expected 2 muted_pubkeys, got %d", len(resp.MutedPubkeys))
	}
}

func TestBlockedEndpointMutedByMode(t *testing.T) {
	oldMuteStore := muteStore
	oldGraph := graph
	muteStore = NewMuteStore()
	graph = NewGraph()
	defer func() {
		muteStore = oldMuteStore
		graph = oldGraph
	}()

	target := padHex(7010)
	blocker1 := padHex(7011)
	blocker2 := padHex(7012)

	muteStore.Add(blocker1, []string{target})
	muteStore.Add(blocker2, []string{target})

	req := httptest.NewRequest("GET", "/blocked?target="+target, nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp BlockedResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Mode != "muted_by" {
		t.Fatalf("expected mode 'muted_by', got %q", resp.Mode)
	}
	if resp.MutedByCount != 2 {
		t.Fatalf("expected muted_by_count 2, got %d", resp.MutedByCount)
	}
}

func TestBlockedEndpointCommunitySignalStrong(t *testing.T) {
	oldMuteStore := muteStore
	oldGraph := graph
	muteStore = NewMuteStore()
	graph = NewGraph()
	defer func() {
		muteStore = oldMuteStore
		graph = oldGraph
	}()

	target := padHex(7020)

	// Create 6 high-WoT blockers with concentrated followers.
	// Each follower also follows the next follower in the chain,
	// creating outgoing links so they aren't dangling nodes.
	for i := 0; i < 6; i++ {
		blocker := padHex(7030 + i)
		muteStore.Add(blocker, []string{target})
		for j := 0; j < 30; j++ {
			follower := padHex(8000 + i*30 + j)
			graph.AddFollow(follower, blocker)
			// Chain follows to avoid dangling nodes
			if j > 0 {
				prev := padHex(8000 + i*30 + j - 1)
				graph.AddFollow(follower, prev)
			}
		}
	}
	// Cross-follow between blockers to boost their scores
	for i := 0; i < 6; i++ {
		for j := i + 1; j < 6; j++ {
			graph.AddFollow(padHex(7030+i), padHex(7030+j))
			graph.AddFollow(padHex(7030+j), padHex(7030+i))
		}
	}
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/blocked?target="+target, nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)

	var resp BlockedResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.CommunitySignal != "strong_negative" {
		t.Fatalf("expected 'strong_negative' community signal, got %q (muted_by_count=%d)", resp.CommunitySignal, resp.MutedByCount)
	}
}

func TestBlockedEndpointNoData(t *testing.T) {
	oldMuteStore := muteStore
	oldGraph := graph
	muteStore = NewMuteStore()
	graph = NewGraph()
	defer func() {
		muteStore = oldMuteStore
		graph = oldGraph
	}()

	pubkey := padHex(7040)

	req := httptest.NewRequest("GET", "/blocked?pubkey="+pubkey, nil)
	w := httptest.NewRecorder()
	handleBlocked(w, req)

	var resp BlockedResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if resp.MutedCount != 0 {
		t.Fatalf("expected muted_count 0, got %d", resp.MutedCount)
	}
}

func TestCommunitySignal(t *testing.T) {
	tests := []struct {
		name     string
		entries  []BlockedEntry
		expected string
	}{
		{"no data", nil, "no_data"},
		{"one low-trust blocker", []BlockedEntry{{WoTScore: 5}}, "insufficient_data"},
		{"three low-trust blockers", []BlockedEntry{{WoTScore: 5}, {WoTScore: 10}, {WoTScore: 8}}, "weak_negative"},
		{"two high-trust blockers", []BlockedEntry{{WoTScore: 40}, {WoTScore: 35}}, "moderate_negative"},
		{"five high-trust blockers", []BlockedEntry{
			{WoTScore: 50}, {WoTScore: 45}, {WoTScore: 40}, {WoTScore: 35}, {WoTScore: 30},
		}, "strong_negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := communitySignal(tt.entries)
			if got != tt.expected {
				t.Fatalf("communitySignal() = %q, want %q", got, tt.expected)
			}
		})
	}
}
