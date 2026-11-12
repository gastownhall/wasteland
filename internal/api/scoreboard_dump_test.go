package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
)

func TestScoreboardDump_Handler(t *testing.T) {
	db := newFakeDB()
	db.results = map[string]string{
		"FROM rigs ORDER":        "handle,display_name,dolthub_org,trust_level,registered_at,last_seen,rig_type,parent_rig\nalice,Alice Chen,hop,0,2024-01-15,2024-06-01,human,\n",
		"FROM stamps ORDER":      "id,author,subject,valence,confidence,severity,context_id,context_type,skill_tags,message,created_at\ns-1,bob,alice,{},1,root,c-1,completion,,great,2024-06-01\n",
		"FROM completions ORDER": "id,wanted_id,completed_by,evidence,validated_by,stamp_id,completed_at,validated_at\nc-1,w-1,alice,http://example.com,bob,s-1,2024-06-01,2024-06-02\n",
		"FROM wanted ORDER":      "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\nw-1,Fix bug,desc,proj,task,1,,bob,,open,medium,2024-05-01,2024-05-01\n",
		"FROM badges ORDER":      "id,rig_handle,badge_type,awarded_at,evidence\nb-1,alice,pioneer,2024-03-01,first completion\n",
	}

	client := newTestClient(db)
	srv := New(client)
	cache := NewCachedEndpoint(newScoreboardDumpRefresh(db), time.Hour)
	srv.SetScoreboardDump(cache)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scoreboard/dump")
	if err != nil {
		t.Fatalf("GET /api/scoreboard/dump: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q, want 'public, max-age=300'", got)
	}

	var result ScoreboardDumpResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Rigs) != 1 {
		t.Fatalf("expected 1 rig, got %d", len(result.Rigs))
	}
	if result.Rigs[0].Handle != "alice" {
		t.Errorf("rig handle = %q, want alice", result.Rigs[0].Handle)
	}
	if result.Rigs[0].TrustTier != "newcomer" {
		t.Errorf("rig trust_tier = %q, want newcomer (1 root stamp = weight 5)", result.Rigs[0].TrustTier)
	}
	if len(result.Stamps) != 1 {
		t.Errorf("stamps count = %d, want 1", len(result.Stamps))
	}
	if result.UpdatedAt == "" {
		t.Error("expected updated_at, got empty")
	}
}

func TestScoreboardDump_NotConfigured(t *testing.T) {
	db := newFakeDB()
	client := newTestClient(db)
	srv := New(client)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/scoreboard/dump")
	if err != nil {
		t.Fatalf("GET /api/scoreboard/dump: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestScoreboardDump_CORSPreflight(t *testing.T) {
	db := newFakeDB()
	client := newTestClient(db)
	srv := New(client)
	cache := NewCachedEndpoint(newScoreboardDumpRefresh(db), time.Hour)
	srv.SetScoreboardDump(cache)

	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/scoreboard/dump", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS /api/scoreboard/dump: %v", err)
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

// newScoreboardDumpRefresh creates the refresh function used in tests.
func newScoreboardDumpRefresh(db *fakeDB) func() ([]byte, error) {
	return func() ([]byte, error) {
		dump, err := commons.QueryScoreboardDump(db)
		if err != nil {
			return nil, err
		}
		return json.Marshal(ToScoreboardDumpResponse(dump))
	}
}
