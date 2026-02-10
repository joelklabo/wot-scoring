package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func dummyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})
}

func TestL402FreeTierAllowsRequests(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     3,
	})
	handler := m.Wrap(dummyHandler())

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/score?pubkey=abc123", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i+1, w.Code)
		}
	}
}

func TestL402FreeTierExhaustedReturns402(t *testing.T) {
	// Mock LNbits for invoice creation
	mockLNbits := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"payment_request": "lnbc10n1ptest",
			"payment_hash":    "testhash",
		})
	}))
	defer mockLNbits.Close()

	m := NewL402Middleware(L402Config{
		LNbitsURL:    mockLNbits.URL,
		LNbitsAPIKey: "test-key",
		FreeTier:     2,
	})
	handler := m.Wrap(dummyHandler())

	// Use up free tier
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/score?pubkey=abc123", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("free request %d: expected 200, got %d", i+1, w.Code)
		}
	}

	// Third request should get 402
	req := httptest.NewRequest("GET", "/score?pubkey=abc123", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected 402, got %d", w.Code)
	}

	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "payment_required" {
		t.Errorf("expected status 'payment_required', got %v", body["status"])
	}
	if body["invoice"] != "lnbc10n1ptest" {
		t.Errorf("expected invoice 'lnbc10n1ptest', got %v", body["invoice"])
	}
	protocols, ok := body["protocols"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected protocols object in 402 response")
	}
	l402, ok := protocols["l402"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected protocols.l402 object in 402 response")
	}
	if l402["payment_hash"] != "testhash" {
		t.Errorf("expected protocols.l402.payment_hash 'testhash', got %v", l402["payment_hash"])
	}
	if l402["payment_request"] != "lnbc10n1ptest" {
		t.Errorf("expected protocols.l402.payment_request 'lnbc10n1ptest', got %v", l402["payment_request"])
	}
}

func TestL402UnpricedEndpointsPassThrough(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     0, // No free tier
	})
	handler := m.Wrap(dummyHandler())

	// Unpriced endpoints should pass through
	endpoints := []string{"/health", "/top", "/stats", "/export", "/providers"}
	for _, ep := range endpoints {
		req := httptest.NewRequest("GET", ep, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", ep, w.Code)
		}
	}
}

func TestL402DifferentIPsGetOwnFreeTier(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     1,
	})
	handler := m.Wrap(dummyHandler())

	// First IP: one free request
	req1 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req1.RemoteAddr = "1.1.1.1:1234"
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("IP1 first request: expected 200, got %d", w1.Code)
	}

	// Second IP: also gets one free request
	req2 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req2.RemoteAddr = "2.2.2.2:1234"
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("IP2 first request: expected 200, got %d", w2.Code)
	}
}

func TestL402XForwardedForUsed(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     1,
	})
	handler := m.Wrap(dummyHandler())

	// First request with XFF
	req1 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req1.RemoteAddr = "proxy:1234"
	req1.Header.Set("X-Forwarded-For", "10.0.0.1")
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", w1.Code)
	}

	// Second request from same XFF IP — should fail (free tier = 1)
	req2 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req2.RemoteAddr = "proxy:1234"
	req2.Header.Set("X-Forwarded-For", "10.0.0.1")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	// Will be 402 or 500 (LNbits not running)
	if w2.Code == http.StatusOK {
		t.Errorf("second request should not be free, got 200")
	}
}

func TestL402PaymentHashQueryParam(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})
	handler := m.Wrap(dummyHandler())

	// Request with payment_hash query param (LNbits not running, will fail verification)
	req := httptest.NewRequest("GET", "/score?pubkey=abc&payment_hash=deadbeef", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	// Should get 401 because LNbits verification fails
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unverified payment, got %d", w.Code)
	}
}

func TestL402PaymentHashHeader(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})
	handler := m.Wrap(dummyHandler())

	// Request with X-Payment-Hash header (LNbits not running, will fail verification)
	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.Header.Set("X-Payment-Hash", "deadbeef")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unverified payment, got %d", w.Code)
	}
}

func TestL402PaymentHashAuthorizationHeader(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})
	handler := m.Wrap(dummyHandler())

	// Request with Authorization header (LNbits not running, will fail verification)
	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.Header.Set("Authorization", "L402 deadbeef")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unverified payment, got %d", w.Code)
	}
}

func TestL402VerifyPaymentWithMockLNbits(t *testing.T) {
	// Start a mock LNbits server
	mockLNbits := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/payments/valid-hash" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"paid": true})
			return
		}
		if r.URL.Path == "/api/v1/payments/unpaid-hash" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"paid": false})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLNbits.Close()

	m := NewL402Middleware(L402Config{
		LNbitsURL:    mockLNbits.URL,
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})
	handler := m.Wrap(dummyHandler())

	// Valid payment
	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.Header.Set("X-Payment-Hash", "valid-hash")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("valid payment: expected 200, got %d", w.Code)
	}

	// Same payment hash again — should be rejected (already used)
	req2 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req2.Header.Set("X-Payment-Hash", "valid-hash")
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("reused payment: expected 401, got %d", w2.Code)
	}

	// Unpaid invoice
	req3 := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req3.Header.Set("X-Payment-Hash", "unpaid-hash")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("unpaid invoice: expected 401, got %d", w3.Code)
	}
}

func TestL402InvoiceCreationWithMockLNbits(t *testing.T) {
	// Start a mock LNbits server
	mockLNbits := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/v1/payments" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"payment_request": "lnbc10n1ptest",
				"payment_hash":    "hash123",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mockLNbits.Close()

	m := NewL402Middleware(L402Config{
		LNbitsURL:    mockLNbits.URL,
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})
	handler := m.Wrap(dummyHandler())

	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)

	if body["invoice"] != "lnbc10n1ptest" {
		t.Errorf("expected invoice 'lnbc10n1ptest', got %v", body["invoice"])
	}
	if body["payment_hash"] != "hash123" {
		t.Errorf("expected payment_hash 'hash123', got %v", body["payment_hash"])
	}
	if body["amount_sats"] != float64(1) {
		t.Errorf("expected amount_sats 1, got %v", body["amount_sats"])
	}

	// Check WWW-Authenticate header
	authHeader := w.Header().Get("WWW-Authenticate")
	if authHeader == "" {
		t.Error("expected WWW-Authenticate header")
	}
}

func TestL402PricedEndpoints(t *testing.T) {
	m := NewL402Middleware(L402Config{
		LNbitsURL:    "http://localhost:5000",
		LNbitsAPIKey: "test-key",
		FreeTier:     0,
	})

	// Verify pricing
	expected := map[string]int64{
		"/score":        1,
		"/audit":        5,
		"/batch":        10,
		"/personalized": 2,
		"/similar":      2,
		"/recommend":    2,
		"/compare":      2,
		"/decay":        1,
	}

	for path, price := range expected {
		if m.pricedEndpoints[path] != price {
			t.Errorf("%s: expected %d sats, got %d", path, price, m.pricedEndpoints[path])
		}
	}
}

func TestClientIP(t *testing.T) {
	// No XFF
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	if ip := clientIP(req); ip != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip)
	}

	// With XFF
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "proxy:1234"
	req2.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	if ip := clientIP(req2); ip != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", ip)
	}

	// With XFF, trim spaces
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "proxy:1234"
	req3.Header.Set("X-Forwarded-For", " 10.0.0.9  , 10.0.0.2")
	if ip := clientIP(req3); ip != "10.0.0.9" {
		t.Errorf("expected 10.0.0.9, got %s", ip)
	}

	// X-Real-IP
	req4 := httptest.NewRequest("GET", "/", nil)
	req4.RemoteAddr = "proxy:1234"
	req4.Header.Set("X-Real-IP", "10.1.2.3")
	if ip := clientIP(req4); ip != "10.1.2.3" {
		t.Errorf("expected 10.1.2.3, got %s", ip)
	}
}

func TestL402InvoiceCreationFallsBackOn503(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/v1/payments" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"payment_request": "lnbc10n1pfallback",
				"payment_hash":    "hash-fallback",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fallback.Close()

	m := NewL402Middleware(L402Config{
		LNbitsURL:          primary.URL,
		LNbitsFallbackURLs: []string{fallback.URL},
		LNbitsAPIKey:       "test-key",
		FreeTier:           0,
	})
	handler := m.Wrap(dummyHandler())

	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("expected 402, got %d", w.Code)
	}

	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)
	if body["invoice"] != "lnbc10n1pfallback" {
		t.Fatalf("expected fallback invoice, got %v", body["invoice"])
	}
	if body["payment_hash"] != "hash-fallback" {
		t.Fatalf("expected fallback payment_hash, got %v", body["payment_hash"])
	}
}

func TestL402VerifyPaymentFallsBackOn503(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/payments/valid-hash" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"paid": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer fallback.Close()

	m := NewL402Middleware(L402Config{
		LNbitsURL:          primary.URL,
		LNbitsFallbackURLs: []string{fallback.URL},
		LNbitsAPIKey:       "test-key",
		FreeTier:           0,
	})
	handler := m.Wrap(dummyHandler())

	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.Header.Set("X-Payment-Hash", "valid-hash")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}
