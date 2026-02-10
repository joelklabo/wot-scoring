package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// L402Config holds L402 paywall configuration.
type L402Config struct {
	LNbitsURL          string   // LNbits base URL (e.g., https://lnbits.klabo.world)
	LNbitsFallbackURLs []string // Optional fallbacks used only on transient errors (429/5xx/network)
	LNbitsAPIKey       string   // LNbits invoice/read key
	FreeTier           int      // Free requests per IP per day (0 = all paid)
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

type lnbitsEndpoint struct {
	baseURL       string
	hostOverride  string // Optional HTTP Host header override (for IP fallbacks behind TLS)
	tlsServerName string // Optional TLS SNI ServerName override (for IP fallbacks behind TLS)
}

// NewL402Middleware creates a new L402 paywall middleware.
func NewL402Middleware(config L402Config) *L402Middleware {
	m := &L402Middleware{
		config: config,
		pricedEndpoints: map[string]int64{
			"/score":                1,
			"/audit":                5,
			"/batch":                10,
			"/personalized":         2,
			"/similar":              2,
			"/recommend":            2,
			"/compare":              2,
			"/decay":                1,
			"/nip05":                1,
			"/nip05/batch":          5,
			"/nip05/reverse":        2,
			"/timeline":             2,
			"/spam":                 2,
			"/spam/batch":           10,
			"/weboftrust":           3,
			"/blocked":              2,
			"/verify":               2,
			"/anomalies":            3,
			"/sybil":                3,
			"/sybil/batch":          10,
			"/trust-path":           5,
			"/reputation":           5,
			"/predict":              3,
			"/influence":            5,
			"/influence/batch":      10,
			"/network-health":       5,
			"/compare-providers":    5,
			"/trust-circle":         5,
			"/trust-circle/compare": 5,
			"/follow-quality":       5,
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
		if paymentHash == "" {
			// Interop: some L402 clients retry with `Authorization: L402 <payment_hash>`.
			// We still document/support `X-Payment-Hash` and `?payment_hash=` as the primary mechanisms.
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			if strings.HasPrefix(auth, "L402 ") {
				paymentHash = strings.TrimSpace(strings.TrimPrefix(auth, "L402 "))
			}
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
			"message":      fmt.Sprintf("Pay %d sats to access %s. Retry with X-Payment-Hash header (preferred), ?payment_hash= query param, or Authorization: L402 <payment_hash>.", price, r.URL.Path),
			"protocols": map[string]interface{}{
				"l402": map[string]interface{}{
					"price_sats":       price,
					"payment_request":  invoice,
					"payment_hash":     hash,
					"verify_header":    "X-Payment-Hash",
					"verify_query_arg": "payment_hash",
				},
			},
			"free_tier": m.config.FreeTier,
			"endpoint":  r.URL.Path,
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

func (m *L402Middleware) lnbitsEndpoints() []lnbitsEndpoint {
	primary := strings.TrimSpace(m.config.LNbitsURL)
	if primary == "" {
		return nil
	}

	primaryURL, err := url.Parse(primary)
	if err != nil || primaryURL.Hostname() == "" {
		return []lnbitsEndpoint{{baseURL: primary}}
	}

	primaryHost := primaryURL.Hostname()
	primaryHostIsIP := net.ParseIP(primaryHost) != nil

	out := make([]lnbitsEndpoint, 0, 1+len(m.config.LNbitsFallbackURLs))
	out = append(out, lnbitsEndpoint{baseURL: primary})

	for _, raw := range m.config.LNbitsFallbackURLs {
		raw = strings.TrimSpace(raw)
		if raw == "" || raw == primary {
			continue
		}

		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			out = append(out, lnbitsEndpoint{baseURL: raw})
			continue
		}

		fallbackHost := u.Hostname()
		fallbackHostIsIP := net.ParseIP(fallbackHost) != nil

		ep := lnbitsEndpoint{baseURL: raw}
		// If the fallback connects to an IP over HTTPS but the primary host is a DNS name,
		// force Host + SNI to the primary host so certificates still validate.
		if !primaryHostIsIP && fallbackHostIsIP && strings.EqualFold(u.Scheme, "https") {
			ep.hostOverride = primaryHost
			ep.tlsServerName = primaryHost
		}

		out = append(out, ep)
	}

	return out
}

func newHTTPClient(tlsServerName string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tlsServerName != "" {
		if tr.TLSClientConfig == nil {
			tr.TLSClientConfig = &tls.Config{}
		} else {
			tr.TLSClientConfig = tr.TLSClientConfig.Clone()
		}
		tr.TLSClientConfig.ServerName = tlsServerName
	}
	return &http.Client{Timeout: 10 * time.Second, Transport: tr}
}

func isTransientLNbitsStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// verifyPayment checks if a payment hash has been paid via LNbits.
func (m *L402Middleware) verifyPayment(paymentHash string) bool {
	m.mu.Lock()
	if m.paidHashes[paymentHash] {
		m.mu.Unlock()
		return false // Already used
	}
	m.mu.Unlock()

	// Check LNbits for payment status (with best-effort fallbacks on transient errors).
	for _, ep := range m.lnbitsEndpoints() {
		u := fmt.Sprintf("%s/api/v1/payments/%s", strings.TrimRight(ep.baseURL, "/"), paymentHash)
		client := newHTTPClient(ep.tlsServerName)

		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequest("GET", u, nil)
			if err != nil {
				break
			}
			if ep.hostOverride != "" {
				req.Host = ep.hostOverride
			}
			req.Header.Set("X-Api-Key", m.config.LNbitsAPIKey)

			resp, err := client.Do(req)
			if err != nil {
				if attempt == 0 {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				break
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Auth/config errors should not fall back.
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return false
			}

			if resp.StatusCode != http.StatusOK {
				if isTransientLNbitsStatus(resp.StatusCode) && attempt == 0 {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				break
			}

			var result map[string]interface{}
			if err := json.Unmarshal(body, &result); err != nil {
				break
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
	}

	return false
}

// createInvoice creates a Lightning invoice via LNbits.
func (m *L402Middleware) createInvoice(amountSats int64, memo string) (invoice string, paymentHash string, err error) {
	payloadBytes, err := json.Marshal(map[string]interface{}{
		"out":    false,
		"amount": amountSats,
		"memo":   memo,
	})
	if err != nil {
		return "", "", err
	}

	var lastErr error
	for _, ep := range m.lnbitsEndpoints() {
		u := fmt.Sprintf("%s/api/v1/payments", strings.TrimRight(ep.baseURL, "/"))
		client := newHTTPClient(ep.tlsServerName)

		for attempt := 0; attempt < 2; attempt++ {
			req, err := http.NewRequest("POST", u, bytes.NewReader(payloadBytes))
			if err != nil {
				lastErr = err
				break
			}
			if ep.hostOverride != "" {
				req.Host = ep.hostOverride
			}
			req.Header.Set("X-Api-Key", m.config.LNbitsAPIKey)
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			if err != nil {
				lastErr = err
				if attempt == 0 {
					time.Sleep(250 * time.Millisecond)
					continue
				}
				break
			}

			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			// Auth/config errors should not fall back.
			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				return "", "", fmt.Errorf("LNbits returned %d", resp.StatusCode)
			}

			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				if isTransientLNbitsStatus(resp.StatusCode) {
					lastErr = fmt.Errorf("LNbits returned %d", resp.StatusCode)
					if attempt == 0 {
						time.Sleep(250 * time.Millisecond)
						continue
					}
					// After a retry, fall back to the next configured base URL (if any).
					break
				}
				if len(body) > 0 {
					return "", "", fmt.Errorf("LNbits returned %d: %s", resp.StatusCode, string(body))
				}
				return "", "", fmt.Errorf("LNbits returned %d", resp.StatusCode)
			}

			var result map[string]interface{}
			if err := json.Unmarshal(body, &result); err != nil {
				lastErr = err
				break
			}

			inv, _ := result["payment_request"].(string)
			hash, _ := result["payment_hash"].(string)
			if inv == "" || hash == "" {
				lastErr = fmt.Errorf("missing invoice or hash in LNbits response")
				break
			}

			return inv, hash, nil
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("failed to create invoice")
	}
	return "", "", lastErr
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
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xrip := strings.TrimSpace(r.Header.Get("X-Real-IP")); xrip != "" {
		return xrip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

// L402Enabled returns true if L402 paywall is configured.
func L402Enabled() bool {
	return os.Getenv("LNBITS_URL") != "" && os.Getenv("LNBITS_KEY") != ""
}

// NewL402FromEnv creates an L402 middleware from environment variables.
func NewL402FromEnv() *L402Middleware {
	freeTier := 50 // Free requests per IP per day (increased for demo/presentation)
	return NewL402Middleware(L402Config{
		LNbitsURL:          os.Getenv("LNBITS_URL"),
		LNbitsFallbackURLs: splitCommaList(os.Getenv("LNBITS_FALLBACK_URLS")),
		LNbitsAPIKey:       os.Getenv("LNBITS_KEY"),
		FreeTier:           freeTier,
	})
}

func splitCommaList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
