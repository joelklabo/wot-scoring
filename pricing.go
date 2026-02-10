package main

import (
	"encoding/json"
	"net/http"
	"sort"
)

type PricingEndpoint struct {
	Path      string `json:"path"`
	PriceSats int64  `json:"price_sats"`
}

type PricingPaymentHints struct {
	HeaderName     string `json:"header_name"`
	QueryParamName string `json:"query_param_name"`
	StatusCode     int    `json:"status_code"`
}

type PricingResponse struct {
	L402Enabled          bool                `json:"l402_enabled"`
	FreeTierPerIPPerDay  int                 `json:"free_tier_per_ip_per_day,omitempty"`
	PricedEndpoints      []PricingEndpoint   `json:"priced_endpoints,omitempty"`
	PaymentHints         PricingPaymentHints `json:"payment_hints"`
	RateLimitPerIPPerMin int                 `json:"rate_limit_per_ip_per_min"`
}

func handlePricing(w http.ResponseWriter, r *http.Request, l402 *L402Middleware) {
	resp := PricingResponse{
		L402Enabled: l402 != nil,
		PaymentHints: PricingPaymentHints{
			HeaderName:     "X-Payment-Hash",
			QueryParamName: "payment_hash",
			StatusCode:     http.StatusPaymentRequired,
		},
		RateLimitPerIPPerMin: 100, // See main.go: NewRateLimiter(100, time.Minute)
	}

	if l402 != nil {
		resp.FreeTierPerIPPerDay = l402.config.FreeTier
		resp.PricedEndpoints = pricedEndpointsSorted(l402.pricedEndpoints)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_ = json.NewEncoder(w).Encode(resp)
}

func pricedEndpointsSorted(m map[string]int64) []PricingEndpoint {
	out := make([]PricingEndpoint, 0, len(m))
	for p, s := range m {
		out = append(out, PricingEndpoint{Path: p, PriceSats: s})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

