package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestScoreboard_Handler(t *testing.T) {
	db := newFakeDB()
	// Wire stamp aggregate data.
	db.results = map[string]string{
		"GROUP BY s.subject":   "subject,stamp_count,weighted_score,unique_towns,avg_quality,avg_reliability\nalice,4,15,3,4.2,3.8\n",
		"stamp_id IS NOT NULL": "completed_by,completions\nalice,2\n",
		"s.skill_tags":         "subject,skill_tags\n",
		"FROM rigs":            "handle,display_name\nalice,Alice Chen\n",
	}

	client := newTestClient(db)
	srv := New(client)
	cache := NewScoreboardCache(db, time.Hour)
	srv.SetScoreboard(cache)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scoreboard")
	if err != nil {
		t.Fatalf("GET /api/scoreboard: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Check CORS header.
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS Allow-Origin = %q, want *", got)
	}

	// Check Cache-Control header.
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=300'", got)
	}

	var result ScoreboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result.Entries))
	}
	if result.Entries[0].RigHandle != "alice" {
		t.Errorf("rig_handle = %q, want alice", result.Entries[0].RigHandle)
	}
	if result.Entries[0].DisplayName != "Alice Chen" {
		t.Errorf("display_name = %q, want Alice Chen", result.Entries[0].DisplayName)
	}
	if result.Entries[0].WeightedScore != 15 {
		t.Errorf("weighted_score = %d, want 15", result.Entries[0].WeightedScore)
	}
	if result.UpdatedAt == "" {
		t.Error("expected updated_at, got empty")
	}
}

func TestScoreboard_NotConfigured(t *testing.T) {
	db := newFakeDB()
	client := newTestClient(db)
	srv := New(client)
	// Do NOT set scoreboard cache.

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scoreboard")
	if err != nil {
		t.Fatalf("GET /api/scoreboard: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestScoreboard_CORSPreflight(t *testing.T) {
	db := newFakeDB()
	client := newTestClient(db)
	srv := New(client)
	cache := NewScoreboardCache(db, time.Hour)
	srv.SetScoreboard(cache)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/scoreboard", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/scoreboard: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); got != "GET, OPTIONS" {
		t.Errorf("CORS Allow-Methods = %q, want 'GET, OPTIONS'", got)
	}
}
