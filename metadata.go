package main

import (
	"context"
	"log"
	"sort"
	"sync"

	"github.com/nbd-wtf/go-nostr"
)

// PubkeyMeta holds NIP-85 metadata collected for a single pubkey.
type PubkeyMeta struct {
	Followers    int   // number of kind 3 lists that include this pubkey
	PostCount    int   // kind 1 notes (not replies)
	ReplyCount   int   // kind 1 notes that are replies (have "e" tag)
	ReactionsSent int  // kind 7 reactions sent by this pubkey
	ReactionsRecd int  // kind 7 reactions received by this pubkey
	ZapAmtRecd   int64 // sats received via kind 9735 zap receipts
	ZapAmtSent   int64 // sats sent via kind 9735 zap receipts
	ZapCntRecd   int   // number of zap receipts received
	ZapCntSent   int   // number of zap receipts sent
	FirstCreated int64 // earliest known event timestamp (unix)
}

// MetaStore holds metadata for all crawled pubkeys.
type MetaStore struct {
	mu   sync.Mutex
	data map[string]*PubkeyMeta
}

func NewMetaStore() *MetaStore {
	return &MetaStore{data: make(map[string]*PubkeyMeta)}
}

func (ms *MetaStore) Get(pubkey string) *PubkeyMeta {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	m, ok := ms.data[pubkey]
	if !ok {
		m = &PubkeyMeta{}
		ms.data[pubkey] = m
	}
	return m
}

func (ms *MetaStore) Set(pubkey string, meta *PubkeyMeta) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	ms.data[pubkey] = meta
}

// CountFollowers populates the Followers field from the follow graph.
func (ms *MetaStore) CountFollowers(g *Graph) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ms.mu.Lock()
	defer ms.mu.Unlock()

	for pubkey, followers := range g.followers {
		m, ok := ms.data[pubkey]
		if !ok {
			m = &PubkeyMeta{}
			ms.data[pubkey] = m
		}
		m.Followers = len(followers)
	}
}

// CrawlMetadata fetches kind 1 (notes), kind 7 (reactions), and kind 9735 (zap receipts)
// for the given pubkeys, populating the MetaStore.
func (ms *MetaStore) CrawlMetadata(ctx context.Context, pubkeys []string) {
	if len(pubkeys) == 0 {
		return
	}
	pool := nostr.NewSimplePool(ctx)

	// Crawl notes and reactions in batches
	batchSize := 100
	for i := 0; i < len(pubkeys); i += batchSize {
		end := i + batchSize
		if end > len(pubkeys) {
			end = len(pubkeys)
		}
		batch := pubkeys[i:end]

		ms.crawlNotes(ctx, pool, batch)
		ms.crawlReactions(ctx, pool, batch)
		ms.crawlZaps(ctx, pool, batch)

		if (i/batchSize+1)%5 == 0 {
			log.Printf("Metadata crawl: processed %d/%d pubkeys", end, len(pubkeys))
		}
	}

	log.Printf("Metadata crawl complete for %d pubkeys", len(pubkeys))
}

// crawlNotes fetches kind 1 events and classifies them as posts or replies.
func (ms *MetaStore) crawlNotes(ctx context.Context, pool *nostr.SimplePool, pubkeys []string) {
	filter := nostr.Filter{
		Kinds:   []int{1},
		Authors: pubkeys,
		Limit:   len(pubkeys) * 20, // sample up to 20 notes per author
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		m := ms.Get(ev.Event.PubKey)

		// Track earliest event
		ts := int64(ev.Event.CreatedAt)
		ms.mu.Lock()
		if m.FirstCreated == 0 || ts < m.FirstCreated {
			m.FirstCreated = ts
		}
		ms.mu.Unlock()

		// Classify: reply if it has an "e" tag (referencing another event)
		isReply := false
		for _, tag := range ev.Event.Tags {
			if tag[0] == "e" {
				isReply = true
				break
			}
		}

		ms.mu.Lock()
		if isReply {
			m.ReplyCount++
		} else {
			m.PostCount++
		}
		ms.mu.Unlock()
	}
}

// crawlReactions fetches kind 7 events and counts reactions sent and received.
func (ms *MetaStore) crawlReactions(ctx context.Context, pool *nostr.SimplePool, pubkeys []string) {
	// Reactions SENT by these pubkeys
	filter := nostr.Filter{
		Kinds:   []int{7},
		Authors: pubkeys,
		Limit:   len(pubkeys) * 10,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		m := ms.Get(ev.Event.PubKey)
		ms.mu.Lock()
		m.ReactionsSent++
		ms.mu.Unlock()

		// Also count as received by the "p" tagged pubkey
		for _, tag := range ev.Event.Tags {
			if tag[0] == "p" && len(tag) >= 2 {
				target := ms.Get(tag[1])
				ms.mu.Lock()
				target.ReactionsRecd++
				ms.mu.Unlock()
				break // count first p-tag as the reaction target
			}
		}
	}
}

// crawlZaps fetches kind 9735 zap receipt events.
func (ms *MetaStore) crawlZaps(ctx context.Context, pool *nostr.SimplePool, pubkeys []string) {
	// Zap receipts where these pubkeys are the recipient (p-tagged)
	filter := nostr.Filter{
		Kinds: []int{9735},
		Tags: nostr.TagMap{
			"p": pubkeys,
		},
		Limit: len(pubkeys) * 5,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		amount := extractZapAmount(ev.Event)
		if amount <= 0 {
			continue
		}

		// Find recipient (p-tag) and sender (from bolt11 or description)
		for _, tag := range ev.Event.Tags {
			if tag[0] == "p" && len(tag) >= 2 {
				recipient := ms.Get(tag[1])
				ms.mu.Lock()
				recipient.ZapAmtRecd += amount
				recipient.ZapCntRecd++
				ms.mu.Unlock()
				break
			}
		}

		// The sender is typically in the "description" tag's event JSON,
		// but parsing that is complex. We skip sender attribution for now.
	}
}

// extractZapAmount extracts the sats amount from a zap receipt's bolt11 tag.
func extractZapAmount(ev *nostr.Event) int64 {
	for _, tag := range ev.Tags {
		if tag[0] == "bolt11" && len(tag) >= 2 {
			return decodeBolt11Amount(tag[1])
		}
	}
	return 0
}

// decodeBolt11Amount extracts millisats from a BOLT11 invoice string and converts to sats.
// BOLT11 format: lnbc<amount><multiplier>1... where multiplier is m(milli), u(micro), n(nano), p(pico)
func decodeBolt11Amount(invoice string) int64 {
	if len(invoice) < 6 {
		return 0
	}

	// Find the prefix: lnbc, lntb, lnbcrt, etc.
	idx := 0
	for idx < len(invoice) && (invoice[idx] < '0' || invoice[idx] > '9') {
		idx++
	}
	if idx >= len(invoice) {
		return 0
	}

	// Extract the numeric part
	numStart := idx
	for idx < len(invoice) && invoice[idx] >= '0' && invoice[idx] <= '9' {
		idx++
	}
	if idx >= len(invoice) || numStart == idx {
		return 0
	}

	numStr := invoice[numStart:idx]
	var amount int64
	for _, c := range numStr {
		amount = amount*10 + int64(c-'0')
	}

	// Multiplier character
	mult := invoice[idx]
	var msats int64
	switch mult {
	case 'm': // milli-bitcoin = 100_000_000 msats
		msats = amount * 100_000_000
	case 'u': // micro-bitcoin = 100_000 msats
		msats = amount * 100_000
	case 'n': // nano-bitcoin = 100 msats
		msats = amount * 100
	case 'p': // pico-bitcoin = 0.1 msats
		msats = amount / 10
	default:
		// No multiplier = full bitcoin amount
		msats = amount * 100_000_000_000
	}

	// Convert millisats to sats
	return msats / 1000
}

// TopNPubkeys returns the hex pubkeys of the top N scored entries.
func TopNPubkeys(g *Graph, n int) []string {
	entries := g.TopN(n)
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.Pubkey
	}
	return result
}

// SortedPubkeys returns all pubkeys from the graph sorted by score descending.
func SortedPubkeys(g *Graph) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	type kv struct {
		key   string
		value float64
	}
	pairs := make([]kv, 0, len(g.scores))
	for k, v := range g.scores {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].value > pairs[j].value
	})

	result := make([]string, len(pairs))
	for i, p := range pairs {
		result[i] = p.key
	}
	return result
}
