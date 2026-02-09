package main

import (
	"encoding/json"
	"math"
	"net/http"
	"strconv"

	"github.com/nbd-wtf/go-nostr"
)

// VerifyCheck represents a single verification check on a NIP-85 assertion.
type VerifyCheck struct {
	Field    string      `json:"field"`
	Claimed  interface{} `json:"claimed"`
	Observed interface{} `json:"observed"`
	Status   string      `json:"status"` // "match", "close", "divergent", "unverifiable"
}

// VerifyResponse is the response for the /verify endpoint.
type VerifyResponse struct {
	Valid          bool          `json:"valid"`            // signature and ID are cryptographically valid
	Verdict        string        `json:"verdict"`          // "consistent", "divergent", "unverifiable", "invalid"
	Kind           int           `json:"kind"`             // event kind
	ProviderPubkey string        `json:"provider_pubkey"`  // who signed the assertion
	SubjectPubkey  string        `json:"subject_pubkey"`   // who the assertion is about
	Checks         []VerifyCheck `json:"checks"`           // individual field comparisons
	MatchCount     int           `json:"match_count"`      // how many checks matched
	TotalChecks    int           `json:"total_checks"`     // total checks performed
	GraphSize      int           `json:"graph_size"`       // our current graph size for context
}

// handleVerify accepts a NIP-85 kind 30382 event (JSON) via POST body and verifies it
// against our own graph data. It checks:
//   - Cryptographic validity (signature + event ID)
//   - Claimed rank vs our computed score
//   - Claimed followers vs our observed followers
//
// POST /verify with JSON body containing a Nostr event.
func handleVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST required"}`, http.StatusMethodNotAllowed)
		return
	}

	var ev nostr.Event
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, `{"error":"invalid JSON: `+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	if ev.Kind != 30382 {
		http.Error(w, `{"error":"only kind 30382 (NIP-85 user assertions) supported"}`, http.StatusBadRequest)
		return
	}

	stats := graph.Stats()

	resp := VerifyResponse{
		Kind:           ev.Kind,
		ProviderPubkey: ev.PubKey,
		GraphSize:      stats.Nodes,
	}

	// Check cryptographic validity
	if !ev.CheckID() {
		resp.Valid = false
		resp.Verdict = "invalid"
		resp.Checks = append(resp.Checks, VerifyCheck{
			Field:  "event_id",
			Status: "divergent",
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	sigOK, sigErr := ev.CheckSignature()
	if sigErr != nil || !sigOK {
		resp.Valid = false
		resp.Verdict = "invalid"
		resp.Checks = append(resp.Checks, VerifyCheck{
			Field:  "signature",
			Status: "divergent",
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp.Valid = true

	// Extract subject pubkey from d tag
	var subjectPubkey string
	var claimedRank, claimedFollowers int
	hasRank, hasFollowers := false, false

	for _, tag := range ev.Tags {
		if len(tag) < 2 {
			continue
		}
		switch tag[0] {
		case "d":
			subjectPubkey = tag[1]
		case "rank":
			if v, err := strconv.Atoi(tag[1]); err == nil {
				claimedRank = v
				hasRank = true
			}
		case "followers":
			if v, err := strconv.Atoi(tag[1]); err == nil {
				claimedFollowers = v
				hasFollowers = true
			}
		}
	}

	if subjectPubkey == "" {
		resp.Verdict = "invalid"
		resp.Checks = append(resp.Checks, VerifyCheck{
			Field:  "d_tag",
			Status: "divergent",
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	resp.SubjectPubkey = subjectPubkey

	// Get our own data for the subject
	followers := graph.GetFollowers(subjectPubkey)
	ourFollowerCount := len(followers)
	rawScore, _ := graph.GetScore(subjectPubkey)
	ourScore := normalizeScore(rawScore, stats.Nodes)

	// Check rank
	if hasRank {
		check := verifyNumericField("rank", claimedRank, ourScore, 15)
		resp.Checks = append(resp.Checks, check)
	}

	// Check followers
	if hasFollowers {
		check := verifyNumericField("followers", claimedFollowers, ourFollowerCount, 20)
		resp.Checks = append(resp.Checks, check)
	}

	if !hasRank && !hasFollowers {
		resp.Verdict = "unverifiable"
		resp.Checks = append(resp.Checks, VerifyCheck{
			Field:  "claims",
			Status: "unverifiable",
		})
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Tally results
	matchCount := 0
	divergentCount := 0
	for _, c := range resp.Checks {
		switch c.Status {
		case "match", "close":
			matchCount++
		case "divergent":
			divergentCount++
		}
	}
	resp.MatchCount = matchCount
	resp.TotalChecks = len(resp.Checks)

	if divergentCount > 0 {
		resp.Verdict = "divergent"
	} else {
		resp.Verdict = "consistent"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// verifyNumericField compares a claimed value against an observed value.
// tolerancePercent defines how much deviation counts as "close" vs "divergent".
func verifyNumericField(field string, claimed, observed, tolerancePercent int) VerifyCheck {
	check := VerifyCheck{
		Field:    field,
		Claimed:  claimed,
		Observed: observed,
	}

	if claimed == observed {
		check.Status = "match"
		return check
	}

	// Calculate percentage difference relative to max of the two values
	maxVal := math.Max(float64(claimed), float64(observed))
	if maxVal == 0 {
		// Both near zero — treat as match
		check.Status = "match"
		return check
	}

	diff := math.Abs(float64(claimed) - float64(observed))
	pctDiff := (diff / maxVal) * 100

	if pctDiff <= float64(tolerancePercent) {
		check.Status = "close"
	} else {
		check.Status = "divergent"
	}

	return check
}
