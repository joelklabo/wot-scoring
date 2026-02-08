package main

import (
	"testing"
)

func TestDecodeBolt11Amount(t *testing.T) {
	tests := []struct {
		name    string
		invoice string
		want    int64
	}{
		{
			name:    "micro-bitcoin 100u = 10000 sats",
			invoice: "lnbc100u1pjtest",
			want:    10000,
		},
		{
			name:    "milli-bitcoin 1m = 100000 sats",
			invoice: "lnbc1m1pjtest",
			want:    100000,
		},
		{
			name:    "nano-bitcoin 50000n = 5000 sats",
			invoice: "lnbc50000n1pjtest",
			want:    5000,
		},
		{
			name:    "pico-bitcoin 10000p = 0 sats (rounds down)",
			invoice: "lnbc10000p1pjtest",
			want:    1,
		},
		{
			name:    "micro-bitcoin 21u = 2100 sats",
			invoice: "lnbc21u1pjtest",
			want:    2100,
		},
		{
			name:    "micro-bitcoin 500u = 50000 sats",
			invoice: "lnbc500u1pjtest",
			want:    50000,
		},
		{
			name:    "empty invoice",
			invoice: "",
			want:    0,
		},
		{
			name:    "too short",
			invoice: "lnbc",
			want:    0,
		},
		{
			name:    "testnet invoice",
			invoice: "lntb100u1pjtest",
			want:    10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeBolt11Amount(tt.invoice)
			if got != tt.want {
				t.Errorf("decodeBolt11Amount(%q) = %d, want %d", tt.invoice, got, tt.want)
			}
		})
	}
}

func TestNormalizeScore(t *testing.T) {
	tests := []struct {
		name  string
		raw   float64
		total int
		want  int
	}{
		{"zero raw", 0, 1000, 0},
		{"zero total", 0.5, 0, 0},
		{"average score", 0.001, 1000, 8},
		{"high score", 0.01, 1000, 26},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeScore(tt.raw, tt.total)
			if got != tt.want {
				t.Errorf("normalizeScore(%f, %d) = %d, want %d", tt.raw, tt.total, got, tt.want)
			}
		})
	}
}

func TestMetaStore(t *testing.T) {
	ms := NewMetaStore()

	// Get should create entry if missing
	m := ms.Get("abc123")
	if m == nil {
		t.Fatal("Get returned nil")
	}
	if m.PostCount != 0 {
		t.Errorf("expected 0 PostCount, got %d", m.PostCount)
	}

	// Modify and verify persistence
	m.PostCount = 5
	m.Followers = 100
	m2 := ms.Get("abc123")
	if m2.PostCount != 5 {
		t.Errorf("expected 5 PostCount, got %d", m2.PostCount)
	}
	if m2.Followers != 100 {
		t.Errorf("expected 100 Followers, got %d", m2.Followers)
	}

	// Different key should be independent
	m3 := ms.Get("def456")
	if m3.PostCount != 0 {
		t.Errorf("expected 0 PostCount for new key, got %d", m3.PostCount)
	}
}

func TestCountFollowers(t *testing.T) {
	g := NewGraph()
	g.AddFollow("alice", "bob")
	g.AddFollow("charlie", "bob")
	g.AddFollow("dave", "bob")
	g.AddFollow("alice", "charlie")

	ms := NewMetaStore()
	ms.CountFollowers(g)

	bob := ms.Get("bob")
	if bob.Followers != 3 {
		t.Errorf("bob followers = %d, want 3", bob.Followers)
	}

	charlie := ms.Get("charlie")
	if charlie.Followers != 1 {
		t.Errorf("charlie followers = %d, want 1", charlie.Followers)
	}
}

func TestGraph_ComputePageRank(t *testing.T) {
	g := NewGraph()
	// Simple triangle: A->B, B->C, C->A
	g.AddFollow("A", "B")
	g.AddFollow("B", "C")
	g.AddFollow("C", "A")

	g.ComputePageRank(20, 0.85)

	// In a symmetric cycle, all scores should be approximately equal
	scoreA, okA := g.GetScore("A")
	scoreB, okB := g.GetScore("B")
	scoreC, okC := g.GetScore("C")

	if !okA || !okB || !okC {
		t.Fatal("expected all scores to be found")
	}

	// Scores should be roughly 1/3 each
	for _, s := range []float64{scoreA, scoreB, scoreC} {
		if s < 0.2 || s > 0.5 {
			t.Errorf("expected score near 0.33, got %f", s)
		}
	}
}

func TestGraph_TopN(t *testing.T) {
	g := NewGraph()
	// Hub-and-spoke: everyone follows A
	g.AddFollow("B", "A")
	g.AddFollow("C", "A")
	g.AddFollow("D", "A")
	g.AddFollow("A", "B")

	g.ComputePageRank(20, 0.85)
	top := g.TopN(1)

	if len(top) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(top))
	}
	if top[0].Pubkey != "A" {
		t.Errorf("expected A to be top, got %s", top[0].Pubkey)
	}
}

func TestTopNPubkeys(t *testing.T) {
	g := NewGraph()
	g.AddFollow("B", "A")
	g.AddFollow("C", "A")
	g.AddFollow("A", "B")
	g.ComputePageRank(20, 0.85)

	pubs := TopNPubkeys(g, 2)
	if len(pubs) != 2 {
		t.Fatalf("expected 2 pubkeys, got %d", len(pubs))
	}
	if pubs[0] != "A" {
		t.Errorf("expected A first, got %s", pubs[0])
	}
}
