package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDemo_ReturnsHTML(t *testing.T) {
	req := httptest.NewRequest("GET", "/demo", nil)
	rr := httptest.NewRecorder()
	handleDemo(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	ct := rr.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("expected text/html content type, got %s", ct)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "WoT Explorer") {
		t.Error("expected page to contain 'WoT Explorer' title")
	}
	if !strings.Contains(body, "NIP-85") {
		t.Error("expected page to reference NIP-85")
	}
	if !strings.Contains(body, "pubkeyInput") {
		t.Error("expected page to contain pubkey input field")
	}
	if !strings.Contains(body, "/score?pubkey=") {
		t.Error("expected page to call /score endpoint")
	}
	if !strings.Contains(body, "/sybil?pubkey=") {
		t.Error("expected page to call /sybil endpoint")
	}
	if !strings.Contains(body, "/reputation?pubkey=") {
		t.Error("expected page to call /reputation endpoint")
	}
	if !strings.Contains(body, "/trust-circle?pubkey=") {
		t.Error("expected page to call /trust-circle endpoint")
	}
	if !strings.Contains(body, "/influence/batch") {
		t.Error("expected page to call /influence/batch endpoint")
	}
}

func TestDemo_ContainsVisualizationComponents(t *testing.T) {
	req := httptest.NewRequest("GET", "/demo", nil)
	rr := httptest.NewRecorder()
	handleDemo(rr, req)

	body := rr.Body.String()

	// Check for gauge (trust score visual)
	if !strings.Contains(body, "gaugeCircle") {
		t.Error("expected trust score gauge SVG element")
	}

	// Check for reputation bars
	if !strings.Contains(body, "rep-bar") {
		t.Error("expected reputation bar elements")
	}

	// Check for sybil section
	if !strings.Contains(body, "sybilContent") {
		t.Error("expected sybil content section")
	}

	// Check for trust circle member list
	if !strings.Contains(body, "member-list") {
		t.Error("expected trust circle member list")
	}

	// Check for role badges
	if !strings.Contains(body, "role-hub") {
		t.Error("expected role badge CSS classes")
	}
}

func TestDemo_HasResponsiveLayout(t *testing.T) {
	req := httptest.NewRequest("GET", "/demo", nil)
	rr := httptest.NewRecorder()
	handleDemo(rr, req)

	body := rr.Body.String()

	if !strings.Contains(body, "max-width: 700px") {
		t.Error("expected responsive breakpoint for mobile")
	}
	if !strings.Contains(body, "grid-template-columns") {
		t.Error("expected grid layout for dashboard")
	}
}
