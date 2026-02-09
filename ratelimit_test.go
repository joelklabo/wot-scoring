package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		remaining, allowed := rl.Allow("1.2.3.4")
		if !allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
		if remaining != 2-i {
			t.Fatalf("request %d: expected remaining=%d, got=%d", i+1, 2-i, remaining)
		}
	}

	// 4th request should be blocked
	remaining, allowed := rl.Allow("1.2.3.4")
	if allowed {
		t.Fatal("4th request should be blocked")
	}
	if remaining != 0 {
		t.Fatalf("expected remaining=0, got=%d", remaining)
	}

	// Different IP should still be allowed
	remaining, allowed = rl.Allow("5.6.7.8")
	if !allowed {
		t.Fatal("different IP should be allowed")
	}
	if remaining != 2 {
		t.Fatalf("expected remaining=2, got=%d", remaining)
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := NewRateLimiter(2, 50*time.Millisecond)

	rl.Allow("1.2.3.4")
	rl.Allow("1.2.3.4")
	_, allowed := rl.Allow("1.2.3.4")
	if allowed {
		t.Fatal("should be blocked after limit")
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	_, allowed = rl.Allow("1.2.3.4")
	if !allowed {
		t.Fatal("should be allowed after window reset")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	wrapped := RateLimitMiddleware(rl, handler)

	// Health endpoint should bypass rate limiting
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/health", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("health request %d should not be rate limited", i+1)
		}
	}

	// Root path should bypass rate limiting
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("root request %d should not be rate limited", i+1)
		}
	}

	// API endpoint should be rate limited (use fresh IP)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
		req.RemoteAddr = "9.9.9.9:1234"
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("API request %d should be allowed, got %d", i+1, rec.Code)
		}
		if rec.Header().Get("X-RateLimit-Limit") != "2" {
			t.Fatal("missing X-RateLimit-Limit header")
		}
	}

	// 3rd API request should be blocked
	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.RemoteAddr = "9.9.9.9:1234"
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}

	var body map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&body)
	if body["error"] != "rate limit exceeded" {
		t.Fatalf("expected rate limit error, got: %v", body)
	}
}

func TestRateLimitMiddlewareXForwardedFor(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := RateLimitMiddleware(rl, handler)

	// First request with X-Forwarded-For
	req := httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.RemoteAddr = "proxy:1234"
	req.Header.Set("X-Forwarded-For", "real-client-ip")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatal("first request should be allowed")
	}

	// Second request from same forwarded IP should be blocked
	req = httptest.NewRequest("GET", "/score?pubkey=abc", nil)
	req.RemoteAddr = "proxy:5678"
	req.Header.Set("X-Forwarded-For", "real-client-ip")
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
}
