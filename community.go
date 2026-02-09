package main

import (
	"math/rand"
	"sort"
	"sync"
)

// Community represents a detected cluster of pubkeys in the follow graph.
type Community struct {
	ID      int      `json:"id"`
	Size    int      `json:"size"`
	Members []string `json:"members,omitempty"` // top members by score
	TopRank int      `json:"top_rank"`          // highest WoT rank in community
	AvgRank float64  `json:"avg_rank"`          // average WoT rank
}

// CommunityDetector performs label propagation on the follow graph.
type CommunityDetector struct {
	mu     sync.RWMutex
	labels map[string]int // pubkey -> community label
}

func NewCommunityDetector() *CommunityDetector {
	return &CommunityDetector{
		labels: make(map[string]int),
	}
}

// DetectCommunities runs label propagation on the given graph.
// iterations controls convergence (5-10 is usually sufficient).
// Returns the number of communities detected.
func (cd *CommunityDetector) DetectCommunities(g *Graph, iterations int) int {
	g.mu.RLock()

	// Collect all nodes
	nodes := make([]string, 0, len(g.follows))
	for k := range g.follows {
		nodes = append(nodes, k)
	}

	// Initialize: each node is its own community
	labels := make(map[string]int, len(nodes))
	for i, n := range nodes {
		labels[n] = i
	}

	// Copy adjacency for unlocked access
	follows := make(map[string][]string, len(g.follows))
	for k, v := range g.follows {
		follows[k] = v
	}
	followers := make(map[string][]string, len(g.followers))
	for k, v := range g.followers {
		followers[k] = v
	}
	g.mu.RUnlock()

	// Label propagation: each node adopts the most common label among neighbors
	for iter := 0; iter < iterations; iter++ {
		// Shuffle to avoid order bias
		rand.Shuffle(len(nodes), func(i, j int) {
			nodes[i], nodes[j] = nodes[j], nodes[i]
		})

		changed := 0
		for _, node := range nodes {
			// Collect neighbor labels (both follows and followers = undirected)
			counts := make(map[int]int)
			for _, f := range follows[node] {
				if l, ok := labels[f]; ok {
					counts[l]++
				}
			}
			for _, f := range followers[node] {
				if l, ok := labels[f]; ok {
					counts[l]++
				}
			}

			if len(counts) == 0 {
				continue
			}

			// Find most common label
			bestLabel := labels[node]
			bestCount := 0
			for l, c := range counts {
				if c > bestCount || (c == bestCount && l < bestLabel) {
					bestLabel = l
					bestCount = c
				}
			}

			if labels[node] != bestLabel {
				labels[node] = bestLabel
				changed++
			}
		}

		if changed == 0 {
			break // converged
		}
	}

	cd.mu.Lock()
	cd.labels = labels
	cd.mu.Unlock()

	// Count distinct communities
	seen := make(map[int]bool)
	for _, l := range labels {
		seen[l] = true
	}
	return len(seen)
}

// GetCommunity returns the community label for a pubkey.
func (cd *CommunityDetector) GetCommunity(pubkey string) (int, bool) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	l, ok := cd.labels[pubkey]
	return l, ok
}

// GetCommunityMembers returns all pubkeys in the same community as the given pubkey.
func (cd *CommunityDetector) GetCommunityMembers(pubkey string) []string {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	label, ok := cd.labels[pubkey]
	if !ok {
		return nil
	}

	var members []string
	for pk, l := range cd.labels {
		if l == label {
			members = append(members, pk)
		}
	}
	return members
}

// TopCommunities returns the N largest communities with metadata.
// topMembersPerCommunity limits how many member pubkeys are included.
func (cd *CommunityDetector) TopCommunities(g *Graph, n int, topMembersPerCommunity int) []Community {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	// Group by label
	groups := make(map[int][]string)
	for pk, l := range cd.labels {
		groups[l] = append(groups[l], pk)
	}

	// Build community objects
	communities := make([]Community, 0, len(groups))
	for id, members := range groups {
		if len(members) < 3 {
			continue // skip trivial clusters
		}

		// Sort members by score (highest first)
		g.mu.RLock()
		sort.Slice(members, func(i, j int) bool {
			return g.scores[members[i]] > g.scores[members[j]]
		})
		g.mu.RUnlock()

		topRank := 0
		totalRank := 0.0
		stats := g.Stats()
		for _, m := range members {
			score, _ := g.GetScore(m)
			rank := normalizeScore(score, stats.Nodes)
			if rank > topRank {
				topRank = rank
			}
			totalRank += float64(rank)
		}

		top := members
		if topMembersPerCommunity > 0 && len(top) > topMembersPerCommunity {
			top = top[:topMembersPerCommunity]
		}

		communities = append(communities, Community{
			ID:      id,
			Size:    len(members),
			Members: top,
			TopRank: topRank,
			AvgRank: totalRank / float64(len(members)),
		})
	}

	// Sort by size descending
	sort.Slice(communities, func(i, j int) bool {
		return communities[i].Size > communities[j].Size
	})

	if n > 0 && n < len(communities) {
		communities = communities[:n]
	}

	return communities
}

// TotalCommunities returns the count of non-trivial communities (size >= 3).
func (cd *CommunityDetector) TotalCommunities() int {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	groups := make(map[int]int)
	for _, l := range cd.labels {
		groups[l]++
	}

	count := 0
	for _, size := range groups {
		if size >= 3 {
			count++
		}
	}
	return count
}
