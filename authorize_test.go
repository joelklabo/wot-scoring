package main

import (
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestAuthStore_AddAndRetrieve(t *testing.T) {
	store := NewAuthStore()

	a := &Authorization{
		UserPubkey:     "user1",
		ProviderPubkey: "provider1",
		Kinds:          []string{"30382:rank"},
		CreatedAt:      1000,
	}
	store.Add(a)

	if store.TotalUsers() != 1 {
		t.Errorf("expected 1 user, got %d", store.TotalUsers())
	}
	if store.TotalAuthorizations() != 1 {
		t.Errorf("expected 1 authorization, got %d", store.TotalAuthorizations())
	}
	if store.AuthorizedCount("provider1") != 1 {
		t.Errorf("expected 1 authorized user for provider1, got %d", store.AuthorizedCount("provider1"))
	}

	users := store.AuthorizedUsers("provider1")
	if len(users) != 1 || users[0] != "user1" {
		t.Errorf("expected [user1], got %v", users)
	}
}

func TestAuthStore_NewerReplacesOlder(t *testing.T) {
	store := NewAuthStore()

	store.Add(&Authorization{
		UserPubkey:     "user1",
		ProviderPubkey: "provider1",
		Kinds:          []string{"30382:rank"},
		CreatedAt:      1000,
	})
	store.Add(&Authorization{
		UserPubkey:     "user1",
		ProviderPubkey: "provider1",
		Kinds:          []string{"30382:rank", "30383:rank"},
		CreatedAt:      2000,
	})

	auths := store.GetForUser("user1")
	if len(auths) != 1 {
		t.Fatalf("expected 1 auth, got %d", len(auths))
	}
	if len(auths[0].Kinds) != 2 {
		t.Errorf("expected 2 kinds in newer auth, got %d", len(auths[0].Kinds))
	}
}

func TestAuthStore_OlderDoesNotReplace(t *testing.T) {
	store := NewAuthStore()

	store.Add(&Authorization{
		UserPubkey:     "user1",
		ProviderPubkey: "provider1",
		Kinds:          []string{"30382:rank", "30383:rank"},
		CreatedAt:      2000,
	})
	store.Add(&Authorization{
		UserPubkey:     "user1",
		ProviderPubkey: "provider1",
		Kinds:          []string{"30382:rank"},
		CreatedAt:      1000,
	})

	auths := store.GetForUser("user1")
	if len(auths) != 1 {
		t.Fatalf("expected 1 auth, got %d", len(auths))
	}
	if len(auths[0].Kinds) != 2 {
		t.Errorf("expected 2 kinds (newer version), got %d", len(auths[0].Kinds))
	}
}

func TestAuthStore_MultipleProviders(t *testing.T) {
	store := NewAuthStore()

	store.Add(&Authorization{
		UserPubkey: "user1", ProviderPubkey: "providerA",
		Kinds: []string{"30382:rank"}, CreatedAt: 1000,
	})
	store.Add(&Authorization{
		UserPubkey: "user1", ProviderPubkey: "providerB",
		Kinds: []string{"30383:rank"}, CreatedAt: 1000,
	})

	auths := store.GetForUser("user1")
	if len(auths) != 2 {
		t.Errorf("expected 2 auths for user1, got %d", len(auths))
	}
	if store.AuthorizedCount("providerA") != 1 {
		t.Errorf("expected 1 user for providerA")
	}
	if store.AuthorizedCount("providerB") != 1 {
		t.Errorf("expected 1 user for providerB")
	}
}

func TestAuthStore_AuthorizedCount_NoUsers(t *testing.T) {
	store := NewAuthStore()
	if store.AuthorizedCount("nonexistent") != 0 {
		t.Error("expected 0 for nonexistent provider")
	}
}

func TestParseAuthorization(t *testing.T) {
	ev := &nostr.Event{
		Kind:      10040,
		PubKey:    "user1hex",
		CreatedAt: nostr.Timestamp(1700000000),
		Tags: nostr.Tags{
			{"30382:rank", "provider1hex", "wss://relay.example.com"},
			{"30383:rank", "provider1hex"},
			{"30382:rank", "provider2hex"},
		},
	}

	auths := parseAuthorization(ev)
	if len(auths) != 2 {
		t.Fatalf("expected 2 authorizations (one per provider), got %d", len(auths))
	}

	// Find provider1
	var p1, p2 *Authorization
	for _, a := range auths {
		switch a.ProviderPubkey {
		case "provider1hex":
			p1 = a
		case "provider2hex":
			p2 = a
		}
	}

	if p1 == nil {
		t.Fatal("missing provider1hex")
	}
	if len(p1.Kinds) != 2 {
		t.Errorf("provider1 expected 2 kinds, got %d", len(p1.Kinds))
	}
	if p1.RelayHint != "wss://relay.example.com" {
		t.Errorf("expected relay hint, got %q", p1.RelayHint)
	}
	if p1.UserPubkey != "user1hex" {
		t.Errorf("expected user1hex, got %q", p1.UserPubkey)
	}

	if p2 == nil {
		t.Fatal("missing provider2hex")
	}
	if len(p2.Kinds) != 1 {
		t.Errorf("provider2 expected 1 kind, got %d", len(p2.Kinds))
	}
}

func TestParseAuthorization_WrongKind(t *testing.T) {
	ev := &nostr.Event{
		Kind:   30382,
		PubKey: "user1",
		Tags: nostr.Tags{
			{"30382:rank", "provider1"},
		},
	}
	auths := parseAuthorization(ev)
	if auths != nil {
		t.Error("expected nil for non-10040 kind")
	}
}

func TestParseAuthorization_NoColonTags(t *testing.T) {
	ev := &nostr.Event{
		Kind:      10040,
		PubKey:    "user1",
		CreatedAt: nostr.Timestamp(1700000000),
		Tags: nostr.Tags{
			{"p", "somepubkey"},
			{"d", "something"},
		},
	}
	auths := parseAuthorization(ev)
	if len(auths) != 0 {
		t.Errorf("expected 0 auths for tags without colon, got %d", len(auths))
	}
}
