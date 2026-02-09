package main

import (
	"testing"
)

func TestCommunityDetector_BasicClusters(t *testing.T) {
	g := NewGraph()

	// Create two clear clusters: A-B-C and D-E-F
	g.AddFollow("A", "B")
	g.AddFollow("B", "A")
	g.AddFollow("B", "C")
	g.AddFollow("C", "B")
	g.AddFollow("A", "C")
	g.AddFollow("C", "A")

	g.AddFollow("D", "E")
	g.AddFollow("E", "D")
	g.AddFollow("E", "F")
	g.AddFollow("F", "E")
	g.AddFollow("D", "F")
	g.AddFollow("F", "D")

	g.ComputePageRank(20, 0.85)

	cd := NewCommunityDetector()
	numCommunities := cd.DetectCommunities(g, 10)

	if numCommunities < 2 {
		t.Errorf("expected at least 2 communities, got %d", numCommunities)
	}

	// A, B, C should be in the same community
	labelA, _ := cd.GetCommunity("A")
	labelB, _ := cd.GetCommunity("B")
	labelC, _ := cd.GetCommunity("C")

	if labelA != labelB || labelB != labelC {
		t.Errorf("A, B, C should be same community: A=%d B=%d C=%d", labelA, labelB, labelC)
	}

	// D, E, F should be in the same community
	labelD, _ := cd.GetCommunity("D")
	labelE, _ := cd.GetCommunity("E")
	labelF, _ := cd.GetCommunity("F")

	if labelD != labelE || labelE != labelF {
		t.Errorf("D, E, F should be same community: D=%d E=%d F=%d", labelD, labelE, labelF)
	}

	// The two clusters should be different
	if labelA == labelD {
		t.Error("clusters {A,B,C} and {D,E,F} should be different communities")
	}
}

func TestCommunityDetector_Members(t *testing.T) {
	g := NewGraph()
	g.AddFollow("A", "B")
	g.AddFollow("B", "A")
	g.AddFollow("B", "C")
	g.AddFollow("C", "B")
	g.ComputePageRank(20, 0.85)

	cd := NewCommunityDetector()
	cd.DetectCommunities(g, 10)

	members := cd.GetCommunityMembers("A")
	if len(members) < 2 {
		t.Errorf("expected at least 2 members in A's community, got %d", len(members))
	}
}

func TestCommunityDetector_UnknownPubkey(t *testing.T) {
	cd := NewCommunityDetector()
	_, ok := cd.GetCommunity("nonexistent")
	if ok {
		t.Error("expected false for unknown pubkey")
	}

	members := cd.GetCommunityMembers("nonexistent")
	if members != nil {
		t.Error("expected nil for unknown pubkey")
	}
}

func TestCommunityDetector_TopCommunities(t *testing.T) {
	g := NewGraph()

	// Cluster 1: 5 nodes
	for _, pair := range [][2]string{{"A", "B"}, {"B", "C"}, {"C", "D"}, {"D", "E"}, {"A", "E"}} {
		g.AddFollow(pair[0], pair[1])
		g.AddFollow(pair[1], pair[0])
	}

	// Cluster 2: 3 nodes
	g.AddFollow("X", "Y")
	g.AddFollow("Y", "X")
	g.AddFollow("Y", "Z")
	g.AddFollow("Z", "Y")
	g.AddFollow("X", "Z")
	g.AddFollow("Z", "X")

	g.ComputePageRank(20, 0.85)

	cd := NewCommunityDetector()
	cd.DetectCommunities(g, 10)

	top := cd.TopCommunities(g, 10, 3)
	if len(top) < 2 {
		t.Fatalf("expected at least 2 communities, got %d", len(top))
	}

	// First should be the larger cluster
	if top[0].Size < top[1].Size {
		t.Error("communities should be sorted by size descending")
	}
}

func TestCommunityDetector_TotalCommunities(t *testing.T) {
	cd := NewCommunityDetector()
	if cd.TotalCommunities() != 0 {
		t.Error("expected 0 communities initially")
	}
}
