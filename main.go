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
	mu        sync.RWMutex
	follows   map[string][]string // pubkey -> list of followed pubkeys
	followers map[string][]string // pubkey -> list of followers
	scores    map[string]float64  // pubkey -> PageRank score
	lastBuild time.Time
}

func NewGraph() *Graph {
	return &Graph{
		follows:   make(map[string][]string),
		followers: make(map[string][]string),
		scores:    make(map[string]float64),
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

				for _, tag := range ev.Event.Tags {
					if tag[0] == "p" && len(tag) >= 2 {
						target := tag[1]
						graph.AddFollow(author, target)
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
.search{margin:2rem 0}
.search input{width:100%%;padding:.75rem 1rem;background:#111;border:1px solid #333;border-radius:8px;color:#fff;font-size:1rem}
.search input::placeholder{color:#555}
.search input:focus{outline:none;border-color:#7c3aed}
#result{margin-top:1rem;min-height:2rem}
.score-card{background:#111;border:1px solid #222;border-radius:8px;padding:1.5rem;margin-top:1rem}
.score-big{font-size:3rem;font-weight:700;color:#7c3aed}
.score-details{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:.75rem;margin-top:1rem}
.score-detail{text-align:center}
.score-detail-value{font-size:1.2rem;font-weight:600;color:#fff}
.score-detail-label{font-size:.75rem;color:#666}
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
</style>
</head>
<body>
<div class="container">
<h1>WoT Scoring</h1>
<p class="subtitle">NIP-85 Trusted Assertions for the Nostr Web of Trust</p>
<span class="badge">NIP-85</span>
<span class="badge">PageRank</span>
<span class="badge">Go</span>

<div class="stats">
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Nodes</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Edges</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Events Scored</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Articles</div></div>
<div class="stat"><div class="stat-value">%d</div><div class="stat-label">Identifiers</div></div>
<div class="stat"><div class="stat-value">%s</div><div class="stat-label">Uptime</div></div>
</div>

<div class="search">
<input type="text" id="pubkey-input" placeholder="Enter npub or hex pubkey to look up trust score..." autofocus>
<div id="result"></div>
</div>

<div class="leaderboard">
<h2>Trust Leaderboard</h2>
<table class="lb-table">
<thead><tr><th>Rank</th><th>Pubkey</th><th style="text-align:right">Score</th><th style="text-align:right">Followers</th></tr></thead>
<tbody id="lb-body"><tr><td colspan="4" style="color:#555">Loading...</td></tr></tbody>
</table>
</div>

<div class="nip85">
<h2>NIP-85 Assertion Kinds</h2>
<div class="kind"><span class="kind-num">30382</span><span class="kind-desc">User Trust Assertions — PageRank score, follower count, post/reply/reaction/zap stats</span></div>
<div class="kind"><span class="kind-num">30383</span><span class="kind-desc">Event Assertions — engagement scores for individual notes (comments, reposts, reactions, zaps)</span></div>
<div class="kind"><span class="kind-num">30384</span><span class="kind-desc">Addressable Event Assertions — scores for articles (kind 30023) and live activities (kind 30311)</span></div>
<div class="kind"><span class="kind-num">30385</span><span class="kind-desc">External Identifier Assertions — scores for hashtags and URLs (NIP-73)</span></div>
</div>

<div class="endpoints">
<h2>API Endpoints</h2>
<div class="endpoint"><span class="method">GET</span><span class="path">/score?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Trust score + metadata</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/personalized?viewer=&lt;hex&gt;&amp;target=&lt;hex&gt;</span><span class="desc">— Personalized trust score</span></div>
<div class="endpoint"><span class="method">POST</span><span class="path">/batch</span><span class="desc">— Score multiple pubkeys at once</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/metadata?pubkey=&lt;hex|npub&gt;</span><span class="desc">— Full NIP-85 metadata</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/event?id=&lt;hex&gt;</span><span class="desc">— Event engagement (kind 30383)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/external?id=&lt;ident&gt;</span><span class="desc">— Identifier score (kind 30385)</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/relay?url=&lt;wss://...&gt;</span><span class="desc">— Relay trust + operator WoT</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/top</span><span class="desc">— Top 50 scored pubkeys</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/external</span><span class="desc">— Top 50 external identifiers</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/stats</span><span class="desc">— Service statistics</span></div>
<div class="endpoint"><span class="method">GET</span><span class="path">/health</span><span class="desc">— Health check</span></div>
</div>

<footer>
<span>Built for <a href="https://nosfabrica.com/wotathon/">WoT-a-thon</a></span>
<span><a href="https://github.com/joelklabo/wot-scoring">Source (MIT)</a></span>
<span>Operator: <a href="https://njump.me/max@klabo.world">max@klabo.world</a></span>
</footer>
</div>
<script>
const input=document.getElementById("pubkey-input"),result=document.getElementById("result");
let timer;
function fmt(n){if(n>=1e6)return(n/1e6).toFixed(1)+"M";if(n>=1e3)return(n/1e3).toFixed(1)+"K";return n.toString()}
input.addEventListener("input",()=>{clearTimeout(timer);const v=input.value.trim();if(!v){result.innerHTML="";return}
timer=setTimeout(()=>{fetch("/score?pubkey="+encodeURIComponent(v)).then(r=>r.json()).then(d=>{
if(d.error){result.innerHTML='<div class="score-card fade-in" style="color:#f87171">'+d.error+'</div>';return}
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
}).catch(()=>{result.innerHTML='<div class="score-card fade-in" style="color:#f87171">Error fetching score</div>'})},400)});

// Load leaderboard
fetch("/top").then(r=>r.json()).then(data=>{
const tbody=document.getElementById("lb-body");
if(!data||!data.length){tbody.innerHTML='<tr><td colspan="4" style="color:#555">No data yet</td></tr>';return}
const top10=data.slice(0,10);
tbody.innerHTML=top10.map((e,i)=>'<tr><td class="lb-rank">'+(i+1)+'</td><td class="lb-pubkey">'+e.pubkey.slice(0,12)+'...'+e.pubkey.slice(-8)+'</td><td class="lb-score">'+(e.norm_score||0)+'/100</td><td class="lb-followers">'+fmt(e.followers||0)+'</td></tr>').join("");
}).catch(()=>{document.getElementById("lb-body").innerHTML='<tr><td colspan="4" style="color:#555">Failed to load</td></tr>'});
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
				stats := graph.Stats()
				log.Printf("Re-crawl complete: %d nodes, %d edges, %d events, %d addressable, %d external, %d ext_assertions",
					stats.Nodes, stats.Edges, events.EventCount(), events.AddressableCount(), external.Count(),
					externalAssertions.TotalAssertions())

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
	http.HandleFunc("/batch", handleBatch)
	http.HandleFunc("/personalized", handlePersonalized)
	http.HandleFunc("/top", handleTop)
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/export", handleExport)
	http.HandleFunc("/publish", handlePublish)
	http.HandleFunc("/metadata", handleMetadata)
	http.HandleFunc("/event", handleEventScore)
	http.HandleFunc("/external", handleExternal)
	http.HandleFunc("/relay", handleRelay)
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
/personalized?viewer=<hex>&target=<hex> — Personalized trust score relative to viewer's follow graph
POST /batch — Score multiple pubkeys in one request (JSON body: {"pubkeys":["hex1","hex2",...]})
/metadata?pubkey=<hex> — Full NIP-85 metadata (followers, posts, reactions, zaps)
/event?id=<hex> — Event engagement score (kind 30383)
/external?id=<identifier> — External identifier score (kind 30385, NIP-73)
/external — Top 50 external identifiers (hashtags, URLs)
/relay?url=<wss://...> — Relay trust + operator WoT (via trustedrelays.xyz)
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

	log.Printf("WoT Scoring API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, corsMiddleware(http.DefaultServeMux)))
}
