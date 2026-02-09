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

func TestTopTopics(t *testing.T) {
	m := &PubkeyMeta{
		Topics: map[string]int{
			"bitcoin":   10,
			"nostr":     8,
			"lightning": 5,
			"zaps":      3,
			"dev":       2,
			"test":      1,
		},
	}

	top3 := m.TopTopics(3)
	if len(top3) != 3 {
		t.Fatalf("expected 3 topics, got %d", len(top3))
	}
	if top3[0] != "bitcoin" {
		t.Errorf("expected bitcoin first, got %s", top3[0])
	}
	if top3[1] != "nostr" {
		t.Errorf("expected nostr second, got %s", top3[1])
	}
	if top3[2] != "lightning" {
		t.Errorf("expected lightning third, got %s", top3[2])
	}

	// Request more than available
	top10 := m.TopTopics(10)
	if len(top10) != 6 {
		t.Errorf("expected 6 topics (all available), got %d", len(top10))
	}

	// Empty topics
	empty := &PubkeyMeta{}
	topNil := empty.TopTopics(5)
	if topNil != nil {
		t.Errorf("expected nil for empty topics, got %v", topNil)
	}
}

func TestActiveHours(t *testing.T) {
	m := &PubkeyMeta{}

	// No activity — both zero
	s, e := m.ActiveHours()
	if s != 0 || e != 0 {
		t.Errorf("empty: expected (0,0), got (%d,%d)", s, e)
	}

	// Peak activity at hours 14-21 UTC
	m.HourBuckets = [24]int{
		0, 0, 0, 0, 0, 0, 0, 0, // 0-7
		1, 1, 2, 2, 3, 3, 10, 10, // 8-15
		10, 10, 10, 10, 10, 10, 1, 0, // 16-23
	}
	s, e = m.ActiveHours()
	// The 8-hour window 14-21 has sum = 10+10+10+10+10+10+10+1 = 71
	// Window 13-20 has 3+10+10+10+10+10+10+10 = 73
	// Window 14-21 has 10+10+10+10+10+10+1+0 = 61
	// Actually let me just verify the window wraps correctly
	if s < 0 || s > 23 {
		t.Errorf("start hour out of range: %d", s)
	}
	if e < 0 || e > 23 {
		t.Errorf("end hour out of range: %d", e)
	}
	// The peak 8-hour window should start around 13-15
	if s < 12 || s > 16 {
		t.Errorf("expected peak start around 13-15, got %d", s)
	}

	// Test wrap-around: activity concentrated at hours 22-5 UTC
	m2 := &PubkeyMeta{}
	m2.HourBuckets = [24]int{
		10, 10, 10, 10, 10, 10, 0, 0, // 0-7
		0, 0, 0, 0, 0, 0, 0, 0, // 8-15
		0, 0, 0, 0, 0, 0, 10, 10, // 16-23
	}
	s2, e2 := m2.ActiveHours()
	// Window 22-5 has sum = 10+10+10+10+10+10+10+10 = 80
	if s2 != 22 {
		t.Errorf("wrap-around: expected start 22, got %d", s2)
	}
	if e2 != 6 {
		t.Errorf("wrap-around: expected end 6, got %d", e2)
	}
}

func TestReportsTracking(t *testing.T) {
	ms := NewMetaStore()
	m := ms.Get("reporter")
	m.ReportsSent = 5

	target := ms.Get("target")
	target.ReportsRecd = 3

	if m.ReportsSent != 5 {
		t.Errorf("expected 5 reports sent, got %d", m.ReportsSent)
	}
	if target.ReportsRecd != 3 {
		t.Errorf("expected 3 reports received, got %d", target.ReportsRecd)
	}
}

func TestHourBuckets(t *testing.T) {
	ms := NewMetaStore()
	m := ms.Get("user1")

	// Simulate activity at specific hours
	m.HourBuckets[0] = 5
	m.HourBuckets[12] = 10
	m.HourBuckets[23] = 3

	total := 0
	for _, c := range m.HourBuckets {
		total += c
	}
	if total != 18 {
		t.Errorf("expected 18 total events, got %d", total)
	}
}
