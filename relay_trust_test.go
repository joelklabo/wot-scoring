package main

import (
	"testing"
)

func TestComputeCombinedScore(t *testing.T) {
	tests := []struct {
		name     string
		relay    *RelayTrustScores
		wot      *OperatorWoTScore
		expected int
	}{
		{
			name:     "both nil",
			relay:    nil,
			wot:      nil,
			expected: 0,
		},
		{
			name:  "relay only",
			relay: &RelayTrustScores{Overall: 80},
			wot:   nil,
			expected: 80,
		},
		{
			name:  "wot only",
			relay: nil,
			wot:   &OperatorWoTScore{WoTScore: 60, InGraph: true},
			expected: 60,
		},
		{
			name:  "wot not in graph",
			relay: &RelayTrustScores{Overall: 90},
			wot:   &OperatorWoTScore{WoTScore: 50, InGraph: false},
			expected: 90,
		},
		{
			name:  "both present - high relay high wot",
			relay: &RelayTrustScores{Overall: 90},
			wot:   &OperatorWoTScore{WoTScore: 80, InGraph: true},
			expected: 87, // 90*0.7 + 80*0.3 = 63 + 24 = 87
		},
		{
			name:  "both present - low relay high wot",
			relay: &RelayTrustScores{Overall: 40},
			wot:   &OperatorWoTScore{WoTScore: 90, InGraph: true},
			expected: 55, // 40*0.7 + 90*0.3 = 28 + 27 = 55
		},
		{
			name:  "both present - perfect scores",
			relay: &RelayTrustScores{Overall: 100},
			wot:   &OperatorWoTScore{WoTScore: 100, InGraph: true},
			expected: 100, // 100*0.7 + 100*0.3 = 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeCombinedScore(tt.relay, tt.wot)
			if got != tt.expected {
				t.Errorf("computeCombinedScore() = %d, want %d", got, tt.expected)
			}
		})
	}
}
