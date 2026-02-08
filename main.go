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

func handleScore(w http.ResponseWriter, r *http.Request) {
	pubkey := r.URL.Query().Get("pubkey")
	if pubkey == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
		return
	}

	score, ok := graph.GetScore(pubkey)
	stats := graph.Stats()

	resp := map[string]interface{}{
		"pubkey":     pubkey,
		"raw_score":  score,
		"rank_score": normalizeScore(score, stats.Nodes),
		"found":      ok,
		"graph_size": stats.Nodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func handleTop(w http.ResponseWriter, r *http.Request) {
	entries := graph.TopN(50)
	for i := range entries {
		entries[i].Rank = i + 1
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
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
		"kind_31990":  nip89Status,
		"total":       count382 + count383 + count384,
		"algorithm":   "pagerank + engagement",
		"graph_nodes": stats.Nodes,
		"graph_edges": stats.Edges,
		"relays":      relays,
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
	})
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

func handleMetadata(w http.ResponseWriter, r *http.Request) {
	pubkey := r.URL.Query().Get("pubkey")
	if pubkey == "" {
		http.Error(w, `{"error":"pubkey parameter required"}`, http.StatusBadRequest)
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

		// Schedule periodic re-crawl every 6 hours
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
				stats := graph.Stats()
				log.Printf("Re-crawl complete: %d nodes, %d edges, %d events, %d addressable",
					stats.Nodes, stats.Edges, events.EventCount(), events.AddressableCount())
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
			"status":      status,
			"graph_nodes": stats.Nodes,
			"graph_edges": stats.Edges,
			"events":      events.EventCount(),
			"addressable": events.AddressableCount(),
			"uptime":      time.Since(startTime).String(),
		})
	})
	http.HandleFunc("/score", handleScore)
	http.HandleFunc("/top", handleTop)
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/export", handleExport)
	http.HandleFunc("/publish", handlePublish)
	http.HandleFunc("/metadata", handleMetadata)
	http.HandleFunc("/event", handleEventScore)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"name":        "WoT Scoring Service",
			"description": "NIP-85 Trusted Assertions provider. PageRank trust scoring over the Nostr follow graph with full metadata collection.",
			"endpoints": `/score?pubkey=<hex> — Trust score for a pubkey (kind 30382)
/metadata?pubkey=<hex> — Full NIP-85 metadata (followers, posts, reactions, zaps)
/event?id=<hex> — Event engagement score (kind 30383)
/top — Top 50 scored pubkeys
/export — All scores as JSON
/stats — Service stats and graph info
POST /publish — Publish NIP-85 kind 30382/30383/30384 events to relays`,
			"nip":      "85",
			"operator": "max@klabo.world",
			"source":   "https://github.com/joelklabo/wot-scoring",
		})
	})

	log.Printf("WoT Scoring API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
