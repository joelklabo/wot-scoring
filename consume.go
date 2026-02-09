package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// ExternalAssertion represents a kind 30382 trusted assertion from another provider.
type ExternalAssertion struct {
	ProviderPubkey string `json:"provider_pubkey"`
	SubjectPubkey  string `json:"subject_pubkey"`
	Rank           int    `json:"rank"`
	Followers      int    `json:"followers,omitempty"`
	CreatedAt      int64  `json:"created_at"`
}

// ProviderInfo tracks metadata about an external NIP-85 assertion provider.
type ProviderInfo struct {
	Pubkey       string    `json:"pubkey"`
	AssertionCnt int       `json:"assertion_count"`
	LastSeen     time.Time `json:"last_seen"`
}

// AssertionStore stores external kind 30382 assertions keyed by subject pubkey.
type AssertionStore struct {
	mu sync.RWMutex
	// subject pubkey -> provider pubkey -> assertion
	assertions map[string]map[string]*ExternalAssertion
	// provider pubkey -> info
	providers map[string]*ProviderInfo
}

func NewAssertionStore() *AssertionStore {
	return &AssertionStore{
		assertions: make(map[string]map[string]*ExternalAssertion),
		providers:  make(map[string]*ProviderInfo),
	}
}

// Add stores an external assertion, replacing any prior assertion from the same provider.
func (s *AssertionStore) Add(a *ExternalAssertion) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.assertions[a.SubjectPubkey] == nil {
		s.assertions[a.SubjectPubkey] = make(map[string]*ExternalAssertion)
	}

	// Only keep the newest assertion per provider per subject
	existing := s.assertions[a.SubjectPubkey][a.ProviderPubkey]
	if existing != nil && existing.CreatedAt >= a.CreatedAt {
		return
	}

	s.assertions[a.SubjectPubkey][a.ProviderPubkey] = a

	if s.providers[a.ProviderPubkey] == nil {
		s.providers[a.ProviderPubkey] = &ProviderInfo{Pubkey: a.ProviderPubkey}
	}
	s.providers[a.ProviderPubkey].LastSeen = time.Now()
	// Recount assertions for this provider
	count := 0
	for _, byProvider := range s.assertions {
		if _, ok := byProvider[a.ProviderPubkey]; ok {
			count++
		}
	}
	s.providers[a.ProviderPubkey].AssertionCnt = count
}

// GetForSubject returns all external assertions for a given subject pubkey.
func (s *AssertionStore) GetForSubject(subjectPubkey string) []*ExternalAssertion {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byProvider := s.assertions[subjectPubkey]
	if byProvider == nil {
		return nil
	}

	result := make([]*ExternalAssertion, 0, len(byProvider))
	for _, a := range byProvider {
		result = append(result, a)
	}
	return result
}

// Providers returns info about all known external providers.
func (s *AssertionStore) Providers() []*ProviderInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*ProviderInfo, 0, len(s.providers))
	for _, p := range s.providers {
		result = append(result, p)
	}
	return result
}

// TotalAssertions returns the total number of stored external assertions.
func (s *AssertionStore) TotalAssertions() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, byProvider := range s.assertions {
		count += len(byProvider)
	}
	return count
}

// ProviderCount returns the number of distinct external providers.
func (s *AssertionStore) ProviderCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.providers)
}

// consumeExternalAssertions subscribes to kind 30382 events on relays from other providers.
// It filters out events from our own pubkey (we only want external assertions).
func consumeExternalAssertions(ctx context.Context, store *AssertionStore, ownPubkey string) {
	log.Printf("Consuming external NIP-85 assertions (kind 30382) from relays...")

	pool := nostr.NewSimplePool(ctx)

	// Query recent kind 30382 events (last 7 days)
	since := nostr.Timestamp(time.Now().Add(-7 * 24 * time.Hour).Unix())
	filter := nostr.Filter{
		Kinds: []int{30382},
		Since: &since,
		Limit: 5000,
	}

	total := 0
	skippedOwn := 0

	for ev := range pool.SubManyEose(ctx, relays, nostr.Filters{filter}) {
		// Skip our own assertions
		if ev.Event.PubKey == ownPubkey {
			skippedOwn++
			continue
		}

		a := parseAssertion(ev.Event)
		if a != nil {
			store.Add(a)
			total++
		}
	}

	log.Printf("Consumed %d external assertions from %d providers (skipped %d own)",
		total, store.ProviderCount(), skippedOwn)
}

// parseAssertion extracts an ExternalAssertion from a kind 30382 event.
func parseAssertion(ev *nostr.Event) *ExternalAssertion {
	if ev.Kind != 30382 {
		return nil
	}

	a := &ExternalAssertion{
		ProviderPubkey: ev.PubKey,
		CreatedAt:      int64(ev.CreatedAt),
	}

	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			a.SubjectPubkey = tag[1]
		case "rank":
			if v, err := strconv.Atoi(tag[1]); err == nil {
				a.Rank = v
			}
		case "followers":
			if v, err := strconv.Atoi(tag[1]); err == nil {
				a.Followers = v
			}
		}
	}

	if a.SubjectPubkey == "" {
		return nil
	}

	return a
}

// CompositeScore blends our internal score with external assertions.
// Returns the composite score and a breakdown of sources.
func CompositeScore(internalScore int, externalAssertions []*ExternalAssertion) (int, []map[string]interface{}) {
	if len(externalAssertions) == 0 {
		return internalScore, nil
	}

	// Weight: 70% internal, 30% external average
	externalSum := 0
	for _, a := range externalAssertions {
		externalSum += a.Rank
	}
	externalAvg := float64(externalSum) / float64(len(externalAssertions))

	composite := int(float64(internalScore)*0.7 + externalAvg*0.3)
	if composite > 100 {
		composite = 100
	}

	sources := make([]map[string]interface{}, len(externalAssertions))
	for i, a := range externalAssertions {
		sources[i] = map[string]interface{}{
			"provider": a.ProviderPubkey,
			"rank":     a.Rank,
			"age":      fmt.Sprintf("%ds", time.Now().Unix()-a.CreatedAt),
		}
	}

	return composite, sources
}
