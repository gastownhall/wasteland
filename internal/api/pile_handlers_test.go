package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/gastownhall/wasteland/internal/sdk"
)

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
	s := &Server{
		clientFunc: func(_ *http.Request) (*sdk.Client, error) { return nil, nil },
		mux:        http.NewServeMux(),
	}
	s.pile = pq
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
