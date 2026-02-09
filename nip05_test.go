package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveNIP05_InvalidFormat(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"no at sign", "noatsign"},
		{"empty name", "@domain.com"},
		{"empty domain", "user@"},
		{"empty string", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolveNIP05(tt.input)
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tt.input)
			}
		})
	}
}

func TestHandleNIP05_MissingParam(t *testing.T) {
	req := httptest.NewRequest("GET", "/nip05", nil)
	w := httptest.NewRecorder()

	handleNIP05(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleNIP05_InvalidIdentifier(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"no at sign", "noatsign"},
		{"empty name", "@domain.com"},
		{"empty domain", "user@"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/nip05?id="+tt.id, nil)
			w := httptest.NewRecorder()

			handleNIP05(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestNIP05Response_Unmarshal(t *testing.T) {
	data := `{"names":{"alice":"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"},"relays":{"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d":["wss://relay.example.com"]}}`

	var resp NIP05Response
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp.Names["alice"] != "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d" {
		t.Errorf("unexpected pubkey: %s", resp.Names["alice"])
	}

	relays := resp.Relays["3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"]
	if len(relays) != 1 || relays[0] != "wss://relay.example.com" {
		t.Errorf("unexpected relays: %v", relays)
	}
}

func TestNIP05Response_NoRelays(t *testing.T) {
	data := `{"names":{"bob":"7fa56f5d6962ab1e3cd424e758c3002b8665f7b0d8dcee9fe9e288d7751ac194"}}`

	var resp NIP05Response
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if resp.Names["bob"] != "7fa56f5d6962ab1e3cd424e758c3002b8665f7b0d8dcee9fe9e288d7751ac194" {
		t.Errorf("unexpected pubkey: %s", resp.Names["bob"])
	}

	if resp.Relays != nil {
		t.Error("expected nil relays")
	}
}

func TestNIP05Response_EmptyNames(t *testing.T) {
	data := `{"names":{}}`

	var resp NIP05Response
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(resp.Names) != 0 {
		t.Errorf("expected empty names, got %d", len(resp.Names))
	}
}

func TestNIP05Response_MultipleNames(t *testing.T) {
	data := `{"names":{"alice":"3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d","bob":"7fa56f5d6962ab1e3cd424e758c3002b8665f7b0d8dcee9fe9e288d7751ac194"}}`

	var resp NIP05Response
	err := json.Unmarshal([]byte(data), &resp)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if len(resp.Names) != 2 {
		t.Errorf("expected 2 names, got %d", len(resp.Names))
	}
}

func TestHandleNIP05Batch_WrongMethod(t *testing.T) {
	req := httptest.NewRequest("GET", "/nip05/batch", nil)
	w := httptest.NewRecorder()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleNIP05Batch_EmptyBody(t *testing.T) {
	req := httptest.NewRequest("POST", "/nip05/batch", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNIP05Batch_EmptyIdentifiers(t *testing.T) {
	req := httptest.NewRequest("POST", "/nip05/batch", strings.NewReader(`{"identifiers":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNIP05Batch_TooMany(t *testing.T) {
	ids := make([]string, 51)
	for i := range ids {
		ids[i] = "test@example.com"
	}
	body, _ := json.Marshal(map[string][]string{"identifiers": ids})
	req := httptest.NewRequest("POST", "/nip05/batch", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNIP05Batch_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/nip05/batch", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleNIP05Batch_InvalidIdentifiers(t *testing.T) {
	body := `{"identifiers":["invalid", "also-invalid"]}`
	req := httptest.NewRequest("POST", "/nip05/batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Need graph initialized for this test
	graph = NewGraph()
	meta = NewMetaStore()

	handleNIP05Batch(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 (with per-item errors), got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	results, ok := resp["results"].([]interface{})
	if !ok {
		t.Fatal("expected results array")
	}
	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	// Each result should have an error since they're invalid NIP-05 identifiers
	for i, r := range results {
		entry := r.(map[string]interface{})
		if entry["error"] == nil {
			t.Errorf("result %d: expected error field", i)
		}
		if entry["verified"] != false {
			t.Errorf("result %d: expected verified=false", i)
		}
	}
}

func TestNIP05TrustLevel(t *testing.T) {
	tests := []struct {
		score    int
		found    bool
		expected string
	}{
		{90, true, "highly_trusted"},
		{80, true, "highly_trusted"},
		{50, true, "trusted"},
		{20, true, "moderate"},
		{1, true, "low"},
		{0, true, "untrusted"},
		{50, false, "unknown"},
	}

	for _, tt := range tests {
		result := nip05TrustLevel(tt.score, tt.found)
		if result != tt.expected {
			t.Errorf("nip05TrustLevel(%v, %v) = %q, want %q", tt.score, tt.found, result, tt.expected)
		}
	}
}
