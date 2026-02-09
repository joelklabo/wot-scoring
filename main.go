package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

var relays = []string{
	"wss://relay.damus.io",
	"wss://nos.lol",
	"wss://relay.primal.net",
}

// Graph stores the follow relationships
type Graph struct {
	mu          sync.RWMutex
	follows     map[string][]string    // pubkey -> list of followed pubkeys
	followers   map[string][]string    // pubkey -> list of followers
	scores      map[string]float64     // pubkey -> PageRank score
	followTimes map[string]time.Time   // "from:to" -> when the follow was created
	lastBuild   time.Time
}

func NewGraph() *Graph {
	return &Graph{
		follows:     make(map[string][]string),
		followers:   make(map[string][]string),
		scores:      make(map[string]float64),
		followTimes: make(map[string]time.Time),
	}
}

func (g *Graph) AddFollow(from, to string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.follows[from] = append(g.follows[from], to)
	g.followers[to] = append(g.followers[to], from)
}

// PageRank computes scores over the follow graph
func (g *Graph) ComputePageRank(iterations int, damping float64) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Collect all nodes
	nodes := make(map[string]bool)
	for k, vs := range g.follows {
		nodes[k] = true
		for _, v := range vs {
			nodes[v] = true
		}
	}

	n := float64(len(nodes))
	if n == 0 {
		return
	}

	// Initialize scores uniformly
	scores := make(map[string]float64)
	for node := range nodes {
		scores[node] = 1.0 / n
	}

	for i := 0; i < iterations; i++ {
		newScores := make(map[string]float64)
		for node := range nodes {
			sum := 0.0
			for _, follower := range g.followers[node] {
				outDegree := len(g.follows[follower])
				if outDegree > 0 {
					sum += scores[follower] / float64(outDegree)
				}
			}
			newScores[node] = (1-damping)/n + damping*sum
		}
		scores = newScores
	}

	g.scores = scores
	g.lastBuild = time.Now()
}

func (g *Graph) GetScore(pubkey string) (float64, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s, ok := g.scores[pubkey]
	return s, ok
}

func (g *Graph) GetFollows(pubkey string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.follows[pubkey]
}

func (g *Graph) GetFollowers(pubkey string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.followers[pubkey]
}

func (g *Graph) TopN(n int) []ScoreEntry {
	g.mu.RLock()
	defer g.mu.RUnlock()

	entries := make([]ScoreEntry, 0, len(g.scores))
	for k, v := range g.scores {
		entries = append(entries, ScoreEntry{Pubkey: k, Score: v})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Score > entries[j].Score
	})
	if n > 0 && n < len(entries) {
		entries = entries[:n]
	}
	return entries
}

// AllFollowers returns all pubkeys that have a follows list (active users with contact lists).
func (g *Graph) AllFollowers() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	result := make([]string, 0, len(g.follows))
	for k := range g.follows {
		result = append(result, k)
	}
	return result
}

// Percentile returns the percentile rank of a pubkey (0.0-1.0).
// A percentile of 0.95 means this pubkey scores higher than 95% of all nodes.
func (g *Graph) Percentile(pubkey string) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()

	score, ok := g.scores[pubkey]
	if !ok || len(g.scores) == 0 {
		return 0
	}

	below := 0
	for _, s := range g.scores {
		if s < score {
			below++
		}
	}
	return float64(below) / float64(len(g.scores))
}

// Rank returns the 1-based rank of a pubkey among all scored nodes (1 = highest).
func (g *Graph) Rank(pubkey string) int {
	g.mu.RLock()
	defer g.mu.RUnlock()

	score, ok := g.scores[pubkey]
	if !ok {
		return 0
	}

	rank := 1
	for _, s := range g.scores {
		if s > score {
			rank++
		}
	}
	return rank
}

func (g *Graph) Stats() GraphStats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return GraphStats{
		Nodes:     len(g.scores),
		Edges:     countEdges(g.follows),
		LastBuild: g.lastBuild,
	}
}

func countEdges(follows map[string][]string) int {
	total := 0
	for _, vs := range follows {
		total += len(vs)
	}
	return total
}

type ScoreEntry struct {
	Pubkey string  `json:"pubkey"`
	Score  float64 `json:"score"`
	Rank   int     `json:"rank,omitempty"`
}

type GraphStats struct {
	Nodes     int       `json:"nodes"`
	Edges     int       `json:"edges"`
	LastBuild time.Time `json:"last_build"`
}

var graph = NewGraph()
var meta = NewMetaStore()
var events = NewEventStore()
var external = NewExternalStore()
var externalAssertions = NewAssertionStore()
var authStore = NewAuthStore()
var communities = NewCommunityDetector()
var startTime = time.Now()

func crawlFollows(ctx context.Context, seedPubkeys []string, depth int) {
	pool := nostr.NewSimplePool(ctx)
	seen := make(map[string]bool)
	queue := seedPubkeys

	for d := 0; d < depth && len(queue) > 0; d++ {
		log.Printf("Crawl depth %d: %d pubkeys to process", d, len(queue))
		var nextQueue []string

		// Process in batches
		batchSize := 50
		for i := 0; i < len(queue); i += batchSize {
			end := i + batchSize
			if end > len(queue) {
				end = len(queue)
			}
			batch := queue[i:end]

			filter := nostr.Filter{
				Kinds:   []int{3}, // kind 3 = contact list
				Authors: batch,
				Limit:   len(batch),
			}

			evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
			for ev := range evCh {
				author := ev.Event.PubKey
				if seen[author] {
					continue
				}
				seen[author] = true

				eventTime := ev.Event.CreatedAt.Time()
				for _, tag := range ev.Event.Tags {
					if tag[0] == "p" && len(tag) >= 2 {
						target := tag[1]
						graph.AddFollowWithTime(author, target, eventTime)
						if !seen[target] {
							nextQueue = append(nextQueue, target)
						}
					}
				}
			}
		}
		queue = nextQueue
		log.Printf("Crawl depth %d complete: graph has %d nodes, %d edges", d, len(seen), countEdges(graph.follows))
	}
}

func normalizeScore(raw float64, total int) int {
	// Normalize to 0-100 scale using log transformation
	if total == 0 || raw == 0 {
		return 0
	}
	avg := 1.0 / float64(total)
	ratio := raw / avg
	score := math.Log10(ratio+1) * 25
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return int(math.Round(score))
}

// resolvePubkey converts npub to hex if needed, returns hex pubkey or error.
func resolvePubkey(input string) (string, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "npub") {
		_, v, err := nip19.Decode(input)
		if err != nil {
			return "", fmt.Errorf("invalid npub: %w", err)
		}
		return v.(string), nil
	}
	return input, nil
}

func handleScore(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	score, ok := graph.GetScore(pubkey)
	stats := graph.Stats()
	m := meta.Get(pubkey)

	internalScore := normalizeScore(score, stats.Nodes)
	extAssertions := externalAssertions.GetForSubject(pubkey)
	compositeScore, extSources := CompositeScore(internalScore, extAssertions, externalAssertions)

	resp := map[string]interface{}{
		"pubkey":     pubkey,
		"raw_score":  score,
		"score":      internalScore,
		"found":      ok,
		"graph_size": stats.Nodes,
		"followers":     m.Followers,
		"post_count":    m.PostCount,
		"reply_count":   m.ReplyCount,
		"reactions":     m.ReactionsRecd,
		"zap_amount":    m.ZapAmtRecd,
		"zap_count":     m.ZapCntRecd,
	}

	if len(extSources) > 0 {
		resp["composite_score"] = compositeScore
		resp["external_assertions"] = extSources
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAudit explains why a pubkey has its score, breaking down all components.
func handleAudit(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	rawScore, found := graph.GetScore(pubkey)
	stats := graph.Stats()
	m := meta.Get(pubkey)
	internalScore := normalizeScore(rawScore, stats.Nodes)

	follows := graph.GetFollows(pubkey)
	followers := graph.GetFollowers(pubkey)
	percentile := graph.Percentile(pubkey)
	rank := graph.Rank(pubkey)

	// PageRank breakdown
	pagerank := map[string]interface{}{
		"raw_score":        rawScore,
		"normalized_score": internalScore,
		"follower_count":   len(followers),
		"following_count":  len(follows),
		"percentile":       math.Round(percentile*10000) / 10000,
		"rank":             rank,
		"algorithm":        "PageRank",
		"damping":          0.85,
		"iterations":       20,
		"normalization":    "log10(raw/avg + 1) * 25, capped at 100",
	}

	// Engagement breakdown
	engagement := map[string]interface{}{
		"posts":              m.PostCount,
		"replies":            m.ReplyCount,
		"reactions_received": m.ReactionsRecd,
		"reactions_sent":     m.ReactionsSent,
		"zaps_received_sats": m.ZapAmtRecd,
		"zaps_received_count": m.ZapCntRecd,
		"zaps_sent_sats":     m.ZapAmtSent,
		"zaps_sent_count":    m.ZapCntSent,
	}
	if m.FirstCreated > 0 {
		engagement["first_event"] = time.Unix(m.FirstCreated, 0).UTC().Format(time.RFC3339)
	}

	// External assertions breakdown
	extAssertions := externalAssertions.GetForSubject(pubkey)
	compositeScore, extSources := CompositeScore(internalScore, extAssertions, externalAssertions)

	var composite map[string]interface{}
	if len(extSources) > 0 {
		normalizedSum := 0
		for _, src := range extSources {
			normalizedSum += src["normalized_rank"].(int)
		}
		externalAvg := float64(normalizedSum) / float64(len(extSources))

		composite = map[string]interface{}{
			"final_score":     compositeScore,
			"internal_weight": 0.70,
			"external_weight": 0.30,
			"internal_score":  internalScore,
			"external_average": math.Round(externalAvg*100) / 100,
			"external_sources": extSources,
		}
	}

	// Top followers by WoT score (up to 5)
	type followerScore struct {
		Pubkey string  `json:"pubkey"`
		Score  int     `json:"score"`
	}
	topFollowers := make([]followerScore, 0)
	for _, f := range followers {
		s, ok := graph.GetScore(f)
		if ok {
			topFollowers = append(topFollowers, followerScore{
				Pubkey: f,
				Score:  normalizeScore(s, stats.Nodes),
			})
		}
	}
	sort.Slice(topFollowers, func(i, j int) bool {
		return topFollowers[i].Score > topFollowers[j].Score
	})
	if len(topFollowers) > 5 {
		topFollowers = topFollowers[:5]
	}

	resp := map[string]interface{}{
		"pubkey":         pubkey,
		"found":          found,
		"pagerank":       pagerank,
		"engagement":     engagement,
		"top_followers":  topFollowers,
		"graph_context": map[string]interface{}{
			"total_nodes":  stats.Nodes,
			"total_edges":  stats.Edges,
			"last_rebuild": stats.LastBuild.UTC().Format(time.RFC3339),
		},
	}

	if composite != nil {
		resp["composite"] = composite
	} else {
		resp["final_score"] = internalScore
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Pubkeys []string `json:"pubkeys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Pubkeys) == 0 {
		http.Error(w, `{"error":"pubkeys array required"}`, http.StatusBadRequest)
		return
	}
	if len(req.Pubkeys) > 100 {
		http.Error(w, `{"error":"max 100 pubkeys per request"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()
	results := make([]map[string]interface{}, 0, len(req.Pubkeys))
	for _, raw := range req.Pubkeys {
		pubkey, err := resolvePubkey(raw)
		if err != nil {
			results = append(results, map[string]interface{}{
				"pubkey": raw,
				"error":  err.Error(),
			})
			continue
		}

		score, ok := graph.GetScore(pubkey)
		internalScore := normalizeScore(score, stats.Nodes)
		m := meta.Get(pubkey)
		extAssertions := externalAssertions.GetForSubject(pubkey)
		compositeScore, _ := CompositeScore(internalScore, extAssertions, externalAssertions)

		entry := map[string]interface{}{
			"pubkey":    pubkey,
			"score":     internalScore,
			"found":     ok,
			"followers": m.Followers,
		}
		if len(extAssertions) > 0 {
			entry["composite_score"] = compositeScore
		}
		results = append(results, entry)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"results":    results,
		"graph_size": stats.Nodes,
	})
}

func handlePersonalized(w http.ResponseWriter, r *http.Request) {
	viewerRaw := r.URL.Query().Get("viewer")
	targetRaw := r.URL.Query().Get("target")
	if viewerRaw == "" || targetRaw == "" {
		http.Error(w, `{"error":"viewer and target parameters required"}`, http.StatusBadRequest)
		return
	}

	viewer, err := resolvePubkey(viewerRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid viewer: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	target, err := resolvePubkey(targetRaw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"invalid target: %s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	stats := graph.Stats()
	viewerFollows := graph.GetFollows(viewer)
	targetFollows := graph.GetFollows(target)
	targetFollowers := graph.GetFollowers(target)

	// Check direct follow relationship
	viewerFollowsTarget := false
	targetFollowsViewer := false
	viewerFollowSet := make(map[string]bool, len(viewerFollows))
	for _, f := range viewerFollows {
		viewerFollowSet[f] = true
		if f == target {
			viewerFollowsTarget = true
		}
	}
	for _, f := range targetFollows {
		if f == viewer {
			targetFollowsViewer = true
			break
		}
	}

	// Count shared follows (people both viewer and target follow)
	targetFollowSet := make(map[string]bool, len(targetFollows))
	for _, f := range targetFollows {
		targetFollowSet[f] = true
	}
	sharedFollows := 0
	for f := range viewerFollowSet {
		if targetFollowSet[f] {
			sharedFollows++
		}
	}

	// Count how many of the viewer's follows also follow the target
	trustedFollowers := 0
	trustedFollowerList := make([]string, 0)
	for _, follower := range targetFollowers {
		if viewerFollowSet[follower] {
			trustedFollowers++
			if len(trustedFollowerList) < 10 {
				trustedFollowerList = append(trustedFollowerList, follower)
			}
		}
	}

	// Global score
	rawScore, found := graph.GetScore(target)
	globalScore := normalizeScore(rawScore, stats.Nodes)

	// Personalized score: blend global score with social proximity signals
	proximityScore := 0.0
	if viewerFollowsTarget {
		proximityScore += 40.0 // Direct follow = strong signal
	}
	if targetFollowsViewer {
		proximityScore += 10.0 // Mutual = extra signal
	}
	if len(viewerFollows) > 0 {
		// What fraction of your follows also follow this person?
		trustedRatio := float64(trustedFollowers) / float64(len(viewerFollows))
		proximityScore += trustedRatio * 50.0 // Up to 50 points from trusted followers
	}
	if proximityScore > 100 {
		proximityScore = 100
	}

	// Blend: 50% global PageRank + 50% social proximity
	personalizedScore := int(float64(globalScore)*0.5 + proximityScore*0.5)
	if personalizedScore > 100 {
		personalizedScore = 100
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"viewer":              viewer,
		"target":              target,
		"personalized_score":  personalizedScore,
		"global_score":        globalScore,
		"found":               found,
		"viewer_follows_target": viewerFollowsTarget,
		"target_follows_viewer": targetFollowsViewer,
		"mutual_follow":       viewerFollowsTarget && targetFollowsViewer,
		"trusted_followers":   trustedFollowers,
		"trusted_follower_sample": trustedFollowerList,
		"shared_follows":      sharedFollows,
		"graph_size":          stats.Nodes,
	})
}

func handleSimilar(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}
	}

	targetFollows := graph.GetFollows(pubkey)
	if len(targetFollows) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pubkey":  pubkey,
			"similar": []interface{}{},
			"error":   "pubkey has no follows in graph",
		})
		return
	}

	// Build set of target's follows for fast lookup
	targetSet := make(map[string]bool, len(targetFollows))
	for _, f := range targetFollows {
		targetSet[f] = true
	}

	stats := graph.Stats()

	// Compare with all other pubkeys that have follows
	type candidate struct {
		Pubkey     string
		Jaccard    float64
		Shared     int
		TotalUnion int
		WotScore   int
	}

	allPubkeys := graph.AllFollowers()
	candidates := make([]candidate, 0, 256)

	for _, pk := range allPubkeys {
		if pk == pubkey {
			continue
		}
		pkFollows := graph.GetFollows(pk)
		if len(pkFollows) < 3 {
			continue // skip very low-activity accounts
		}

		// Compute Jaccard similarity: |intersection| / |union|
		shared := 0
		for _, f := range pkFollows {
			if targetSet[f] {
				shared++
			}
		}
		if shared == 0 {
			continue
		}

		union := len(targetSet) + len(pkFollows) - shared
		jaccard := float64(shared) / float64(union)

		rawScore, _ := graph.GetScore(pk)
		wotScore := normalizeScore(rawScore, stats.Nodes)

		candidates = append(candidates, candidate{
			Pubkey:     pk,
			Jaccard:    jaccard,
			Shared:     shared,
			TotalUnion: union,
			WotScore:   wotScore,
		})
	}

	// Sort by weighted score: 70% Jaccard similarity + 30% WoT score (normalized to 0-1)
	sort.Slice(candidates, func(i, j int) bool {
		scoreI := candidates[i].Jaccard*0.7 + float64(candidates[i].WotScore)/100.0*0.3
		scoreJ := candidates[j].Jaccard*0.7 + float64(candidates[j].WotScore)/100.0*0.3
		return scoreI > scoreJ
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	type resultEntry struct {
		Pubkey       string  `json:"pubkey"`
		Similarity   float64 `json:"similarity"`
		SharedFollows int    `json:"shared_follows"`
		WotScore     int     `json:"wot_score"`
	}

	results := make([]resultEntry, len(candidates))
	for i, c := range candidates {
		results[i] = resultEntry{
			Pubkey:        c.Pubkey,
			Similarity:    math.Round(c.Jaccard*1000) / 1000, // 3 decimal places
			SharedFollows: c.Shared,
			WotScore:      c.WotScore,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pubkey":      pubkey,
		"similar":     results,
		"total_found": len(candidates),
		"graph_size":  stats.Nodes,
	})
}

func handleRecommend(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 {
			limit = 20
		}
		if limit > 50 {
			limit = 50
		}
	}

	targetFollows := graph.GetFollows(pubkey)
	if len(targetFollows) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pubkey":          pubkey,
			"recommendations": []interface{}{},
			"error":           "pubkey has no follows in graph",
		})
		return
	}

	// Build set of who the target already follows (for exclusion)
	alreadyFollows := make(map[string]bool, len(targetFollows)+1)
	alreadyFollows[pubkey] = true // exclude self
	for _, f := range targetFollows {
		alreadyFollows[f] = true
	}

	// Count how many of target's follows also follow each candidate
	// "Friends of friends" — if many of your follows also follow X, you'd probably like X
	candidateCounts := make(map[string]int)
	for _, friend := range targetFollows {
		friendFollows := graph.GetFollows(friend)
		for _, candidate := range friendFollows {
			if !alreadyFollows[candidate] {
				candidateCounts[candidate]++
			}
		}
	}

	stats := graph.Stats()

	type candidate struct {
		Pubkey      string
		MutualCount int // how many of target's follows also follow this candidate
		WotScore    int
	}

	candidates := make([]candidate, 0, len(candidateCounts))
	for pk, count := range candidateCounts {
		if count < 2 {
			continue // need at least 2 mutual connections to be a recommendation
		}
		rawScore, _ := graph.GetScore(pk)
		wotScore := normalizeScore(rawScore, stats.Nodes)
		candidates = append(candidates, candidate{
			Pubkey:      pk,
			MutualCount: count,
			WotScore:    wotScore,
		})
	}

	// Sort by weighted score: 60% mutual ratio + 40% WoT score
	totalFollows := float64(len(targetFollows))
	sort.Slice(candidates, func(i, j int) bool {
		ratioI := float64(candidates[i].MutualCount) / totalFollows
		ratioJ := float64(candidates[j].MutualCount) / totalFollows
		scoreI := ratioI*0.6 + float64(candidates[i].WotScore)/100.0*0.4
		scoreJ := ratioJ*0.6 + float64(candidates[j].WotScore)/100.0*0.4
		return scoreI > scoreJ
	})

	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	type resultEntry struct {
		Pubkey       string  `json:"pubkey"`
		MutualCount  int     `json:"mutual_follows"`  // how many of your follows also follow this person
		MutualRatio  float64 `json:"mutual_ratio"`    // mutual_follows / your total follows (0-1)
		WotScore     int     `json:"wot_score"`
	}

	results := make([]resultEntry, len(candidates))
	for i, c := range candidates {
		results[i] = resultEntry{
			Pubkey:      c.Pubkey,
			MutualCount: c.MutualCount,
			MutualRatio: math.Round(float64(c.MutualCount)/totalFollows*1000) / 1000,
			WotScore:    c.WotScore,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"pubkey":          pubkey,
		"recommendations": results,
		"total_found":     len(candidates),
		"follows_count":   len(targetFollows),
		"graph_size":      stats.Nodes,
	})
}

// handleGraph serves two modes:
// Path mode: GET /graph?from=<pubkey>&to=<pubkey> — BFS shortest trust path
// Neighborhood mode: GET /graph?pubkey=<pubkey>&depth=1 — local graph around a pubkey
func handleGraph(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	pubkey := r.URL.Query().Get("pubkey")

	// Path mode: find shortest path between two pubkeys
	if from != "" && to != "" {
		fromHex, err := resolvePubkey(from)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid from pubkey: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		toHex, err := resolvePubkey(to)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"invalid to pubkey: %s"}`, err.Error()), http.StatusBadRequest)
			return
		}
		if fromHex == toHex {
			http.Error(w, `{"error":"from and to are the same pubkey"}`, http.StatusBadRequest)
			return
		}

		path, found := bfsPath(fromHex, toHex, 6)
		stats := graph.Stats()

		if !found {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"from":       fromHex,
				"to":         toHex,
				"found":      false,
				"path":       []string{},
				"hops":       0,
				"graph_size": stats.Nodes,
			})
			return
		}

		// Annotate each node in the path with WoT score
		type pathNode struct {
			Pubkey   string `json:"pubkey"`
			WotScore int    `json:"wot_score"`
		}
		nodes := make([]pathNode, len(path))
		for i, pk := range path {
			rawScore, _ := graph.GetScore(pk)
			nodes[i] = pathNode{
				Pubkey:   pk,
				WotScore: normalizeScore(rawScore, stats.Nodes),
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"from":       fromHex,
			"to":         toHex,
			"found":      true,
			"path":       nodes,
			"hops":       len(path) - 1,
			"graph_size": stats.Nodes,
		})
		return
	}

	// Neighborhood mode: local graph around a pubkey
	if pubkey != "" {
		pk, err := resolvePubkey(pubkey)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
			return
		}

		depthStr := r.URL.Query().Get("depth")
		depth := 1
		if depthStr != "" {
			if n, err := fmt.Sscanf(depthStr, "%d", &depth); n != 1 || err != nil || depth < 1 {
				depth = 1
			}
			if depth > 2 {
				depth = 2 // cap at 2 to prevent huge responses
			}
		}

		limitStr := r.URL.Query().Get("limit")
		limit := 50
		if limitStr != "" {
			if n, err := fmt.Sscanf(limitStr, "%d", &limit); n != 1 || err != nil || limit < 1 {
				limit = 50
			}
			if limit > 200 {
				limit = 200
			}
		}

		stats := graph.Stats()
		rawScore, _ := graph.GetScore(pk)

		type neighborNode struct {
			Pubkey   string `json:"pubkey"`
			WotScore int    `json:"wot_score"`
			Relation string `json:"relation"` // "follows", "follower", "mutual"
		}

		follows := graph.GetFollows(pk)
		followers := graph.GetFollowers(pk)

		followSet := make(map[string]bool, len(follows))
		for _, f := range follows {
			followSet[f] = true
		}
		followerSet := make(map[string]bool, len(followers))
		for _, f := range followers {
			followerSet[f] = true
		}

		// Collect unique neighbors with relation type
		seen := make(map[string]bool)
		neighbors := make([]neighborNode, 0)

		for _, f := range follows {
			if seen[f] || f == pk {
				continue
			}
			seen[f] = true
			relation := "follows"
			if followerSet[f] {
				relation = "mutual"
			}
			raw, _ := graph.GetScore(f)
			neighbors = append(neighbors, neighborNode{
				Pubkey:   f,
				WotScore: normalizeScore(raw, stats.Nodes),
				Relation: relation,
			})
		}
		for _, f := range followers {
			if seen[f] || f == pk {
				continue
			}
			seen[f] = true
			raw, _ := graph.GetScore(f)
			neighbors = append(neighbors, neighborNode{
				Pubkey:   f,
				WotScore: normalizeScore(raw, stats.Nodes),
				Relation: "follower",
			})
		}

		// If depth=2, also include follows-of-follows (trimmed)
		if depth == 2 {
			for _, f := range follows {
				fof := graph.GetFollows(f)
				for _, ff := range fof {
					if seen[ff] || ff == pk {
						continue
					}
					if len(neighbors) >= limit {
						break
					}
					seen[ff] = true
					raw, _ := graph.GetScore(ff)
					neighbors = append(neighbors, neighborNode{
						Pubkey:   ff,
						WotScore: normalizeScore(raw, stats.Nodes),
						Relation: "extended",
					})
				}
				if len(neighbors) >= limit {
					break
				}
			}
		}

		// Sort by WoT score descending, then trim
		sort.Slice(neighbors, func(i, j int) bool {
			return neighbors[i].WotScore > neighbors[j].WotScore
		})
		if len(neighbors) > limit {
			neighbors = neighbors[:limit]
		}

		// Count relation types
		mutualCount := 0
		for _, n := range neighbors {
			if n.Relation == "mutual" {
				mutualCount++
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pubkey":          pk,
			"wot_score":       normalizeScore(rawScore, stats.Nodes),
			"follows_count":   len(follows),
			"followers_count": len(followers),
			"mutual_count":    mutualCount,
			"neighbors":       neighbors,
			"depth":           depth,
			"graph_size":      stats.Nodes,
		})
		return
	}

	http.Error(w, `{"error":"provide either ?from=&to= (path mode) or ?pubkey= (neighborhood mode)"}`, http.StatusBadRequest)
}

// bfsPath finds the shortest path from source to target through the follow graph.
// maxDepth limits search depth to prevent runaway BFS on large graphs.
func bfsPath(source, target string, maxDepth int) ([]string, bool) {
	if source == target {
		return []string{source}, true
	}

	type bfsNode struct {
		pubkey string
		path   []string
	}

	visited := make(map[string]bool)
	visited[source] = true
	queue := []bfsNode{{pubkey: source, path: []string{source}}}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if len(current.path) > maxDepth {
			break
		}

		follows := graph.GetFollows(current.pubkey)
		for _, next := range follows {
			if next == target {
				return append(current.path, target), true
			}
			if !visited[next] {
				visited[next] = true
				newPath := make([]string, len(current.path)+1)
				copy(newPath, current.path)
				newPath[len(current.path)] = next
				queue = append(queue, bfsNode{pubkey: next, path: newPath})
			}
		}
	}

	return nil, false
}

type TopEntry struct {
	Pubkey    string  `json:"pubkey"`
	Score     float64 `json:"score"`
	Rank      int     `json:"rank"`
	NormScore int     `json:"norm_score"`
	Followers int     `json:"followers"`
}

func handleTop(w http.ResponseWriter, r *http.Request) {
	entries := graph.TopN(50)
	stats := graph.Stats()
	result := make([]TopEntry, len(entries))
	for i, e := range entries {
		m := meta.Get(e.Pubkey)
		result[i] = TopEntry{
			Pubkey:    e.Pubkey,
			Score:     e.Score,
			Rank:      i + 1,
			NormScore: normalizeScore(e.Score, stats.Nodes),
			Followers: m.Followers,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleStats(w http.ResponseWriter, r *http.Request) {
	stats := graph.Stats()
	resp := map[string]interface{}{
		"service":             "wot-scoring",
		"protocol":            "NIP-85",
		"operator":            "max@klabo.world",
		"graph_nodes":         stats.Nodes,
		"graph_edges":         stats.Edges,
		"last_build":          stats.LastBuild,
		"algorithm":           "PageRank",
		"iterations":          20,
		"damping_factor":      0.85,
		"relays":              relays,
		"score_range":         "0-100 (normalized)",
		"rate_limit":          "100 req/min per IP",
		"timestamp":           time.Now().UTC().Format(time.RFC3339),
		"verification_method": "follow-graph-crawl",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

type ExportEntry struct {
	Pubkey string  `json:"pubkey"`
	Rank   int     `json:"rank"`
	Raw    float64 `json:"raw"`
}

func handleExport(w http.ResponseWriter, r *http.Request) {
	stats := graph.Stats()
	if stats.Nodes == 0 {
		http.Error(w, `{"error":"graph not built yet"}`, http.StatusServiceUnavailable)
		return
	}
	entries := graph.TopN(0) // 0 = all
	result := make([]ExportEntry, len(entries))
	for i, e := range entries {
		result[i] = ExportEntry{
			Pubkey: e.Pubkey,
			Rank:   normalizeScore(e.Score, stats.Nodes),
			Raw:    e.Score,
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleAuthorized(w http.ResponseWriter, r *http.Request) {
	pubkey := r.URL.Query().Get("pubkey")

	// If no pubkey specified, show our own authorized users
	if pubkey == "" {
		// Get our own pubkey
		ownPub := ""
		if nsec, err := getNsec(); err == nil {
			if _, pub, err := decodeKey(nsec); err == nil {
				ownPub = pub
			}
		}
		if ownPub == "" {
			http.Error(w, `{"error":"provider pubkey not available"}`, http.StatusInternalServerError)
			return
		}
		pubkey = ownPub
	}

	users := authStore.AuthorizedUsers(pubkey)
	count := authStore.AuthorizedCount(pubkey)

	// Enrich with scores
	type AuthUser struct {
		Pubkey string `json:"pubkey"`
		Rank   int    `json:"rank"`
	}
	stats := graph.Stats()
	enriched := make([]AuthUser, 0, len(users))
	for _, u := range users {
		score, _ := graph.GetScore(u)
		enriched = append(enriched, AuthUser{
			Pubkey: u,
			Rank:   normalizeScore(score, stats.Nodes),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"provider":            pubkey,
		"authorized_users":    enriched,
		"authorized_count":    count,
		"total_users":         authStore.TotalUsers(),
		"total_authorizations": authStore.TotalAuthorizations(),
	})
}

func handleCommunities(w http.ResponseWriter, r *http.Request) {
	pubkey := r.URL.Query().Get("pubkey")

	if pubkey != "" {
		// Show community for a specific pubkey
		label, ok := communities.GetCommunity(pubkey)
		if !ok {
			http.Error(w, `{"error":"pubkey not found in community graph"}`, http.StatusNotFound)
			return
		}

		members := communities.GetCommunityMembers(pubkey)
		stats := graph.Stats()

		type MemberEntry struct {
			Pubkey string `json:"pubkey"`
			Rank   int    `json:"rank"`
		}

		// Sort by score, limit to top 20
		memberEntries := make([]MemberEntry, 0, len(members))
		for _, m := range members {
			score, _ := graph.GetScore(m)
			memberEntries = append(memberEntries, MemberEntry{
				Pubkey: m,
				Rank:   normalizeScore(score, stats.Nodes),
			})
		}
		sort.Slice(memberEntries, func(i, j int) bool {
			return memberEntries[i].Rank > memberEntries[j].Rank
		})
		if len(memberEntries) > 20 {
			memberEntries = memberEntries[:20]
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"pubkey":       pubkey,
			"community_id": label,
			"size":         len(members),
			"top_members":  memberEntries,
		})
		return
	}

	// No pubkey: return top communities
	top := communities.TopCommunities(graph, 20, 5)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"total_communities": communities.TotalCommunities(),
		"top":               top,
	})
}

// getNsec reads the nsec from env or 1Password
func getNsec() (string, error) {
	if nsec := os.Getenv("NOSTR_NSEC"); nsec != "" {
		return nsec, nil
	}
	out, err := exec.Command("op", "item", "get", "SATMAX Nostr Identity - Max", "--vault", "Agents", "--fields", "nsec", "--reveal").Output()
	if err != nil {
		return "", fmt.Errorf("failed to get nsec from 1Password: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// publishNIP85 publishes kind 30382 events for top-scored pubkeys
func publishNIP85(ctx context.Context, topN int) (int, error) {
	nsec, err := getNsec()
	if err != nil {
		return 0, fmt.Errorf("getNsec: %w", err)
	}

	var sk string
	if strings.HasPrefix(nsec, "nsec") {
		_, v, err := nip19.Decode(nsec)
		if err != nil {
			return 0, fmt.Errorf("nip19 decode: %w", err)
		}
		sk = v.(string)
	} else {
		sk = nsec
	}

	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		return 0, fmt.Errorf("getPublicKey: %w", err)
	}

	entries := graph.TopN(topN)
	stats := graph.Stats()
	pool := nostr.NewSimplePool(ctx)
	published := 0
	failed := 0

	for i, entry := range entries {
		rankScore := normalizeScore(entry.Score, stats.Nodes)
		m := meta.Get(entry.Pubkey)

		tags := nostr.Tags{
			{"d", entry.Pubkey},
			{"p", entry.Pubkey},
			{"rank", fmt.Sprintf("%d", rankScore)},
			{"followers", fmt.Sprintf("%d", m.Followers)},
			{"post_cnt", fmt.Sprintf("%d", m.PostCount)},
			{"reply_cnt", fmt.Sprintf("%d", m.ReplyCount)},
			{"reactions_cnt", fmt.Sprintf("%d", m.ReactionsRecd)},
			{"zap_amt_recd", fmt.Sprintf("%d", m.ZapAmtRecd)},
			{"zap_cnt_recd", fmt.Sprintf("%d", m.ZapCntRecd)},
			{"zap_amt_sent", fmt.Sprintf("%d", m.ZapAmtSent)},
			{"zap_cnt_sent", fmt.Sprintf("%d", m.ZapCntSent)},
		}
		if m.FirstCreated > 0 {
			tags = append(tags, nostr.Tag{"first_created_at", fmt.Sprintf("%d", m.FirstCreated)})
		}

		ev := nostr.Event{
			PubKey:    pub,
			CreatedAt: nostr.Now(),
			Kind:      30382,
			Tags:      tags,
		}

		err := ev.Sign(sk)
		if err != nil {
			log.Printf("Failed to sign event for %s: %v", entry.Pubkey, err)
			failed++
			continue
		}

		ok := false
		for result := range pool.PublishMany(ctx, relays, ev) {
			if result.Error != nil {
				log.Printf("Publish to %s failed: %v", result.RelayURL, result.Error)
			} else {
				ok = true
			}
		}
		if ok {
			published++
		} else {
			failed++
		}

		// Rate limit: sleep between events to avoid relay rate limits
		time.Sleep(100 * time.Millisecond)
		if (i+1)%50 == 0 {
			log.Printf("Published %d/%d NIP-85 events (%d failed)", published, i+1, failed)
			time.Sleep(2 * time.Second) // longer pause every 50
		}
	}

	log.Printf("Published %d NIP-85 kind 30382 events (%d failed)", published, failed)
	return published, nil
}

// publishNIP89Handler publishes a kind 31990 event announcing this service
// as a NIP-85 assertion provider (NIP-89 Recommended Application Handlers).
func publishNIP89Handler(ctx context.Context, sk, pub string) error {
	pool := nostr.NewSimplePool(ctx)

	// Content is kind-0-style metadata about the service
	content, _ := json.Marshal(map[string]string{
		"name":        "WoT Scoring Service",
		"about":       "NIP-85 Trusted Assertions provider. PageRank trust scoring over the Nostr follow graph with engagement metrics.",
		"picture":     "",
		"nip05":       "max@klabo.world",
		"website":     "https://github.com/joelklabo/wot-scoring",
		"lud16":       "max@klabo.world",
	})

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: nostr.Now(),
		Kind:      31990,
		Content:   string(content),
		Tags: nostr.Tags{
			{"d", "wot-scoring-nip85"},
			{"k", "30382"},
			{"k", "30383"},
			{"k", "30384"},
			{"k", "30385"},
			{"web", "https://github.com/joelklabo/wot-scoring"},
		},
	}

	if err := ev.Sign(sk); err != nil {
		return fmt.Errorf("sign kind 31990: %w", err)
	}

	published := false
	for result := range pool.PublishMany(ctx, relays, ev) {
		if result.Error != nil {
			log.Printf("NIP-89 publish to %s failed: %v", result.RelayURL, result.Error)
		} else {
			published = true
			log.Printf("NIP-89 handler published to %s", result.RelayURL)
		}
	}
	if !published {
		return fmt.Errorf("failed to publish NIP-89 handler to any relay")
	}
	return nil
}

func handlePublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	stats := graph.Stats()
	if stats.Nodes == 0 {
		http.Error(w, `{"error":"graph not built yet"}`, http.StatusServiceUnavailable)
		return
	}

	nsec, err := getNsec()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	sk, pub, err := decodeKey(nsec)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// Publish kind 30382 (user assertions)
	count382, err := publishNIP85(ctx, 50)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"30382: %s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Publish kind 30383 (event assertions)
	count383, err := publishEventAssertions(ctx, events, sk, pub)
	if err != nil {
		log.Printf("Error publishing kind 30383: %v", err)
	}

	// Publish kind 30384 (addressable event assertions)
	count384, err := publishAddressableAssertions(ctx, events, sk, pub)
	if err != nil {
		log.Printf("Error publishing kind 30384: %v", err)
	}

	// Publish kind 30385 (external identifier assertions)
	count385, err := publishExternalAssertions(ctx, external, sk, pub)
	if err != nil {
		log.Printf("Error publishing kind 30385: %v", err)
	}

	// Publish NIP-89 handler announcement (kind 31990)
	nip89Err := publishNIP89Handler(ctx, sk, pub)
	nip89Status := "published"
	if nip89Err != nil {
		nip89Status = fmt.Sprintf("error: %s", nip89Err.Error())
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"kind_30382":  count382,
		"kind_30383":  count383,
		"kind_30384":  count384,
		"kind_30385":  count385,
		"kind_31990":  nip89Status,
		"total":       count382 + count383 + count384 + count385,
		"algorithm":   "pagerank + engagement",
		"graph_nodes": stats.Nodes,
		"graph_edges": stats.Edges,
		"relays":      relays,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
}

// autoPublish runs a full NIP-85 publish cycle (all four assertion kinds + NIP-89).
// Called after initial crawl and after each scheduled re-crawl.
func autoPublish(ctx context.Context) {
	stats := graph.Stats()
	if stats.Nodes == 0 {
		log.Printf("Auto-publish skipped: graph not built yet")
		return
	}

	nsec, err := getNsec()
	if err != nil {
		log.Printf("Auto-publish skipped: %v", err)
		return
	}
	sk, pub, err := decodeKey(nsec)
	if err != nil {
		log.Printf("Auto-publish skipped: %v", err)
		return
	}

	log.Printf("Auto-publish starting (graph: %d nodes, %d edges)...", stats.Nodes, stats.Edges)

	count382, err := publishNIP85(ctx, 50)
	if err != nil {
		log.Printf("Auto-publish kind 30382 error: %v", err)
	}

	count383, err := publishEventAssertions(ctx, events, sk, pub)
	if err != nil {
		log.Printf("Auto-publish kind 30383 error: %v", err)
	}

	count384, err := publishAddressableAssertions(ctx, events, sk, pub)
	if err != nil {
		log.Printf("Auto-publish kind 30384 error: %v", err)
	}

	count385, err := publishExternalAssertions(ctx, external, sk, pub)
	if err != nil {
		log.Printf("Auto-publish kind 30385 error: %v", err)
	}

	nip89Err := publishNIP89Handler(ctx, sk, pub)
	if nip89Err != nil {
		log.Printf("Auto-publish NIP-89 error: %v", nip89Err)
	}

	log.Printf("Auto-publish complete: 30382=%d, 30383=%d, 30384=%d, 30385=%d (total=%d)",
		count382, count383, count384, count385, count382+count383+count384+count385)
}

// decodeKey converts an nsec (or raw hex) into sk and pubkey.
func decodeKey(nsec string) (string, string, error) {
	var sk string
	if strings.HasPrefix(nsec, "nsec") {
		_, v, err := nip19.Decode(nsec)
		if err != nil {
			return "", "", fmt.Errorf("nip19 decode: %w", err)
		}
		sk = v.(string)
	} else {
		sk = nsec
	}
	pub, err := nostr.GetPublicKey(sk)
	if err != nil {
		return "", "", fmt.Errorf("getPublicKey: %w", err)
	}
	return sk, pub, nil
}

func handleEventScore(w http.ResponseWriter, r *http.Request) {
	eventID := r.URL.Query().Get("id")
	if eventID == "" {
		http.Error(w, `{"error":"id parameter required"}`, http.StatusBadRequest)
		return
	}

	m := events.GetEvent(eventID)

	topEvents := events.TopEvents(1)
	var maxEng int64
	if len(topEvents) > 0 {
		maxEng = eventEngagement(topEvents[0])
	}

	resp := map[string]interface{}{
		"event_id":  eventID,
		"rank":      eventRank(m, maxEng),
		"comments":  m.Comments,
		"reposts":   m.Reposts,
		"reactions": m.Reactions,
		"zap_count": m.ZapCount,
		"zap_amount": m.ZapAmount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleExternal(w http.ResponseWriter, r *http.Request) {
	identifier := r.URL.Query().Get("id")
	if identifier == "" {
		// Return top external identifiers
		topExternal := external.TopExternal(50)
		var maxEng int64
		if len(topExternal) > 0 {
			maxEng = externalEngagement(topExternal[0])
		}

		type entry struct {
			Identifier    string `json:"identifier"`
			Kind          string `json:"kind"`
			Rank          int    `json:"rank"`
			Mentions      int    `json:"mentions"`
			UniqueAuthors int    `json:"unique_authors"`
			Reactions     int    `json:"reactions"`
			Reposts       int    `json:"reposts"`
			Comments      int    `json:"comments"`
			ZapCount      int    `json:"zap_count"`
			ZapAmount     int64  `json:"zap_amount"`
		}
		result := make([]entry, len(topExternal))
		for i, m := range topExternal {
			result[i] = entry{
				Identifier:    m.Identifier,
				Kind:          m.Kind,
				Rank:          externalRank(m, maxEng),
				Mentions:      m.Mentions,
				UniqueAuthors: len(m.Authors),
				Reactions:     m.Reactions,
				Reposts:       m.Reposts,
				Comments:      m.Comments,
				ZapCount:      m.ZapCount,
				ZapAmount:     m.ZapAmount,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
		return
	}

	m := external.Get(identifier)
	topExternal := external.TopExternal(1)
	var maxEng int64
	if len(topExternal) > 0 {
		maxEng = externalEngagement(topExternal[0])
	}

	resp := map[string]interface{}{
		"identifier":     identifier,
		"kind":           m.Kind,
		"rank":           externalRank(m, maxEng),
		"mentions":       m.Mentions,
		"unique_authors": len(m.Authors),
		"reactions":      m.Reactions,
		"reposts":        m.Reposts,
		"comments":       m.Comments,
		"zap_count":      m.ZapCount,
		"zap_amount":     m.ZapAmount,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleMetadata(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("pubkey")
	if raw == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	pubkey, err := resolvePubkey(raw)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	m := meta.Get(pubkey)
	score, found := graph.GetScore(pubkey)
	stats := graph.Stats()

	resp := map[string]interface{}{
		"pubkey":        pubkey,
		"found":         found,
		"rank":          normalizeScore(score, stats.Nodes),
		"raw_score":     score,
		"followers":     m.Followers,
		"post_cnt":      m.PostCount,
		"reply_cnt":     m.ReplyCount,
		"reactions_cnt": m.ReactionsRecd,
		"zap_amt_recd":  m.ZapAmtRecd,
		"zap_cnt_recd":  m.ZapCntRecd,
		"zap_amt_sent":  m.ZapAmtSent,
		"zap_cnt_sent":  m.ZapCntSent,
	}
	if m.FirstCreated > 0 {
		resp["first_created_at"] = m.FirstCreated
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// corsMiddleware adds CORS headers so web apps can query the API directly.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

const landingPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WoT Scoring — Nostr Web of Trust</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#0a0a0a;color:#e0e0e0;line-height:1.6}
.container{max-width:960px;margin:0 auto;padding:2rem 1.5rem}
h1{font-size:2rem;color:#fff;margin-bottom:.25rem}
.subtitle{color:#888;font-size:1.1rem;margin-bottom:2rem}
.badge{display:inline-block;background:#1a1a2e;border:1px solid #333;border-radius:6px;padding:.15rem .5rem;font-size:.8rem;color:#7c3aed;margin-right:.5rem}
.stats{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:1rem;margin:2rem 0}
.stat{background:#111;border:1px solid #222;border-radius:8px;padding:1rem;text-align:center}
.stat-value{font-size:1.8rem;font-weight:700;color:#7c3aed}
.stat-label{font-size:.85rem;color:#888;margin-top:.25rem}
.tabs{display:flex;gap:0;margin:2rem 0 0 0;border-bottom:2px solid #222}
.tab{padding:.6rem 1.2rem;cursor:pointer;color:#888;font-size:.95rem;font-weight:500;border-bottom:2px solid transparent;margin-bottom:-2px;transition:all .2s}
.tab:hover{color:#ccc}
.tab.active{color:#7c3aed;border-bottom-color:#7c3aed}
.tab-content{display:none;margin:1.5rem 0 2rem 0}
.tab-content.active{display:block}
.search input,.compare-input,.path-input{width:100%%;padding:.75rem 1rem;background:#111;border:1px solid #333;border-radius:8px;color:#fff;font-size:1rem}
.search input::placeholder,.compare-input::placeholder,.path-input::placeholder{color:#555}
.search input:focus,.compare-input:focus,.path-input:focus{outline:none;border-color:#7c3aed}
#result,#compare-result,#path-result{margin-top:1rem;min-height:2rem}
.score-card{background:#111;border:1px solid #222;border-radius:8px;padding:1.5rem;margin-top:1rem}
.score-big{font-size:3rem;font-weight:700;color:#7c3aed}
.score-details{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:.75rem;margin-top:1rem}
.score-detail{text-align:center}
.score-detail-value{font-size:1.2rem;font-weight:600;color:#fff}
.score-detail-label{font-size:.75rem;color:#666}
.compare-grid{display:grid;grid-template-columns:1fr auto 1fr;gap:1rem;align-items:start}
.compare-side{background:#111;border:1px solid #222;border-radius:8px;padding:1.25rem}
.compare-vs{display:flex;align-items:center;justify-content:center;color:#555;font-size:1.5rem;font-weight:700}
.compare-name{font-family:monospace;font-size:.8rem;color:#888;word-break:break-all;margin-bottom:.75rem}
.compare-score-big{font-size:2.5rem;font-weight:700;color:#7c3aed;text-align:center}
.compare-bar{height:6px;border-radius:3px;background:#1a1a2e;margin:.75rem 0;overflow:hidden}
.compare-bar-fill{height:100%%;border-radius:3px;transition:width .5s ease}
.compare-meta{display:grid;grid-template-columns:1fr 1fr;gap:.5rem;margin-top:.75rem}
.compare-meta-item{text-align:center}
.compare-meta-val{font-size:1rem;font-weight:600;color:#fff}
.compare-meta-lbl{font-size:.7rem;color:#666}
.relationship{background:#111;border:1px solid #222;border-radius:8px;padding:1.25rem;margin-top:1rem}
.relationship h3{font-size:1rem;color:#fff;margin-bottom:.75rem}
.rel-badges{display:flex;flex-wrap:wrap;gap:.5rem}
.rel-badge{display:inline-flex;align-items:center;gap:.35rem;padding:.3rem .7rem;border-radius:6px;font-size:.85rem;font-weight:500}
.rel-badge.positive{background:#052e16;color:#4ade80;border:1px solid #166534}
.rel-badge.negative{background:#1c1917;color:#a8a29e;border:1px solid #292524}
.rel-badge.neutral{background:#1a1a2e;color:#a78bfa;border:1px solid #333}
.path-inputs{display:grid;grid-template-columns:1fr auto 1fr;gap:.75rem;align-items:center}
.path-arrow{color:#555;font-size:1.5rem;text-align:center}
.path-btn{padding:.75rem 1.5rem;background:#7c3aed;color:#fff;border:none;border-radius:8px;cursor:pointer;font-size:1rem;font-weight:600;margin-top:.75rem;transition:background .2s}
.path-btn:hover{background:#6d28d9}
.path-btn:disabled{background:#333;color:#666;cursor:not-allowed}
.path-chain{display:flex;align-items:center;gap:0;flex-wrap:wrap;margin-top:1rem;padding:1rem;background:#111;border:1px solid #222;border-radius:8px}
.path-node{background:#1a1a2e;border:1px solid #333;border-radius:8px;padding:.5rem .75rem;text-align:center;min-width:100px}
.path-node-pk{font-family:monospace;font-size:.7rem;color:#aaa}
.path-node-score{font-size:1.1rem;font-weight:700;color:#7c3aed}
.path-hop{color:#555;font-size:1.2rem;padding:0 .25rem}
.path-summary{margin-top:.75rem;color:#888;font-size:.9rem}
.leaderboard{margin:2rem 0}
.leaderboard h2{font-size:1.3rem;color:#fff;margin-bottom:1rem}
.lb-table{width:100%%;border-collapse:collapse}
.lb-table th{text-align:left;padding:.5rem .75rem;color:#888;font-size:.8rem;font-weight:500;border-bottom:1px solid #222}
.lb-table td{padding:.5rem .75rem;border-bottom:1px solid #1a1a1a;font-size:.9rem}
.lb-table tr:hover{background:#111}
.lb-rank{color:#7c3aed;font-weight:700;width:3rem}
.lb-pubkey{font-family:monospace;color:#aaa}
.lb-score{color:#fff;font-weight:600;text-align:right}
.lb-followers{color:#666;text-align:right}
.endpoints{margin:2rem 0}
.endpoints h2{font-size:1.3rem;color:#fff;margin-bottom:1rem}
.endpoint{background:#111;border:1px solid #222;border-radius:6px;padding:.75rem 1rem;margin-bottom:.5rem;font-family:monospace;font-size:.9rem}
.endpoint .method{color:#7c3aed;font-weight:700;margin-right:.5rem}
.endpoint .path{color:#fff}
.endpoint .desc{color:#666;margin-left:.5rem}
.nip85{background:#111;border:1px solid #222;border-radius:8px;padding:1.5rem;margin:2rem 0}
.nip85 h2{color:#fff;margin-bottom:.75rem}
.kind{display:flex;gap:1rem;align-items:baseline;margin-bottom:.5rem}
.kind-num{color:#7c3aed;font-weight:700;font-family:monospace;min-width:5rem}
.kind-desc{color:#aaa}
footer{margin-top:3rem;padding-top:1.5rem;border-top:1px solid #222;color:#555;font-size:.85rem;display:flex;justify-content:space-between;flex-wrap:wrap;gap:.5rem}
footer a{color:#7c3aed;text-decoration:none}
footer a:hover{text-decoration:underline}
@keyframes fadeIn{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:translateY(0)}}
.fade-in{animation:fadeIn .3s ease-out}
@media(max-width:640px){.compare-grid{grid-template-columns:1fr;}.compare-vs{padding:.5rem 0}.path-inputs{grid-template-columns:1fr}}
</style>
</head>
<body>
<div class="container">
<h1>WoT Scoring</h1>
<p class="subtitle">NIP-85 Trusted Assertions for the Nostr Web of Trust</p>
<span class="badge">NIP-85</span>
<span class="badge">PageRank</span>
<span class="badge">Trust Decay</span>
<span class="badge">Go</span>

<div class="stats">
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Nodes</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Edges</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Events Scored</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Articles</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Identifiers</div></div>
<div class="stat"><div class="stat-value">%s</div><div class="stat-label">Uptime</div></div>
</div>

<div class="tabs">
<div class="tab active" data-tab="lookup">Score Lookup</div>
<div class="tab" data-tab="compare">Compare</div>
<div class="tab" data-tab="path">Trust Path</div>
</div>

<div class="tab-content active" id="tab-lookup">
<div class="search">
<input type="text" id="pubkey-input" placeholder="Enter npub or hex pubkey to look up trust score..." autofocus>
<div id="result"></div>
</div>
</div>

<div class="tab-content" id="tab-compare">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Compare two Nostr identities side-by-side to see their trust relationship</p>
<div class="compare-grid">
<div><input type="text" class="compare-input" id="compare-a" placeholder="First pubkey or npub..."></div>
<div class="compare-vs">vs</div>
<div><input type="text" class="compare-input" id="compare-b" placeholder="Second pubkey or npub..."></div>
</div>
<div id="compare-result"></div>
</div>

<div class="tab-content" id="tab-path">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Find the shortest trust path between two Nostr identities through the follow graph</p>
<div class="path-inputs">
<input type="text" class="path-input" id="path-from" placeholder="From pubkey or npub...">
<div class="path-arrow">&rarr;</div>
<input type="text" class="path-input" id="path-to" placeholder="To pubkey or npub...">
</div>
<button class="path-btn" id="path-btn" onclick="findPath()">Find Trust Path</button>
<div id="path-result"></div>
</div>

<div class="leaderboard">
<h2>Trust Leaderboard</h2>
<table class="lb-table">
<thead><tr><th>Rank</th><th>Pubkey</th><th style="text-align:right">Score</th><th style="text-align:right">Followers</th></tr></thead>
<tbody id="lb-body"><tr><td colspan="4" style="color:#555">Loading...</td></tr></tbody>
</table>
</div>

<div class="leaderboard">
<h2>Trust Communities</h2>
<p style="color:#888;font-size:.85rem;margin-bottom:1rem">Clusters detected via label propagation over the follow graph</p>
<div id="communities-list" style="color:#555">Loading communities...</div>
</div>

<div class="nip85">
<h2>NIP-85 Event Kinds</h2>
<div class="kind"><span class="kind-num">10040</span><span class="kind-desc">Provider Authorization — users declare trust in this service for WoT calculations</span></div>
<div class="kind"><span class="kind-num">30382</span><span class="kind-desc">User Trust Assertions — PageRank score, follower count, post/reply/reaction/zap stats</span></div>
<div class="kind"><span class="kind-num">30383</span><span class="kind-desc">Event Assertions — engagement scores for individual notes (comments, reposts, reactions, zaps)</span></div>
<div class="kind"><span class="kind-num">30384</span><span class="kind-desc">Addressable Event Assertions — scores for articles (kind 30023) and live activities (kind 30311)</span></div>
<div class="kind"><span class="kind-num">30385</span><span class="kind-desc">External Identifier Assertions — scores for hashtags and URLs (NIP-73)</span></div>
</div>

<div class="endpoints">
<h2>API Endpoints</h2>
<div class="endpoint"><span class="method">GET</span><span class="path">/score?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Trust score + metadata</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/audit?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Score audit: full breakdown of why a pubkey has its score</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/personalized?viewer=&lt;hex&gt;&amp;target=&lt;hex&gt;</span><span class="desc">— Personalized trust score</span></div>
<div class="endpoint"><span class="method">POST</span><span class="path">/batch</span><span class="desc">— Score multiple pubkeys at once</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/similar?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Find similar pubkeys by follow overlap</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/recommend?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Follow recommendations (friends-of-friends)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/graph?from=&lt;hex&gt;&amp;to=&lt;hex&gt;</span><span class="desc">— Trust path finder (shortest connection)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/graph?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Neighborhood graph (local follow network)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/metadata?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Full NIP-85 metadata</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/event?id=&lt;hex&gt;</span><span class="desc">— Event engagement (kind 30383)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/external?id=&lt;ident&gt;</span><span class="desc">— Identifier score (kind 30385)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/relay?url=&lt;wss://...&gt;</span><span class="desc">— Relay trust + operator WoT</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/compare?a=&lt;pubkey&gt;&amp;b=&lt;pubkey&gt;</span><span class="desc">— Compare two pubkeys trust relationship</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/decay?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Time-decayed trust score (newer follows weigh more)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/decay/top</span><span class="desc">— Top pubkeys by decay-adjusted score with rank changes</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/authorized</span><span class="desc">— Kind 10040 authorized users (who trusts us)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/communities?pubkey=&lt;hex&gt;</span><span class="desc">— Trust community detection (label propagation)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/top</span><span class="desc">— Top 50 scored pubkeys</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/external</span><span class="desc">— Top 50 external identifiers</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/stats</span><span class="desc">— Service statistics</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/health</span><span class="desc">— Health check</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/providers</span><span class="desc">— External NIP-85 assertion providers</span></div>
</div>

<div class="nip85-kinds" style="margin-top:2rem">
<h2 style="font-size:1.3rem;color:#fff;margin-bottom:1rem">L402 Lightning Paywall</h2>
<p style="color:#aaa;font-size:.95rem;margin-bottom:1rem">Pay-per-query via Lightning Network. Free tier: 10 requests/day per IP. After that, pay sats per query.</p>
<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:.5rem">
<div class="kind"><span class="kind-num" style="background:#16a34a">1 sat</span><span class="kind-desc">/score, /decay</span></div>
<div class="kind"><span class="kind-num" style="background:#2563eb">2 sats</span><span class="kind-desc">/personalized, /similar, /recommend, /compare</span></div>
<div class="kind"><span class="kind-num" style="background:#9333ea">5 sats</span><span class="kind-desc">/audit</span></div>
<div class="kind"><span class="kind-num" style="background:#dc2626">10 sats</span><span class="kind-desc">/batch (up to 100 pubkeys)</span></div>
</div>
<p style="color:#666;font-size:.85rem;margin-top:.75rem">Endpoints not listed above are free and unlimited. Payment via L402 protocol: request → 402 + invoice → pay → retry with X-Payment-Hash header.</p>
</div>

<footer>
<span>Built for <a href="https://nosfabrica.com/wotathon/">WoT-a-thon</a></span>
<span><a href="https://github.com/joelklabo/wot-scoring">Source (MIT)</a></span>
<span>Operator: <a href="https://njump.me/max@klabo.world">max@klabo.world</a></span>
</footer>
</div>
<script>
function fmt(n){if(n>=1e6)return(n/1e6).toFixed(1)+"M";if(n>=1e3)return(n/1e3).toFixed(1)+"K";return n.toString()}
function pk(s){return s.slice(0,8)+"..."+s.slice(-6)}
function err(el,msg){el.innerHTML='<div class="score-card fade-in" style="color:#f87171">'+msg+'</div>'}

// Tab switching
document.querySelectorAll(".tab").forEach(t=>{
t.addEventListener("click",()=>{
document.querySelectorAll(".tab").forEach(x=>x.classList.remove("active"));
document.querySelectorAll(".tab-content").forEach(x=>x.classList.remove("active"));
t.classList.add("active");
document.getElementById("tab-"+t.dataset.tab).classList.add("active");
})});

// Score lookup
const input=document.getElementById("pubkey-input"),result=document.getElementById("result");
let timer;
input.addEventListener("input",()=>{clearTimeout(timer);const v=input.value.trim();if(!v){result.innerHTML="";return}
timer=setTimeout(()=>{fetch("/score?pubkey="+encodeURIComponent(v)).then(r=>r.json()).then(d=>{
if(d.error){err(result,d.error);return}
let html='<div class="score-card fade-in"><div class="score-big">'+d.score+'/100</div>';
html+='<div style="color:#888;margin-top:.25rem;font-family:monospace;font-size:.85rem">'+d.pubkey+'</div>';
html+='<div class="score-details">';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.followers||0)+'</div><div class="score-detail-label">Followers</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.post_count||0)+'</div><div class="score-detail-label">Posts</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reactions||0)+'</div><div class="score-detail-label">Reactions</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.zap_amount||0)+'</div><div class="score-detail-label">Sats Received</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.zap_count||0)+'</div><div class="score-detail-label">Zaps</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reply_count||0)+'</div><div class="score-detail-label">Replies</div></div>';
html+='</div></div>';
result.innerHTML=html;
}).catch(()=>{err(result,"Error fetching score")})},400)});

// Compare tool
let cmpTimer;
function doCompare(){
clearTimeout(cmpTimer);
const a=document.getElementById("compare-a").value.trim();
const b=document.getElementById("compare-b").value.trim();
const out=document.getElementById("compare-result");
if(!a||!b){out.innerHTML="";return}
cmpTimer=setTimeout(()=>{
out.innerHTML='<div style="color:#555;margin-top:1rem">Loading...</div>';
Promise.all([
fetch("/compare?a="+encodeURIComponent(a)+"&b="+encodeURIComponent(b)).then(r=>r.json()),
fetch("/score?pubkey="+encodeURIComponent(a)).then(r=>r.json()),
fetch("/score?pubkey="+encodeURIComponent(b)).then(r=>r.json())
]).then(([cmp,sa,sb])=>{
if(cmp.error){err(out,cmp.error);return}
const sA=sa.score||0,sB=sb.score||0;
const maxS=Math.max(sA,sB,1);
let html='<div class="fade-in">';
html+='<div class="compare-grid">';
// Side A
html+='<div class="compare-side"><div class="compare-name">'+pk(cmp.a.pubkey)+'</div>';
html+='<div class="compare-score-big">'+sA+'</div>';
html+='<div class="compare-bar"><div class="compare-bar-fill" style="width:'+(sA/maxS*100)+'%%;background:#7c3aed"></div></div>';
html+='<div class="compare-meta">';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sa.followers||0)+'</div><div class="compare-meta-lbl">Followers</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sa.post_count||0)+'</div><div class="compare-meta-lbl">Posts</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sa.zap_amount||0)+'</div><div class="compare-meta-lbl">Sats Recd</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sa.reactions||0)+'</div><div class="compare-meta-lbl">Reactions</div></div>';
html+='</div></div>';
// VS
html+='<div class="compare-vs">vs</div>';
// Side B
html+='<div class="compare-side"><div class="compare-name">'+pk(cmp.b.pubkey)+'</div>';
html+='<div class="compare-score-big">'+sB+'</div>';
html+='<div class="compare-bar"><div class="compare-bar-fill" style="width:'+(sB/maxS*100)+'%%;background:#a78bfa"></div></div>';
html+='<div class="compare-meta">';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sb.followers||0)+'</div><div class="compare-meta-lbl">Followers</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sb.post_count||0)+'</div><div class="compare-meta-lbl">Posts</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sb.zap_amount||0)+'</div><div class="compare-meta-lbl">Sats Recd</div></div>';
html+='<div class="compare-meta-item"><div class="compare-meta-val">'+fmt(sb.reactions||0)+'</div><div class="compare-meta-lbl">Reactions</div></div>';
html+='</div></div>';
html+='</div>';
// Relationship
html+='<div class="relationship"><h3>Trust Relationship</h3><div class="rel-badges">';
if(cmp.mutual_follow){html+='<span class="rel-badge positive">Mutual Follow</span>';}
else{
if(cmp.a_follows_b){html+='<span class="rel-badge positive">A follows B</span>';}else{html+='<span class="rel-badge negative">A does not follow B</span>';}
if(cmp.b_follows_a){html+='<span class="rel-badge positive">B follows A</span>';}else{html+='<span class="rel-badge negative">B does not follow A</span>';}
}
html+='<span class="rel-badge neutral">'+fmt(cmp.shared_follows||0)+' shared follows</span>';
html+='<span class="rel-badge neutral">'+fmt(cmp.a_trusted_followers_of_b||0)+' of A\'s follows trust B</span>';
html+='</div></div>';
html+='</div>';
out.innerHTML=html;
}).catch(()=>{err(out,"Error comparing pubkeys")});
},500);
}
document.getElementById("compare-a").addEventListener("input",doCompare);
document.getElementById("compare-b").addEventListener("input",doCompare);

// Trust path finder
function findPath(){
const from=document.getElementById("path-from").value.trim();
const to=document.getElementById("path-to").value.trim();
const out=document.getElementById("path-result");
const btn=document.getElementById("path-btn");
if(!from||!to){err(out,"Enter both pubkeys");return}
btn.disabled=true;btn.textContent="Searching...";
out.innerHTML='<div style="color:#555;margin-top:1rem">Searching the graph...</div>';
fetch("/graph?from="+encodeURIComponent(from)+"&to="+encodeURIComponent(to))
.then(r=>r.json()).then(d=>{
btn.disabled=false;btn.textContent="Find Trust Path";
if(d.error){err(out,d.error);return}
if(!d.found){
out.innerHTML='<div class="score-card fade-in"><div style="color:#f59e0b;font-size:1.1rem;font-weight:600">No path found</div><div style="color:#888;margin-top:.5rem">These pubkeys are not connected within 6 hops in the follow graph.</div></div>';
return;
}
let html='<div class="fade-in"><div class="path-chain">';
d.path.forEach((node,i)=>{
if(i>0)html+='<div class="path-hop">&rarr;</div>';
html+='<div class="path-node"><div class="path-node-score">'+node.wot_score+'</div><div class="path-node-pk">'+pk(node.pubkey)+'</div></div>';
});
html+='</div>';
html+='<div class="path-summary">'+d.hops+' hop'+(d.hops!==1?'s':'')+' &middot; '+d.path.length+' nodes &middot; searched '+fmt(d.graph_size)+' node graph</div>';
html+='</div>';
out.innerHTML=html;
}).catch(()=>{btn.disabled=false;btn.textContent="Find Trust Path";err(out,"Error finding path")});
}
document.getElementById("path-from").addEventListener("keydown",e=>{if(e.key==="Enter")findPath()});
document.getElementById("path-to").addEventListener("keydown",e=>{if(e.key==="Enter")findPath()});

// Load leaderboard
fetch("/top").then(r=>r.json()).then(data=>{
const tbody=document.getElementById("lb-body");
if(!data||!data.length){tbody.innerHTML='<tr><td colspan="4" style="color:#555">No data yet</td></tr>';return}
const top10=data.slice(0,10);
tbody.innerHTML=top10.map((e,i)=>'<tr><td class="lb-rank">'+(i+1)+'</td><td class="lb-pubkey">'+e.pubkey.slice(0,12)+'...'+e.pubkey.slice(-8)+'</td><td class="lb-score">'+(e.norm_score||0)+'/100</td><td class="lb-followers">'+fmt(e.followers||0)+'</td></tr>').join("");
}).catch(()=>{document.getElementById("lb-body").innerHTML='<tr><td colspan="4" style="color:#555">Failed to load</td></tr>'});

// Load communities
fetch("/communities").then(r=>r.json()).then(data=>{
const el=document.getElementById("communities-list");
if(!data||!data.top||!data.top.length){el.innerHTML='<div style="color:#555">No communities detected yet</div>';return}
let html='<div style="color:#888;margin-bottom:.75rem">'+data.total_communities+' communities detected</div>';
html+='<div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:.75rem">';
data.top.slice(0,8).forEach((c,i)=>{
html+='<div style="background:#111;border:1px solid #222;border-radius:8px;padding:.75rem">';
html+='<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:.5rem">';
html+='<span style="color:#a78bfa;font-weight:600">Cluster #'+(i+1)+'</span>';
html+='<span style="color:#10b981;font-size:.85rem">'+c.size+' members</span></div>';
html+='<div style="font-size:.8rem;color:#888">Avg rank: '+c.avg_rank.toFixed(1)+' · Top: '+c.top_rank+'/100</div>';
if(c.members&&c.members.length){html+='<div style="margin-top:.5rem;font-size:.75rem;color:#555">';
c.members.slice(0,3).forEach(m=>{html+='<div style="font-family:monospace;padding:.1rem 0">'+m.slice(0,12)+'...'+m.slice(-6)+'</div>'});
html+='</div>'}
html+='</div>'});
html+='</div>';
el.innerHTML=html;
}).catch(()=>{document.getElementById("communities-list").innerHTML='<div style="color:#555">Failed to load communities</div>'});
</script>
</body>
</html>`

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	// Seed pubkeys: well-known Nostr accounts for initial graph crawl
	seeds := []string{
		"82341f882b6eabcd2ba7f1ef90aad961cf074af15b9ef44a09f9d2a8fbfbe6a2", // jack
		"fa984bd7dbb282f07e16e7ae87b26a2a7b9b90b7246a44771f0cf5ae58018f52", // pablo
		"32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245", // jb55
		"f2da54d2d1edfe02c052972e2eeb192a5046751ed38e94e2f9be0c156456e2aa", // max (SATMAX)
	}

	// Crawl depth (1 = direct follows, 2 = follows-of-follows)
	depth := 2
	log.Printf("Starting WoT graph crawl with %d seeds, depth %d...", len(seeds), depth)

	ctx := context.Background()
	go func() {
		crawlFollows(ctx, seeds, depth)
		log.Printf("Computing PageRank...")
		graph.ComputePageRank(20, 0.85)
		stats := graph.Stats()
		log.Printf("WoT graph ready: %d nodes, %d edges", stats.Nodes, stats.Edges)

		// Populate follower counts from graph
		meta.CountFollowers(graph)
		log.Printf("Follower counts populated")

		// Crawl metadata (notes, reactions, zaps) for top-scored pubkeys
		topPubkeys := TopNPubkeys(graph, 500)
		log.Printf("Crawling metadata for top %d pubkeys...", len(topPubkeys))
		meta.CrawlMetadata(ctx, topPubkeys)
		log.Printf("Metadata crawl complete")

		// Crawl event engagement for NIP-85 kind 30383/30384
		log.Printf("Crawling event engagement for top %d pubkeys...", len(topPubkeys))
		events.CrawlEventEngagement(ctx, topPubkeys)
		log.Printf("Event engagement crawl complete: %d events, %d addressable",
			events.EventCount(), events.AddressableCount())

		// Crawl external identifiers (hashtags, URLs) for NIP-85 kind 30385
		log.Printf("Crawling external identifiers for top %d pubkeys...", len(topPubkeys))
		external.CrawlExternalIdentifiers(ctx, topPubkeys)
		log.Printf("External identifier crawl complete: %d identifiers", external.Count())

		// Consume external NIP-85 assertions from other providers
		ownPub := ""
		if nsec, err := getNsec(); err == nil {
			if _, pub, err := decodeKey(nsec); err == nil {
				ownPub = pub
			}
		}
		consumeExternalAssertions(ctx, externalAssertions, ownPub)

		// Consume NIP-85 kind 10040 authorizations
		consumeAuthorizations(ctx, authStore)

		// Detect trust communities via label propagation
		log.Printf("Detecting trust communities...")
		numCommunities := communities.DetectCommunities(graph, 10)
		log.Printf("Community detection complete: %d non-trivial communities", communities.TotalCommunities())
		_ = numCommunities

		// Auto-publish NIP-85 events after initial crawl
		autoPublish(ctx)

		// Schedule periodic re-crawl + auto-publish every 6 hours
		go func() {
			ticker := time.NewTicker(6 * time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				log.Printf("Starting scheduled re-crawl...")
				crawlFollows(ctx, seeds, depth)
				graph.ComputePageRank(20, 0.85)
				meta.CountFollowers(graph)
				topPubkeys := TopNPubkeys(graph, 500)
				meta.CrawlMetadata(ctx, topPubkeys)
				events.CrawlEventEngagement(ctx, topPubkeys)
				external.CrawlExternalIdentifiers(ctx, topPubkeys)
				consumeExternalAssertions(ctx, externalAssertions, ownPub)
				consumeAuthorizations(ctx, authStore)
				communities.DetectCommunities(graph, 10)
				stats := graph.Stats()
				log.Printf("Re-crawl complete: %d nodes, %d edges, %d events, %d addressable, %d external, %d ext_assertions, %d auths, %d communities",
					stats.Nodes, stats.Edges, events.EventCount(), events.AddressableCount(), external.Count(),
					externalAssertions.TotalAssertions(), authStore.TotalAuthorizations(), communities.TotalCommunities())

				autoPublish(ctx)
			}
		}()
	}()

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		stats := graph.Stats()
		status := "starting"
		if stats.Nodes > 0 {
			status = "ready"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":               status,
			"graph_nodes":          stats.Nodes,
			"graph_edges":          stats.Edges,
			"events":               events.EventCount(),
			"addressable":          events.AddressableCount(),
			"external":             external.Count(),
			"external_providers":   externalAssertions.ProviderCount(),
			"external_assertions":  externalAssertions.TotalAssertions(),
			"authorizations":       authStore.TotalAuthorizations(),
			"authorized_users":     authStore.TotalUsers(),
			"communities":          communities.TotalCommunities(),
			"uptime":               time.Since(startTime).String(),
		})
	})
	http.HandleFunc("/providers", func(w http.ResponseWriter, r *http.Request) {
		providers := externalAssertions.Providers()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"providers":        providers,
			"provider_count":   externalAssertions.ProviderCount(),
			"total_assertions": externalAssertions.TotalAssertions(),
		})
	})
	http.HandleFunc("/score", handleScore)
	http.HandleFunc("/audit", handleAudit)
	http.HandleFunc("/batch", handleBatch)
	http.HandleFunc("/personalized", handlePersonalized)
	http.HandleFunc("/similar", handleSimilar)
	http.HandleFunc("/recommend", handleRecommend)
	http.HandleFunc("/graph", handleGraph)
	http.HandleFunc("/top", handleTop)
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/export", handleExport)
	http.HandleFunc("/publish", handlePublish)
	http.HandleFunc("/metadata", handleMetadata)
	http.HandleFunc("/event", handleEventScore)
	http.HandleFunc("/external", handleExternal)
	http.HandleFunc("/relay", handleRelay)
	http.HandleFunc("/compare", handleCompare)
	http.HandleFunc("/decay", handleDecay)
	http.HandleFunc("/decay/top", handleDecayTop)
	http.HandleFunc("/authorized", handleAuthorized)
	http.HandleFunc("/communities", handleCommunities)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		// Serve HTML for browsers, JSON for API clients
		accept := r.Header.Get("Accept")
		if strings.Contains(accept, "text/html") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			stats := graph.Stats()
			fmt.Fprintf(w, landingPageHTML,
				stats.Nodes, stats.Edges, events.EventCount(),
				events.AddressableCount(), external.Count(),
				time.Since(startTime).Truncate(time.Second))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"name":        "WoT Scoring Service",
			"description": "NIP-85 Trusted Assertions provider. PageRank trust scoring over the Nostr follow graph with full metadata collection.",
			"endpoints": `/score?pubkey=<hex> — Trust score for a pubkey (kind 30382), with composite scoring from external providers
/audit?pubkey=<hex> — Score audit: full breakdown of why a pubkey has its score (PageRank, engagement, external, percentile)
/personalized?viewer=<hex>&target=<hex> — Personalized trust score relative to viewer's follow graph
POST /batch — Score multiple pubkeys in one request (JSON body: {"pubkeys":["hex1","hex2",...]})
/similar?pubkey=<hex> — Find similar pubkeys by follow-graph overlap (Jaccard + WoT weighted)
/recommend?pubkey=<hex> — Follow recommendations (friends-of-friends who you don't yet follow)
/graph?from=<hex>&to=<hex> — Trust path finder (shortest connection between two pubkeys)
/graph?pubkey=<hex>&depth=1 — Neighborhood graph (local follow network around a pubkey)
/metadata?pubkey=<hex> — Full NIP-85 metadata (followers, posts, reactions, zaps)
/event?id=<hex> — Event engagement score (kind 30383)
/external?id=<identifier> — External identifier score (kind 30385, NIP-73)
/external — Top 50 external identifiers (hashtags, URLs)
/relay?url=<wss://...> — Relay trust + operator WoT (via trustedrelays.xyz)
/decay?pubkey=<hex> — Time-decayed trust score (newer follows weigh more, configurable half-life)
/decay/top — Top pubkeys by decay-adjusted score with rank changes vs static
/authorized — Kind 10040 authorized users (who declared trust in this provider)
/authorized?pubkey=<hex> — Authorizations for a specific provider
/communities — Top trust communities detected via label propagation
/communities?pubkey=<hex> — Community membership and peers for a pubkey
/providers — External NIP-85 assertion providers and their assertion counts
/top — Top 50 scored pubkeys
/export — All scores as JSON
/stats — Service stats and graph info
POST /publish — Publish NIP-85 kind 30382/30383/30384/30385 events to relays`,
			"nip":      "85",
			"operator": "max@klabo.world",
			"source":   "https://github.com/joelklabo/wot-scoring",
		})
	})

	// Rate limiter: 100 requests/minute per IP (free tier)
	limiter := NewRateLimiter(100, time.Minute)
	log.Printf("Rate limiting enabled: 100 req/min per IP")

	// Build handler chain: CORS -> Rate Limit -> L402 -> handlers
	var handler http.Handler = http.DefaultServeMux
	if L402Enabled() {
		l402 := NewL402FromEnv()
		handler = l402.Wrap(handler)
		log.Printf("L402 paywall enabled: %d free requests/day per IP, paid via Lightning", l402.config.FreeTier)
	} else {
		log.Printf("L402 paywall disabled (set LNBITS_URL and LNBITS_KEY to enable)")
	}

	log.Printf("WoT Scoring API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, RateLimitMiddleware(limiter, corsMiddleware(handler))))
}
