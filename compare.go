package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
)

// handleCompare shows the relationship between two pubkeys in the Web of Trust.
// GET /compare?a=<pubkey|npub>&b=<pubkey|npub>
func handleCompare(w http.ResponseWriter, r *http.Request) {
	rawA := r.URL.Query().Get("a")
	rawB := r.URL.Query().Get("b")
	if rawA == "" || rawB == "" {
		http.Error(w, `{"error":"both 'a' and 'b' parameters required"}`, http.StatusBadRequest)
		return
	}

	pubkeyA, err := resolvePubkey(rawA)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey a: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	pubkeyB, err := resolvePubkey(rawB)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid pubkey b: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	if pubkeyA == pubkeyB {
		http.Error(w, `{"error":"a and b are the same pubkey"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()

	// Scores and ranks
	rawScoreA, okA := graph.GetScore(pubkeyA)
	rawScoreB, okB := graph.GetScore(pubkeyB)
	normA := normalizeScore(rawScoreA, stats.Nodes)
	normB := normalizeScore(rawScoreB, stats.Nodes)
	rankA := graph.Rank(pubkeyA)
	rankB := graph.Rank(pubkeyB)
	pctA := graph.Percentile(pubkeyA)
	pctB := graph.Percentile(pubkeyB)

	// Follow relationships
	followsA := graph.GetFollows(pubkeyA)
	followsB := graph.GetFollows(pubkeyB)
	followersA := graph.GetFollowers(pubkeyA)
	followersB := graph.GetFollowers(pubkeyB)

	// Direct relationship
	aFollowsB := false
	bFollowsA := false
	for _, f := range followsA {
		if f == pubkeyB {
			aFollowsB = true
			break
		}
	}
	for _, f := range followsB {
		if f == pubkeyA {
			bFollowsA = true
			break
		}
	}

	relationship := "none"
	if aFollowsB && bFollowsA {
		relationship = "mutual"
	} else if aFollowsB {
		relationship = "a_follows_b"
	} else if bFollowsA {
		relationship = "b_follows_a"
	}

	// Shared follows (people both A and B follow)
	setA := make(map[string]bool, len(followsA))
	for _, f := range followsA {
		setA[f] = true
	}
	setB := make(map[string]bool, len(followsB))
	for _, f := range followsB {
		setB[f] = true
	}

	var sharedFollows []string
	for _, f := range followsA {
		if setB[f] {
			sharedFollows = append(sharedFollows, f)
		}
	}

	// Shared followers (people who follow both A and B)
	followerSetA := make(map[string]bool, len(followersA))
	for _, f := range followersA {
		followerSetA[f] = true
	}
	var sharedFollowers []string
	for _, f := range followersB {
		if followerSetA[f] {
			sharedFollowers = append(sharedFollowers, f)
		}
	}

	// Jaccard similarity of follow sets
	unionSize := len(setA) + len(setB) - len(sharedFollows)
	var jaccard float64
	if unionSize > 0 {
		jaccard = float64(len(sharedFollows)) / float64(unionSize)
	}

	// Sort shared follows by WoT score (highest first), return top 20
	type scoredPubkey struct {
		Pubkey   string `json:"pubkey"`
		WotScore int    `json:"wot_score"`
	}

	sortedSharedFollows := make([]scoredPubkey, len(sharedFollows))
	for i, pk := range sharedFollows {
		raw, _ := graph.GetScore(pk)
		sortedSharedFollows[i] = scoredPubkey{pk, normalizeScore(raw, stats.Nodes)}
	}
	sort.Slice(sortedSharedFollows, func(i, j int) bool {
		return sortedSharedFollows[i].WotScore > sortedSharedFollows[j].WotScore
	})
	if len(sortedSharedFollows) > 20 {
		sortedSharedFollows = sortedSharedFollows[:20]
	}

	sortedSharedFollowers := make([]scoredPubkey, len(sharedFollowers))
	for i, pk := range sharedFollowers {
		raw, _ := graph.GetScore(pk)
		sortedSharedFollowers[i] = scoredPubkey{pk, normalizeScore(raw, stats.Nodes)}
	}
	sort.Slice(sortedSharedFollowers, func(i, j int) bool {
		return sortedSharedFollowers[i].WotScore > sortedSharedFollowers[j].WotScore
	})
	if len(sortedSharedFollowers) > 20 {
		sortedSharedFollowers = sortedSharedFollowers[:20]
	}

	// Shortest path via BFS (already implemented)
	path, pathFound := bfsPath(pubkeyA, pubkeyB, 6)
	hops := 0
	if pathFound {
		hops = len(path) - 1
	}

	type profileInfo struct {
		Pubkey     string  `json:"pubkey"`
		InGraph    bool    `json:"in_graph"`
		WotScore   int     `json:"wot_score"`
		Rank       int     `json:"rank"`
		Percentile float64 `json:"percentile"`
		Follows    int     `json:"follows_count"`
		Followers  int     `json:"followers_count"`
	}

	resp := map[string]interface{}{
		"a": profileInfo{
			Pubkey:     pubkeyA,
			InGraph:    okA,
			WotScore:   normA,
			Rank:       rankA,
			Percentile: math.Round(pctA*1000) / 1000,
			Follows:    len(followsA),
			Followers:  len(followersA),
		},
		"b": profileInfo{
			Pubkey:     pubkeyB,
			InGraph:    okB,
			WotScore:   normB,
			Rank:       rankB,
			Percentile: math.Round(pctB*1000) / 1000,
			Follows:    len(followsB),
			Followers:  len(followersB),
		},
		"relationship":          relationship,
		"shared_follows_count":  len(sharedFollows),
		"shared_followers_count": len(sharedFollowers),
		"follow_similarity":     math.Round(jaccard*1000) / 1000,
		"top_shared_follows":    sortedSharedFollows,
		"top_shared_followers":  sortedSharedFollowers,
		"trust_path": map[string]interface{}{
			"found": pathFound,
			"hops":  hops,
			"path":  path,
		},
		"graph_size": stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
