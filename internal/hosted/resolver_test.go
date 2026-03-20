package hosted

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newFakeNangoForResolver(t *testing.T) *httptest.Server {
	t.Helper()
	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{
				Upstream: "wasteland/wl-commons",
				ForkOrg:  "alice-org",
				ForkDB:   "wl-commons",
				Mode:     "wild-west",
			},
		},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer resolver-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if r.Method == "GET" && r.URL.Path == "/connection/conn-1" {
			resp := nangoConnectionResponse{
				ConnectionID: "conn-1",
			}
			resp.Credentials.APIKey = "test-dolthub-token"
			b, _ := json.Marshal(meta)
			resp.Metadata = json.RawMessage(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		http.Error(w, "not found", http.StatusNotFound)
	}))
}

func TestWorkspaceResolver_Resolve(t *testing.T) {
	ts := newFakeNangoForResolver(t)
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "resolver-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	ws, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws == nil {
		t.Fatal("expected non-nil workspace")
	}
	if ws.RigHandle() != "alice" {
		t.Errorf("expected alice, got %s", ws.RigHandle())
	}

	// Should have one upstream.
	upstreams := ws.Upstreams()
	if len(upstreams) != 1 {
		t.Fatalf("expected 1 upstream, got %d", len(upstreams))
	}
	if upstreams[0].Upstream != "wasteland/wl-commons" {
		t.Errorf("expected wasteland/wl-commons, got %s", upstreams[0].Upstream)
	}

	// Client should be accessible.
	client, err := ws.Client("wasteland/wl-commons")
	if err != nil {
		t.Fatalf("expected client: %v", err)
	}
	if client.RigHandle() != "alice" {
		t.Errorf("expected alice, got %s", client.RigHandle())
	}
	if client.Mode() != "wild-west" {
		t.Errorf("expected wild-west, got %s", client.Mode())
	}
}

func TestWorkspaceResolver_CachesWorkspace(t *testing.T) {
	ts := newFakeNangoForResolver(t)
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "resolver-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	ws1, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	ws2, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	// Should get the same cached instance.
	if ws1 != ws2 {
		t.Error("expected same workspace instance from cache")
	}
}

func TestWorkspaceResolver_InvalidateConnection(t *testing.T) {
	ts := newFakeNangoForResolver(t)
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "resolver-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	ws1, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	resolver.InvalidateConnection("conn-1")

	ws2, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	if ws1 == ws2 {
		t.Error("expected different workspace after invalidation")
	}
}

func TestWorkspaceResolver_NoToken_StillWorks(t *testing.T) {
	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{
				Upstream: "wasteland/wl-commons",
				ForkOrg:  "alice-org",
				ForkDB:   "wl-commons",
				Mode:     "wild-west",
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := nangoConnectionResponse{ConnectionID: "conn-1"}
		b, _ := json.Marshal(meta)
		resp.Metadata = json.RawMessage(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	ws, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ws == nil {
		t.Fatal("expected non-nil workspace")
	}
}

func TestWorkspaceResolver_NoConfig(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := nangoConnectionResponse{ConnectionID: "conn-1"}
		resp.Credentials.APIKey = "token"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	_, err := resolver.Resolve(session)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestWorkspaceResolver_InvalidMode(t *testing.T) {
	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{Upstream: "wasteland/wl-commons", ForkOrg: "alice-org", ForkDB: "wl-commons", Mode: "typo"},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := nangoConnectionResponse{ConnectionID: "conn-1"}
		resp.Credentials.APIKey = "token"
		b, _ := json.Marshal(meta)
		resp.Metadata = json.RawMessage(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{
		ID:           "sess-1",
		ConnectionID: "conn-1",
		CreatedAt:    time.Now(),
	}

	_, err := resolver.Resolve(session)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), `invalid hosted wasteland config: mode "typo" must be "pr" or "wild-west"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWorkspaceResolver_MultipleWastelands(t *testing.T) {
	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{Upstream: "hop/wl-commons", ForkOrg: "alice-org", ForkDB: "wl-commons", Mode: "wild-west"},
			{Upstream: "gastownhall/gascity", ForkOrg: "alice-org", ForkDB: "gascity", Mode: "pr"},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := nangoConnectionResponse{ConnectionID: "conn-1"}
		resp.Credentials.APIKey = "token"
		b, _ := json.Marshal(meta)
		resp.Metadata = json.RawMessage(b)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	nango := NewNangoClient(NangoConfig{
		BaseURL:       ts.URL,
		SecretKey:     "secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)

	session := &UserSession{ID: "sess-1", ConnectionID: "conn-1", CreatedAt: time.Now()}

	ws, err := resolver.Resolve(session)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	upstreams := ws.Upstreams()
	if len(upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(upstreams))
	}

	// Both clients should be accessible.
	c1, err := ws.Client("hop/wl-commons")
	if err != nil {
		t.Fatalf("expected client for hop/wl-commons: %v", err)
	}
	if c1.Mode() != "wild-west" {
		t.Errorf("expected wild-west, got %s", c1.Mode())
	}

	c2, err := ws.Client("gastownhall/gascity")
	if err != nil {
		t.Fatalf("expected client for gastownhall/gascity: %v", err)
	}
	if c2.Mode() != "pr" {
		t.Errorf("expected pr, got %s", c2.Mode())
	}
}
