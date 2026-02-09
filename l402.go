package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// L402Config holds L402 paywall configuration.
type L402Config struct {
	LNbitsURL    string // LNbits base URL (e.g., https://lnbits.klabo.world)
	LNbitsAPIKey string // LNbits invoice/read key
	FreeTier     int    // Free requests per IP per day (0 = all paid)
}

// L402Middleware implements an L402 paywall with a free tier.
// Endpoints not in pricedEndpoints pass through freely.
type L402Middleware struct {
	config          L402Config
	pricedEndpoints map[string]int64 // path -> price in sats
	mu              sync.Mutex
	freeUsage       map[string]*dailyUsage // IP -> usage
	paidHashes      map[string]bool        // payment_hash -> already used
}

type dailyUsage struct {
	count   int
	resetAt time.Time
}

// NewL402Middleware creates a new L402 paywall middleware.
func NewL402Middleware(config L402Config) *L402Middleware {
	m := &L402Middleware{
		config: config,
		pricedEndpoints: map[string]int64{
			"/score":        1,
			"/audit":        5,
			"/batch":        10,
			"/personalized": 2,
			"/similar":      2,
			"/recommend":    2,
			"/compare":      2,
			"/decay":        1,
			"/nip05":         1,
			"/nip05/batch":   5,
			"/nip05/reverse": 2,
			"/timeline":      2,
			"/spam":          2,
			"/spam/batch":    10,
			"/weboftrust":    3,
		},
		freeUsage:  make(map[string]*dailyUsage),
		paidHashes: make(map[string]bool),
	}
	// Cleanup expired free-tier entries every hour
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			m.cleanupFreeUsage()
		}
	}()
	return m
}

// Wrap wraps an http.Handler with L402 paywall logic.
func (m *L402Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		price, isPriced := m.pricedEndpoints[r.URL.Path]
		if !isPriced {
			next.ServeHTTP(w, r)
			return
		}

		// Check if request includes a valid payment proof
		paymentHash := r.Header.Get("X-Payment-Hash")
		if paymentHash == "" {
			paymentHash = r.URL.Query().Get("payment_hash")
		}

		if paymentHash != "" {
			if m.verifyPayment(paymentHash) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error":   "invalid or expired payment",
				"message": "Payment hash not found or already used. Request a new invoice.",
			})
			return
		}

		// Check free tier
		if m.config.FreeTier > 0 {
			ip := clientIP(r)
			if m.consumeFreeTier(ip) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Free tier exhausted or disabled — require payment
		invoice, hash, err := m.createInvoice(price, fmt.Sprintf("WoT %s query", r.URL.Path))
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "failed to create invoice",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`L402 invoice="%s", macaroon="none"`, invoice))
		w.WriteHeader(http.StatusPaymentRequired)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":       "payment_required",
			"payment_hash": hash,
			"invoice":      invoice,
			"amount_sats":  price,
			"message":      fmt.Sprintf("Pay %d sats to access %s. Retry with X-Payment-Hash header or ?payment_hash= query param.", price, r.URL.Path),
			"free_tier":    m.config.FreeTier,
			"endpoint":     r.URL.Path,
		})
	})
}

// consumeFreeTier checks if the IP has free requests remaining and decrements.
func (m *L402Middleware) consumeFreeTier(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	usage, ok := m.freeUsage[ip]
	if !ok || now.After(usage.resetAt) {
		m.freeUsage[ip] = &dailyUsage{
			count:   1,
			resetAt: now.Truncate(24 * time.Hour).Add(24 * time.Hour),
		}
		return true
	}

	if usage.count >= m.config.FreeTier {
		return false
	}

	usage.count++
	return true
}

// verifyPayment checks if a payment hash has been paid via LNbits.
func (m *L402Middleware) verifyPayment(paymentHash string) bool {
	m.mu.Lock()
	if m.paidHashes[paymentHash] {
		m.mu.Unlock()
		return false // Already used
	}
	m.mu.Unlock()

	// Check LNbits for payment status
	url := fmt.Sprintf("%s/api/v1/payments/%s", m.config.LNbitsURL, paymentHash)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Api-Key", m.config.LNbitsAPIKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return false
	}

	paid, ok := result["paid"].(bool)
	if !ok || !paid {
		return false
	}

	// Mark as used
	m.mu.Lock()
	m.paidHashes[paymentHash] = true
	m.mu.Unlock()

	return true
}

// createInvoice creates a Lightning invoice via LNbits.
func (m *L402Middleware) createInvoice(amountSats int64, memo string) (invoice string, paymentHash string, err error) {
	url := fmt.Sprintf("%s/api/v1/payments", m.config.LNbitsURL)

	payload := fmt.Sprintf(`{"out":false,"amount":%d,"memo":"%s"}`, amountSats, memo)
	req, err := http.NewRequest("POST", url, strings.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("X-Api-Key", m.config.LNbitsAPIKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("LNbits returned %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}

	inv, _ := result["payment_request"].(string)
	hash, _ := result["payment_hash"].(string)
	if inv == "" || hash == "" {
		return "", "", fmt.Errorf("missing invoice or hash in LNbits response")
	}

	return inv, hash, nil
}

func (m *L402Middleware) cleanupFreeUsage() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for ip, usage := range m.freeUsage {
		if now.After(usage.resetAt) {
			delete(m.freeUsage, ip)
		}
	}
}

// clientIP extracts the client IP from the request.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.Split(xff, ",")[0]
	}
	return r.RemoteAddr
}

// L402Enabled returns true if L402 paywall is configured.
func L402Enabled() bool {
	return os.Getenv("LNBITS_URL") != "" && os.Getenv("LNBITS_KEY") != ""
}

// NewL402FromEnv creates an L402 middleware from environment variables.
func NewL402FromEnv() *L402Middleware {
	freeTier := 10 // Default: 10 free requests per IP per day
	return NewL402Middleware(L402Config{
		LNbitsURL:    os.Getenv("LNBITS_URL"),
		LNbitsAPIKey: os.Getenv("LNBITS_KEY"),
		FreeTier:     freeTier,
	})
}
