package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSpamMissingPubkey(t *testing.T) {
	req := httptest.NewRequest("GET", "/spam", nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSpamInvalidNpub(t *testing.T) {
	req := httptest.NewRequest("GET", "/spam?pubkey=npub1invalid", nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestSpamUnknownPubkey(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	pubkey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	req := httptest.NewRequest("GET", "/spam?pubkey="+pubkey, nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp SpamResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Pubkey != pubkey {
		t.Fatalf("expected pubkey %s, got %s", pubkey, resp.Pubkey)
	}
	// Unknown pubkey should have high spam probability
	if resp.SpamProbability < 0.4 {
		t.Fatalf("expected spam_probability >= 0.4 for unknown pubkey, got %f", resp.SpamProbability)
	}
	if len(resp.Signals) != 6 {
		t.Fatalf("expected 6 signals, got %d", len(resp.Signals))
	}
}

func TestSpamTrustedPubkey(t *testing.T) {
	oldGraph := graph
	oldMeta := meta
	graph = NewGraph()
	meta = NewMetaStore()
	defer func() {
		graph = oldGraph
		meta = oldMeta
	}()

	target := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Build a graph where target has many followers
	for i := 0; i < 50; i++ {
		follower := padHex(i)
		graph.AddFollow(follower, target)
		// Also add some reverse follows to make graph non-trivial
		if i > 0 {
			graph.AddFollow(follower, padHex(i-1))
		}
	}
	graph.ComputePageRank(20, 0.85)

	// Set metadata for an active, engaged account
	m := meta.Get(target)
	m.Followers = 50
	m.PostCount = 20
	m.ReplyCount = 15
	m.ReactionsSent = 30
	m.ReactionsRecd = 25
	m.ZapCntRecd = 5
	m.FirstCreated = time.Now().AddDate(-2, 0, 0).Unix() // 2 years old

	req := httptest.NewRequest("GET", "/spam?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp SpamResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Classification != "likely_human" {
		t.Fatalf("expected 'likely_human' for trusted pubkey, got %q (prob: %f)", resp.Classification, resp.SpamProbability)
	}
	if resp.SpamProbability >= 0.4 {
		t.Fatalf("expected spam_probability < 0.4 for trusted pubkey, got %f", resp.SpamProbability)
	}
}

func TestSpamSpammyPubkey(t *testing.T) {
	oldGraph := graph
	oldMeta := meta
	graph = NewGraph()
	meta = NewMetaStore()
	defer func() {
		graph = oldGraph
		meta = oldMeta
	}()

	target := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

	// Spammy account: follows many, has 0 followers, no engagement
	for i := 0; i < 100; i++ {
		graph.AddFollow(target, padHex(i))
	}
	// Add some other nodes so graph has structure
	for i := 0; i < 50; i++ {
		graph.AddFollow(padHex(i), padHex(i+1))
	}
	graph.ComputePageRank(20, 0.85)

	// Set metadata: lots of posts, no engagement, reports, new account
	m := meta.Get(target)
	m.PostCount = 50
	m.ReplyCount = 0
	m.ReactionsSent = 0
	m.ReactionsRecd = 0
	m.ZapCntRecd = 0
	m.ReportsRecd = 5
	m.FirstCreated = time.Now().Add(-24 * time.Hour).Unix() // 1 day old

	req := httptest.NewRequest("GET", "/spam?pubkey="+target, nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp SpamResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Classification != "likely_spam" {
		t.Fatalf("expected 'likely_spam' for spammy pubkey, got %q (prob: %f)", resp.Classification, resp.SpamProbability)
	}
	if resp.SpamProbability < 0.7 {
		t.Fatalf("expected spam_probability >= 0.7 for spammy pubkey, got %f", resp.SpamProbability)
	}
}

func TestSpamClassification(t *testing.T) {
	tests := []struct {
		prob     float64
		expected string
	}{
		{0.0, "likely_human"},
		{0.1, "likely_human"},
		{0.39, "likely_human"},
		{0.4, "suspicious"},
		{0.5, "suspicious"},
		{0.69, "suspicious"},
		{0.7, "likely_spam"},
		{0.9, "likely_spam"},
		{1.0, "likely_spam"},
	}
	for _, tt := range tests {
		got := classifySpam(tt.prob)
		if got != tt.expected {
			t.Errorf("classifySpam(%f) = %q, want %q", tt.prob, got, tt.expected)
		}
	}
}

func TestSpamSignalWoTNotFound(t *testing.T) {
	signal := spamSignalWoT(0, false, 0)
	if signal.Score != 0.30 {
		t.Fatalf("expected score 0.30 for not-found pubkey, got %f", signal.Score)
	}
}

func TestSpamSignalWoTHighScore(t *testing.T) {
	signal := spamSignalWoT(80, true, 0.95)
	if signal.Score > 0.05 {
		t.Fatalf("expected low spam score for high WoT, got %f", signal.Score)
	}
}

func TestSpamSignalFollowRatioSpammy(t *testing.T) {
	// 2 followers, 500 following — very spammy
	signal := spamSignalFollowRatio(2, 500)
	if signal.Score < 0.1 {
		t.Fatalf("expected high spam score for bad follow ratio, got %f", signal.Score)
	}
}

func TestSpamSignalFollowRatioHealthy(t *testing.T) {
	// 200 followers, 100 following — healthy
	signal := spamSignalFollowRatio(200, 100)
	if signal.Score != 0 {
		t.Fatalf("expected 0 spam score for healthy follow ratio, got %f", signal.Score)
	}
}

func TestSpamSignalAgeNew(t *testing.T) {
	// 2 days old
	ts := time.Now().Add(-48 * time.Hour).Unix()
	signal := spamSignalAge(ts)
	if signal.Score < 0.1 {
		t.Fatalf("expected high spam score for new account, got %f", signal.Score)
	}
}

func TestSpamSignalAgeOld(t *testing.T) {
	// 2 years old
	ts := time.Now().AddDate(-2, 0, 0).Unix()
	signal := spamSignalAge(ts)
	if signal.Score != 0 {
		t.Fatalf("expected 0 spam score for old account, got %f", signal.Score)
	}
}

func TestSpamSignalReportsNone(t *testing.T) {
	signal := spamSignalReports(0)
	if signal.Score != 0 {
		t.Fatalf("expected 0 spam score for no reports, got %f", signal.Score)
	}
}

func TestSpamSignalReportsMany(t *testing.T) {
	signal := spamSignalReports(10)
	if signal.Score != 0.15 {
		t.Fatalf("expected 0.15 spam score for many reports, got %f", signal.Score)
	}
}

func TestSpamResponseHas6Signals(t *testing.T) {
	oldGraph := graph
	graph = NewGraph()
	defer func() { graph = oldGraph }()

	pubkey := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	req := httptest.NewRequest("GET", "/spam?pubkey="+pubkey, nil)
	w := httptest.NewRecorder()
	handleSpam(w, req)

	var resp SpamResponse
	json.NewDecoder(w.Body).Decode(&resp)

	if len(resp.Signals) != 6 {
		t.Fatalf("expected 6 signals, got %d", len(resp.Signals))
	}

	// Verify all signal names are present
	names := map[string]bool{}
	for _, s := range resp.Signals {
		names[s.Name] = true
	}
	expected := []string{"wot_score", "follow_ratio", "account_age_days", "engagement_received", "reports_received", "activity_pattern"}
	for _, name := range expected {
		if !names[name] {
			t.Fatalf("missing signal: %s", name)
		}
	}
}

// padHex returns a 64-char hex string for test use.
func padHex(n int) string {
	s := ""
	for len(s) < 64 {
		s += "0"
	}
	hex := []byte(s)
	// Encode n into the last few chars
	digits := "0123456789abcdef"
	pos := 63
	val := n
	if val == 0 {
		hex[pos] = '1' // avoid all-zeros which might collide
	}
	for val > 0 && pos >= 0 {
		hex[pos] = digits[val%16]
		val /= 16
		pos--
	}
	return string(hex)
}
