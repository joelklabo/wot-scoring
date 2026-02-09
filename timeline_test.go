package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTimelineMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/timeline", nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTimelineShortPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/timeline?pubkey=abc", nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTimelineInvalidHex(t *testing.T) {
	// 64 chars but not valid hex (contains 'g')
	req := httptest.NewRequest("GET", "/timeline?pubkey=gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg", nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTimelineUnknownPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/timeline?pubkey=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp TimelineResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Points) != 0 {
		t.Fatalf("expected 0 points, got %d", len(resp.Points))
	}
	if resp.CurrentFollowers != 0 {
		t.Fatalf("expected 0 followers, got %d", resp.CurrentFollowers)
	}
}

func TestTimelineWithFollowers(t *testing.T) {
	// Save and restore graph state
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Add followers with timestamps spread across months
	now := time.Now()
	f1 := "1111111111111111111111111111111111111111111111111111111111111111"
	f2 := "2222222222222222222222222222222222222222222222222222222222222222"
	f3 := "3333333333333333333333333333333333333333333333333333333333333333"
	f4 := "4444444444444444444444444444444444444444444444444444444444444444"

	graph.AddFollowWithTime(f1, target, now.AddDate(0, -3, 0))
	graph.AddFollowWithTime(f2, target, now.AddDate(0, -2, 0))
	graph.AddFollowWithTime(f3, target, now.AddDate(0, -1, 0))
	graph.AddFollowWithTime(f4, target, now.AddDate(0, 0, -5))

	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/timeline?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp TimelineResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.Pubkey != target {
		t.Fatalf("expected pubkey %s, got %s", target, resp.Pubkey)
	}
	if resp.TotalFollowers != 4 {
		t.Fatalf("expected 4 total followers, got %d", resp.TotalFollowers)
	}
	if resp.FollowersWithDates != 4 {
		t.Fatalf("expected 4 followers with dates, got %d", resp.FollowersWithDates)
	}
	if len(resp.Points) == 0 {
		t.Fatal("expected timeline points, got 0")
	}
	if resp.FirstFollow == "" {
		t.Fatal("expected first_follow to be set")
	}
	if resp.LatestFollow == "" {
		t.Fatal("expected latest_follow to be set")
	}

	// Verify cumulative followers increase monotonically
	prev := 0
	for i, p := range resp.Points {
		if p.CumulativeFollows < prev {
			t.Fatalf("point %d: cumulative %d < previous %d", i, p.CumulativeFollows, prev)
		}
		prev = p.CumulativeFollows
		if p.NewFollows <= 0 {
			t.Fatalf("point %d: new_follows should be positive, got %d", i, p.NewFollows)
		}
	}

	// Last point should have all 4 followers
	lastPoint := resp.Points[len(resp.Points)-1]
	if lastPoint.CumulativeFollows != 4 {
		t.Fatalf("last point should have 4 cumulative followers, got %d", lastPoint.CumulativeFollows)
	}
}

func TestTimelineFollowersWithoutDates(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	target := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	f1 := "1111111111111111111111111111111111111111111111111111111111111111"
	f2 := "2222222222222222222222222222222222222222222222222222222222222222"

	// Add followers without timestamps
	graph.AddFollow(f1, target)
	graph.AddFollow(f2, target)
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/timeline?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleTimeline(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp TimelineResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}

	if resp.TotalFollowers != 2 {
		t.Fatalf("expected 2 total followers, got %d", resp.TotalFollowers)
	}
	if resp.FollowersWithDates != 0 {
		t.Fatalf("expected 0 followers with dates, got %d", resp.FollowersWithDates)
	}
	if len(resp.Points) != 0 {
		t.Fatalf("expected 0 timeline points without date data, got %d", len(resp.Points))
	}
}

func TestEstimateScoreFromFollowers(t *testing.T) {
	// Zero cases
	if s := estimateScoreFromFollowers(0, 1000); s != 0 {
		t.Fatalf("expected 0 for 0 followers, got %d", s)
	}
	if s := estimateScoreFromFollowers(100, 0); s != 0 {
		t.Fatalf("expected 0 for 0 graph size, got %d", s)
	}

	// More followers = higher score
	s1 := estimateScoreFromFollowers(10, 50000)
	s2 := estimateScoreFromFollowers(100, 50000)
	s3 := estimateScoreFromFollowers(1000, 50000)
	if s1 >= s2 || s2 >= s3 {
		t.Fatalf("scores should increase with followers: %d, %d, %d", s1, s2, s3)
	}

	// Score should be capped at 100
	s := estimateScoreFromFollowers(1000000, 50000)
	if s > 100 {
		t.Fatalf("score should be capped at 100, got %d", s)
	}
}
