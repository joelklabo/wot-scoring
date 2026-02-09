package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

// Authorization represents a kind 10040 event where a user declares trust
// in a service provider for specific assertion kinds.
type Authorization struct {
	UserPubkey     string   `json:"user_pubkey"`
	ProviderPubkey string   `json:"provider_pubkey"`
	Kinds          []string `json:"kinds"`          // e.g. ["30382:rank", "30383:rank"]
	RelayHint      string   `json:"relay_hint"`     // optional relay where provider publishes
	CreatedAt      int64    `json:"created_at"`
}

// AuthStore stores kind 10040 authorization events.
type AuthStore struct {
	mu sync.RWMutex
	// user pubkey -> provider pubkey -> authorization
	auths map[string]map[string]*Authorization
}

func NewAuthStore() *AuthStore {
	return &AuthStore{
		auths: make(map[string]map[string]*Authorization),
	}
}

// Add stores an authorization, keeping only the newest per user+provider pair.
func (s *AuthStore) Add(a *Authorization) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.auths[a.UserPubkey] == nil {
		s.auths[a.UserPubkey] = make(map[string]*Authorization)
	}

	existing := s.auths[a.UserPubkey][a.ProviderPubkey]
	if existing != nil && existing.CreatedAt >= a.CreatedAt {
		return
	}
	s.auths[a.UserPubkey][a.ProviderPubkey] = a
}

// AuthorizedUsers returns all user pubkeys that have authorized a given provider.
func (s *AuthStore) AuthorizedUsers(providerPubkey string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var users []string
	for userPub, byProvider := range s.auths {
		if _, ok := byProvider[providerPubkey]; ok {
			users = append(users, userPub)
		}
	}
	return users
}

// AuthorizedCount returns the number of users that have authorized a given provider.
func (s *AuthStore) AuthorizedCount(providerPubkey string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, byProvider := range s.auths {
		if _, ok := byProvider[providerPubkey]; ok {
			count++
		}
	}
	return count
}

// TotalUsers returns the total number of unique users who have published authorizations.
func (s *AuthStore) TotalUsers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.auths)
}

// TotalAuthorizations returns the total number of stored authorization entries.
func (s *AuthStore) TotalAuthorizations() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	count := 0
	for _, byProvider := range s.auths {
		count += len(byProvider)
	}
	return count
}

// GetForUser returns all authorizations a user has published.
func (s *AuthStore) GetForUser(userPubkey string) []*Authorization {
	s.mu.RLock()
	defer s.mu.RUnlock()

	byProvider := s.auths[userPubkey]
	if byProvider == nil {
		return nil
	}
	result := make([]*Authorization, 0, len(byProvider))
	for _, a := range byProvider {
		result = append(result, a)
	}
	return result
}

// parseAuthorization extracts authorizations from a kind 10040 event.
// Each tag in a kind 10040 event declares trust in one provider for one result type:
//
//	["30382:rank", "provider_pubkey", "relay_url"]
func parseAuthorization(ev *nostr.Event) []*Authorization {
	if ev.Kind != 10040 {
		return nil
	}

	// Group by provider pubkey
	byProvider := make(map[string]*Authorization)

	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		// Tags look like: ["30382:rank", "provider_pubkey", "optional_relay"]
		kindStr := tag[0]
		if !strings.Contains(kindStr, ":") {
			continue
		}
		providerPubkey := tag[1]
		if providerPubkey == "" {
			continue
		}

		a, ok := byProvider[providerPubkey]
		if !ok {
			a = &Authorization{
				UserPubkey:     ev.PubKey,
				ProviderPubkey: providerPubkey,
				CreatedAt:      int64(ev.CreatedAt),
			}
			byProvider[providerPubkey] = a
		}
		a.Kinds = append(a.Kinds, kindStr)
		if len(tag) >= 3 && tag[2] != "" {
			a.RelayHint = tag[2]
		}
	}

	result := make([]*Authorization, 0, len(byProvider))
	for _, a := range byProvider {
		result = append(result, a)
	}
	return result
}

// consumeAuthorizations subscribes to kind 10040 events on relays.
func consumeAuthorizations(ctx context.Context, store *AuthStore) {
	log.Printf("Consuming NIP-85 authorizations (kind 10040) from relays...")

	pool := nostr.NewSimplePool(ctx)

	since := nostr.Timestamp(time.Now().Add(-30 * 24 * time.Hour).Unix())
	filter := nostr.Filter{
		Kinds: []int{10040},
		Since: &since,
		Limit: 5000,
	}

	total := 0
	for ev := range pool.SubManyEose(ctx, relays, nostr.Filters{filter}) {
		auths := parseAuthorization(ev.Event)
		for _, a := range auths {
			store.Add(a)
			total++
		}
	}

	log.Printf("Consumed %d authorizations from %d users", total, store.TotalUsers())
}
