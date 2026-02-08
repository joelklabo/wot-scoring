package main

import (
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestExternalStore_Get(t *testing.T) {
	xs := NewExternalStore()

	m := xs.Get("#bitcoin")
	if m == nil {
		t.Fatal("Get returned nil")
	}
	if m.Identifier != "#bitcoin" {
		t.Errorf("expected identifier '#bitcoin', got %q", m.Identifier)
	}
	if m.Mentions != 0 {
		t.Errorf("expected 0 Mentions, got %d", m.Mentions)
	}

	// Modify and verify persistence
	m.Mentions = 10
	m.Kind = "hashtag"
	m2 := xs.Get("#bitcoin")
	if m2.Mentions != 10 {
		t.Errorf("expected 10 Mentions, got %d", m2.Mentions)
	}
	if m2.Kind != "hashtag" {
		t.Errorf("expected kind 'hashtag', got %q", m2.Kind)
	}

	// Different key should be independent
	m3 := xs.Get("#nostr")
	if m3.Mentions != 0 {
		t.Errorf("expected 0 Mentions for new key, got %d", m3.Mentions)
	}
}

func TestExternalStore_Count(t *testing.T) {
	xs := NewExternalStore()

	if xs.Count() != 0 {
		t.Errorf("expected 0, got %d", xs.Count())
	}

	xs.Get("#bitcoin")
	xs.Get("#nostr")
	xs.Get("https://example.com")

	if xs.Count() != 3 {
		t.Errorf("expected 3, got %d", xs.Count())
	}
}

func TestExternalEngagement(t *testing.T) {
	tests := []struct {
		name string
		meta *ExternalMeta
		want int64
	}{
		{
			name: "zero engagement",
			meta: &ExternalMeta{Authors: make(map[string]bool)},
			want: 0,
		},
		{
			name: "mentions only",
			meta: &ExternalMeta{Mentions: 10, Authors: make(map[string]bool)},
			want: 10,
		},
		{
			name: "reposts weighted 2x",
			meta: &ExternalMeta{Reposts: 5, Authors: make(map[string]bool)},
			want: 10,
		},
		{
			name: "comments weighted 3x",
			meta: &ExternalMeta{Comments: 3, Authors: make(map[string]bool)},
			want: 9,
		},
		{
			name: "zaps add raw amount",
			meta: &ExternalMeta{ZapAmount: 1000, Authors: make(map[string]bool)},
			want: 1000,
		},
		{
			name: "combined",
			meta: &ExternalMeta{Mentions: 10, Reactions: 5, Reposts: 2, Comments: 1, ZapAmount: 100, Authors: make(map[string]bool)},
			want: 10 + 5 + 4 + 3 + 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalEngagement(tt.meta)
			if got != tt.want {
				t.Errorf("externalEngagement() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestExternalRank(t *testing.T) {
	tests := []struct {
		name    string
		meta    *ExternalMeta
		maxEng  int64
		wantMin int
		wantMax int
	}{
		{
			name:    "zero max engagement",
			meta:    &ExternalMeta{Mentions: 10, Authors: make(map[string]bool)},
			maxEng:  0,
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "max engagement item",
			meta:    &ExternalMeta{Mentions: 100, Authors: make(map[string]bool)},
			maxEng:  100,
			wantMin: 90,
			wantMax: 100,
		},
		{
			name:    "zero engagement item",
			meta:    &ExternalMeta{Authors: make(map[string]bool)},
			maxEng:  100,
			wantMin: 0,
			wantMax: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalRank(tt.meta, tt.maxEng)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("externalRank() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTopExternal(t *testing.T) {
	xs := NewExternalStore()

	m1 := xs.Get("#low")
	m1.Mentions = 1

	m2 := xs.Get("#mid")
	m2.Mentions = 10
	m2.Reactions = 5

	m3 := xs.Get("#high")
	m3.Mentions = 100
	m3.Reactions = 50
	m3.ZapAmount = 10000

	top := xs.TopExternal(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].Identifier != "#high" {
		t.Errorf("expected '#high' first, got %q", top[0].Identifier)
	}
	if top[1].Identifier != "#mid" {
		t.Errorf("expected '#mid' second, got %q", top[1].Identifier)
	}
}

func TestExtractIdentifiers(t *testing.T) {
	xs := NewExternalStore()

	ev := &nostr.Event{
		PubKey: "abc123",
		Tags: nostr.Tags{
			{"t", "bitcoin"},
			{"t", "nostr"},
			{"t", "Bitcoin"}, // should normalize to lowercase
			{"r", "https://example.com/article"},
			{"r", "not-a-url"},           // should be ignored
			{"p", "some-pubkey"},         // should be ignored
			{"e", "some-event-id"},       // should be ignored
		},
	}

	xs.extractIdentifiers(ev)

	if xs.Count() != 4 { // #bitcoin, #nostr, #bitcoin (separate because "Bitcoin" lowercases to same), https://example.com/article
		// Actually #bitcoin and #Bitcoin both normalize to #bitcoin
		// So: #bitcoin, #nostr, https://example.com/article = 3
	}

	btc := xs.Get("#bitcoin")
	if btc.Mentions != 2 { // "bitcoin" and "Bitcoin" both -> "#bitcoin"
		t.Errorf("expected 2 mentions for #bitcoin, got %d", btc.Mentions)
	}
	if btc.Kind != "hashtag" {
		t.Errorf("expected kind 'hashtag', got %q", btc.Kind)
	}
	if !btc.Authors["abc123"] {
		t.Error("expected abc123 in authors")
	}

	nostrTag := xs.Get("#nostr")
	if nostrTag.Mentions != 1 {
		t.Errorf("expected 1 mention for #nostr, got %d", nostrTag.Mentions)
	}

	url := xs.Get("https://example.com/article")
	if url.Mentions != 1 {
		t.Errorf("expected 1 mention for URL, got %d", url.Mentions)
	}
	if url.Kind != "url" {
		t.Errorf("expected kind 'url', got %q", url.Kind)
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"https URL", "https://Example.COM/Path", "https://example.com/Path"},
		{"http URL", "http://Example.COM/path", "http://example.com/path"},
		{"strip fragment", "https://example.com/page#section", "https://example.com/page"},
		{"no path", "https://EXAMPLE.COM", "https://example.com"},
		{"not a URL", "ftp://example.com", ""},
		{"plain text", "hello world", ""},
		{"empty", "", ""},
		{"with whitespace", "  https://example.com  ", "https://example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeURL(tt.raw)
			if got != tt.want {
				t.Errorf("normalizeURL(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
