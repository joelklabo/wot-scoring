package main

import (
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
)

func TestAssertionStoreAddAndGet(t *testing.T) {
	store := NewAssertionStore()

	a := &ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "subject1",
		Rank:           75,
		Followers:      100,
		CreatedAt:      time.Now().Unix(),
	}

	store.Add(a)

	results := store.GetForSubject("subject1")
	if len(results) != 1 {
		t.Fatalf("expected 1 assertion, got %d", len(results))
	}
	if results[0].Rank != 75 {
		t.Errorf("expected rank 75, got %d", results[0].Rank)
	}
	if results[0].ProviderPubkey != "provider1" {
		t.Errorf("expected provider1, got %s", results[0].ProviderPubkey)
	}
}

func TestAssertionStoreMultipleProviders(t *testing.T) {
	store := NewAssertionStore()

	store.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "subject1",
		Rank:           70,
		CreatedAt:      time.Now().Unix(),
	})
	store.Add(&ExternalAssertion{
		ProviderPubkey: "provider2",
		SubjectPubkey:  "subject1",
		Rank:           80,
		CreatedAt:      time.Now().Unix(),
	})

	results := store.GetForSubject("subject1")
	if len(results) != 2 {
		t.Fatalf("expected 2 assertions, got %d", len(results))
	}
	if store.ProviderCount() != 2 {
		t.Errorf("expected 2 providers, got %d", store.ProviderCount())
	}
	if store.TotalAssertions() != 2 {
		t.Errorf("expected 2 total assertions, got %d", store.TotalAssertions())
	}
}

func TestAssertionStoreDedup(t *testing.T) {
	store := NewAssertionStore()

	// Add older assertion
	store.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "subject1",
		Rank:           50,
		CreatedAt:      1000,
	})

	// Add newer assertion from same provider — should replace
	store.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "subject1",
		Rank:           90,
		CreatedAt:      2000,
	})

	results := store.GetForSubject("subject1")
	if len(results) != 1 {
		t.Fatalf("expected 1 assertion (dedup), got %d", len(results))
	}
	if results[0].Rank != 90 {
		t.Errorf("expected newer rank 90, got %d", results[0].Rank)
	}

	// Add older assertion — should NOT replace
	store.Add(&ExternalAssertion{
		ProviderPubkey: "provider1",
		SubjectPubkey:  "subject1",
		Rank:           10,
		CreatedAt:      500,
	})

	results = store.GetForSubject("subject1")
	if results[0].Rank != 90 {
		t.Errorf("older assertion should not replace; expected 90, got %d", results[0].Rank)
	}
}

func TestAssertionStoreNotFound(t *testing.T) {
	store := NewAssertionStore()
	results := store.GetForSubject("nonexistent")
	if results != nil {
		t.Errorf("expected nil for nonexistent subject, got %v", results)
	}
}

func TestParseAssertion(t *testing.T) {
	ev := &nostr.Event{
		PubKey:    "providerABC",
		CreatedAt: nostr.Timestamp(1700000000),
		Kind:      30382,
		Tags: nostr.Tags{
			{"d", "subjectXYZ"},
			{"rank", "85"},
			{"followers", "1234"},
		},
	}

	a := parseAssertion(ev)
	if a == nil {
		t.Fatal("expected non-nil assertion")
	}
	if a.ProviderPubkey != "providerABC" {
		t.Errorf("expected providerABC, got %s", a.ProviderPubkey)
	}
	if a.SubjectPubkey != "subjectXYZ" {
		t.Errorf("expected subjectXYZ, got %s", a.SubjectPubkey)
	}
	if a.Rank != 85 {
		t.Errorf("expected rank 85, got %d", a.Rank)
	}
	if a.Followers != 1234 {
		t.Errorf("expected followers 1234, got %d", a.Followers)
	}
}

func TestParseAssertionWrongKind(t *testing.T) {
	ev := &nostr.Event{
		Kind: 30383,
		Tags: nostr.Tags{{"d", "test"}},
	}
	if parseAssertion(ev) != nil {
		t.Error("expected nil for non-30382 kind")
	}
}

func TestParseAssertionMissingDTag(t *testing.T) {
	ev := &nostr.Event{
		Kind: 30382,
		Tags: nostr.Tags{{"rank", "50"}},
	}
	if parseAssertion(ev) != nil {
		t.Error("expected nil for missing d-tag")
	}
}

func TestCompositeScoreNoExternal(t *testing.T) {
	score, sources := CompositeScore(80, nil)
	if score != 80 {
		t.Errorf("expected 80 with no external, got %d", score)
	}
	if sources != nil {
		t.Errorf("expected nil sources, got %v", sources)
	}
}

func TestCompositeScoreWithExternal(t *testing.T) {
	externals := []*ExternalAssertion{
		{ProviderPubkey: "p1", Rank: 90, CreatedAt: time.Now().Unix()},
		{ProviderPubkey: "p2", Rank: 70, CreatedAt: time.Now().Unix()},
	}

	// internal=80, external avg=(90+70)/2=80
	// composite = 80*0.7 + 80*0.3 = 56 + 24 = 80
	score, sources := CompositeScore(80, externals)
	if score != 80 {
		t.Errorf("expected 80, got %d", score)
	}
	if len(sources) != 2 {
		t.Errorf("expected 2 sources, got %d", len(sources))
	}
}

func TestCompositeScoreBlending(t *testing.T) {
	externals := []*ExternalAssertion{
		{ProviderPubkey: "p1", Rank: 100, CreatedAt: time.Now().Unix()},
	}

	// internal=50, external avg=100
	// composite = 50*0.7 + 100*0.3 = 35 + 30 = 65
	score, _ := CompositeScore(50, externals)
	if score != 65 {
		t.Errorf("expected 65, got %d", score)
	}
}

func TestProviders(t *testing.T) {
	store := NewAssertionStore()

	store.Add(&ExternalAssertion{
		ProviderPubkey: "p1",
		SubjectPubkey:  "s1",
		Rank:           50,
		CreatedAt:      time.Now().Unix(),
	})
	store.Add(&ExternalAssertion{
		ProviderPubkey: "p2",
		SubjectPubkey:  "s2",
		Rank:           60,
		CreatedAt:      time.Now().Unix(),
	})

	providers := store.Providers()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(providers))
	}
}
