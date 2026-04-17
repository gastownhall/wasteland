package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/gastownhall/wasteland/internal/sdk"
)

// stubGithubCache is an in-memory githubcache.Cache for handler tests.
type stubGithubCache struct {
	entries map[string]githubcache.Entry
}

func (s *stubGithubCache) Get(handle string) (githubcache.Entry, bool) {
	e, ok := s.entries[handle]
	return e, ok
}

func (s *stubGithubCache) Put(handle string, entry githubcache.Entry) error {
	if s.entries == nil {
		s.entries = map[string]githubcache.Entry{}
	}
	s.entries[handle] = entry
	return nil
}

func (s *stubGithubCache) All() map[string]githubcache.Entry {
	out := make(map[string]githubcache.Entry, len(s.entries))
	for k, v := range s.entries {
		out[k] = v
	}
	return out
}

// fakePileQuerier returns canned rows for profile queries.
type fakePileQuerier struct {
	rows map[string][]map[string]any
	err  error
}

func (f *fakePileQuerier) QueryRows(sql string) ([]map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	for prefix, rows := range f.rows {
		if len(sql) >= len(prefix) && sql[:len(prefix)] == prefix {
			return rows, nil
		}
	}
	return nil, nil
}

func newTestProfileServer(pq pile.RowQuerier) *httptest.Server {
	return newTestProfileServerWithCommons(pq, &fakePileQuerier{})
}

func newTestProfileServerWithCommons(pq, cq pile.RowQuerier) *httptest.Server {
	s := &Server{
		clientFunc: func(_ *http.Request) (*sdk.Client, error) { return nil, nil },
		mux:        http.NewServeMux(),
	}
	s.pile = pq
	s.commons = cq
	s.registerRoutes()
	return httptest.NewServer(s)
}

func TestHandleProfile_NotFound(t *testing.T) {
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {},
	}}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleProfile_UpstreamError(t *testing.T) {
	pq := &fakePileQuerier{err: fmt.Errorf("connection timeout")}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestHandleProfile_Success(t *testing.T) {
	sheetJSON := `{"identity":{"display_name":"Test"},"value_dimensions":{"quality":0.5}}`
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {
			{"handle": "test", "source": "github", "sheet_json": sheetJSON, "confidence": "0.9", "created_at": "2024-01-01"},
		},
		"SELECT skill_tags": {},
	}}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var profile pile.Profile
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if profile.Handle != "test" {
		t.Errorf("handle = %q, want test", profile.Handle)
	}
	if profile.AssessmentCount != 0 {
		t.Errorf("assessment_count = %d, want 0", profile.AssessmentCount)
	}
}

func TestHandleProfile_StampFeed_Success(t *testing.T) {
	// Pile has no boot_block for "ghost"; commons has one stamp.
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {}, // no boot_block
	}}
	cq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT s.id": {
			{
				"id":         "s1",
				"skill_tags": `["go"]`,
				"valence":    `{"quality":4,"reliability":5}`,
				"message":    "",
				"author":     "validator",
				"created_at": "2026-04-13",
				"evidence":   "https://github.com/foo/bar/pull/1",
			},
		},
	}}
	ts := newTestProfileServerWithCommons(pq, cq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["kind"] != "stamp_feed" {
		t.Errorf("kind = %v, want stamp_feed", body["kind"])
	}
	if body["handle"] != "ghost" {
		t.Errorf("handle = %v", body["handle"])
	}
	if _, ok := body["stamps_error"]; !ok {
		t.Error("stamps_error field missing from stamp_feed response")
	}
	// With no GitHub handle cache wired up, the stamp feed must not
	// fabricate a link from the bare rig handle.
	if body["github_url"] != "" {
		t.Errorf("github_url = %v, want empty string when cache is not wired", body["github_url"])
	}
	stamps, ok := body["stamps"].([]any)
	if !ok || len(stamps) != 1 {
		t.Fatalf("stamps = %v", body["stamps"])
	}
}

// TestHandleProfile_StampFeed_GithubURLFromCache wires a stub githubcache
// and asserts the stamp feed renders the resolved username, not the raw
// rig handle.
func TestHandleProfile_StampFeed_GithubURLFromCache(t *testing.T) {
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {},
	}}
	cq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT s.id": {
			{
				"id":         "s1",
				"skill_tags": `["go"]`,
				"valence":    `{"quality":4,"reliability":5}`,
				"author":     "validator",
				"created_at": "2026-04-13",
				"evidence":   "https://github.com/foo/bar/pull/1",
			},
		},
	}}
	cache := &stubGithubCache{entries: map[string]githubcache.Entry{
		"ghost": {GitHub: "ghost-gh"},
	}}
	s := &Server{
		clientFunc: func(_ *http.Request) (*sdk.Client, error) { return nil, nil },
		mux:        http.NewServeMux(),
	}
	s.pile = pq
	s.commons = cq
	s.SetGitHubCache(cache)
	s.registerRoutes()
	ts := httptest.NewServer(s)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["github_url"] != "https://github.com/ghost-gh" {
		t.Errorf("github_url = %v, want https://github.com/ghost-gh", body["github_url"])
	}
}

func TestHandleProfile_StampFeed_StampsUnavailable(t *testing.T) {
	// Pile empty; commons errors. Expect 200 + stamps_error=stamps_unavailable.
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {},
	}}
	cq := &fakePileQuerier{err: fmt.Errorf("dolthub timeout")}
	ts := newTestProfileServerWithCommons(pq, cq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for degraded response", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["stamps_error"] != "stamps_unavailable" {
		t.Errorf("stamps_error = %v, want stamps_unavailable", body["stamps_error"])
	}
}

func TestHandleProfile_NotFound_BothSourcesEmpty(t *testing.T) {
	pq := &fakePileQuerier{rows: map[string][]map[string]any{"SELECT handle": {}}}
	cq := &fakePileQuerier{rows: map[string][]map[string]any{"SELECT s.id": {}}}
	ts := newTestProfileServerWithCommons(pq, cq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/nobody")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when both sources empty", resp.StatusCode)
	}
}

func TestHandleProfile_PileMissNilCommons_Returns503(t *testing.T) {
	// When the pile misses and no commons reader is wired, the handler
	// cannot honestly say "404 (both sources empty)". It must surface
	// that the fallback source is unconfigured.
	pq := &fakePileQuerier{rows: map[string][]map[string]any{"SELECT handle": {}}}
	ts := newTestProfileServerWithCommons(pq, nil)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 when pile misses and commons is nil", resp.StatusCode)
	}
}

func TestHandleProfile_CharacterSheet_KindField(t *testing.T) {
	// Confirm the character_sheet response still carries the new kind discriminator.
	sheetJSON := `{"identity":{"display_name":"Test"},"value_dimensions":{"quality":0.5}}`
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {
			{"handle": "test", "source": "github", "sheet_json": sheetJSON, "confidence": "0.9", "created_at": "2024-01-01"},
		},
		"SELECT skill_tags": {},
	}}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["kind"] != "character_sheet" {
		t.Errorf("kind = %v, want character_sheet", body["kind"])
	}
	if body["handle"] != "test" {
		t.Errorf("handle = %v", body["handle"])
	}
}

func TestHandleProfileSearch_LimitClamped(t *testing.T) {
	called := false
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle, display_name": {},
	}}
	// We can't easily check the SQL limit from here, but we verify
	// the endpoint doesn't error with a large limit.
	_ = called
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile?q=test&limit=999999")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestHandleProfileSearch_UpstreamError(t *testing.T) {
	pq := &fakePileQuerier{err: fmt.Errorf("connection timeout")}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile?q=test")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestHandleProfileSearch_MissingQuery(t *testing.T) {
	pq := &fakePileQuerier{}
	ts := newTestProfileServer(pq)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile?limit=10")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
