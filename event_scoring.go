package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// EventMeta holds NIP-85 engagement metrics for a single event.
type EventMeta struct {
	EventID      string
	AuthorPubkey string
	Kind         int
	Comments     int
	Quotes       int
	Reposts      int
	Reactions    int
	ZapCount     int
	ZapAmount    int64 // sats
	CreatedAt    int64
}

// AddressableEventMeta holds NIP-85 engagement metrics for an addressable event.
type AddressableEventMeta struct {
	Address      string // kind:pubkey:d-tag
	AuthorPubkey string
	Kind         int
	DTag         string
	Comments     int
	Quotes       int
	Reposts      int
	Reactions    int
	ZapCount     int
	ZapAmount    int64 // sats
	CreatedAt    int64
}

// EventStore holds engagement metrics for events.
type EventStore struct {
	mu          sync.Mutex
	events      map[string]*EventMeta          // event ID -> metrics
	addressable map[string]*AddressableEventMeta // address -> metrics
}

func NewEventStore() *EventStore {
	return &EventStore{
		events:      make(map[string]*EventMeta),
		addressable: make(map[string]*AddressableEventMeta),
	}
}

func (es *EventStore) GetEvent(id string) *EventMeta {
	es.mu.Lock()
	defer es.mu.Unlock()
	m, ok := es.events[id]
	if !ok {
		m = &EventMeta{EventID: id}
		es.events[id] = m
	}
	return m
}

func (es *EventStore) GetAddressable(address string) *AddressableEventMeta {
	es.mu.Lock()
	defer es.mu.Unlock()
	m, ok := es.addressable[address]
	if !ok {
		m = &AddressableEventMeta{Address: address}
		es.addressable[address] = m
	}
	return m
}

func (es *EventStore) EventCount() int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return len(es.events)
}

func (es *EventStore) AddressableCount() int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return len(es.addressable)
}

// TopEvents returns the top N events by engagement score.
func (es *EventStore) TopEvents(n int) []*EventMeta {
	es.mu.Lock()
	defer es.mu.Unlock()

	entries := make([]*EventMeta, 0, len(es.events))
	for _, m := range es.events {
		entries = append(entries, m)
	}

	// Sort by total engagement (reactions + reposts + comments + zaps)
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if eventEngagement(entries[j]) > eventEngagement(entries[i]) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if n > 0 && n < len(entries) {
		entries = entries[:n]
	}
	return entries
}

func eventEngagement(m *EventMeta) int64 {
	return int64(m.Reactions) + int64(m.Reposts)*2 + int64(m.Comments)*3 + m.ZapAmount
}

// eventRank normalizes engagement to a 0-100 score.
func eventRank(m *EventMeta, maxEngagement int64) int {
	if maxEngagement == 0 {
		return 0
	}
	eng := eventEngagement(m)
	ratio := float64(eng) / float64(maxEngagement)
	score := math.Log10(ratio*99+1) * 50 // log scale 0-100
	if score > 100 {
		score = 100
	}
	return int(math.Round(score))
}

// CrawlEventEngagement crawls engagement metrics for events by top-scored authors.
func (es *EventStore) CrawlEventEngagement(ctx context.Context, authorPubkeys []string) {
	if len(authorPubkeys) == 0 {
		return
	}
	pool := nostr.NewSimplePool(ctx)

	// Step 1: Fetch recent kind 1 events from top authors
	batchSize := 50
	for i := 0; i < len(authorPubkeys); i += batchSize {
		end := i + batchSize
		if end > len(authorPubkeys) {
			end = len(authorPubkeys)
		}
		batch := authorPubkeys[i:end]

		// Fetch recent notes (last 30 days)
		since := nostr.Timestamp(time.Now().Add(-30 * 24 * time.Hour).Unix())
		filter := nostr.Filter{
			Kinds:   []int{1},
			Authors: batch,
			Since:   &since,
			Limit:   len(batch) * 10,
		}

		evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
		eventIDs := make([]string, 0)
		for ev := range evCh {
			m := es.GetEvent(ev.Event.ID)
			es.mu.Lock()
			m.AuthorPubkey = ev.Event.PubKey
			m.Kind = ev.Event.Kind
			m.CreatedAt = int64(ev.Event.CreatedAt)
			es.mu.Unlock()
			eventIDs = append(eventIDs, ev.Event.ID)
		}

		// Step 2: Fetch reactions, reposts, and replies for these events
		if len(eventIDs) > 0 {
			es.crawlEventReactions(ctx, pool, eventIDs)
			es.crawlEventReposts(ctx, pool, eventIDs)
			es.crawlEventReplies(ctx, pool, eventIDs)
			es.crawlEventZaps(ctx, pool, eventIDs)
		}

		if (i/batchSize+1)%5 == 0 {
			log.Printf("Event engagement crawl: processed %d/%d authors, %d events tracked",
				end, len(authorPubkeys), es.EventCount())
		}
	}

	// Step 3: Fetch addressable events (kind 30023 long-form, kind 30311 live activities, etc.)
	es.crawlAddressableEvents(ctx, pool, authorPubkeys)

	log.Printf("Event engagement crawl complete: %d events, %d addressable events",
		es.EventCount(), es.AddressableCount())
}

func (es *EventStore) crawlEventReactions(ctx context.Context, pool *nostr.SimplePool, eventIDs []string) {
	// Reactions (kind 7) referencing these events via e-tag
	filter := nostr.Filter{
		Kinds: []int{7},
		Tags: nostr.TagMap{
			"e": eventIDs,
		},
		Limit: len(eventIDs) * 5,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		for _, tag := range ev.Event.Tags {
			if tag[0] == "e" && len(tag) >= 2 {
				m := es.GetEvent(tag[1])
				es.mu.Lock()
				m.Reactions++
				es.mu.Unlock()
				break
			}
		}
	}
}

func (es *EventStore) crawlEventReposts(ctx context.Context, pool *nostr.SimplePool, eventIDs []string) {
	// Reposts (kind 6) referencing these events
	filter := nostr.Filter{
		Kinds: []int{6},
		Tags: nostr.TagMap{
			"e": eventIDs,
		},
		Limit: len(eventIDs) * 3,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		for _, tag := range ev.Event.Tags {
			if tag[0] == "e" && len(tag) >= 2 {
				m := es.GetEvent(tag[1])
				es.mu.Lock()
				m.Reposts++
				es.mu.Unlock()
				break
			}
		}
	}
}

func (es *EventStore) crawlEventReplies(ctx context.Context, pool *nostr.SimplePool, eventIDs []string) {
	// Replies (kind 1) referencing these events via e-tag
	filter := nostr.Filter{
		Kinds: []int{1},
		Tags: nostr.TagMap{
			"e": eventIDs,
		},
		Limit: len(eventIDs) * 5,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		for _, tag := range ev.Event.Tags {
			if tag[0] == "e" && len(tag) >= 2 {
				m := es.GetEvent(tag[1])
				es.mu.Lock()
				m.Comments++
				es.mu.Unlock()
				break
			}
		}
	}
}

func (es *EventStore) crawlEventZaps(ctx context.Context, pool *nostr.SimplePool, eventIDs []string) {
	// Zap receipts (kind 9735) referencing these events
	filter := nostr.Filter{
		Kinds: []int{9735},
		Tags: nostr.TagMap{
			"e": eventIDs,
		},
		Limit: len(eventIDs) * 3,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		amount := extractZapAmount(ev.Event)
		if amount <= 0 {
			continue
		}
		for _, tag := range ev.Event.Tags {
			if tag[0] == "e" && len(tag) >= 2 {
				m := es.GetEvent(tag[1])
				es.mu.Lock()
				m.ZapCount++
				m.ZapAmount += amount
				es.mu.Unlock()
				break
			}
		}
	}
}

// crawlAddressableEvents fetches long-form articles (kind 30023) and other addressable events.
func (es *EventStore) crawlAddressableEvents(ctx context.Context, pool *nostr.SimplePool, authorPubkeys []string) {
	batchSize := 50
	for i := 0; i < len(authorPubkeys); i += batchSize {
		end := i + batchSize
		if end > len(authorPubkeys) {
			end = len(authorPubkeys)
		}
		batch := authorPubkeys[i:end]

		// Fetch addressable events: long-form (30023), live activities (30311)
		filter := nostr.Filter{
			Kinds:   []int{30023, 30311},
			Authors: batch,
			Limit:   len(batch) * 5,
		}

		evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
		addresses := make([]string, 0)
		for ev := range evCh {
			dTag := ""
			for _, tag := range ev.Event.Tags {
				if tag[0] == "d" && len(tag) >= 2 {
					dTag = tag[1]
					break
				}
			}
			address := fmt.Sprintf("%d:%s:%s", ev.Event.Kind, ev.Event.PubKey, dTag)

			m := es.GetAddressable(address)
			es.mu.Lock()
			m.AuthorPubkey = ev.Event.PubKey
			m.Kind = ev.Event.Kind
			m.DTag = dTag
			m.CreatedAt = int64(ev.Event.CreatedAt)
			es.mu.Unlock()
			addresses = append(addresses, address)
		}

		// Fetch engagement for addressable events by their a-tags
		if len(addresses) > 0 {
			es.crawlAddressableReactions(ctx, pool, addresses)
			es.crawlAddressableZaps(ctx, pool, addresses)
		}
	}
}

func (es *EventStore) crawlAddressableReactions(ctx context.Context, pool *nostr.SimplePool, addresses []string) {
	filter := nostr.Filter{
		Kinds: []int{7},
		Tags: nostr.TagMap{
			"a": addresses,
		},
		Limit: len(addresses) * 5,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		for _, tag := range ev.Event.Tags {
			if tag[0] == "a" && len(tag) >= 2 {
				m := es.GetAddressable(tag[1])
				es.mu.Lock()
				m.Reactions++
				es.mu.Unlock()
				break
			}
		}
	}
}

func (es *EventStore) crawlAddressableZaps(ctx context.Context, pool *nostr.SimplePool, addresses []string) {
	filter := nostr.Filter{
		Kinds: []int{9735},
		Tags: nostr.TagMap{
			"a": addresses,
		},
		Limit: len(addresses) * 3,
	}

	evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
	for ev := range evCh {
		amount := extractZapAmount(ev.Event)
		if amount <= 0 {
			continue
		}
		for _, tag := range ev.Event.Tags {
			if tag[0] == "a" && len(tag) >= 2 {
				m := es.GetAddressable(tag[1])
				es.mu.Lock()
				m.ZapCount++
				m.ZapAmount += amount
				es.mu.Unlock()
				break
			}
		}
	}
}

// publishEventAssertions publishes kind 30383 events for top-scored events.
func publishEventAssertions(ctx context.Context, es *EventStore, sk, pub string) (int, error) {
	topEvents := es.TopEvents(100)
	if len(topEvents) == 0 {
		return 0, nil
	}

	maxEng := eventEngagement(topEvents[0])
	pool := nostr.NewSimplePool(ctx)
	published := 0

	for i, m := range topEvents {
		rank := eventRank(m, maxEng)

		ev := nostr.Event{
			PubKey:    pub,
			CreatedAt: nostr.Now(),
			Kind:      30383,
			Tags: nostr.Tags{
				{"d", m.EventID},
				{"e", m.EventID},
				{"p", m.AuthorPubkey},
				{"rank", fmt.Sprintf("%d", rank)},
				{"comments", fmt.Sprintf("%d", m.Comments)},
				{"reposts", fmt.Sprintf("%d", m.Reposts)},
				{"reactions", fmt.Sprintf("%d", m.Reactions)},
				{"zap_count", fmt.Sprintf("%d", m.ZapCount)},
				{"zap_amount", fmt.Sprintf("%d", m.ZapAmount)},
			},
		}

		if err := ev.Sign(sk); err != nil {
			log.Printf("Failed to sign kind 30383 for %s: %v", m.EventID, err)
			continue
		}

		ok := false
		for result := range pool.PublishMany(ctx, relays, ev) {
			if result.Error == nil {
				ok = true
			}
		}
		if ok {
			published++
		}

		time.Sleep(100 * time.Millisecond)
		if (i+1)%50 == 0 {
			log.Printf("Published %d/%d kind 30383 events", published, i+1)
			time.Sleep(2 * time.Second)
		}
	}

	log.Printf("Published %d kind 30383 (event assertion) events", published)
	return published, nil
}

// publishAddressableAssertions publishes kind 30384 events for addressable events.
func publishAddressableAssertions(ctx context.Context, es *EventStore, sk, pub string) (int, error) {
	es.mu.Lock()
	entries := make([]*AddressableEventMeta, 0, len(es.addressable))
	for _, m := range es.addressable {
		entries = append(entries, m)
	}
	es.mu.Unlock()

	if len(entries) == 0 {
		return 0, nil
	}

	// Find max engagement for normalization
	var maxEng int64
	for _, m := range entries {
		eng := int64(m.Reactions) + int64(m.Reposts)*2 + int64(m.Comments)*3 + m.ZapAmount
		if eng > maxEng {
			maxEng = eng
		}
	}

	pool := nostr.NewSimplePool(ctx)
	published := 0

	for i, m := range entries {
		eng := int64(m.Reactions) + int64(m.Reposts)*2 + int64(m.Comments)*3 + m.ZapAmount
		rank := 0
		if maxEng > 0 {
			ratio := float64(eng) / float64(maxEng)
			score := math.Log10(ratio*99+1) * 50
			if score > 100 {
				score = 100
			}
			rank = int(math.Round(score))
		}

		ev := nostr.Event{
			PubKey:    pub,
			CreatedAt: nostr.Now(),
			Kind:      30384,
			Tags: nostr.Tags{
				{"d", m.Address},
				{"a", m.Address},
				{"p", m.AuthorPubkey},
				{"rank", fmt.Sprintf("%d", rank)},
				{"comments", fmt.Sprintf("%d", m.Comments)},
				{"reposts", fmt.Sprintf("%d", m.Reposts)},
				{"reactions", fmt.Sprintf("%d", m.Reactions)},
				{"zap_count", fmt.Sprintf("%d", m.ZapCount)},
				{"zap_amount", fmt.Sprintf("%d", m.ZapAmount)},
			},
		}

		if err := ev.Sign(sk); err != nil {
			log.Printf("Failed to sign kind 30384 for %s: %v", m.Address, err)
			continue
		}

		ok := false
		for result := range pool.PublishMany(ctx, relays, ev) {
			if result.Error == nil {
				ok = true
			}
		}
		if ok {
			published++
		}

		time.Sleep(100 * time.Millisecond)
		if (i+1)%50 == 0 {
			log.Printf("Published %d/%d kind 30384 events", published, i+1)
			time.Sleep(2 * time.Second)
		}
	}

	log.Printf("Published %d kind 30384 (addressable event assertion) events", published)
	return published, nil
}
