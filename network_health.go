package main

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
)

// NetworkHealthResponse represents the overall network topology health analysis.
type NetworkHealthResponse struct {
	GraphSize       int                   `json:"graph_size"`
	EdgeCount       int                   `json:"edge_count"`
	Density         float64               `json:"density"`
	Reciprocity     float64               `json:"reciprocity"`
	Connectivity    ConnectivityMetrics   `json:"connectivity"`
	DegreeStats     DegreeDistribution    `json:"degree_stats"`
	ScoreDistrib    ScoreDistribution     `json:"score_distribution"`
	TopHubs         []HubEntry            `json:"top_hubs"`
	Classification  string                `json:"classification"`
	HealthScore     int                   `json:"health_score"`
}

// ConnectivityMetrics describes how well-connected the graph is.
type ConnectivityMetrics struct {
	LargestComponentSize    int     `json:"largest_component_size"`
	LargestComponentPercent float64 `json:"largest_component_percent"`
	ComponentCount          int     `json:"component_count"`
	IsolatedNodes           int     `json:"isolated_nodes"`
}

// DegreeDistribution describes in/out degree statistics.
type DegreeDistribution struct {
	MeanInDegree   float64 `json:"mean_in_degree"`
	MeanOutDegree  float64 `json:"mean_out_degree"`
	MedianInDegree int     `json:"median_in_degree"`
	MedianOutDegree int    `json:"median_out_degree"`
	MaxInDegree    int     `json:"max_in_degree"`
	MaxOutDegree   int     `json:"max_out_degree"`
	PowerLawAlpha  float64 `json:"power_law_alpha"`
}

// ScoreDistribution describes the distribution of PageRank scores.
type ScoreDistribution struct {
	GiniCoefficient float64 `json:"gini_coefficient"`
	Top1Percent     float64 `json:"top_1_percent_share"`
	Top10Percent    float64 `json:"top_10_percent_share"`
	MedianScore     int     `json:"median_score"`
	Centralization  string  `json:"centralization"`
}

// HubEntry represents a high-degree node in the network.
type HubEntry struct {
	Pubkey    string `json:"pubkey"`
	InDegree  int    `json:"in_degree"`
	OutDegree int    `json:"out_degree"`
	Score     int    `json:"score"`
}

func handleNetworkHealth(w http.ResponseWriter, r *http.Request) {
	stats := graph.Stats()
	if stats.Nodes == 0 {
		http.Error(w, `{"error":"graph not built yet"}`, http.StatusServiceUnavailable)
		return
	}

	follows, followers := graph.FollowsSnapshot()
	scores := graph.ScoresSnapshot()

	// Collect all nodes
	allNodes := make(map[string]bool, stats.Nodes)
	for k := range scores {
		allNodes[k] = true
	}
	for k := range follows {
		allNodes[k] = true
	}
	for k := range followers {
		allNodes[k] = true
	}
	nodeCount := len(allNodes)

	// Degree distributions
	inDegrees := make([]int, 0, nodeCount)
	outDegrees := make([]int, 0, nodeCount)
	combinedDegrees := make(map[string]int, nodeCount)

	for node := range allNodes {
		inDeg := len(followers[node])
		outDeg := len(follows[node])
		inDegrees = append(inDegrees, inDeg)
		outDegrees = append(outDegrees, outDeg)
		combinedDegrees[node] = inDeg + outDeg
	}

	sort.Ints(inDegrees)
	sort.Ints(outDegrees)

	degreeStats := DegreeDistribution{
		MeanInDegree:    mean(inDegrees),
		MeanOutDegree:   mean(outDegrees),
		MedianInDegree:  median(inDegrees),
		MedianOutDegree: median(outDegrees),
		MaxInDegree:     maxInt(inDegrees),
		MaxOutDegree:    maxInt(outDegrees),
		PowerLawAlpha:   estimatePowerLaw(inDegrees),
	}

	// Density
	density := 0.0
	if nodeCount > 1 {
		density = float64(stats.Edges) / (float64(nodeCount) * float64(nodeCount-1))
	}

	// Reciprocity — fraction of edges that are mutual
	reciprocity := computeReciprocity(follows)

	// Connectivity — weakly connected components via BFS
	connectivity := computeConnectivity(follows, followers, allNodes)

	// Score distribution
	scoreDistrib := computeScoreDistribution(scores, nodeCount)

	// Top hubs by combined degree
	topHubs := computeTopHubs(combinedDegrees, followers, follows, scores, nodeCount, 5)

	// Health score and classification
	healthScore := computeHealthScore(density, reciprocity, connectivity, scoreDistrib, degreeStats, nodeCount)
	classification := classifyHealth(healthScore)

	resp := NetworkHealthResponse{
		GraphSize:      nodeCount,
		EdgeCount:      stats.Edges,
		Density:        round6(density),
		Reciprocity:    round6(reciprocity),
		Connectivity:   connectivity,
		DegreeStats:    degreeStats,
		ScoreDistrib:   scoreDistrib,
		TopHubs:        topHubs,
		Classification: classification,
		HealthScore:    healthScore,
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(resp)
}

func computeReciprocity(follows map[string][]string) float64 {
	// Build a set of edges for fast lookup
	edgeSet := make(map[string]bool)
	totalEdges := 0
	for from, tos := range follows {
		for _, to := range tos {
			edgeSet[from+":"+to] = true
			totalEdges++
		}
	}

	if totalEdges == 0 {
		return 0
	}

	mutualCount := 0
	for from, tos := range follows {
		for _, to := range tos {
			if edgeSet[to+":"+from] {
				mutualCount++
			}
		}
	}

	// mutualCount counts each mutual pair twice (A->B and B->A)
	return float64(mutualCount) / float64(totalEdges)
}

func computeConnectivity(follows, followers map[string][]string, allNodes map[string]bool) ConnectivityMetrics {
	visited := make(map[string]bool, len(allNodes))
	largestSize := 0
	componentCount := 0
	isolatedNodes := 0

	for node := range allNodes {
		if visited[node] {
			continue
		}

		// BFS for weakly connected component
		queue := []string{node}
		visited[node] = true
		size := 0

		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			size++

			// Traverse follows (outgoing edges)
			for _, neighbor := range follows[curr] {
				if !visited[neighbor] && allNodes[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}

			// Traverse followers (incoming edges, treated as undirected)
			for _, neighbor := range followers[curr] {
				if !visited[neighbor] && allNodes[neighbor] {
					visited[neighbor] = true
					queue = append(queue, neighbor)
				}
			}
		}

		componentCount++
		if size > largestSize {
			largestSize = size
		}
		if size == 1 {
			isolatedNodes++
		}
	}

	pct := 0.0
	if len(allNodes) > 0 {
		pct = round6(float64(largestSize) / float64(len(allNodes)) * 100)
	}

	return ConnectivityMetrics{
		LargestComponentSize:    largestSize,
		LargestComponentPercent: pct,
		ComponentCount:          componentCount,
		IsolatedNodes:           isolatedNodes,
	}
}

func computeScoreDistribution(scores map[string]float64, nodeCount int) ScoreDistribution {
	if len(scores) == 0 {
		return ScoreDistribution{Centralization: "unknown"}
	}

	vals := make([]float64, 0, len(scores))
	for _, v := range scores {
		vals = append(vals, v)
	}
	sort.Float64s(vals)

	// Gini coefficient
	gini := giniCoefficient(vals)

	// Top 1% and 10% share of total score
	totalScore := 0.0
	for _, v := range vals {
		totalScore += v
	}

	top1Share := 0.0
	top10Share := 0.0
	if totalScore > 0 {
		n := len(vals)
		top1Idx := n - n/100
		if top1Idx == n {
			top1Idx = n - 1
		}
		top10Idx := n - n/10
		if top10Idx == n {
			top10Idx = n - 1
		}

		for i := top1Idx; i < n; i++ {
			top1Share += vals[i]
		}
		for i := top10Idx; i < n; i++ {
			top10Share += vals[i]
		}
		top1Share = round6(top1Share / totalScore * 100)
		top10Share = round6(top10Share / totalScore * 100)
	}

	// Median normalized score
	medianRaw := vals[len(vals)/2]
	medianNorm := normalizeScore(medianRaw, nodeCount)

	centralization := classifyCentralization(gini)

	return ScoreDistribution{
		GiniCoefficient: round6(gini),
		Top1Percent:     top1Share,
		Top10Percent:    top10Share,
		MedianScore:     medianNorm,
		Centralization:  centralization,
	}
}

func giniCoefficient(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range sorted {
		sum += v
	}

	if sum == 0 {
		return 0
	}

	numerator := 0.0
	for i, v := range sorted {
		numerator += float64(2*(i+1)-n-1) * v
	}

	return numerator / (float64(n) * sum)
}

func classifyCentralization(gini float64) string {
	switch {
	case gini >= 0.8:
		return "highly_centralized"
	case gini >= 0.6:
		return "centralized"
	case gini >= 0.4:
		return "moderate"
	case gini >= 0.2:
		return "decentralized"
	default:
		return "highly_decentralized"
	}
}

func computeTopHubs(combined map[string]int, followers, follows map[string][]string, scores map[string]float64, nodeCount int, limit int) []HubEntry {
	type kv struct {
		key string
		val int
	}
	pairs := make([]kv, 0, len(combined))
	for k, v := range combined {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].val > pairs[j].val
	})

	if len(pairs) > limit {
		pairs = pairs[:limit]
	}

	hubs := make([]HubEntry, len(pairs))
	for i, p := range pairs {
		hubs[i] = HubEntry{
			Pubkey:    p.key,
			InDegree:  len(followers[p.key]),
			OutDegree: len(follows[p.key]),
			Score:     normalizeScore(scores[p.key], nodeCount),
		}
	}
	return hubs
}

func computeHealthScore(density, reciprocity float64, conn ConnectivityMetrics, scoreDist ScoreDistribution, degreeStats DegreeDistribution, nodeCount int) int {
	// Health score 0-100 based on multiple weighted factors
	score := 0.0

	// 1. Connectivity (30%): % of nodes in largest component
	connScore := conn.LargestComponentPercent / 100.0
	score += connScore * 30

	// 2. Reciprocity (20%): mutual follow ratio — higher is healthier
	// Cap at 50% since very high reciprocity might indicate gaming
	recipScore := math.Min(reciprocity/0.5, 1.0)
	score += recipScore * 20

	// 3. Decentralization (20%): inverse of Gini — lower Gini is healthier
	// Gini of 0 = perfectly equal, 1 = maximally unequal
	decentScore := 1.0 - scoreDist.GiniCoefficient
	score += decentScore * 20

	// 4. Scale (15%): log-scale bonus for network size
	// 1000 nodes = ~50%, 10k = ~67%, 50k = ~80%, 100k = ~83%
	if nodeCount > 0 {
		sizeScore := math.Min(math.Log10(float64(nodeCount))/5.0, 1.0)
		score += sizeScore * 15
	}

	// 5. Power law fit (15%): social networks should follow power law (alpha 2-3)
	// Alpha near 2.5 is ideal
	plScore := 0.0
	if degreeStats.PowerLawAlpha > 0 {
		dist := math.Abs(degreeStats.PowerLawAlpha - 2.5)
		plScore = math.Max(0, 1.0-dist/2.0)
	}
	score += plScore * 15

	return int(math.Round(score))
}

func classifyHealth(score int) string {
	switch {
	case score >= 80:
		return "excellent"
	case score >= 60:
		return "good"
	case score >= 40:
		return "developing"
	case score >= 20:
		return "weak"
	default:
		return "nascent"
	}
}

// estimatePowerLaw estimates the power law exponent using the Hill estimator
// on the tail of the degree distribution (top 10% of non-zero values).
func estimatePowerLaw(degrees []int) float64 {
	// Filter non-zero degrees
	nonZero := make([]int, 0)
	for _, d := range degrees {
		if d > 0 {
			nonZero = append(nonZero, d)
		}
	}

	if len(nonZero) < 10 {
		return 0
	}

	sort.Ints(nonZero)

	// Use top 10% for tail estimation
	tailStart := len(nonZero) - len(nonZero)/10
	if tailStart >= len(nonZero) {
		tailStart = len(nonZero) - 1
	}
	tail := nonZero[tailStart:]

	if len(tail) < 2 {
		return 0
	}

	xmin := float64(tail[0])
	if xmin < 1 {
		xmin = 1
	}

	// Hill estimator: alpha = 1 + n * (sum(ln(xi/xmin)))^-1
	n := float64(len(tail))
	sumLog := 0.0
	for _, x := range tail {
		if float64(x) > xmin {
			sumLog += math.Log(float64(x) / xmin)
		}
	}

	if sumLog == 0 {
		return 0
	}

	return round6(1.0 + n/sumLog)
}

func mean(vals []int) float64 {
	if len(vals) == 0 {
		return 0
	}
	sum := 0
	for _, v := range vals {
		sum += v
	}
	return round6(float64(sum) / float64(len(vals)))
}

func median(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	return vals[len(vals)/2]
}

func maxInt(vals []int) int {
	if len(vals) == 0 {
		return 0
	}
	return vals[len(vals)-1]
}

func round6(v float64) float64 {
	return math.Round(v*1e6) / 1e6
}
