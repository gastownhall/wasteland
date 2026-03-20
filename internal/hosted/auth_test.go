package hosted

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testSecret = "test-session-secret"

func setupHostedTestServer(t *testing.T) (*SessionStore, *httptest.Server) {
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

	// Fake Nango server.
	nangoTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/connection/conn-1":
			resp := nangoConnectionResponse{ConnectionID: "conn-1"}
			resp.Credentials.APIKey = "test-token"
			b, _ := json.Marshal(meta)
			resp.Metadata = json.RawMessage(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)

		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/connection/"):
			w.WriteHeader(http.StatusOK)

		case r.Method == "POST" && r.URL.Path == "/connect/sessions":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "nango_connect_session_test"},
			})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(nangoTS.Close)

	nango := NewNangoClient(NangoConfig{
		BaseURL:       nangoTS.URL,
		SecretKey:     "nango-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)
	server := NewServer(resolver, sessions, nango, testSecret, "")

	// Create a simple test handler for the auth middleware.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// For API routes, check that client is in context.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			_, clientOK := ClientFromContext(r.Context())
			_, wsOK := WorkspaceFromContext(r.Context())
			if clientOK && wsOK {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"no client or workspace in context"}`))
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("static"))
	})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/connect", server.handleConnect)
	mux.HandleFunc("GET /api/auth/status", server.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/logout", server.handleLogout)
	mux.HandleFunc("POST /api/auth/connect-session", server.handleConnectSession)
	mux.HandleFunc("POST /api/auth/join", server.handleJoin)
	mux.HandleFunc("DELETE /api/auth/wastelands/{upstream...}", server.handleLeaveWasteland)
	mux.Handle("/", server.AuthMiddleware(inner))

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return sessions, ts
}

// setupMultiWastelandTestServer creates a test server with two wastelands.
func setupMultiWastelandTestServer(t *testing.T) (*SessionStore, *httptest.Server) {
	t.Helper()

	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{Upstream: "hop/wl-commons", ForkOrg: "alice-org", ForkDB: "wl-commons", Mode: "wild-west"},
			{Upstream: "gastownhall/gascity", ForkOrg: "alice-org", ForkDB: "gascity", Mode: "pr"},
		},
	}

	nangoTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/connection/conn-1":
			resp := nangoConnectionResponse{ConnectionID: "conn-1"}
			resp.Credentials.APIKey = "test-token"
			b, _ := json.Marshal(meta)
			resp.Metadata = json.RawMessage(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/connection/"):
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(nangoTS.Close)

	nango := NewNangoClient(NangoConfig{
		BaseURL:       nangoTS.URL,
		SecretKey:     "nango-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)
	server := NewServer(resolver, sessions, nango, testSecret, "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			_, ok := ClientFromContext(r.Context())
			if ok {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("/", server.AuthMiddleware(inner))

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	return sessions, ts
}

func TestAuthMiddleware_NoSession(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// Mutations without session get 401.
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_NoSession_GET_PassesThrough(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// GET without session passes through for anonymous public reads.
	resp, err := http.Get(ts.URL + "/api/wanted")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	// Inner handler returns 500 (no client in context), but the middleware
	// itself did NOT block — it passed the request through.
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusPreconditionFailed {
		t.Errorf("expected pass-through for anonymous GET, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_ValidSession_SingleWasteland(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	// Single wasteland: X-Wasteland header is optional (auto-fallback).
	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_ValidSession_WithHeader(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})
	req.Header.Set("X-Wasteland", "wasteland/wl-commons")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_MultiWasteland_NoHeader_Returns400(t *testing.T) {
	sessions, ts := setupMultiWastelandTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	// Mutations without X-Wasteland header get 400.
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 400 for missing header with multiple wastelands, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_MultiWasteland_WithHeader(t *testing.T) {
	sessions, ts := setupMultiWastelandTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})
	req.Header.Set("X-Wasteland", "hop/wl-commons")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_UnknownUpstream(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	// Mutations with unknown upstream get 400.
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})
	req.Header.Set("X-Wasteland", "nonexistent/repo")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown upstream, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_ExpiredSession(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// Use a session ID that doesn't exist in the store.
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionID("nonexistent", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_NoConnectionID(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	// Create session without connection ID (old format cookie).
	sessionID, _ := sessions.Create("")

	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionID(sessionID, testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("expected 412, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_StaticRoutes(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// Static routes should not require auth.
	resp, err := http.Get(ts.URL + "/some-page")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for static route, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_AuthRoutes(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// Auth routes should not require auth.
	body := `{"end_user_id": "test-user"}`
	resp, err := http.Post(ts.URL+"/api/auth/connect-session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for auth route, got %d", resp.StatusCode)
	}
}

func TestHandleConnect(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{
		"connection_id": "conn-1",
		"rig_handle": "alice",
		"fork_org": "alice-org",
		"fork_db": "wl-commons",
		"upstream": "wasteland/wl-commons"
	}`
	resp, err := http.Post(ts.URL+"/api/auth/connect", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	// Check that a session cookie was set.
	cookies := resp.Cookies()
	found := false
	for _, c := range cookies {
		if c.Name == cookieName {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected wl_session cookie in response")
	}
}

func TestHandleConnect_MissingFields(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{"connection_id": "conn-1"}`
	resp, err := http.Post(ts.URL+"/api/auth/connect", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleConnect_InvalidMode(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{
		"connection_id": "conn-1",
		"rig_handle": "alice",
		"fork_org": "alice-org",
		"fork_db": "wl-commons",
		"upstream": "wasteland/wl-commons",
		"mode": "typo"
	}`
	resp, err := http.Post(ts.URL+"/api/auth/connect", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if result["error"] != `mode "typo" must be "pr" or "wild-west"` {
		t.Fatalf("expected invalid mode error, got %q", result["error"])
	}
}

func TestHandleAuthStatus_NotAuthenticated(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	resp, err := http.Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	var status authStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&status)
	if status.Authenticated {
		t.Error("expected not authenticated")
	}
}

func TestHandleAuthStatus_Authenticated(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/auth/status", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	var status authStatusResponse
	_ = json.NewDecoder(resp.Body).Decode(&status)
	if !status.Authenticated {
		t.Error("expected authenticated")
	}
	if !status.Connected {
		t.Error("expected connected")
	}
	if status.RigHandle != "alice" {
		t.Errorf("expected alice, got %s", status.RigHandle)
	}
	if len(status.Wastelands) != 1 {
		t.Fatalf("expected 1 wasteland, got %d", len(status.Wastelands))
	}
	if status.Wastelands[0].Upstream != "wasteland/wl-commons" {
		t.Errorf("expected wasteland/wl-commons, got %s", status.Wastelands[0].Upstream)
	}
}

func TestHandleLogout(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Session should be deleted.
	_, ok := sessions.Get(sessionID)
	if ok {
		t.Error("expected session to be deleted after logout")
	}
}

func TestHandleConnectSession(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{"end_user_id": "alice"}`
	resp, err := http.Post(ts.URL+"/api/auth/connect-session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var session connectSessionResponse
	_ = json.NewDecoder(resp.Body).Decode(&session)
	if session.Token != "nango_connect_session_test" {
		t.Errorf("expected nango_connect_session_test, got %s", session.Token)
	}
	if session.IntegrationID != "dolthub" {
		t.Errorf("expected dolthub, got %s", session.IntegrationID)
	}
}

func TestHandleConnectSession_MissingEndUserID(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{}`
	resp, err := http.Post(ts.URL+"/api/auth/connect-session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleJoinWasteland(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	body := `{
		"fork_org": "alice-org",
		"fork_db": "gascity",
		"upstream": "gastownhall/gascity"
	}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["status"] != "joined" {
		t.Errorf("expected status=joined, got %s", result["status"])
	}
}

func TestHandleJoinWasteland_MissingFields(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	body := `{"fork_org": "alice-org"}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestHandleJoinWasteland_InvalidMode(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	body := `{
		"fork_org": "alice-org",
		"fork_db": "gascity",
		"upstream": "gastownhall/gascity",
		"mode": "typo"
	}`
	req, _ := http.NewRequest("POST", ts.URL+"/api/auth/join", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if result["error"] != `mode "typo" must be "pr" or "wild-west"` {
		t.Fatalf("expected invalid mode error, got %q", result["error"])
	}
}

func TestHandleJoinWasteland_NotAuthenticated(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	body := `{"fork_org":"a","fork_db":"b","upstream":"c/d"}`
	resp, err := http.Post(ts.URL+"/api/auth/join", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestHandleLeaveWasteland(t *testing.T) {
	// Use a multi-wasteland Nango server.
	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{Upstream: "hop/wl-commons", ForkOrg: "alice-org", ForkDB: "wl-commons", Mode: "wild-west"},
			{Upstream: "gastownhall/gascity", ForkOrg: "alice-org", ForkDB: "gascity", Mode: "pr"},
		},
	}

	nangoTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/connection/conn-1":
			resp := nangoConnectionResponse{ConnectionID: "conn-1"}
			resp.Credentials.APIKey = "test-token"
			b, _ := json.Marshal(meta)
			resp.Metadata = json.RawMessage(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/connection/"):
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(nangoTS.Close)

	nango := NewNangoClient(NangoConfig{
		BaseURL:       nangoTS.URL,
		SecretKey:     "nango-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)
	server := NewServer(resolver, sessions, nango, testSecret, "")

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/auth/wastelands/{upstream...}", server.handleLeaveWasteland)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("DELETE", ts.URL+"/api/auth/wastelands/hop/wl-commons", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestHandleLeaveWasteland_CannotRemoveLast(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	sessionID, _ := sessions.Create("conn-1")

	// Single wasteland — should fail.
	req, _ := http.NewRequest("DELETE", ts.URL+"/api/auth/wastelands/wasteland/wl-commons", nil)
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: SignSessionCookie(sessionID, "conn-1", testSecret),
	})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 400 for last wasteland, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_RehydrateAfterRestart(t *testing.T) {
	sessions, ts := setupHostedTestServer(t)

	// Create session, get the cookie, then delete from store (simulating restart).
	sessionID, _ := sessions.Create("conn-1")
	signed := SignSessionCookie(sessionID, "conn-1", testSecret)
	sessions.Delete(sessionID)

	// Verify session is gone from store.
	if _, ok := sessions.Get(sessionID); ok {
		t.Fatal("expected session to be deleted")
	}

	// Request with valid cookie should succeed via Nango re-hydration.
	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: signed})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200 after re-hydration, got %d: %s", resp.StatusCode, string(body))
	}

	// Session should now be restored in the store.
	sess, ok := sessions.Get(sessionID)
	if !ok {
		t.Fatal("expected session to be restored in store")
	}
	if sess.ConnectionID != "conn-1" {
		t.Errorf("expected conn-1, got %s", sess.ConnectionID)
	}
}

func TestAuthMiddleware_RehydrateFails_InvalidConnection(t *testing.T) {
	// Set up a Nango server that returns 404 for unknown connections.
	nangoTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(nangoTS.Close)

	nango := NewNangoClient(NangoConfig{
		BaseURL:       nangoTS.URL,
		SecretKey:     "nango-secret",
		IntegrationID: "dolthub",
	})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)
	server := NewServer(resolver, sessions, nango, testSecret, "")

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("/", server.AuthMiddleware(inner))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	// Cookie with connectionID that Nango rejects — mutations get 401.
	signed := SignSessionCookie("sess-revoked", "conn-revoked", testSecret)
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: signed})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for revoked connection, got %d", resp.StatusCode)
	}
}

func TestAuthMiddleware_OldFormatCookie_NoRehydration(t *testing.T) {
	_, ts := setupHostedTestServer(t)

	// Old-format cookie (no connectionID) for a session not in store — mutations get 401.
	signed := SignSessionID("old-sess", testSecret)
	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: signed})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for old-format cookie, got %d", resp.StatusCode)
	}
}

// setupStagingTestServer is like setupHostedTestServer but with environment="staging".
// The inner handler echoes the client's rig_handle so tests can verify impersonation.
func setupStagingTestServer(t *testing.T) (*SessionStore, *httptest.Server) {
	t.Helper()

	meta := &UserMetadata{
		RigHandle: "alice",
		Wastelands: []WastelandConfig{
			{Upstream: "wasteland/wl-commons", ForkOrg: "alice-org", ForkDB: "wl-commons", Mode: "wild-west"},
		},
	}

	nangoTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/connection/conn-1":
			resp := nangoConnectionResponse{ConnectionID: "conn-1"}
			resp.Credentials.APIKey = "test-token"
			b, _ := json.Marshal(meta)
			resp.Metadata = json.RawMessage(b)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(nangoTS.Close)

	nango := NewNangoClient(NangoConfig{BaseURL: nangoTS.URL, SecretKey: "nango-secret", IntegrationID: "dolthub"})
	sessions := NewSessionStore()
	resolver := NewWorkspaceResolver(nango, sessions)
	server := NewServer(resolver, sessions, nango, testSecret, "staging")

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			client, ok := ClientFromContext(r.Context())
			if ok {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"rig_handle": client.RigHandle()})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("/", server.AuthMiddleware(inner))
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return sessions, ts
}

func TestAuthMiddleware_Impersonate_Staging_GET(t *testing.T) {
	sessions, ts := setupStagingTestServer(t)
	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: SignSessionCookie(sessionID, "conn-1", testSecret)})
	req.Header.Set("X-Impersonate", "bob")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if result["rig_handle"] != "bob" {
		t.Errorf("expected impersonated rig_handle=bob, got %s", result["rig_handle"])
	}
}

func TestAuthMiddleware_Impersonate_Staging_POST_Blocked(t *testing.T) {
	sessions, ts := setupStagingTestServer(t)
	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("POST", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: SignSessionCookie(sessionID, "conn-1", testSecret)})
	req.Header.Set("X-Wasteland", "wasteland/wl-commons")
	req.Header.Set("X-Impersonate", "bob")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 403, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestAuthMiddleware_Impersonate_NonStaging_Ignored(t *testing.T) {
	// Use the default (non-staging) test server.
	sessions, ts := setupHostedTestServer(t)
	sessionID, _ := sessions.Create("conn-1")

	req, _ := http.NewRequest("GET", ts.URL+"/api/wanted", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: SignSessionCookie(sessionID, "conn-1", testSecret)})
	req.Header.Set("X-Impersonate", "bob")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	// Should succeed — the header is ignored on non-staging.
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("expected 200 (header ignored), got %d: %s", resp.StatusCode, string(body))
	}
}
