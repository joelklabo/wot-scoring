package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareEndpoint(t *testing.T) {
	// Reset graph
	graph = NewGraph()
	graph.AddFollow("aaa", "bbb")
	graph.AddFollow("aaa", "ccc")
	graph.AddFollow("bbb", "aaa")
	graph.AddFollow("bbb", "ccc")
	graph.AddFollow("ccc", "aaa")
	graph.AddFollow("ddd", "aaa")
	graph.AddFollow("ddd", "bbb")
	graph.ComputePageRank(20, 0.85)

	tests := []struct {
		name       string
		queryA     string
		queryB     string
		wantStatus int
		checkBody  func(t *testing.T, body map[string]interface{})
	}{
		{
			name:       "mutual followers",
			queryA:     "aaa",
			queryB:     "bbb",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]interface{}) {
				rel := body["relationship"].(string)
				if rel != "mutual" {
					t.Errorf("expected mutual, got %s", rel)
				}
				sharedCount := body["shared_follows_count"].(float64)
				if sharedCount != 1 { // both follow ccc
					t.Errorf("expected 1 shared follow, got %v", sharedCount)
				}
			},
		},
		{
			name:       "one-way follow",
			queryA:     "ddd",
			queryB:     "aaa",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]interface{}) {
				rel := body["relationship"].(string)
				if rel != "a_follows_b" {
					t.Errorf("expected a_follows_b, got %s", rel)
				}
			},
		},
		{
			name:       "no relationship",
			queryA:     "ccc",
			queryB:     "ddd",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]interface{}) {
				rel := body["relationship"].(string)
				if rel != "none" {
					t.Errorf("expected none, got %s", rel)
				}
			},
		},
		{
			name:       "missing params",
			queryA:     "aaa",
			queryB:     "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "same pubkey",
			queryA:     "aaa",
			queryB:     "aaa",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "trust path exists",
			queryA:     "aaa",
			queryB:     "bbb",
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, body map[string]interface{}) {
				tp := body["trust_path"].(map[string]interface{})
				if !tp["found"].(bool) {
					t.Error("expected trust path to be found")
				}
				hops := tp["hops"].(float64)
				if hops != 1 {
					t.Errorf("expected 1 hop, got %v", hops)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/compare?a=" + tt.queryA + "&b=" + tt.queryB
			req := httptest.NewRequest("GET", url, nil)
			rec := httptest.NewRecorder()

			handleCompare(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d. body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if tt.checkBody != nil {
				var body map[string]interface{}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("failed to parse response: %v", err)
				}
				tt.checkBody(t, body)
			}
		})
	}
}

func TestCompareFollowSimilarity(t *testing.T) {
	graph = NewGraph()
	// A follows: x, y, z
	graph.AddFollow("aaa", "xxx")
	graph.AddFollow("aaa", "yyy")
	graph.AddFollow("aaa", "zzz")
	// B follows: x, y, w
	graph.AddFollow("bbb", "xxx")
	graph.AddFollow("bbb", "yyy")
	graph.AddFollow("bbb", "www")
	graph.ComputePageRank(20, 0.85)

	req := httptest.NewRequest("GET", "/compare?a=aaa&b=bbb", nil)
	rec := httptest.NewRecorder()
	handleCompare(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &body)

	// Shared follows: x, y = 2. Union: x, y, z, w = 4. Jaccard = 2/4 = 0.5
	similarity := body["follow_similarity"].(float64)
	if similarity != 0.5 {
		t.Errorf("expected similarity 0.5, got %v", similarity)
	}

	sharedCount := body["shared_follows_count"].(float64)
	if sharedCount != 2 {
		t.Errorf("expected 2 shared follows, got %v", sharedCount)
	}
}
