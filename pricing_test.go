package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestHandlePricing_L402Enabled(t *testing.T) {
	mw := NewL402Middleware(L402Config{FreeTier: 50})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pricing", nil)
	handlePricing(rr, req, mw)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp PricingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if !resp.L402Enabled {
		t.Fatalf("expected l402_enabled=true")
	}
	if resp.FreeTierPerIPPerDay != 50 {
		t.Fatalf("expected free tier 50, got %d", resp.FreeTierPerIPPerDay)
	}
	if len(resp.PricedEndpoints) == 0 {
		t.Fatalf("expected priced_endpoints to be non-empty")
	}

	var foundScore, foundBatch bool
	for _, ep := range resp.PricedEndpoints {
		if ep.Path == "/score" && ep.PriceSats == 1 {
			foundScore = true
		}
		if ep.Path == "/batch" && ep.PriceSats == 10 {
			foundBatch = true
		}
	}
	if !foundScore {
		t.Fatalf("expected /score priced at 1 sat")
	}
	if !foundBatch {
		t.Fatalf("expected /batch priced at 10 sats")
	}
}

func TestHandlePricing_L402Disabled(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pricing", nil)
	handlePricing(rr, req, nil)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp PricingResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if resp.L402Enabled {
		t.Fatalf("expected l402_enabled=false")
	}
	if resp.FreeTierPerIPPerDay != 0 {
		t.Fatalf("expected free tier omitted/0, got %d", resp.FreeTierPerIPPerDay)
	}
	if len(resp.PricedEndpoints) != 0 {
		t.Fatalf("expected no priced_endpoints when disabled")
	}
}

