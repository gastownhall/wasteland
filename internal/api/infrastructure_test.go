package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/sdk"
)

func TestCachedEndpoint_StartStop(t *testing.T) {
	var calls atomic.Int32
	ready := make(chan struct{}, 1)
	cache := NewCachedEndpoint(func() ([]byte, error) {
		if calls.Add(1) == 1 {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
		return []byte(`{"ok":true}`), nil
	}, 5*time.Millisecond)

	cache.Start()
	defer cache.Stop()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for background refresh")
	}

	if got := string(cache.Get()); got != `{"ok":true}` {
		t.Fatalf("cache.Get() = %q, want %q", got, `{"ok":true}`)
	}

	cache.Stop()
}

func TestScoreboardHandler_DataUnavailable(t *testing.T) {
	srv := New(newTestClient(newFakeDB()))
	srv.SetScoreboard(NewCachedEndpoint(func() ([]byte, error) {
		return nil, errors.New("refresh failed")
	}, time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/api/scoreboard", nil)
	rec := httptest.NewRecorder()
	srv.ScoreboardHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if !strings.Contains(resp.Error, "scoreboard data unavailable") {
		t.Fatalf("error = %q, want scoreboard data unavailable", resp.Error)
	}
}

func TestNewHostedWorkspace_ConfigIncludesWorkspaceAndActiveUpstream(t *testing.T) {
	db := newFakeDB()
	client := newTestClient(db)
	workspace := sdk.NewWorkspace("alice")
	workspace.Add(sdk.UpstreamInfo{
		Upstream: "hop/wl-commons",
		ForkOrg:  "alice",
		ForkDB:   "wl-commons",
		Mode:     "pr",
	}, client)

	srv := NewHostedWorkspace(
		func(*http.Request) (*sdk.Client, error) { return client, nil },
		func(*http.Request) (*sdk.Workspace, error) { return workspace, nil },
	)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/config", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Wasteland", "hop/wl-commons")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var cfg ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if !cfg.Hosted || !cfg.Connected {
		t.Fatalf("expected hosted connected config, got hosted=%v connected=%v", cfg.Hosted, cfg.Connected)
	}
	if cfg.Upstream != "hop/wl-commons" {
		t.Fatalf("upstream = %q, want %q", cfg.Upstream, "hop/wl-commons")
	}
	if len(cfg.Upstreams) != 1 {
		t.Fatalf("upstreams len = %d, want 1", len(cfg.Upstreams))
	}
	if cfg.Upstreams[0].ForkOrg != "alice" || cfg.Upstreams[0].Mode != "pr" {
		t.Fatalf("unexpected upstream info: %+v", cfg.Upstreams[0])
	}
}

func TestBootstrap_ReturnsRigHandleAndNoStore(t *testing.T) {
	srv := New(newTestClient(newFakeDB()))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/bootstrap", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Wasteland", "stale/upstream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/bootstrap: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}

	var boot BootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&boot); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if boot.RigHandle != "alice" {
		t.Fatalf("rig_handle = %q, want %q", boot.RigHandle, "alice")
	}
	if !boot.Connected {
		t.Fatalf("expected connected bootstrap response")
	}
	if boot.ActiveUpstream != "stale/upstream" {
		t.Fatalf("active_upstream = %q, want %q", boot.ActiveUpstream, "stale/upstream")
	}
}

func TestBootstrap_StagingImpersonationUsesImpersonatedViewerAndEnvironment(t *testing.T) {
	srv := New(newTestClient(newFakeDB()))
	srv.SetEnvironment("staging")
	ts := httptest.NewServer(srv)
	defer ts.Close()

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/bootstrap", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-Impersonate", "bob")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/bootstrap: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var boot BootstrapResponse
	if err := json.NewDecoder(resp.Body).Decode(&boot); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if boot.RigHandle != "bob" {
		t.Fatalf("rig_handle = %q, want %q", boot.RigHandle, "bob")
	}
	if boot.Environment != "staging" {
		t.Fatalf("environment = %q, want %q", boot.Environment, "staging")
	}
}

func TestSetProfileQuerier_OverridesProfileSource(t *testing.T) {
	sheetJSON := `{"identity":{"display_name":"Injected"},"value_dimensions":{"quality":0.7}}`
	pq := &fakePileQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {
			{"handle": "test", "source": "github", "sheet_json": sheetJSON, "confidence": "0.9", "created_at": "2024-01-01"},
		},
		"SELECT skill_tags": {},
	}}

	srv := &Server{
		clientFunc: func(*http.Request) (*sdk.Client, error) { return nil, nil },
		mux:        http.NewServeMux(),
	}
	srv.registerRoutes()
	srv.SetProfileQuerier(pq)
	srv.SetCommonsQuerier(&fakePileQuerier{})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/profile/test")
	if err != nil {
		t.Fatalf("GET /api/profile/test: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var profile struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.Handle != "test" {
		t.Fatalf("handle = %q, want %q", profile.Handle, "test")
	}
}

func TestBrowse_UsesStaleCacheOnTransientError(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	srv := New(newTestClient(db))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var warm BrowseResponse
	r := getJSON(t, ts, "/api/wanted", &warm)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("warm browse status = %d, want 200", r.StatusCode)
	}

	srv.browseCache.mu.Lock()
	entry := srv.browseCache.entries["browse:local:alice:wild-west::"]
	if entry == nil {
		srv.browseCache.mu.Unlock()
		t.Fatal("expected warm browse cache entry")
	}
	entry.storedAt = time.Now().Add(-time.Minute)
	srv.browseCache.mu.Unlock()

	db.queryErrors = map[string]error{
		"FROM wanted": errors.New("no such repository"),
	}

	var stale BrowseResponse
	r = getJSON(t, ts, "/api/wanted", &stale)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("stale browse status = %d, want 200", r.StatusCode)
	}
	if got := r.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if len(stale.Items) != 1 || stale.Items[0].ID != "w-1" {
		t.Fatalf("unexpected stale items: %+v", stale.Items)
	}
	if !strings.Contains(stale.Warning, "Showing cached data") {
		t.Fatalf("warning = %q, want stale-data warning", stale.Warning)
	}
}

func TestBrowse_AuthErrorDoesNotServeStaleCache(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	srv := New(newTestClient(db))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var warm BrowseResponse
	r := getJSON(t, ts, "/api/wanted", &warm)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("warm browse status = %d, want 200", r.StatusCode)
	}

	srv.browseCache.mu.Lock()
	entry := srv.browseCache.entries["browse:local:alice:wild-west::"]
	if entry == nil {
		srv.browseCache.mu.Unlock()
		t.Fatal("expected warm browse cache entry")
	}
	entry.storedAt = time.Now().Add(-time.Minute)
	srv.browseCache.mu.Unlock()

	db.queryErrors = map[string]error{
		"FROM wanted": errors.New("invalid authorization"),
	}

	var resp ErrorResponse
	r = getJSON(t, ts, "/api/wanted", &resp)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", r.StatusCode, http.StatusUnauthorized)
	}
	if !strings.Contains(resp.Error, "please reconnect") {
		t.Fatalf("error = %q, want reconnect hint", resp.Error)
	}
}

func TestDetail_UsesStaleCacheOnTransientError(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	srv := New(newTestClient(db))
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var warm DetailResponse
	r := getJSON(t, ts, "/api/wanted/w-1", &warm)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("warm detail status = %d, want 200", r.StatusCode)
	}

	srv.detailCache.mu.Lock()
	entry := srv.detailCache.entries["detail:local:alice:wild-west::w-1"]
	if entry == nil {
		srv.detailCache.mu.Unlock()
		t.Fatal("expected warm detail cache entry")
	}
	entry.storedAt = time.Now().Add(-time.Minute)
	srv.detailCache.mu.Unlock()

	db.queryErrors = map[string]error{
		"WHERE w.id='w-1'": errors.New("no such repository"),
	}

	var stale DetailResponse
	r = getJSON(t, ts, "/api/wanted/w-1", &stale)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("stale detail status = %d, want 200", r.StatusCode)
	}
	if got := r.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if stale.Item == nil || stale.Item.ID != "w-1" {
		t.Fatalf("unexpected stale detail: %+v", stale.Item)
	}
}

func TestDetail_UsesStaleCacheWhenPendingMetadataIncomplete(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	var failPending atomic.Bool
	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItemsContext: func(context.Context) (map[string][]sdk.PendingItem, error) {
			if failPending.Load() {
				return nil, errors.New("pending metadata incomplete")
			}
			return map[string][]sdk.PendingItem{
				"w-1": {{
					RigHandle: "bob",
					Status:    "in_review",
					Branch:    "wl/bob/w-1",
				}},
			}, nil
		},
		SaveConfig: func(_ string, _ bool) error { return nil },
	})
	srv := New(client)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var warm DetailResponse
	r := getJSON(t, ts, "/api/wanted/w-1", &warm)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("warm detail status = %d, want 200", r.StatusCode)
	}

	srv.detailCache.mu.Lock()
	entry := srv.detailCache.entries["detail:local:alice:wild-west::w-1"]
	if entry == nil {
		srv.detailCache.mu.Unlock()
		t.Fatal("expected warm detail cache entry")
	}
	entry.storedAt = time.Now().Add(-time.Minute)
	srv.detailCache.mu.Unlock()

	failPending.Store(true)

	var stale DetailResponse
	r = getJSON(t, ts, "/api/wanted/w-1", &stale)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("stale detail status = %d, want 200", r.StatusCode)
	}
	if got := r.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want %q", got, "no-store")
	}
	if stale.Item == nil || stale.Item.ID != "w-1" {
		t.Fatalf("unexpected stale detail: %+v", stale.Item)
	}
}

func TestHostedUncacheableBrowse_RetainsBestEffortPendingReads(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	client := sdk.New(sdk.ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Upstream:               "hop/wl-commons",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]sdk.PendingItem, error) {
			return nil, errors.New("pending metadata unavailable")
		},
		SaveConfig: func(_ string, _ bool) error { return nil },
	})
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return client, nil
	}, nil)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	var resp BrowseResponse
	r := getJSON(t, ts, "/api/wanted", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", r.StatusCode)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "w-1" {
		t.Fatalf("unexpected browse response: %+v", resp.Items)
	}
}

func TestHostedCacheableBrowse_RetainsBestEffortPendingReads(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "small"}
	client := sdk.New(sdk.ClientConfig{
		DB:                     db,
		RigHandle:              "alice",
		Upstream:               "hop/wl-commons",
		Mode:                   "pr",
		BestEffortPendingReads: true,
		ListPendingItemsContext: func(context.Context) (map[string][]sdk.PendingItem, error) {
			return nil, errors.New("pending metadata unavailable")
		},
		SaveConfig: func(_ string, _ bool) error { return nil },
	})
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return client, nil
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	req = req.WithContext(WithResolvedReadIdentity(req.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}

	var resp BrowseResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode browse response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != "w-1" {
		t.Fatalf("unexpected browse response: %+v", resp.Items)
	}
}

func TestWriteUpstreamError_ClassifiesErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   int
		substr string
	}{
		{
			name:   "auth",
			err:    errors.New("invalid authorization"),
			want:   http.StatusUnauthorized,
			substr: "please reconnect",
		},
		{
			name:   "token permission",
			err:    errors.New("API token is not allowed to operate on this resource"),
			want:   http.StatusServiceUnavailable,
			substr: "lacks SQL permissions",
		},
		{
			name:   "transient outage",
			err:    errors.New("no such repository"),
			want:   http.StatusServiceUnavailable,
			substr: "temporarily unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeUpstreamError(rec, tt.err, "dashboard")

			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}

			var resp ErrorResponse
			if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if !strings.Contains(resp.Error, tt.substr) {
				t.Fatalf("error = %q, want substring %q", resp.Error, tt.substr)
			}
		})
	}
}

func TestBrowseCache_IsScopedByUpstream(t *testing.T) {
	hopDB := newFakeDB()
	hopDB.items["w-1"] = &fakeItem{id: "w-1", title: "Hop Item", status: "open", postedBy: "alice", effortLevel: "small"}
	hopClient := sdk.New(sdk.ClientConfig{
		DB:        hopDB,
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	gasDB := newFakeDB()
	gasDB.items["w-1"] = &fakeItem{id: "w-1", title: "Gas Item", status: "open", postedBy: "alice", effortLevel: "small"}
	gasClient := sdk.New(sdk.ClientConfig{
		DB:        gasDB,
		RigHandle: "alice",
		Upstream:  "gastownhall/gascity",
		Mode:      "wild-west",
	})

	srv := NewHostedWorkspace(func(r *http.Request) (*sdk.Client, error) {
		switch r.Header.Get("X-Wasteland") {
		case "hop/wl-commons":
			return hopClient, nil
		case "gastownhall/gascity":
			return gasClient, nil
		default:
			return nil, errors.New("unknown upstream")
		}
	}, nil)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	reqA, err := http.NewRequest(http.MethodGet, ts.URL+"/api/wanted", nil)
	if err != nil {
		t.Fatalf("request A: %v", err)
	}
	reqA.Header.Set("X-Wasteland", "hop/wl-commons")
	respA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatalf("GET hop browse: %v", err)
	}
	defer respA.Body.Close() //nolint:errcheck // test cleanup
	var browseA BrowseResponse
	if err := json.NewDecoder(respA.Body).Decode(&browseA); err != nil {
		t.Fatalf("decode hop browse: %v", err)
	}

	reqB, err := http.NewRequest(http.MethodGet, ts.URL+"/api/wanted", nil)
	if err != nil {
		t.Fatalf("request B: %v", err)
	}
	reqB.Header.Set("X-Wasteland", "gastownhall/gascity")
	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("GET gas browse: %v", err)
	}
	defer respB.Body.Close() //nolint:errcheck // test cleanup
	var browseB BrowseResponse
	if err := json.NewDecoder(respB.Body).Decode(&browseB); err != nil {
		t.Fatalf("decode gas browse: %v", err)
	}

	if len(browseA.Items) != 1 || browseA.Items[0].Title != "Hop Item" {
		t.Fatalf("hop browse items = %+v", browseA.Items)
	}
	if len(browseB.Items) != 1 || browseB.Items[0].Title != "Gas Item" {
		t.Fatalf("gas browse items = %+v", browseB.Items)
	}
}

func TestDetailCache_IsScopedByUpstream(t *testing.T) {
	hopDB := newFakeDB()
	hopDB.items["w-1"] = &fakeItem{id: "w-1", title: "Hop Item", status: "open", postedBy: "alice", effortLevel: "small"}
	hopClient := sdk.New(sdk.ClientConfig{
		DB:        hopDB,
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	gasDB := newFakeDB()
	gasDB.items["w-1"] = &fakeItem{id: "w-1", title: "Gas Item", status: "open", postedBy: "alice", effortLevel: "small"}
	gasClient := sdk.New(sdk.ClientConfig{
		DB:        gasDB,
		RigHandle: "alice",
		Upstream:  "gastownhall/gascity",
		Mode:      "wild-west",
	})

	srv := NewHostedWorkspace(func(r *http.Request) (*sdk.Client, error) {
		switch r.Header.Get("X-Wasteland") {
		case "hop/wl-commons":
			return hopClient, nil
		case "gastownhall/gascity":
			return gasClient, nil
		default:
			return nil, errors.New("unknown upstream")
		}
	}, nil)
	ts := httptest.NewServer(srv)
	defer ts.Close()

	reqA, err := http.NewRequest(http.MethodGet, ts.URL+"/api/wanted/w-1", nil)
	if err != nil {
		t.Fatalf("request A: %v", err)
	}
	reqA.Header.Set("X-Wasteland", "hop/wl-commons")
	respA, err := http.DefaultClient.Do(reqA)
	if err != nil {
		t.Fatalf("GET hop detail: %v", err)
	}
	defer respA.Body.Close() //nolint:errcheck // test cleanup
	var detailA DetailResponse
	if err := json.NewDecoder(respA.Body).Decode(&detailA); err != nil {
		t.Fatalf("decode hop detail: %v", err)
	}

	reqB, err := http.NewRequest(http.MethodGet, ts.URL+"/api/wanted/w-1", nil)
	if err != nil {
		t.Fatalf("request B: %v", err)
	}
	reqB.Header.Set("X-Wasteland", "gastownhall/gascity")
	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("GET gas detail: %v", err)
	}
	defer respB.Body.Close() //nolint:errcheck // test cleanup
	var detailB DetailResponse
	if err := json.NewDecoder(respB.Body).Decode(&detailB); err != nil {
		t.Fatalf("decode gas detail: %v", err)
	}

	if detailA.Item == nil || detailA.Item.Title != "Hop Item" {
		t.Fatalf("hop detail = %+v", detailA.Item)
	}
	if detailB.Item == nil || detailB.Item.Title != "Gas Item" {
		t.Fatalf("gas detail = %+v", detailB.Item)
	}
}

func TestInvalidateReadCaches_TargetsOnlyMatchingUpstreamAndItem(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	srv.browseCache.entries = map[string]*cacheEntry{
		"browse:wasteland%2Fwl-commons:user%3Aalice:wild-west::":          {data: []byte("hop"), storedAt: time.Now()},
		"browse:gastownhall%2Fgascity:alice:wild-west::":                  {data: []byte("gas"), storedAt: time.Now()},
		"browse:wasteland%2Fwl-commons:user%3Abob:wild-west::status=open": {data: []byte("bob"), storedAt: time.Now()},
	}
	srv.detailCache.entries = map[string]*cacheEntry{
		"detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-1": {data: []byte("hop-item"), storedAt: time.Now()},
		"detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-2": {data: []byte("hop-other"), storedAt: time.Now()},
		"detail:gastownhall%2Fgascity:alice:wild-west::w-1":         {data: []byte("gas-item"), storedAt: time.Now()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/wanted/w-1/claim", nil)
	req = req.WithContext(WithResolvedReadIdentity(req.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateReadCaches(req, client, "w-1")

	if got := srv.browseCache.Get("browse:wasteland%2Fwl-commons:user%3Aalice:wild-west::"); got != nil {
		t.Fatalf("expected hop browse cache to be invalidated, got %q", got)
	}
	if got := srv.browseCache.Get("browse:wasteland%2Fwl-commons:user%3Abob:wild-west::status=open"); got != nil {
		t.Fatalf("expected all hop browse cache entries to be invalidated, got %q", got)
	}
	if got := srv.browseCache.Get("browse:gastownhall%2Fgascity:alice:wild-west::"); got == nil {
		t.Fatal("expected gas browse cache to remain")
	}
	if got := srv.detailCache.Get("detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-1"); got != nil {
		t.Fatalf("expected hop detail w-1 to be invalidated, got %q", got)
	}
	if got := srv.detailCache.Get("detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-2"); got == nil {
		t.Fatal("expected unrelated hop detail entry to remain")
	}
	if got := srv.detailCache.Get("detail:gastownhall%2Fgascity:alice:wild-west::w-1"); got == nil {
		t.Fatal("expected gas detail entry to remain")
	}
}

func TestInvalidateReadCaches_CallsMutationInvalidator(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	var calls atomic.Int32
	srv.SetMutationInvalidator(func(context.Context) {
		calls.Add(1)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/wanted/w-1/reject-upstream", nil)
	req = req.WithContext(WithResolvedReadIdentity(req.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateReadCaches(req, client, "w-1")

	if got := calls.Load(); got != 1 {
		t.Fatalf("mutation invalidator calls = %d, want 1", got)
	}
}

func TestInvalidateUpstreamReadCaches_TargetsOnlyMatchingUpstream(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	srv.browseCache.entries = map[string]*cacheEntry{
		"browse:wasteland%2Fwl-commons:user%3Aalice:wild-west::": {data: []byte("hop"), storedAt: time.Now()},
		"browse:gastownhall%2Fgascity:alice:wild-west::":         {data: []byte("gas"), storedAt: time.Now()},
	}
	srv.detailCache.entries = map[string]*cacheEntry{
		"detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-1": {data: []byte("hop-1"), storedAt: time.Now()},
		"detail:wasteland%2Fwl-commons:user%3Abob:wild-west::w-2":   {data: []byte("hop-2"), storedAt: time.Now()},
		"detail:gastownhall%2Fgascity:alice:wild-west::w-1":         {data: []byte("gas-1"), storedAt: time.Now()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/branches/wl/alice/w-1/apply", nil)
	req = req.WithContext(WithResolvedReadIdentity(req.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateUpstreamReadCaches(req, client)

	if got := srv.browseCache.Get("browse:wasteland%2Fwl-commons:user%3Aalice:wild-west::"); got != nil {
		t.Fatalf("expected hop browse cache to be invalidated, got %q", got)
	}
	if got := srv.detailCache.Get("detail:wasteland%2Fwl-commons:user%3Aalice:wild-west::w-1"); got != nil {
		t.Fatalf("expected hop detail w-1 to be invalidated, got %q", got)
	}
	if got := srv.detailCache.Get("detail:wasteland%2Fwl-commons:user%3Abob:wild-west::w-2"); got != nil {
		t.Fatalf("expected all hop detail cache entries to be invalidated, got %q", got)
	}
	if got := srv.browseCache.Get("browse:gastownhall%2Fgascity:alice:wild-west::"); got == nil {
		t.Fatal("expected gas browse cache to remain")
	}
	if got := srv.detailCache.Get("detail:gastownhall%2Fgascity:alice:wild-west::w-1"); got == nil {
		t.Fatal("expected gas detail cache to remain")
	}
}

func TestInvalidateReadCaches_HostedWithoutCanonicalIdentityFallsBackToGlobalInvalidation(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	srv.browseCache.entries = map[string]*cacheEntry{
		"browse:hop%2Fwl-commons:alice:wild-west::":      {data: []byte("hop"), storedAt: time.Now()},
		"browse:gastownhall%2Fgascity:alice:wild-west::": {data: []byte("gas"), storedAt: time.Now()},
	}
	srv.detailCache.entries = map[string]*cacheEntry{
		"detail:hop%2Fwl-commons:alice:wild-west::w-1":      {data: []byte("hop-1"), storedAt: time.Now()},
		"detail:gastownhall%2Fgascity:alice:wild-west::w-1": {data: []byte("gas-1"), storedAt: time.Now()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/wanted/w-1/claim", nil)
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateReadCaches(req, client, "w-1")

	if got := srv.browseCache.Get("browse:hop%2Fwl-commons:alice:wild-west::"); got != nil {
		t.Fatalf("expected hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.browseCache.Get("browse:gastownhall%2Fgascity:alice:wild-west::"); got != nil {
		t.Fatalf("expected unrelated hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.detailCache.Get("detail:hop%2Fwl-commons:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected hosted detail cache to be cleared, got %q", got)
	}
	if got := srv.detailCache.Get("detail:gastownhall%2Fgascity:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected unrelated hosted detail cache to be cleared, got %q", got)
	}
}

func TestInvalidateBrowseReadCaches_HostedWithoutCanonicalIdentityFallsBackToGlobalInvalidation(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	srv.browseCache.entries = map[string]*cacheEntry{
		"browse:hop%2Fwl-commons:alice:wild-west::":      {data: []byte("hop"), storedAt: time.Now()},
		"browse:gastownhall%2Fgascity:alice:wild-west::": {data: []byte("gas"), storedAt: time.Now()},
	}
	srv.detailCache.entries = map[string]*cacheEntry{
		"detail:hop%2Fwl-commons:alice:wild-west::w-1":      {data: []byte("hop-1"), storedAt: time.Now()},
		"detail:gastownhall%2Fgascity:alice:wild-west::w-1": {data: []byte("gas-1"), storedAt: time.Now()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/wanted", nil)
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateBrowseReadCaches(req, client)

	if got := srv.browseCache.Get("browse:hop%2Fwl-commons:alice:wild-west::"); got != nil {
		t.Fatalf("expected hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.browseCache.Get("browse:gastownhall%2Fgascity:alice:wild-west::"); got != nil {
		t.Fatalf("expected unrelated hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.detailCache.Get("detail:hop%2Fwl-commons:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected hosted detail cache to be cleared by fail-closed invalidation, got %q", got)
	}
	if got := srv.detailCache.Get("detail:gastownhall%2Fgascity:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected unrelated hosted detail cache to be cleared by fail-closed invalidation, got %q", got)
	}
}

func TestInvalidateUpstreamReadCaches_HostedWithoutCanonicalIdentityFallsBackToGlobalInvalidation(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			RigHandle: "alice",
			Upstream:  "hop/wl-commons",
			Mode:      "wild-west",
		}), nil
	}, nil)

	srv.browseCache.entries = map[string]*cacheEntry{
		"browse:hop%2Fwl-commons:alice:wild-west::":      {data: []byte("hop"), storedAt: time.Now()},
		"browse:gastownhall%2Fgascity:alice:wild-west::": {data: []byte("gas"), storedAt: time.Now()},
	}
	srv.detailCache.entries = map[string]*cacheEntry{
		"detail:hop%2Fwl-commons:alice:wild-west::w-1":      {data: []byte("hop-1"), storedAt: time.Now()},
		"detail:gastownhall%2Fgascity:alice:wild-west::w-1": {data: []byte("gas-1"), storedAt: time.Now()},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/branches/wl/alice/w-1/apply", nil)
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	srv.invalidateUpstreamReadCaches(req, client)

	if got := srv.browseCache.Get("browse:hop%2Fwl-commons:alice:wild-west::"); got != nil {
		t.Fatalf("expected hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.browseCache.Get("browse:gastownhall%2Fgascity:alice:wild-west::"); got != nil {
		t.Fatalf("expected unrelated hosted browse cache to be cleared, got %q", got)
	}
	if got := srv.detailCache.Get("detail:hop%2Fwl-commons:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected hosted detail cache to be cleared, got %q", got)
	}
	if got := srv.detailCache.Get("detail:gastownhall%2Fgascity:alice:wild-west::w-1"); got != nil {
		t.Fatalf("expected unrelated hosted detail cache to be cleared, got %q", got)
	}
}

func TestBrowseCacheKey_DistinguishesImpersonatedReads(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)
	baseClient := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})
	baseReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	baseReq = baseReq.WithContext(WithResolvedReadIdentity(baseReq.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))

	impReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	impReq.Header.Set("X-Impersonate", "bob")
	impReq = impReq.WithContext(WithResolvedReadIdentity(impReq.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	impClient := baseClient.WithRigHandle("bob")

	baseKey := browseCacheKey(srv.readCacheScope(baseReq, baseClient), baseReq)
	impKey := browseCacheKey(srv.readCacheScope(impReq, impClient), impReq)
	if baseKey == impKey {
		t.Fatalf("expected impersonated browse cache key to differ, got %q", baseKey)
	}
}

func TestBrowseCacheKey_DistinguishesAnonymousPublicFromRealPublicHandle(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)

	anonReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	anonReq = anonReq.WithContext(WithResolvedReadIdentity(anonReq.Context(), ResolvedReadIdentity{
		Upstream: "wasteland/wl-commons",
		Public:   true,
	}))
	anonClient := sdk.New(sdk.ClientConfig{
		Upstream: "wasteland/wl-commons",
		Mode:     "wild-west",
	})

	realReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	realReq = realReq.WithContext(WithResolvedReadIdentity(realReq.Context(), ResolvedReadIdentity{
		Upstream: "wasteland/wl-commons",
		Viewer:   "public",
	}))
	realClient := sdk.New(sdk.ClientConfig{
		RigHandle: "public",
		Upstream:  "wasteland/wl-commons",
		Mode:      "wild-west",
	})

	anonKey := browseCacheKey(srv.readCacheScope(anonReq, anonClient), anonReq)
	realKey := browseCacheKey(srv.readCacheScope(realReq, realClient), realReq)
	if anonKey == realKey {
		t.Fatalf("expected anonymous public cache key to differ from real public handle, got %q", anonKey)
	}
}

func TestBrowseCacheKey_CanonicalizesLegacyHostedUpstreamAlias(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)

	legacyReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	legacyReq = legacyReq.WithContext(WithResolvedReadIdentity(legacyReq.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
		Viewer:   "alice",
	}))
	legacyClient := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "wild-west",
	})

	canonicalReq := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	canonicalReq = canonicalReq.WithContext(WithResolvedReadIdentity(canonicalReq.Context(), ResolvedReadIdentity{
		Upstream: "wasteland/wl-commons",
		Viewer:   "alice",
	}))
	canonicalClient := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "wasteland/wl-commons",
		Mode:      "wild-west",
	})

	legacyKey := browseCacheKey(srv.readCacheScope(legacyReq, legacyClient), legacyReq)
	canonicalKey := browseCacheKey(srv.readCacheScope(canonicalReq, canonicalClient), canonicalReq)
	if legacyKey != canonicalKey {
		t.Fatalf("expected hosted legacy alias to canonicalize, got %q vs %q", legacyKey, canonicalKey)
	}
}

func TestReadCacheScope_IgnoresRawUpstreamHeaderWithoutResolvedIdentity(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)
	client := sdk.New(sdk.ClientConfig{
		Mode: "pr",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	req.Header.Set("X-Wasteland", "attacker/poison")

	scope := srv.readCacheScope(req, client)
	if scope.upstream != localCacheUpstream {
		t.Fatalf("upstream = %q, want %q", scope.upstream, localCacheUpstream)
	}
}

func TestReadCacheScope_HostedUntrustedClientDisablesCaching(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "pr",
	})
	req := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)

	scope := srv.readCacheScope(req, client)
	if scope.cacheable {
		t.Fatal("expected hosted scope without resolved identity to bypass cache")
	}
	if scope.upstream != localCacheUpstream {
		t.Fatalf("upstream = %q, want %q", scope.upstream, localCacheUpstream)
	}
	if scope.viewer != publicCacheViewer {
		t.Fatalf("viewer = %q, want %q", scope.viewer, publicCacheViewer)
	}
}

func TestReadCacheScope_HostedIncompleteResolvedIdentityDisablesCaching(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, nil
	}, nil)
	client := sdk.New(sdk.ClientConfig{
		RigHandle: "alice",
		Upstream:  "hop/wl-commons",
		Mode:      "pr",
	})

	reqMissingUpstream := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	reqMissingUpstream = reqMissingUpstream.WithContext(WithResolvedReadIdentity(reqMissingUpstream.Context(), ResolvedReadIdentity{
		Viewer: "alice",
	}))
	scope := srv.readCacheScope(reqMissingUpstream, client)
	if scope.cacheable {
		t.Fatal("expected hosted scope with missing upstream to bypass cache")
	}

	reqMissingViewer := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	reqMissingViewer = reqMissingViewer.WithContext(WithResolvedReadIdentity(reqMissingViewer.Context(), ResolvedReadIdentity{
		Upstream: "hop/wl-commons",
	}))
	scope = srv.readCacheScope(reqMissingViewer, client)
	if scope.cacheable {
		t.Fatal("expected hosted scope with missing viewer to bypass cache")
	}
}

func TestResolveClient_HostedPublicFallbackEstablishesCacheIdentity(t *testing.T) {
	srv := NewHostedWorkspace(func(*http.Request) (*sdk.Client, error) {
		return nil, errors.New("not authenticated")
	}, nil)
	srv.SetPublicClient(sdk.New(sdk.ClientConfig{
		Upstream: "hop/wl-commons",
		Mode:     "wild-west",
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/wanted", nil)
	req.Header.Set("X-Impersonate", "bob")

	client, ok := srv.resolveClient(rec, req)
	if !ok {
		t.Fatal("expected hosted public fallback to resolve client")
	}
	scope := srv.readCacheScope(req, client)
	if !scope.cacheable {
		t.Fatal("expected public hosted scope to remain cacheable")
	}
	if scope.upstream != "wasteland/wl-commons" {
		t.Fatalf("upstream = %q, want %q", scope.upstream, "wasteland/wl-commons")
	}
	if scope.viewer != publicCacheViewer {
		t.Fatalf("viewer = %q, want %q", scope.viewer, publicCacheViewer)
	}
	if scope.impersonate != "bob" {
		t.Fatalf("impersonate = %q, want %q", scope.impersonate, "bob")
	}
}
