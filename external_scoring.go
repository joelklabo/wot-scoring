package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// ExternalMeta holds NIP-85 engagement metrics for an external identifier (NIP-73).
type ExternalMeta struct {
	Identifier string // NIP-73 identifier (e.g. "#bitcoin", "https://example.com")
	Kind       string // "hashtag", "url"
	Mentions   int    // how many events reference this identifier
	Reactions  int    // reactions on events mentioning this identifier
	Reposts    int    // reposts of events mentioning this identifier
	Comments   int    // replies to events mentioning this identifier
	ZapCount   int
	ZapAmount  int64
	Authors    map[string]bool // unique authors who mentioned it
}

// ExternalStore holds engagement metrics for external identifiers.
type ExternalStore struct {
	mu   sync.Mutex
	data map[string]*ExternalMeta // NIP-73 identifier -> metrics
}

func NewExternalStore() *ExternalStore {
	return &ExternalStore{data: make(map[string]*ExternalMeta)}
}

func (xs *ExternalStore) Get(identifier string) *ExternalMeta {
	xs.mu.Lock()
	defer xs.mu.Unlock()
	m, ok := xs.data[identifier]
	if !ok {
		m = &ExternalMeta{
			Identifier: identifier,
			Authors:    make(map[string]bool),
		}
		xs.data[identifier] = m
	}
	return m
}

func (xs *ExternalStore) Count() int {
	xs.mu.Lock()
	defer xs.mu.Unlock()
	return len(xs.data)
}

// TopExternal returns the top N external identifiers by engagement.
func (xs *ExternalStore) TopExternal(n int) []*ExternalMeta {
	xs.mu.Lock()
	defer xs.mu.Unlock()

	entries := make([]*ExternalMeta, 0, len(xs.data))
	for _, m := range xs.data {
		entries = append(entries, m)
	}

	// Sort by total engagement
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if externalEngagement(entries[j]) > externalEngagement(entries[i]) {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
	}

	if n > 0 && n < len(entries) {
		entries = entries[:n]
	}
	return entries
}

func externalEngagement(m *ExternalMeta) int64 {
	return int64(m.Mentions) + int64(m.Reactions) + int64(m.Reposts)*2 + int64(m.Comments)*3 + m.ZapAmount
}

func externalRank(m *ExternalMeta, maxEngagement int64) int {
	if maxEngagement == 0 {
		return 0
	}
	eng := externalEngagement(m)
	ratio := float64(eng) / float64(maxEngagement)
	score := math.Log10(ratio*99+1) * 50
	if score > 100 {
		score = 100
	}
	return int(math.Round(score))
}

// CrawlExternalIdentifiers extracts hashtags and URLs from events by top-scored authors.
func (xs *ExternalStore) CrawlExternalIdentifiers(ctx context.Context, authorPubkeys []string) {
	if len(authorPubkeys) == 0 {
		return
	}
	pool := nostr.NewSimplePool(ctx)

	batchSize := 50
	for i := 0; i < len(authorPubkeys); i += batchSize {
		end := i + batchSize
		if end > len(authorPubkeys) {
			end = len(authorPubkeys)
		}
		batch := authorPubkeys[i:end]

		since := nostr.Timestamp(time.Now().Add(-30 * 24 * time.Hour).Unix())
		filter := nostr.Filter{
			Kinds:   []int{1},
			Authors: batch,
			Since:   &since,
			Limit:   len(batch) * 10,
		}

		evCh := pool.SubManyEose(ctx, relays, nostr.Filters{filter})
		for ev := range evCh {
			xs.extractIdentifiers(ev.Event)
		}

		if (i/batchSize+1)%5 == 0 {
			log.Printf("External identifier crawl: processed %d/%d authors, %d identifiers tracked",
				end, len(authorPubkeys), xs.Count())
		}
	}

	log.Printf("External identifier crawl complete: %d identifiers", xs.Count())
}

// extractIdentifiers pulls hashtags from t-tags and URLs from content.
func (xs *ExternalStore) extractIdentifiers(ev *nostr.Event) {
	// Extract hashtags from t-tags
	for _, tag := range ev.Tags {
		if tag[0] == "t" && len(tag) >= 2 {
			hashtag := "#" + strings.ToLower(tag[1])
			m := xs.Get(hashtag)
			xs.mu.Lock()
			m.Kind = "hashtag"
			m.Mentions++
			m.Authors[ev.PubKey] = true
			xs.mu.Unlock()
		}
	}

	// Extract URLs from r-tags (NIP-58 reference tags used by some clients)
	for _, tag := range ev.Tags {
		if tag[0] == "r" && len(tag) >= 2 {
			url := normalizeURL(tag[1])
			if url != "" {
				m := xs.Get(url)
				xs.mu.Lock()
				m.Kind = "url"
				m.Mentions++
				m.Authors[ev.PubKey] = true
				xs.mu.Unlock()
			}
		}
	}
}

// normalizeURL returns a normalized URL or empty string if invalid.
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return ""
	}
	// Strip fragment
	if idx := strings.Index(raw, "#"); idx >= 0 {
		raw = raw[:idx]
	}
	// Lowercase the scheme and host portion
	if idx := strings.Index(raw, "://"); idx >= 0 {
		rest := raw[idx+3:]
		slashIdx := strings.Index(rest, "/")
		if slashIdx < 0 {
			raw = strings.ToLower(raw)
		} else {
			host := rest[:slashIdx]
			path := rest[slashIdx:]
			raw = strings.ToLower(raw[:idx+3]) + strings.ToLower(host) + path
		}
	}
	return raw
}

// publishExternalAssertions publishes kind 30385 events for top external identifiers.
func publishExternalAssertions(ctx context.Context, xs *ExternalStore, sk, pub string) (int, error) {
	topExternal := xs.TopExternal(100)
	if len(topExternal) == 0 {
		return 0, nil
	}

	maxEng := externalEngagement(topExternal[0])
	pool := nostr.NewSimplePool(ctx)
	published := 0

	for i, m := range topExternal {
		rank := externalRank(m, maxEng)

		ev := nostr.Event{
			PubKey:    pub,
			CreatedAt: nostr.Now(),
			Kind:      30385,
			Tags: nostr.Tags{
				{"d", m.Identifier},
				{"rank", fmt.Sprintf("%d", rank)},
				{"mentions", fmt.Sprintf("%d", m.Mentions)},
				{"unique_authors", fmt.Sprintf("%d", len(m.Authors))},
				{"reactions", fmt.Sprintf("%d", m.Reactions)},
				{"reposts", fmt.Sprintf("%d", m.Reposts)},
				{"comments", fmt.Sprintf("%d", m.Comments)},
				{"zap_count", fmt.Sprintf("%d", m.ZapCount)},
				{"zap_amount", fmt.Sprintf("%d", m.ZapAmount)},
			},
		}

		if err := ev.Sign(sk); err != nil {
			log.Printf("Failed to sign kind 30385 for %s: %v", m.Identifier, err)
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
			log.Printf("Published %d/%d kind 30385 events", published, i+1)
			time.Sleep(2 * time.Second)
		}
	}

	log.Printf("Published %d kind 30385 (external identifier assertion) events", published)
	return published, nil
}
