package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIReturnsValidJSON(t *testing.T) {
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	rr := httptest.NewRecorder()
	handleOpenAPI(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Verify OpenAPI version
	if v, ok := spec["openapi"].(string); !ok || v != "3.0.3" {
		t.Fatalf("expected openapi 3.0.3, got %v", spec["openapi"])
	}
}

func TestOpenAPIContainsAllEndpoints(t *testing.T) {
	endpoints := []string{
		"/score", "/audit", "/batch", "/personalized", "/similar",
		"/recommend", "/compare", "/graph", "/weboftrust",
		"/nip05", "/nip05/batch", "/nip05/reverse",
		"/timeline", "/decay", "/decay/top",
		"/spam", "/spam/batch", "/blocked", "/verify", "/anomalies",
		"/sybil", "/sybil/batch", "/trust-path", "/reputation", "/predict", "/influence",
		"/metadata", "/event", "/external",
		"/top", "/export", "/relay", "/authorized", "/communities",
		"/publish", "/providers", "/health", "/docs", "/swagger", "/openapi.json",
	}

	var spec map[string]interface{}
	if err := json.Unmarshal([]byte(openAPISpec), &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	paths, ok := spec["paths"].(map[string]interface{})
	if !ok {
		t.Fatal("no paths in spec")
	}

	for _, ep := range endpoints {
		if _, exists := paths[ep]; !exists {
			t.Errorf("OpenAPI spec missing endpoint: %s", ep)
		}
	}
}

func TestOpenAPIHasCORSHeader(t *testing.T) {
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	rr := httptest.NewRecorder()
	handleOpenAPI(rr, req)

	cors := rr.Header().Get("Access-Control-Allow-Origin")
	if cors != "*" {
		t.Fatalf("expected CORS *, got %s", cors)
	}
}

func TestOpenAPIHasL402SecurityScheme(t *testing.T) {
	body := openAPISpec
	if !strings.Contains(body, "L402") {
		t.Error("OpenAPI spec missing L402 security scheme")
	}
	if !strings.Contains(body, "X-Payment-Hash") {
		t.Error("OpenAPI spec missing X-Payment-Hash header reference")
	}
}

func TestSwaggerPageServesHTML(t *testing.T) {
	body := swaggerPageHTML
	if !strings.Contains(body, "swagger-ui") {
		t.Error("Swagger page should contain swagger-ui div")
	}
	if !strings.Contains(body, "SwaggerUIBundle") {
		t.Error("Swagger page should load SwaggerUIBundle")
	}
	if !strings.Contains(body, "/openapi.json") {
		t.Error("Swagger page should reference /openapi.json")
	}
}

func TestOpenAPIHasNIP85Description(t *testing.T) {
	body := openAPISpec
	if !strings.Contains(body, "NIP-85") {
		t.Error("OpenAPI spec should mention NIP-85")
	}
	if !strings.Contains(body, "PageRank") {
		t.Error("OpenAPI spec should mention PageRank")
	}
}
