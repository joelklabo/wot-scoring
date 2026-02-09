package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NIP05Response is the standard NIP-05 JSON response from a domain's .well-known/nostr.json
type NIP05Response struct {
	Names  map[string]string   `json:"names"`
	Relays map[string][]string `json:"relays,omitempty"`
}

// resolveNIP05 resolves a NIP-05 identifier (user@domain) to a hex pubkey.
func resolveNIP05(identifier string) (pubkey string, relays []string, err error) {
	parts := strings.SplitN(identifier, "@", 2)
	if len(parts) != 2 {
		return "", nil, fmt.Errorf("invalid NIP-05 identifier: must be name@domain")
	}
	name, domain := parts[0], parts[1]
	if name == "" || domain == "" {
		return "", nil, fmt.Errorf("invalid NIP-05 identifier: name and domain required")
	}

	url := fmt.Sprintf("https://%s/.well-known/nostr.json?name=%s", domain, name)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("failed to fetch NIP-05: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("NIP-05 endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return "", nil, fmt.Errorf("failed to read NIP-05 response: %w", err)
	}

	var nip05 NIP05Response
	if err := json.Unmarshal(body, &nip05); err != nil {
		return "", nil, fmt.Errorf("invalid NIP-05 JSON: %w", err)
	}

	pk, ok := nip05.Names[name]
	if !ok {
		return "", nil, fmt.Errorf("name %q not found in NIP-05 response", name)
	}

	if len(pk) != 64 {
		return "", nil, fmt.Errorf("invalid pubkey in NIP-05 response: expected 64 hex chars, got %d", len(pk))
	}

	var userRelays []string
	if nip05.Relays != nil {
		userRelays = nip05.Relays[pk]
	}

	return pk, userRelays, nil
}

// handleNIP05 handles GET /nip05?id=user@domain
// Resolves a NIP-05 identifier, then returns the WoT trust profile for the resolved pubkey.
func handleNIP05(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"id parameter required (e.g. user@domain.com)"}`, http.StatusBadRequest)
		return
	}

	pubkey, nip05Relays, err := resolveNIP05(id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"NIP-05 resolution failed: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	score, found := graph.GetScore(pubkey)
	stats := graph.Stats()
	m := meta.Get(pubkey)
	internalScore := normalizeScore(score, stats.Nodes)
	extAssertions := externalAssertions.GetForSubject(pubkey)
	compositeScore, extSources := CompositeScore(internalScore, extAssertions, externalAssertions)

	trustLevel := nip05TrustLevel(internalScore, found)

	resp := map[string]interface{}{
		"nip05":       id,
		"pubkey":      pubkey,
		"verified":    true,
		"trust_level": trustLevel,
		"score":       internalScore,
		"raw_score":   score,
		"found":       found,
		"graph_size":  stats.Nodes,
		"followers":   m.Followers,
		"post_count":  m.PostCount,
		"reply_count": m.ReplyCount,
		"reactions":   m.ReactionsRecd,
	}

	if len(nip05Relays) > 0 {
		resp["nip05_relays"] = nip05Relays
	}

	if len(extSources) > 0 {
		resp["composite_score"] = compositeScore
		resp["external_assertions"] = extSources
	}

	// NIP-85 extended metadata
	if topics := m.TopTopics(5); len(topics) > 0 {
		resp["topics"] = topics
	}
	activeStart, activeEnd := m.ActiveHours()
	if activeStart != activeEnd {
		resp["active_hours_start"] = activeStart
		resp["active_hours_end"] = activeEnd
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// nip05TrustLevel returns a trust level string based on the normalized score.
func nip05TrustLevel(score int, found bool) string {
	if !found {
		return "unknown"
	}
	switch {
	case score >= 80:
		return "highly_trusted"
	case score >= 50:
		return "trusted"
	case score >= 20:
		return "moderate"
	case score > 0:
		return "low"
	default:
		return "untrusted"
	}
}

// nip05Result holds the result of a single NIP-05 bulk resolution.
type nip05Result struct {
	Index  int
	Result map[string]interface{}
}

// handleNIP05Batch handles POST /nip05/batch
// Resolves multiple NIP-05 identifiers concurrently and returns trust profiles.
func handleNIP05Batch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Identifiers []string `json:"identifiers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Identifiers) == 0 {
		http.Error(w, `{"error":"identifiers array required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Identifiers) > 50 {
		http.Error(w, `{"error":"max 50 identifiers per request"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()

	// Resolve all NIP-05 identifiers concurrently
	var mu sync.Mutex
	results := make([]map[string]interface{}, len(req.Identifiers))
	var wg sync.WaitGroup

	for i, id := range req.Identifiers {
		wg.Add(1)
		go func(idx int, identifier string) {
			defer wg.Done()

			entry := map[string]interface{}{
				"nip05": identifier,
			}

			pubkey, nip05Relays, err := resolveNIP05(identifier)
			if err != nil {
				entry["error"] = err.Error()
				entry["verified"] = false
				mu.Lock()
				results[idx] = entry
				mu.Unlock()
				return
			}

			score, found := graph.GetScore(pubkey)
			m := meta.Get(pubkey)
			internalScore := normalizeScore(score, stats.Nodes)

			entry["pubkey"] = pubkey
			entry["verified"] = true
			entry["trust_level"] = nip05TrustLevel(internalScore, found)
			entry["score"] = internalScore
			entry["found"] = found
			entry["followers"] = m.Followers

			if len(nip05Relays) > 0 {
				entry["nip05_relays"] = nip05Relays
			}

			mu.Lock()
			results[idx] = entry
			mu.Unlock()
		}(i, id)
	}

	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":    results,
		"count":      len(results),
		"graph_size": stats.Nodes,
	})
}
