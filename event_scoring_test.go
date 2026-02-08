package main

import (
	"testing"
)

func TestEventStore_GetEvent(t *testing.T) {
	es := NewEventStore()

	m := es.GetEvent("event1")
	if m == nil {
		t.Fatal("GetEvent returned nil")
	}
	if m.EventID != "event1" {
		t.Errorf("expected EventID 'event1', got %q", m.EventID)
	}
	if m.Reactions != 0 {
		t.Errorf("expected 0 Reactions, got %d", m.Reactions)
	}

	// Modify and verify persistence
	m.Reactions = 10
	m.Comments = 5
	m2 := es.GetEvent("event1")
	if m2.Reactions != 10 {
		t.Errorf("expected 10 Reactions, got %d", m2.Reactions)
	}
	if m2.Comments != 5 {
		t.Errorf("expected 5 Comments, got %d", m2.Comments)
	}

	// Different key should be independent
	m3 := es.GetEvent("event2")
	if m3.Reactions != 0 {
		t.Errorf("expected 0 Reactions for new key, got %d", m3.Reactions)
	}
}

func TestEventStore_GetAddressable(t *testing.T) {
	es := NewEventStore()

	addr := "30023:pubkey1:my-article"
	m := es.GetAddressable(addr)
	if m == nil {
		t.Fatal("GetAddressable returned nil")
	}
	if m.Address != addr {
		t.Errorf("expected Address %q, got %q", addr, m.Address)
	}

	m.Reactions = 20
	m.ZapAmount = 5000
	m2 := es.GetAddressable(addr)
	if m2.Reactions != 20 {
		t.Errorf("expected 20 Reactions, got %d", m2.Reactions)
	}
	if m2.ZapAmount != 5000 {
		t.Errorf("expected 5000 ZapAmount, got %d", m2.ZapAmount)
	}
}

func TestEventEngagement(t *testing.T) {
	tests := []struct {
		name string
		meta *EventMeta
		want int64
	}{
		{
			name: "zero engagement",
			meta: &EventMeta{},
			want: 0,
		},
		{
			name: "reactions only",
			meta: &EventMeta{Reactions: 10},
			want: 10,
		},
		{
			name: "reposts weighted 2x",
			meta: &EventMeta{Reposts: 5},
			want: 10,
		},
		{
			name: "comments weighted 3x",
			meta: &EventMeta{Comments: 3},
			want: 9,
		},
		{
			name: "zaps add raw amount",
			meta: &EventMeta{ZapAmount: 1000},
			want: 1000,
		},
		{
			name: "combined engagement",
			meta: &EventMeta{Reactions: 10, Reposts: 5, Comments: 3, ZapAmount: 100},
			want: 10 + 10 + 9 + 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eventEngagement(tt.meta)
			if got != tt.want {
				t.Errorf("eventEngagement() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEventRank(t *testing.T) {
	tests := []struct {
		name          string
		meta          *EventMeta
		maxEngagement int64
		wantMin       int
		wantMax       int
	}{
		{
			name:          "zero max engagement",
			meta:          &EventMeta{Reactions: 10},
			maxEngagement: 0,
			wantMin:       0,
			wantMax:       0,
		},
		{
			name:          "max engagement event",
			meta:          &EventMeta{Reactions: 100},
			maxEngagement: 100,
			wantMin:       90,
			wantMax:       100,
		},
		{
			name:          "zero engagement event",
			meta:          &EventMeta{},
			maxEngagement: 100,
			wantMin:       0,
			wantMax:       5,
		},
		{
			name:          "mid engagement",
			meta:          &EventMeta{Reactions: 50},
			maxEngagement: 100,
			wantMin:       40,
			wantMax:       100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := eventRank(tt.meta, tt.maxEngagement)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("eventRank() = %d, want between %d and %d", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTopEvents(t *testing.T) {
	es := NewEventStore()

	// Add events with varying engagement
	m1 := es.GetEvent("low")
	m1.Reactions = 1

	m2 := es.GetEvent("mid")
	m2.Reactions = 10
	m2.Comments = 5

	m3 := es.GetEvent("high")
	m3.Reactions = 100
	m3.Reposts = 50
	m3.ZapAmount = 10000

	top := es.TopEvents(2)
	if len(top) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(top))
	}
	if top[0].EventID != "high" {
		t.Errorf("expected 'high' first, got %q", top[0].EventID)
	}
	if top[1].EventID != "mid" {
		t.Errorf("expected 'mid' second, got %q", top[1].EventID)
	}
}

func TestEventStoreCount(t *testing.T) {
	es := NewEventStore()

	if es.EventCount() != 0 {
		t.Errorf("expected 0 events, got %d", es.EventCount())
	}
	if es.AddressableCount() != 0 {
		t.Errorf("expected 0 addressable, got %d", es.AddressableCount())
	}

	es.GetEvent("e1")
	es.GetEvent("e2")
	es.GetAddressable("30023:pk:slug")

	if es.EventCount() != 2 {
		t.Errorf("expected 2 events, got %d", es.EventCount())
	}
	if es.AddressableCount() != 1 {
		t.Errorf("expected 1 addressable, got %d", es.AddressableCount())
	}
}
