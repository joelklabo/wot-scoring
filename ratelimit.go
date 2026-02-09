package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// RateLimiter provides per-IP request rate limiting using a sliding window.
type RateLimiter struct {
	mu       sync.Mutex
	windows  map[string]*window
	limit    int
	interval time.Duration
}

type window struct {
	count   int
	resetAt time.Time
}

// NewRateLimiter creates a rate limiter allowing limit requests per interval per IP.
func NewRateLimiter(limit int, interval time.Duration) *RateLimiter {
	rl := &RateLimiter{
		windows:  make(map[string]*window),
		limit:    limit,
		interval: interval,
	}
	// Cleanup expired entries every minute
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			rl.cleanup()
		}
	}()
	return rl
}

// Allow checks if a request from ip is allowed. Returns remaining requests and whether allowed.
func (rl *RateLimiter) Allow(ip string) (remaining int, allowed bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	w, ok := rl.windows[ip]
	if !ok || now.After(w.resetAt) {
		rl.windows[ip] = &window{count: 1, resetAt: now.Add(rl.interval)}
		return rl.limit - 1, true
	}

	if w.count >= rl.limit {
		return 0, false
	}

	w.count++
	return rl.limit - w.count, true
}

// ResetTime returns when the current window resets for the given IP.
func (rl *RateLimiter) ResetTime(ip string) time.Time {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if w, ok := rl.windows[ip]; ok {
		return w.resetAt
	}
	return time.Now()
}

func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	for ip, w := range rl.windows {
		if now.After(w.resetAt) {
			delete(rl.windows, ip)
		}
	}
}

// RateLimitMiddleware wraps an http.Handler with rate limiting.
// Skips rate limiting for the root path and /health.
func RateLimitMiddleware(limiter *RateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for landing page and health check
		if r.URL.Path == "/" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}

		ip := r.RemoteAddr
		// Use X-Forwarded-For if behind a reverse proxy
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			ip = xff
		}

		remaining, allowed := limiter.Allow(ip)
		resetAt := limiter.ResetTime(ip)

		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limiter.limit))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetAt.Unix()))

		if !allowed {
			retryAfter := int(time.Until(resetAt).Seconds()) + 1
			w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":       "rate limit exceeded",
				"retry_after": retryAfter,
				"limit":       limiter.limit,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
