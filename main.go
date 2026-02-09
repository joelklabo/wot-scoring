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
	"wss://nip85.nostr1.com",
	"wss://nip85.brainstorm.world",
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
var muteStore = NewMuteStore()
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

	// NIP-85 extended metadata
	if topics := m.TopTopics(5); len(topics) > 0 {
		resp["topics"] = topics
	}
	activeStart, activeEnd := m.ActiveHours()
	if activeStart != activeEnd {
		resp["active_hours_start"] = activeStart
		resp["active_hours_end"] = activeEnd
	}
	if m.ReportsRecd > 0 {
		resp["reports_received"] = m.ReportsRecd
	}
	if m.ReportsSent > 0 {
		resp["reports_sent"] = m.ReportsSent
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

			// Compute avg daily zap amounts
			daysSinceFirst := float64(time.Now().Unix()-m.FirstCreated) / 86400.0
			if daysSinceFirst > 1 {
				tags = append(tags, nostr.Tag{"zap_avg_amt_day_recd", fmt.Sprintf("%d", int64(float64(m.ZapAmtRecd)/daysSinceFirst))})
				tags = append(tags, nostr.Tag{"zap_avg_amt_day_sent", fmt.Sprintf("%d", int64(float64(m.ZapAmtSent)/daysSinceFirst))})
			}
		}

		// Active hours
		activeStart, activeEnd := m.ActiveHours()
		if activeStart != activeEnd {
			tags = append(tags, nostr.Tag{"active_hours_start", fmt.Sprintf("%d", activeStart)})
			tags = append(tags, nostr.Tag{"active_hours_end", fmt.Sprintf("%d", activeEnd)})
		}

		// Reports
		if m.ReportsRecd > 0 {
			tags = append(tags, nostr.Tag{"reports_cnt_recd", fmt.Sprintf("%d", m.ReportsRecd)})
		}
		if m.ReportsSent > 0 {
			tags = append(tags, nostr.Tag{"reports_cnt_sent", fmt.Sprintf("%d", m.ReportsSent)})
		}

		// Top topics (up to 5 hashtags)
		for _, topic := range m.TopTopics(5) {
			tags = append(tags, nostr.Tag{"t", topic})
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

const docsPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>API Documentation — WoT Scoring</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:#0a0a0a;color:#e0e0e0;line-height:1.6}
.container{max-width:1100px;margin:0 auto;padding:2rem 1.5rem}
h1{font-size:2rem;color:#fff;margin-bottom:.25rem}
h2{font-size:1.4rem;color:#fff;margin:2.5rem 0 1rem 0;padding-bottom:.5rem;border-bottom:1px solid #222}
h3{font-size:1.1rem;color:#e0e0e0;margin:1.5rem 0 .5rem 0}
.subtitle{color:#888;font-size:1.1rem;margin-bottom:1.5rem}
a{color:#7c3aed;text-decoration:none}a:hover{text-decoration:underline}
.nav{display:flex;gap:1rem;flex-wrap:wrap;margin:1.5rem 0;padding:1rem;background:#111;border:1px solid #222;border-radius:8px}
.nav a{font-size:.85rem;color:#aaa;padding:.25rem .5rem;border-radius:4px}.nav a:hover{color:#fff;background:#1a1a2e;text-decoration:none}
.endpoint-card{background:#111;border:1px solid #222;border-radius:8px;padding:1.25rem;margin-bottom:1rem}
.endpoint-header{display:flex;align-items:center;gap:.75rem;flex-wrap:wrap}
.method{display:inline-block;padding:.2rem .6rem;border-radius:4px;font-weight:700;font-size:.8rem;font-family:monospace}
.method-get{background:#16a34a22;color:#4ade80;border:1px solid #16a34a44}
.method-post{background:#2563eb22;color:#60a5fa;border:1px solid #2563eb44}
.path{font-family:monospace;font-size:1rem;color:#fff;font-weight:600}
.price-tag{font-size:.75rem;padding:.15rem .5rem;border-radius:10px;background:#7c3aed22;color:#a78bfa;border:1px solid #7c3aed44}
.desc{color:#999;font-size:.9rem;margin-top:.5rem}
.params{margin-top:.75rem}
.params-title{font-size:.8rem;color:#888;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:.4rem}
.param{display:flex;gap:.5rem;align-items:baseline;font-size:.85rem;padding:.2rem 0}
.param-name{font-family:monospace;color:#7c3aed;font-weight:600;min-width:100px}
.param-type{color:#666;font-size:.75rem;min-width:60px}
.param-desc{color:#aaa}
.param-req{color:#f59e0b;font-size:.7rem}
.example{margin-top:.75rem}
.example-title{font-size:.8rem;color:#888;font-weight:600;text-transform:uppercase;letter-spacing:.05em;margin-bottom:.4rem}
.code-block{background:#0d0d0d;border:1px solid #1a1a1a;border-radius:6px;padding:.75rem 1rem;font-family:monospace;font-size:.8rem;color:#ccc;overflow-x:auto;white-space:pre;position:relative}
.try-btn{display:inline-block;margin-top:.5rem;padding:.4rem .8rem;background:#7c3aed;color:#fff;border:none;border-radius:6px;cursor:pointer;font-size:.8rem;font-weight:600;transition:background .2s}
.try-btn:hover{background:#6d28d9}
.try-btn:disabled{background:#333;color:#666;cursor:not-allowed}
.try-result{margin-top:.5rem;display:none}
.try-result.active{display:block}
.section-intro{color:#888;font-size:.9rem;margin-bottom:1rem}
.free{color:#10b981;font-size:.75rem;padding:.15rem .5rem;border-radius:10px;background:#10b98122;border:1px solid #10b98144}
.badge{display:inline-block;background:#1a1a2e;border:1px solid #333;border-radius:6px;padding:.15rem .5rem;font-size:.8rem;color:#7c3aed;margin-right:.5rem}
.auth-box{background:#111;border:1px solid #222;border-radius:8px;padding:1.25rem;margin:1.5rem 0}
.auth-box h3{margin-top:0}
footer{margin-top:3rem;padding-top:1.5rem;border-top:1px solid #222;color:#555;font-size:.85rem;display:flex;justify-content:space-between;flex-wrap:wrap;gap:.5rem}
footer a{color:#7c3aed}
@media(max-width:640px){.endpoint-header{flex-direction:column;align-items:flex-start}.param{flex-direction:column;gap:.1rem}}
</style>
</head>
<body>
<div class="container">
<h1>WoT Scoring API</h1>
<p class="subtitle">Complete reference for the Nostr Web of Trust scoring service</p>
<span class="badge">NIP-85</span>
<span class="badge">27 Endpoints</span>
<span class="badge">L402 Lightning Paywall</span>
<span class="badge">REST/JSON</span>

<div class="auth-box">
<h3>Authentication &amp; Pricing</h3>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0">All endpoints support <strong>CORS</strong> and accept <strong>hex pubkeys</strong>, <strong>npub</strong> (bech32), or <strong>NIP-05 identifiers</strong>.</p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>Free tier:</strong> 10 requests/day per IP on priced endpoints. Unpriced endpoints are unlimited.</p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>L402 payment flow:</strong> Request → 402 response with Lightning invoice → Pay invoice → Retry with <code style="color:#7c3aed">X-Payment-Hash</code> header.</p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>Rate limit:</strong> 100 requests/min per IP.</p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>Base URL:</strong> <code style="color:#7c3aed">https://wot.klabo.world</code></p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>OpenAPI Spec:</strong> <a href="/openapi.json" style="color:#7c3aed">GET /openapi.json</a> — machine-readable API specification</p>
<p style="color:#aaa;font-size:.9rem;margin:.5rem 0"><strong>API Explorer:</strong> <a href="/swagger" style="color:#7c3aed">Swagger UI</a> — interactive API testing in your browser</p>
</div>

<div class="nav">
<strong style="color:#888;font-size:.85rem">Jump to:</strong>
<a href="#scoring">Scoring</a>
<a href="#personalized">Personalized</a>
<a href="#graph">Graph</a>
<a href="#identity">Identity</a>
<a href="#temporal">Temporal</a>
<a href="#moderation">Moderation</a>
<a href="#engagement">Engagement</a>
<a href="#ranking">Ranking</a>
<a href="#infrastructure">Infrastructure</a>
</div>

<!-- ===== SCORING ===== -->
<h2 id="scoring">Scoring</h2>
<p class="section-intro">Core trust scoring powered by PageRank over the Nostr follow graph.</p>

<div class="endpoint-card" id="ep-score">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/score</span>
<span class="price-tag">1 sat</span>
</div>
<div class="desc">Get the WoT trust score for any Nostr pubkey. Returns PageRank-based score (0-100), follower/engagement stats, topics, and external assertions.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey, npub, or NIP-05 identifier <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Example</div>
<div class="code-block">curl "https://wot.klabo.world/score?pubkey=npub1sg6plzptd64u62a878hep2kev88swjh3tw00gjsfl8f237lmu63q0uf63m"</div>
</div>
<div class="example">
<div class="example-title">Response</div>
<div class="code-block">{
  "pubkey": "82341f882b6eabcd2ba7f1ef90aad961cf074af15b9ef44a09f9d2a8fbfbe6a2",
  "score": 21, "raw_score": 0.00847, "found": true,
  "followers": 87421, "post_count": 1203, "reactions": 54302,
  "zap_amount": 1250000, "zap_count": 892,
  "topics": ["bitcoin", "nostr", "lightning"],
  "composite_score": 23,
  "graph_size": 145000
}</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/score?pubkey=82341f882b6eabcd2ba7f1ef90aad961cf074af15b9ef44a09f9d2a8fbfbe6a2')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-audit">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/audit</span>
<span class="price-tag">5 sats</span>
</div>
<div class="desc">Detailed breakdown of all scoring components: PageRank position, engagement metrics, top followers, external assertions, and graph context.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey, npub, or NIP-05 <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Example</div>
<div class="code-block">curl "https://wot.klabo.world/audit?pubkey=jb55@jb55.com"</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/audit?pubkey=32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-batch">
<div class="endpoint-header">
<span class="method method-post">POST</span>
<span class="path">/batch</span>
<span class="price-tag">10 sats</span>
</div>
<div class="desc">Score up to 100 pubkeys in a single request. Returns score, found status, and follower count for each.</div>
<div class="params">
<div class="params-title">Request Body (JSON)</div>
<div class="param"><span class="param-name">pubkeys</span><span class="param-type">string[]</span><span class="param-desc">Array of hex pubkeys or npubs (max 100) <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Example</div>
<div class="code-block">curl -X POST "https://wot.klabo.world/batch" \
  -d '{"pubkeys":["82341f...","32e18..."]}'</div>
</div>
</div>

<!-- ===== PERSONALIZED ===== -->
<h2 id="personalized">Personalized</h2>
<p class="section-intro">Trust scoring relative to a viewer's perspective, based on follow graph proximity.</p>

<div class="endpoint-card" id="ep-personalized">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/personalized</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Personalized trust score blending 50% global PageRank with 50% follow-graph proximity. Shows mutual follows, shared connections, and trusted followers of the target.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">viewer</span><span class="param-type">string</span><span class="param-desc">Viewer's pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">target</span><span class="param-type">string</span><span class="param-desc">Target pubkey to evaluate <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Example</div>
<div class="code-block">curl "https://wot.klabo.world/personalized?viewer=82341f...&amp;target=32e18..."</div>
</div>
</div>

<div class="endpoint-card" id="ep-similar">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/similar</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Find users with similar follow patterns using Jaccard similarity (70% follow overlap + 30% WoT score).</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Reference pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">limit</span><span class="param-type">int</span><span class="param-desc">Results to return (1-50, default 20)</span></div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/similar?pubkey=32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245&amp;limit=5')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-recommend">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/recommend</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Friends-of-friends follow recommendations. Shows people your follows trust that you don't yet follow, ranked by mutual connection count.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Your pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">limit</span><span class="param-type">int</span><span class="param-desc">Results (1-50, default 20)</span></div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/recommend?pubkey=82341f882b6eabcd2ba7f1ef90aad961cf074af15b9ef44a09f9d2a8fbfbe6a2&amp;limit=5')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-compare">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/compare</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Compare two pubkeys: relationship type, shared follows/followers, follow similarity (Jaccard), trust path between them, and full profile stats.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">a</span><span class="param-type">string</span><span class="param-desc">First pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">b</span><span class="param-type">string</span><span class="param-desc">Second pubkey <span class="param-req">required</span></span></div>
</div>
</div>

<!-- ===== GRAPH ===== -->
<h2 id="graph">Graph</h2>
<p class="section-intro">Explore the follow graph structure: trust paths, neighborhoods, and visualizations.</p>

<div class="endpoint-card" id="ep-graph-path">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/graph</span>
<span class="free">FREE</span>
</div>
<div class="desc">Two modes: <strong>Trust Path</strong> (shortest path between two pubkeys via BFS, max 6 hops) or <strong>Neighborhood</strong> (local follow network around a pubkey).</div>
<div class="params">
<div class="params-title">Path Mode</div>
<div class="param"><span class="param-name">from</span><span class="param-type">string</span><span class="param-desc">Source pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">to</span><span class="param-type">string</span><span class="param-desc">Target pubkey <span class="param-req">required</span></span></div>
<div class="params-title" style="margin-top:.5rem">Neighborhood Mode</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Center pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">depth</span><span class="param-type">int</span><span class="param-desc">Graph depth (1-2, default 1)</span></div>
<div class="param"><span class="param-name">limit</span><span class="param-type">int</span><span class="param-desc">Max neighbors (1-200, default 50)</span></div>
</div>
</div>

<div class="endpoint-card" id="ep-weboftrust">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/weboftrust</span>
<span class="price-tag">3 sats</span>
</div>
<div class="desc">D3.js-compatible force-directed graph data centered on a pubkey. Returns nodes with group classification (center/follow/follower/mutual) and edges for visualization.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Center pubkey <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">limit</span><span class="param-type">int</span><span class="param-desc">Nodes per direction (1-200, default 50)</span></div>
</div>
<div class="example">
<div class="example-title">Response Structure</div>
<div class="code-block">{
  "pubkey": "...", "score": 18, "rank": 3,
  "nodes": [{"id":"...","score":18,"followers":87421,"follows":892,"group":"center"}, ...],
  "links": [{"source":"...","target":"...","type":"follows"}, ...],
  "node_count": 61, "link_count": 145
}</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/weboftrust?pubkey=32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245&amp;limit=5')">Try it</button>
<div class="try-result"></div>
</div>

<!-- ===== IDENTITY ===== -->
<h2 id="identity">Identity</h2>
<p class="section-intro">NIP-05 identity resolution, verification, and reverse lookups with WoT trust profiles.</p>

<div class="endpoint-card" id="ep-nip05">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/nip05</span>
<span class="price-tag">1 sat</span>
</div>
<div class="desc">Resolve a NIP-05 identifier to a pubkey, verify it, and return a full trust profile with trust level classification.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">id</span><span class="param-type">string</span><span class="param-desc">NIP-05 identifier (e.g. user@domain.com) <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Trust Levels</div>
<div class="code-block">highly_trusted (80-100) | trusted (60-79) | moderate (40-59) | low (20-39) | untrusted (1-19) | unknown (0)</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/nip05?id=jb55@jb55.com')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-nip05-batch">
<div class="endpoint-header">
<span class="method method-post">POST</span>
<span class="path">/nip05/batch</span>
<span class="price-tag">5 sats</span>
</div>
<div class="desc">Batch NIP-05 resolution for up to 50 identifiers with concurrent DNS lookups.</div>
<div class="params">
<div class="params-title">Request Body (JSON)</div>
<div class="param"><span class="param-name">identifiers</span><span class="param-type">string[]</span><span class="param-desc">Array of NIP-05 identifiers (max 50) <span class="param-req">required</span></span></div>
</div>
</div>

<div class="endpoint-card" id="ep-nip05-reverse">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/nip05/reverse</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Reverse NIP-05 lookup: given a pubkey, fetch their profile from relays, extract their NIP-05 claim, then verify it resolves back (bidirectional verification).</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub <span class="param-req">required</span></span></div>
</div>
</div>

<!-- ===== TEMPORAL ===== -->
<h2 id="temporal">Temporal</h2>
<p class="section-intro">Time-aware trust scoring and historical analysis.</p>

<div class="endpoint-card" id="ep-timeline">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/timeline</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Historical trust growth timeline. Uses follow timestamps to reconstruct month-by-month follower accumulation with growth velocity and estimated scores.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Response (abbreviated)</div>
<div class="code-block">{
  "pubkey": "...", "current_score": 18, "current_followers": 87421,
  "points": [
    {"date":"2022-01","cumulative_follows":120,"new_follows":120,"estimated_score":3,"velocity":3.9},
    {"date":"2022-02","cumulative_follows":450,"new_follows":330,"estimated_score":5,"velocity":11.8}
  ],
  "first_follow": "2022-01-15T...", "latest_follow": "2026-02-08T..."
}</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/timeline?pubkey=82341f882b6eabcd2ba7f1ef90aad961cf074af15b9ef44a09f9d2a8fbfbe6a2')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-decay">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/decay</span>
<span class="price-tag">1 sat</span>
</div>
<div class="desc">Time-decayed trust score using exponential decay. Newer follows count more than older ones. Configurable half-life.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub <span class="param-req">required</span></span></div>
<div class="param"><span class="param-name">half_life</span><span class="param-type">int</span><span class="param-desc">Half-life in days (1-3650, default 365)</span></div>
</div>
<div class="example">
<div class="example-title">Algorithm</div>
<div class="code-block">weight(follow) = exp(-ln(2) * age_days / half_life)
decay_score = normalized(sum(weight * pagerank_contribution))</div>
</div>
</div>

<div class="endpoint-card" id="ep-decay-top">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/decay/top</span>
<span class="free">FREE</span>
</div>
<div class="desc">Top pubkeys by time-decayed score. Shows rank changes vs static PageRank — identifies accounts gaining or losing trust momentum.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">half_life</span><span class="param-type">int</span><span class="param-desc">Half-life in days (1-3650, default 365)</span></div>
<div class="param"><span class="param-name">limit</span><span class="param-type">int</span><span class="param-desc">Results (1-200, default 50)</span></div>
</div>
</div>

<!-- ===== MODERATION ===== -->
<h2 id="moderation">Moderation</h2>
<p class="section-intro">Spam detection using multi-signal WoT-based analysis.</p>

<div class="endpoint-card" id="ep-spam">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/spam</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Multi-signal spam probability analysis. Combines WoT score, follow ratio, account age, engagement, reports, and activity patterns into a 0-100% spam probability with detailed signal breakdown.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Signal Weights</div>
<div class="code-block">wot_score: 0.30 | follow_ratio: 0.15 | account_age: 0.15
engagement: 0.15 | reports: 0.15 | activity_pattern: 0.10

Thresholds: &gt;= 70%% likely_spam | 40-70%% suspicious | &lt; 40%% likely_human</div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/spam?pubkey=32e1827635450ebb3c5a7d12c1f8e7b2b514439ac10a67eef3d9fd9c5c68e245')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-spam-batch">
<div class="endpoint-header">
<span class="method method-post">POST</span>
<span class="path">/spam/batch</span>
<span class="price-tag">10 sats</span>
</div>
<div class="desc">Bulk spam check for up to 100 pubkeys. Returns compact classification results with aggregated summary counts.</div>
<div class="params">
<div class="params-title">Request Body (JSON)</div>
<div class="param"><span class="param-name">pubkeys</span><span class="param-type">string[]</span><span class="param-desc">Array of hex pubkeys or npubs (max 100) <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Response (abbreviated)</div>
<div class="code-block">{
  "results": [{"pubkey":"...","spam_probability":0.12,"classification":"likely_human","summary":"..."}],
  "summary": {"likely_human":1,"suspicious":0,"likely_spam":0,"errors":0}
}</div>
</div>
</div>

<!-- ===== VERIFICATION ===== -->
<h2 id="verification">Verification</h2>
<p class="section-intro">Cross-provider NIP-85 assertion verification.</p>

<div class="endpoint-card" id="ep-verify">
<div class="endpoint-header">
<span class="method method-post">POST</span>
<span class="path">/verify</span>
<span class="price-tag">2 sats</span>
</div>
<div class="desc">Verify a NIP-85 kind 30382 assertion from any provider against our graph data. Checks cryptographic signature, then cross-references claimed rank and follower count. Returns verdict: consistent, divergent, unverifiable, or invalid.</div>
<div class="params">
<div class="params-title">Request Body (JSON)</div>
<div class="param"><span class="param-name">event</span><span class="param-type">Nostr Event</span><span class="param-desc">A kind 30382 event with id, pubkey, created_at, kind, tags, content, sig <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Response</div>
<div class="code-block">{
  "valid": true,
  "verdict": "consistent",
  "kind": 30382,
  "provider_pubkey": "abc123...",
  "subject_pubkey": "def456...",
  "checks": [
    {"field": "rank", "claimed": 42, "observed": 45, "status": "close"},
    {"field": "followers", "claimed": 150, "observed": 150, "status": "match"}
  ],
  "match_count": 2,
  "total_checks": 2,
  "graph_size": 51319
}</div>
</div>
</div>

<div class="endpoint-card" id="ep-anomalies">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/anomalies</span>
<span class="price-tag">3 sats</span>
</div>
<div class="desc">Trust anomaly detection: analyzes a pubkey's trust graph for suspicious patterns including follow-farming (high follow-back ratio), ghost/bot followers (zero-score followers), trust concentration (single-source dependency), score-follower divergence, and excessive following. Returns individual anomaly flags with severity levels and an overall risk assessment.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub to analyze <span class="param-req">required</span></span></div>
</div>
<div class="example">
<div class="example-title">Response</div>
<div class="code-block">{
  "pubkey": "abc123...",
  "score": 42,
  "rank": 1500,
  "followers": 500,
  "follows": 480,
  "follow_back_ratio": 0.96,
  "ghost_followers": 350,
  "ghost_ratio": 0.7,
  "top_follower_share": 0.15,
  "score_percentile": 0.85,
  "anomalies": [
    {
      "type": "follow_farming",
      "severity": "high",
      "description": "Follows back 96% of 500 followers...",
      "value": 0.96,
      "threshold": 0.9
    }
  ],
  "anomaly_count": 1,
  "risk_level": "high",
  "graph_size": 51319
}</div>
</div>
</div>

<!-- ===== ENGAGEMENT ===== -->
<h2 id="engagement">Engagement</h2>
<p class="section-intro">Event-level and metadata scoring for NIP-85 assertions.</p>

<div class="endpoint-card" id="ep-metadata">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/metadata</span>
<span class="free">FREE</span>
</div>
<div class="desc">Full NIP-85 engagement metadata: posts, replies, reactions, zaps sent/received, and first event timestamp.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Hex pubkey or npub <span class="param-req">required</span></span></div>
</div>
</div>

<div class="endpoint-card" id="ep-event">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/event</span>
<span class="free">FREE</span>
</div>
<div class="desc">Engagement score for a specific Nostr event (kind 30383): comments, reposts, reactions, zaps.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">id</span><span class="param-type">string</span><span class="param-desc">Event ID (hex) <span class="param-req">required</span></span></div>
</div>
</div>

<div class="endpoint-card" id="ep-external">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/external</span>
<span class="free">FREE</span>
</div>
<div class="desc">External identifier scores (NIP-73, kind 30385). Without an id parameter, returns top 50 identifiers. With id, returns the specific identifier's engagement data.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">id</span><span class="param-type">string</span><span class="param-desc">External identifier (optional — omit for top 50)</span></div>
</div>
</div>

<!-- ===== RANKING ===== -->
<h2 id="ranking">Ranking</h2>

<div class="endpoint-card" id="ep-top">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/top</span>
<span class="free">FREE</span>
</div>
<div class="desc">Top 50 most-trusted pubkeys by PageRank with normalized scores and follower counts.</div>
</div>

<div class="endpoint-card" id="ep-export">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/export</span>
<span class="free">FREE</span>
</div>
<div class="desc">Export all pubkeys and scores as a JSON array. Full graph dump for offline analysis.</div>
</div>

<!-- ===== INFRASTRUCTURE ===== -->
<h2 id="infrastructure">Infrastructure</h2>

<div class="endpoint-card" id="ep-relay">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/relay</span>
<span class="free">FREE</span>
</div>
<div class="desc">Relay trust score combining infrastructure metrics from trustedrelays.xyz (70%) with operator WoT score (30%). Includes uptime, quality, and accessibility data.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">url</span><span class="param-type">string</span><span class="param-desc">Relay URL (e.g. wss://relay.damus.io) <span class="param-req">required</span></span></div>
</div>
<button class="try-btn" onclick="tryEndpoint(this,'/relay?url=wss://relay.damus.io')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-authorized">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/authorized</span>
<span class="free">FREE</span>
</div>
<div class="desc">Kind 10040 authorized users who declared trust in this NIP-85 provider.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Filter by specific provider (optional)</span></div>
</div>
</div>

<div class="endpoint-card" id="ep-communities">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/communities</span>
<span class="free">FREE</span>
</div>
<div class="desc">Trust communities detected via label propagation over the follow graph. With a pubkey, returns that user's community and peers.</div>
<div class="params">
<div class="params-title">Parameters</div>
<div class="param"><span class="param-name">pubkey</span><span class="param-type">string</span><span class="param-desc">Get community for specific pubkey (optional)</span></div>
</div>
</div>

<div class="endpoint-card" id="ep-publish">
<div class="endpoint-header">
<span class="method method-post">POST</span>
<span class="path">/publish</span>
<span class="free">FREE</span>
</div>
<div class="desc">Publish all NIP-85 assertion events (kinds 30382, 30383, 30384, 30385) and NIP-89 handler info to configured relays.</div>
</div>

<div class="endpoint-card" id="ep-providers">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/providers</span>
<span class="free">FREE</span>
</div>
<div class="desc">External NIP-85 assertion providers and their assertion counts.</div>
</div>

<div class="endpoint-card" id="ep-stats">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/stats</span>
<span class="free">FREE</span>
</div>
<div class="desc">Service metadata: graph size, algorithm config, relay list, rate limits, and uptime.</div>
<button class="try-btn" onclick="tryEndpoint(this,'/stats')">Try it</button>
<div class="try-result"></div>
</div>

<div class="endpoint-card" id="ep-health">
<div class="endpoint-header">
<span class="method method-get">GET</span>
<span class="path">/health</span>
<span class="free">FREE</span>
</div>
<div class="desc">Health check endpoint. Returns ready/starting status and graph statistics.</div>
</div>

<footer>
<span><a href="/">← Back to WoT Scoring</a></span>
<span>Built for <a href="https://nosfabrica.com/wotathon/">WoT-a-thon</a></span>
<span><a href="https://github.com/joelklabo/wot-scoring">Source (MIT)</a></span>
<span>Operator: <a href="https://njump.me/max@klabo.world">max@klabo.world</a></span>
</footer>
</div>
<script>
function tryEndpoint(btn,path){
var res=btn.nextElementSibling;
btn.disabled=true;btn.textContent="Loading...";
res.className="try-result active";
res.innerHTML='<div style="color:#555">Fetching...</div>';
fetch(path).then(function(r){return r.json()}).then(function(d){
btn.disabled=false;btn.textContent="Try it";
res.innerHTML='<div class="code-block" style="max-height:300px;overflow:auto">'+JSON.stringify(d,null,2)+'</div>';
}).catch(function(e){
btn.disabled=false;btn.textContent="Try it";
res.innerHTML='<div class="code-block" style="color:#f87171">Error: '+e.message+'</div>';
});}
</script>
</body>
</html>`

const swaggerPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>API Explorer — WoT Scoring</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
<style>
body{margin:0;background:#1a1a2e}
.topbar{display:none!important}
.swagger-ui .info{margin:20px 0 10px 0}
.swagger-ui .info .title{color:#fff}
.swagger-ui .info .description p{color:#ccc}
.swagger-ui .scheme-container{background:#111;border-bottom:1px solid #333}
.swagger-ui .opblock-tag{color:#e0e0e0;border-bottom:1px solid #222}
.swagger-ui .opblock .opblock-summary-method{font-weight:700}
.swagger-ui .btn{border-radius:4px}
.nav-bar{background:#0a0a0a;padding:.75rem 1.5rem;display:flex;gap:1.5rem;align-items:center;border-bottom:1px solid #222}
.nav-bar a{color:#aaa;text-decoration:none;font-size:.9rem;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
.nav-bar a:hover{color:#fff}
.nav-bar .active{color:#7c3aed;font-weight:600}
</style>
</head>
<body>
<div class="nav-bar">
<a href="/">Home</a>
<a href="/docs">Docs</a>
<a class="active" href="/swagger">Explorer</a>
<a href="/openapi.json">OpenAPI</a>
</div>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
SwaggerUIBundle({
  url: "/openapi.json",
  dom_id: "#swagger-ui",
  deepLinking: true,
  presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
  layout: "BaseLayout",
  defaultModelsExpandDepth: -1,
  docExpansion: "list",
  tryItOutEnabled: true,
  requestInterceptor: function(req) {
    // Auto-set base URL for try-it-out
    if (!req.url.startsWith("http")) {
      req.url = window.location.origin + req.url;
    }
    return req;
  }
});
</script>
</body>
</html>`

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
<div class="tab" data-tab="nip05">NIP-05 Verify</div>
<div class="tab" data-tab="nip05reverse">NIP-05 Reverse</div>
<div class="tab" data-tab="timeline">Timeline</div>
<div class="tab" data-tab="spam">Spam Check</div>
<div class="tab" data-tab="trustgraph">Trust Graph</div>
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

<div class="tab-content" id="tab-nip05">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Verify a NIP-05 identity and see their Web of Trust profile</p>
<div class="search">
<input type="text" id="nip05-input" placeholder="Enter NIP-05 identifier (e.g. user@domain.com)...">
<div id="nip05-result"></div>
</div>
</div>

<div class="tab-content" id="tab-nip05reverse">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Find the NIP-05 identity for a pubkey (reverse lookup). Fetches profile from relays and verifies bidirectionally.</p>
<div class="search">
<input type="text" id="nip05reverse-input" placeholder="Enter hex pubkey or npub to find NIP-05 identity...">
<div id="nip05reverse-result"></div>
</div>
</div>

<div class="tab-content" id="tab-timeline">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">See how a pubkey's trust grew over time. Uses follow timestamps to reconstruct trust evolution.</p>
<div class="search">
<input type="text" id="timeline-input" placeholder="Enter hex pubkey or npub to see trust timeline...">
<div id="timeline-result"></div>
</div>
</div>

<div class="tab-content" id="tab-spam">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Analyze a pubkey for spam indicators using WoT graph signals, engagement metrics, and behavioral patterns.</p>
<div class="search">
<input type="text" id="spam-input" placeholder="Enter hex pubkey or npub to check for spam...">
<div id="spam-result"></div>
</div>
</div>

<div class="tab-content" id="tab-trustgraph">
<p style="color:#888;margin-bottom:1rem;font-size:.9rem">Visualize a pubkey's trust network — who they follow, who follows them, and mutual connections.</p>
<div class="search">
<input type="text" id="graph-input" placeholder="Enter hex pubkey or npub to visualize trust network...">
<div id="graph-controls" style="display:none;margin:1rem 0">
<label style="color:#888;font-size:.85rem">Max nodes per direction: <input type="range" id="graph-limit" min="10" max="100" value="30" style="vertical-align:middle"> <span id="graph-limit-val">30</span></label>
</div>
<div id="graph-info" style="margin-bottom:1rem"></div>
<div id="graph-container" style="width:100%%;background:#0a0a0a;border-radius:8px;overflow:hidden"></div>
<div id="graph-legend" style="display:none;margin-top:1rem;padding:1rem;background:#111;border-radius:8px">
<span style="color:#f7931a">● Center</span>&nbsp;&nbsp;
<span style="color:#4ecdc4">● Follows</span>&nbsp;&nbsp;
<span style="color:#ff6b6b">● Followers</span>&nbsp;&nbsp;
<span style="color:#ffd93d">● Mutual</span>
</div>
</div>
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
<div class="kind"><span class="kind-num">30382</span><span class="kind-desc">User Trust Assertions — PageRank score, follower count, post/reply/reaction/zap stats, topics, active hours, reports</span></div>
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
<div class="endpoint"><span class="method">GET</span><span class="path">/nip05?id=user@domain</span><span class="desc">— NIP-05 verification + WoT trust profile</span></div>
<div class="endpoint"><span class="method">POST</span><span class="path">/nip05/batch</span><span class="desc">— Bulk NIP-05 verification (up to 50 identifiers)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/nip05/reverse?pubkey=&lt;hex&gt;</span><span class="desc">— Reverse NIP-05 lookup (pubkey → identity, bidirectional verification)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/spam?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Spam detection: multi-signal analysis with classification</span></div>
<div class="endpoint"><span class="method">POST</span><span class="path">/spam/batch</span><span class="desc">— Bulk spam check (up to 100 pubkeys)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/weboftrust?pubkey=&lt;hex|npub&gt;</span><span class="desc">— D3.js-compatible trust graph visualization</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/timeline?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Historical trust growth timeline</span></div>
<div class="endpoint"><span class="method">POST</span><span class="path">/verify</span><span class="desc">— Cross-provider NIP-85 assertion verification</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/anomalies?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Trust anomaly detection and risk assessment</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/providers</span><span class="desc">— External NIP-85 assertion providers</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/docs</span><span class="desc">— Interactive API documentation</span></div>
</div>

<div class="nip85-kinds" style="margin-top:2rem">
<h2 style="font-size:1.3rem;color:#fff;margin-bottom:1rem">L402 Lightning Paywall</h2>
<p style="color:#aaa;font-size:.95rem;margin-bottom:1rem">Pay-per-query via Lightning Network. Free tier: 10 requests/day per IP. After that, pay sats per query.</p>
<div style="display:grid;grid-template-columns:repeat(auto-fit,minmax(180px,1fr));gap:.5rem">
<div class="kind"><span class="kind-num" style="background:#16a34a">1 sat</span><span class="kind-desc">/score, /decay, /nip05</span></div>
<div class="kind"><span class="kind-num" style="background:#2563eb">2 sats</span><span class="kind-desc">/personalized, /similar, /recommend, /compare, /nip05/reverse, /timeline, /spam, /verify, /weboftrust (3 sats), /anomalies (3 sats)</span></div>
<div class="kind"><span class="kind-num" style="background:#9333ea">5 sats</span><span class="kind-desc">/audit, /nip05/batch</span></div>
<div class="kind"><span class="kind-num" style="background:#dc2626">10 sats</span><span class="kind-desc">/batch (up to 100 pubkeys)</span></div>
</div>
<p style="color:#666;font-size:.85rem;margin-top:.75rem">Endpoints not listed above are free and unlimited. Payment via L402 protocol: request → 402 + invoice → pay → retry with X-Payment-Hash header.</p>
</div>

<footer>
<span>Built for <a href="https://nosfabrica.com/wotathon/">WoT-a-thon</a></span>
<span><a href="/docs">API Docs</a></span>
<span><a href="/swagger">API Explorer</a></span>
<span><a href="/openapi.json">OpenAPI Spec</a></span>
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
html+='</div>';
if(d.topics&&d.topics.length){html+='<div style="margin-top:.75rem"><span style="color:#888;font-size:.85rem">Topics: </span>';d.topics.forEach(t=>{html+='<span style="background:#1a1a2e;color:#f39c12;padding:2px 8px;border-radius:12px;font-size:.8rem;margin:2px">'+t+'</span>'});html+='</div>'}
if(d.active_hours_start!==undefined){html+='<div style="color:#888;font-size:.85rem;margin-top:.5rem">Active: '+d.active_hours_start+':00–'+d.active_hours_end+':00 UTC</div>'}
if(d.reports_received){html+='<div style="color:#e74c3c;font-size:.85rem;margin-top:.25rem">Reports received: '+d.reports_received+'</div>'}
html+='</div>';
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

// NIP-05 lookup
const nip05Input=document.getElementById("nip05-input"),nip05Result=document.getElementById("nip05-result");
let nip05Timer;
nip05Input.addEventListener("input",()=>{clearTimeout(nip05Timer);const v=nip05Input.value.trim();if(!v){nip05Result.innerHTML="";return}
if(!v.includes("@")){nip05Result.innerHTML='<div style="color:#888;margin-top:.5rem;font-size:.9rem">Enter a NIP-05 identifier like user@domain.com</div>';return}
nip05Timer=setTimeout(()=>{
nip05Result.innerHTML='<div style="color:#555;margin-top:.5rem">Resolving NIP-05...</div>';
fetch("/nip05?id="+encodeURIComponent(v)).then(r=>r.json()).then(d=>{
if(d.error){err(nip05Result,d.error);return}
const levelColors={"highly_trusted":"#10b981","trusted":"#3b82f6","moderate":"#f59e0b","low":"#f97316","untrusted":"#ef4444","unknown":"#666"};
const lc=levelColors[d.trust_level]||"#666";
let html='<div class="score-card fade-in">';
html+='<div style="display:flex;align-items:baseline;gap:1rem;flex-wrap:wrap">';
html+='<div class="score-big">'+d.score+'/100</div>';
html+='<span style="background:'+lc+'22;color:'+lc+';padding:.25rem .75rem;border-radius:6px;font-size:.95rem;font-weight:600;border:1px solid '+lc+'44">'+d.trust_level.replace("_"," ")+'</span>';
html+='</div>';
html+='<div style="color:#10b981;margin-top:.5rem;font-size:.9rem">&#10003; Verified: '+d.nip05+'</div>';
html+='<div style="color:#888;margin-top:.25rem;font-family:monospace;font-size:.85rem">'+d.pubkey+'</div>';
html+='<div class="score-details">';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.followers||0)+'</div><div class="score-detail-label">Followers</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.post_count||0)+'</div><div class="score-detail-label">Posts</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reactions||0)+'</div><div class="score-detail-label">Reactions</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reply_count||0)+'</div><div class="score-detail-label">Replies</div></div>';
html+='</div>';
if(d.topics&&d.topics.length){html+='<div style="margin-top:.75rem"><span style="color:#888;font-size:.85rem">Topics: </span>';d.topics.forEach(t=>{html+='<span style="background:#1a1a2e;color:#f39c12;padding:2px 8px;border-radius:12px;font-size:.8rem;margin:2px">'+t+'</span>'});html+='</div>'}
if(d.nip05_relays&&d.nip05_relays.length){html+='<div style="margin-top:.5rem;font-size:.85rem;color:#888">Relays: '+d.nip05_relays.join(", ")+'</div>'}
html+='</div>';
nip05Result.innerHTML=html;
}).catch(()=>{err(nip05Result,"Error resolving NIP-05 identifier")})},600)});

const nip05revInput=document.getElementById("nip05reverse-input"),nip05revResult=document.getElementById("nip05reverse-result");
let nip05revTimer;
nip05revInput.addEventListener("input",()=>{clearTimeout(nip05revTimer);const v=nip05revInput.value.trim();if(!v){nip05revResult.innerHTML="";return}
if(v.length<63&&!v.startsWith("npub1")){nip05revResult.innerHTML='<div style="color:#888;margin-top:.5rem;font-size:.9rem">Enter a 64-char hex pubkey or npub</div>';return}
nip05revTimer=setTimeout(()=>{
nip05revResult.innerHTML='<div style="color:#555;margin-top:.5rem">Looking up NIP-05 from relays...</div>';
fetch("/nip05/reverse?pubkey="+encodeURIComponent(pk)).then(r=>r.json()).then(d=>{
const levelColors={"highly_trusted":"#10b981","trusted":"#3b82f6","moderate":"#f59e0b","low":"#f97316","untrusted":"#ef4444","unknown":"#666"};
let html='<div class="score-card fade-in">';
if(d.nip05&&d.verified){
const lc=levelColors[d.trust_level]||"#666";
html+='<div style="display:flex;align-items:baseline;gap:1rem;flex-wrap:wrap">';
html+='<div class="score-big">'+d.score+'/100</div>';
html+='<span style="background:'+lc+'22;color:'+lc+';padding:.25rem .75rem;border-radius:6px;font-size:.95rem;font-weight:600;border:1px solid '+lc+'44">'+d.trust_level.replace("_"," ")+'</span>';
html+='</div>';
html+='<div style="color:#10b981;margin-top:.5rem;font-size:.95rem">&#10003; '+d.nip05+'</div>';
if(d.display_name){html+='<div style="color:#ccc;font-size:.9rem;margin-top:.25rem">'+d.display_name+'</div>'}
html+='<div style="color:#888;margin-top:.25rem;font-family:monospace;font-size:.85rem">'+d.pubkey+'</div>';
html+='<div class="score-details">';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.followers||0)+'</div><div class="score-detail-label">Followers</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.post_count||0)+'</div><div class="score-detail-label">Posts</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reactions||0)+'</div><div class="score-detail-label">Reactions</div></div>';
html+='<div class="score-detail"><div class="score-detail-value">'+fmt(d.reply_count||0)+'</div><div class="score-detail-label">Replies</div></div>';
html+='</div>';
if(d.topics&&d.topics.length){html+='<div style="margin-top:.75rem"><span style="color:#888;font-size:.85rem">Topics: </span>';d.topics.forEach(t=>{html+='<span style="background:#1a1a2e;color:#f39c12;padding:2px 8px;border-radius:12px;font-size:.8rem;margin:2px">'+t+'</span>'});html+='</div>'}
}else{
html+='<div style="color:#f97316;margin-top:.5rem">&#10007; No verified NIP-05 found</div>';
if(d.display_name){html+='<div style="color:#ccc;font-size:.9rem;margin-top:.25rem">'+d.display_name+'</div>'}
html+='<div style="color:#888;margin-top:.25rem;font-family:monospace;font-size:.85rem">'+d.pubkey+'</div>';
if(d.nip05){html+='<div style="color:#888;font-size:.85rem;margin-top:.25rem">Claimed: '+d.nip05+' (verification failed)</div>'}
if(d.error){html+='<div style="color:#666;font-size:.85rem;margin-top:.25rem">'+d.error+'</div>'}
html+='<div style="color:#888;font-size:.9rem;margin-top:.5rem">Score: '+(d.score||0)+'/100</div>';
}
html+='</div>';
nip05revResult.innerHTML=html;
}).catch(()=>{err(nip05revResult,"Error looking up NIP-05")})},800)});

const tlInput=document.getElementById("timeline-input"),tlResult=document.getElementById("timeline-result");
let tlTimer;
tlInput.addEventListener("input",()=>{clearTimeout(tlTimer);const v=tlInput.value.trim();if(!v){tlResult.innerHTML="";return}
if(v.length<63&&!v.startsWith("npub1")){tlResult.innerHTML='<div style="color:#888;margin-top:.5rem;font-size:.9rem">Enter a 64-char hex pubkey or npub</div>';return}
tlTimer=setTimeout(()=>{
tlResult.innerHTML='<div style="color:#555;margin-top:.5rem">Loading timeline...</div>';
const pk=v.length===64?v:v;
fetch("/timeline?pubkey="+encodeURIComponent(pk)).then(r=>r.json()).then(d=>{
if(d.error){err(tlResult,d.error);return}
let html='<div class="score-card fade-in">';
html+='<div style="display:flex;align-items:baseline;gap:1rem;flex-wrap:wrap">';
html+='<div class="score-big">'+d.current_score+'/100</div>';
html+='<span style="color:#888;font-size:.9rem">'+fmt(d.current_followers)+' followers</span>';
html+='</div>';
if(d.first_follow){html+='<div style="color:#888;font-size:.85rem;margin-top:.5rem">First follow: '+d.first_follow.slice(0,10)+' &mdash; Latest: '+(d.latest_follow||'').slice(0,10)+'</div>'}
html+='<div style="color:#666;font-size:.85rem">'+d.followers_with_dates+' of '+d.total_followers+' followers have date data</div>';
if(d.points&&d.points.length>0){
html+='<div style="margin-top:1rem">';
const maxF=d.points[d.points.length-1].cumulative_follows||1;
d.points.forEach(p=>{
const pct=Math.round((p.cumulative_follows/maxF)*100);
const barColor=p.velocity>1?'#10b981':p.velocity>0.3?'#3b82f6':'#6366f1';
html+='<div style="display:flex;align-items:center;gap:.5rem;margin:.25rem 0;font-size:.8rem">';
html+='<span style="width:60px;color:#888;flex-shrink:0">'+p.date+'</span>';
html+='<div style="flex:1;background:#1a1a2e;border-radius:4px;height:20px;position:relative;overflow:hidden">';
html+='<div style="width:'+pct+'%%;height:100%%;background:'+barColor+';border-radius:4px;transition:width .3s"></div>';
html+='</div>';
html+='<span style="width:40px;color:#ccc;text-align:right;flex-shrink:0">'+p.cumulative_follows+'</span>';
html+='<span style="width:50px;color:#888;text-align:right;flex-shrink:0;font-size:.75rem">+'+p.new_follows+'</span>';
html+='</div>'});
html+='</div>';
html+='<div style="margin-top:.5rem;font-size:.75rem;color:#666">Bar = cumulative followers | +N = new that month | Color: green=fast, blue=moderate, purple=slow growth</div>';
}else{html+='<div style="color:#888;margin-top:1rem">No timeline data available (followers lack date information)</div>'}
html+='</div>';
tlResult.innerHTML=html;
}).catch(()=>{err(tlResult,"Error loading timeline")})},600)});

const spamInput=document.getElementById("spam-input"),spamResult=document.getElementById("spam-result");
let spamTimer;
spamInput.addEventListener("input",()=>{clearTimeout(spamTimer);const v=spamInput.value.trim();if(!v){spamResult.innerHTML="";return}
if(v.length<63&&!v.startsWith("npub1")){spamResult.innerHTML='<div style="color:#888;margin-top:.5rem;font-size:.9rem">Enter a 64-char hex pubkey or npub</div>';return}
spamTimer=setTimeout(()=>{
spamResult.innerHTML='<div style="color:#555;margin-top:.5rem">Analyzing for spam signals...</div>';
fetch("/spam?pubkey="+encodeURIComponent(v)).then(r=>r.json()).then(d=>{
if(d.error){err(spamResult,d.error);return}
let html='<div class="score-card fade-in">';
const probPct=Math.round(d.spam_probability*100);
const cls=d.classification;
const clsColor=cls==="likely_spam"?"#ef4444":cls==="suspicious"?"#f59e0b":"#10b981";
html+='<div style="display:flex;align-items:baseline;gap:1rem;flex-wrap:wrap">';
html+='<div class="score-big" style="color:'+clsColor+'">'+probPct+'%%</div>';
html+='<span style="color:'+clsColor+';font-size:1.1rem;font-weight:600">'+cls.replace(/_/g," ").toUpperCase()+'</span>';
html+='</div>';
html+='<div style="color:#888;font-size:.9rem;margin-top:.5rem">'+d.summary+'</div>';
html+='<div style="margin-top:1rem"><h3 style="color:#ccc;font-size:.95rem;margin-bottom:.5rem">Signal Breakdown</h3>';
d.signals.forEach(s=>{
const sigPct=Math.round(s.score/s.weight*100);
const sigColor=sigPct>70?"#ef4444":sigPct>30?"#f59e0b":"#10b981";
html+='<div style="margin:.4rem 0;font-size:.85rem">';
html+='<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:2px">';
html+='<span style="color:#aaa">'+s.name.replace(/_/g," ")+'</span>';
html+='<span style="color:'+sigColor+';font-weight:500">'+s.score.toFixed(3)+' / '+s.weight.toFixed(2)+'</span>';
html+='</div>';
html+='<div style="background:#1a1a2e;border-radius:4px;height:8px;overflow:hidden">';
html+='<div style="width:'+Math.min(sigPct,100)+'%%;height:100%%;background:'+sigColor+';border-radius:4px"></div>';
html+='</div>';
html+='<div style="color:#666;font-size:.75rem;margin-top:1px">'+s.reason+'</div>';
html+='</div>'});
html+='</div></div>';
spamResult.innerHTML=html;
}).catch(()=>{err(spamResult,"Error analyzing pubkey")})},600)});

// Trust Graph tab
const graphInput=document.getElementById("graph-input"),graphContainer=document.getElementById("graph-container"),graphInfo=document.getElementById("graph-info"),graphControls=document.getElementById("graph-controls"),graphLegend=document.getElementById("graph-legend"),graphLimitSlider=document.getElementById("graph-limit"),graphLimitVal=document.getElementById("graph-limit-val");
let graphTimer;
graphLimitSlider.addEventListener("input",()=>{graphLimitVal.textContent=graphLimitSlider.value});
function loadGraph(){clearTimeout(graphTimer);const v=graphInput.value.trim();if(!v){graphInfo.innerHTML="";graphContainer.innerHTML="";graphControls.style.display="none";graphLegend.style.display="none";return}
if(v.length<63&&!v.startsWith("npub1")){graphInfo.innerHTML='<div style="color:#888;margin-top:.5rem;font-size:.9rem">Enter a 64-char hex pubkey or npub</div>';return}
graphTimer=setTimeout(()=>{
graphInfo.innerHTML='<div style="color:#555">Loading trust network...</div>';
const lim=graphLimitSlider.value;
fetch("/weboftrust?pubkey="+encodeURIComponent(v)+"&limit="+lim).then(r=>r.json()).then(d=>{
if(d.error){err(graphInfo,d.error);return}
graphControls.style.display="block";graphLegend.style.display="block";
let info='<div style="color:#ccc;font-size:.9rem">'+d.node_count+' nodes, '+d.link_count+' links | Score: '+d.score+' | Rank: #'+d.rank+'</div>';
graphInfo.innerHTML=info;
renderGraph(d);
}).catch(()=>{err(graphInfo,"Error loading trust graph")})},600)}
graphInput.addEventListener("input",loadGraph);
graphLimitSlider.addEventListener("change",loadGraph);

function renderGraph(data){
const W=graphContainer.clientWidth||800,H=500;
const nodes=data.nodes.map((n,i)=>({...n,x:W/2+(Math.random()-.5)*200,y:H/2+(Math.random()-.5)*200,vx:0,vy:0}));
const nodeMap={};nodes.forEach(n=>nodeMap[n.id]=n);
const links=data.links.filter(l=>nodeMap[l.source]&&nodeMap[l.target]);
const colors={center:"#f7931a",follow:"#4ecdc4",follower:"#ff6b6b",mutual:"#ffd93d"};
const sizes={center:8,follow:4,follower:4,mutual:5};
// Simple force simulation
for(let iter=0;iter<120;iter++){
// Repulsion
for(let i=0;i<nodes.length;i++){for(let j=i+1;j<nodes.length;j++){
let dx=nodes[j].x-nodes[i].x,dy=nodes[j].y-nodes[i].y;
let dist=Math.sqrt(dx*dx+dy*dy)||1;
let force=200/dist;
let fx=dx/dist*force,fy=dy/dist*force;
nodes[i].vx-=fx;nodes[i].vy-=fy;
nodes[j].vx+=fx;nodes[j].vy+=fy;
}}
// Attraction (links)
links.forEach(l=>{
let s=nodeMap[l.source],t=nodeMap[l.target];if(!s||!t)return;
let dx=t.x-s.x,dy=t.y-s.y;
let dist=Math.sqrt(dx*dx+dy*dy)||1;
let force=(dist-80)*0.01;
let fx=dx/dist*force,fy=dy/dist*force;
s.vx+=fx;s.vy+=fy;t.vx-=fx;t.vy-=fy;
});
// Center gravity
nodes.forEach(n=>{n.vx+=(W/2-n.x)*0.01;n.vy+=(H/2-n.y)*0.01});
// Apply
nodes.forEach(n=>{n.vx*=0.8;n.vy*=0.8;n.x+=n.vx;n.y+=n.vy;n.x=Math.max(20,Math.min(W-20,n.x));n.y=Math.max(20,Math.min(H-20,n.y))});
}
// Render SVG
let svg='<svg width="'+W+'" height="'+H+'" style="display:block">';
// Links
links.forEach(l=>{
let s=nodeMap[l.source],t=nodeMap[l.target];if(!s||!t)return;
svg+='<line x1="'+s.x+'" y1="'+s.y+'" x2="'+t.x+'" y2="'+t.y+'" stroke="#333" stroke-width="0.5" stroke-opacity="0.4"/>';
});
// Nodes
nodes.forEach(n=>{
let c=colors[n.group]||"#888",r=sizes[n.group]||4;
let short=n.id.substring(0,8);
svg+='<circle cx="'+n.x+'" cy="'+n.y+'" r="'+r+'" fill="'+c+'" stroke="'+c+'" stroke-opacity="0.3" stroke-width="2">';
svg+='<title>'+short+'... | Score: '+n.score+' | Follows: '+n.follows+' | Followers: '+n.followers+' | '+n.group+'</title></circle>';
});
// Center label
let cn=nodes.find(n=>n.group==="center");
if(cn){svg+='<text x="'+cn.x+'" y="'+(cn.y-12)+'" text-anchor="middle" fill="#f7931a" font-size="10" font-family="monospace">'+cn.id.substring(0,12)+'...</text>'}
svg+='</svg>';
graphContainer.innerHTML=svg;
}
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

		// Consume NIP-51 kind 10000 mute lists
		consumeMuteLists(ctx, muteStore)

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
				consumeMuteLists(ctx, muteStore)
				communities.DetectCommunities(graph, 10)
				stats := graph.Stats()
				log.Printf("Re-crawl complete: %d nodes, %d edges, %d events, %d addressable, %d external, %d ext_assertions, %d auths, %d mute_lists, %d communities",
					stats.Nodes, stats.Edges, events.EventCount(), events.AddressableCount(), external.Count(),
					externalAssertions.TotalAssertions(), authStore.TotalAuthorizations(), muteStore.TotalMuters(), communities.TotalCommunities())

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
			"mute_lists":           muteStore.TotalMuters(),
			"muted_pubkeys":        muteStore.TotalMuted(),
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
	http.HandleFunc("/nip05", handleNIP05)
	http.HandleFunc("/nip05/batch", handleNIP05Batch)
	http.HandleFunc("/nip05/reverse", handleNIP05Reverse)
	http.HandleFunc("/timeline", handleTimeline)
	http.HandleFunc("/spam", handleSpam)
	http.HandleFunc("/spam/batch", handleSpamBatch)
	http.HandleFunc("/weboftrust", handleWebOfTrust)
	http.HandleFunc("/blocked", handleBlocked)
	http.HandleFunc("/verify", handleVerify)
	http.HandleFunc("/anomalies", handleAnomalies)
	http.HandleFunc("/openapi.json", handleOpenAPI)
	http.HandleFunc("/docs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, docsPageHTML)
	})
	http.HandleFunc("/swagger", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, swaggerPageHTML)
	})
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
/nip05?id=user@domain — NIP-05 verification + WoT trust profile (resolves NIP-05 to pubkey, returns trust score)
POST /nip05/batch — Bulk NIP-05 verification (up to 50 identifiers, concurrent resolution)
/nip05/reverse?pubkey=<hex> — Reverse NIP-05 lookup (find NIP-05 identity from pubkey, verify bidirectionally)
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
