package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// MuteStore stores kind 10000 mute list events.
// NIP-51 defines kind 10000 as a replaceable event containing p-tags of muted pubkeys.
type MuteStore struct {
	mu sync.RWMutex
	// pubkey -> set of muted pubkeys
	mutes map[string]map[string]bool
	// reverse index: target -> set of pubkeys who muted them
	mutedBy map[string]map[string]bool
}

func NewMuteStore() *MuteStore {
	return &MuteStore{
		mutes:   make(map[string]map[string]bool),
		mutedBy: make(map[string]map[string]bool),
	}
}

// Add stores a mute list, replacing any existing list for the author.
func (s *MuteStore) Add(author string, mutedPubkeys []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Remove old reverse entries
	if old, ok := s.mutes[author]; ok {
		for target := range old {
			if s.mutedBy[target] != nil {
				delete(s.mutedBy[target], author)
				if len(s.mutedBy[target]) == 0 {
					delete(s.mutedBy, target)
				}
			}
		}
	}

	// Store new mute list
	newSet := make(map[string]bool, len(mutedPubkeys))
	for _, pk := range mutedPubkeys {
		newSet[pk] = true
		if s.mutedBy[pk] == nil {
			s.mutedBy[pk] = make(map[string]bool)
		}
		s.mutedBy[pk][author] = true
	}
	s.mutes[author] = newSet
}

// GetMutes returns the pubkeys that a given pubkey has muted.
func (s *MuteStore) GetMutes(pubkey string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	set := s.mutes[pubkey]
	if len(set) == 0 {
		return nil
	}
	result := make([]string, 0, len(set))
	for pk := range set {
		result = append(result, pk)
	}
	return result
}

// GetMutedBy returns the pubkeys that have muted a given target.
func (s *MuteStore) GetMutedBy(target string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	set := s.mutedBy[target]
	if len(set) == 0 {
		return nil
	}
	result := make([]string, 0, len(set))
	for pk := range set {
		result = append(result, pk)
	}
	return result
}

// TotalMuters returns the number of unique pubkeys with mute lists.
func (s *MuteStore) TotalMuters() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.mutes)
}

// TotalMuted returns the number of unique pubkeys that have been muted by someone.
func (s *MuteStore) TotalMuted() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.mutedBy)
}

// parseMuteList extracts muted pubkeys from a kind 10000 event.
func parseMuteList(ev *nostr.Event) []string {
	if ev.Kind != 10000 {
		return nil
	}

	var muted []string
	for _, tag := range ev.Tags {
		if len(tag) >= 2 && tag[0] == "p" && len(tag[1]) == 64 {
			muted = append(muted, tag[1])
		}
	}
	return muted
}

// consumeMuteLists fetches kind 10000 events from relays and populates the MuteStore.
func consumeMuteLists(ctx context.Context, store *MuteStore) {
	log.Printf("Consuming mute lists (kind 10000) from relays...")

	pool := nostr.NewSimplePool(ctx)

	since := nostr.Timestamp(time.Now().Add(-90 * 24 * time.Hour).Unix())
	filter := nostr.Filter{
		Kinds: []int{10000},
		Since: &since,
		Limit: 10000,
	}

	total := 0
	for ev := range pool.SubManyEose(ctx, relays, nostr.Filters{filter}) {
		muted := parseMuteList(ev.Event)
		if len(muted) > 0 {
			store.Add(ev.Event.PubKey, muted)
			total++
		}
	}

	log.Printf("Consumed %d mute lists, %d unique muted pubkeys", store.TotalMuters(), store.TotalMuted())
}

// BlockedEntry describes one entry in the /blocked response.
type BlockedEntry struct {
	Pubkey   string `json:"pubkey"`
	WoTScore int    `json:"wot_score"`
	InGraph  bool   `json:"in_graph"`
}

// BlockedResponse is the API response for the /blocked endpoint.
type BlockedResponse struct {
	Mode           string         `json:"mode"`
	Pubkey         string         `json:"pubkey"`
	MutedPubkeys   []BlockedEntry `json:"muted_pubkeys,omitempty"`
	MutedByPubkeys []BlockedEntry `json:"muted_by_pubkeys,omitempty"`
	MutedCount     int            `json:"muted_count"`
	MutedByCount   int            `json:"muted_by_count"`
	CommunitySignal string        `json:"community_signal"`
}

// handleBlocked serves GET /blocked?pubkey=X or GET /blocked?target=X
//
// pubkey mode: returns who pubkey X has blocked (their mute list)
// target mode: returns who has blocked target X (reverse mute lookup — community moderation signal)
func handleBlocked(w http.ResponseWriter, r *http.Request) {
	rawPubkey := r.URL.Query().Get("pubkey")
	rawTarget := r.URL.Query().Get("target")

	if rawPubkey == "" && rawTarget == "" {
		http.Error(w, `{"error":"pubkey or target parameter required"}`, http.StatusBadRequest)
		return
	}

	graphSize := graph.Stats().Nodes

	if rawPubkey != "" {
		pubkey, err := resolvePubkey(rawPubkey)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		muted := muteStore.GetMutes(pubkey)
		entries := enrichWithWoT(muted, graphSize)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BlockedResponse{
			Mode:         "mutes",
			Pubkey:       pubkey,
			MutedPubkeys: entries,
			MutedCount:   len(entries),
		})
		return
	}

	target, err := resolvePubkey(rawTarget)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	mutedBy := muteStore.GetMutedBy(target)
	entries := enrichWithWoT(mutedBy, graphSize)

	// Sort by WoT score descending — high-trust blockers first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].WoTScore > entries[j].WoTScore
	})

	signal := communitySignal(entries)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BlockedResponse{
		Mode:            "muted_by",
		Pubkey:          target,
		MutedByPubkeys:  entries,
		MutedByCount:    len(entries),
		CommunitySignal: signal,
	})
}

// enrichWithWoT takes a list of pubkeys and adds WoT score info.
func enrichWithWoT(pubkeys []string, graphSize int) []BlockedEntry {
	entries := make([]BlockedEntry, 0, len(pubkeys))
	for _, pk := range pubkeys {
		rawScore, found := graph.GetScore(pk)
		score := normalizeScore(rawScore, graphSize)
		entries = append(entries, BlockedEntry{
			Pubkey:   pk,
			WoTScore: score,
			InGraph:  found,
		})
	}
	return entries
}

// communitySignal interprets the muted_by list to produce a trust signal.
func communitySignal(entries []BlockedEntry) string {
	if len(entries) == 0 {
		return "no_data"
	}

	highTrust := 0
	for _, e := range entries {
		if e.WoTScore >= 20 {
			highTrust++
		}
	}

	if highTrust >= 5 {
		return "strong_negative"
	}
	if highTrust >= 2 {
		return "moderate_negative"
	}
	if len(entries) >= 3 {
		return "weak_negative"
	}
	return "insufficient_data"
}
