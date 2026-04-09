package remote

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
)

func TestDoltHubProvider_ForkGraphQL(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantError  bool
	}{
		{"success", 200, `{"data":{"createFork":{"forkOperationName":"op-123"}}}`, false},
		{"already exists", 200, `{"errors":[{"message":"database has already been forked"}]}`, false},
		{"forbidden", 200, `{"errors":[{"message":"forbidden"}]}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					t.Errorf("expected POST, got %s", r.Method)
				}

				// Verify session cookie.
				cookie := r.Header.Get("Cookie")
				if !strings.Contains(cookie, "dolthubToken=test-session-token") {
					t.Errorf("expected dolthubToken cookie, got %q", cookie)
				}

				// Verify GraphQL request body.
				var gqlReq graphqlRequest
				if err := json.NewDecoder(r.Body).Decode(&gqlReq); err != nil {
					t.Errorf("decoding request body: %v", err)
				}
				if !strings.Contains(gqlReq.Query, "createFork") {
					t.Errorf("query should contain createFork, got %q", gqlReq.Query)
				}
				vars := gqlReq.Variables
				if vars["parentOwnerName"] != "steveyegge" {
					t.Errorf("parentOwnerName = %q, want %q", vars["parentOwnerName"], "steveyegge")
				}
				if vars["parentRepoName"] != "wl-commons" {
					t.Errorf("parentRepoName = %q, want %q", vars["parentRepoName"], "wl-commons")
				}
				if vars["ownerName"] != "alice-dev" {
					t.Errorf("ownerName = %q, want %q", vars["ownerName"], "alice-dev")
				}

				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			oldURL := dolthubGraphQLURL
			dolthubGraphQLURL = server.URL
			defer func() { dolthubGraphQLURL = oldURL }()

			provider := NewDoltHubProvider("api-token")
			err := provider.forkGraphQL("steveyegge", "wl-commons", "alice-dev", "test-session-token")
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestDoltHubProvider_ForkGraphQL_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	oldURL := dolthubGraphQLURL
	dolthubGraphQLURL = server.URL
	defer func() { dolthubGraphQLURL = oldURL }()

	provider := NewDoltHubProvider("api-token")
	err := provider.forkGraphQL("org", "db", "fork-org", "session-token")
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}

func TestDoltHubProvider_ForkDispatch_WithSessionToken(t *testing.T) {
	// When DOLTHUB_SESSION_TOKEN is set, Fork should use GraphQL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"createFork":{"forkOperationName":"op-1"}}}`))
	}))
	defer server.Close()

	oldURL := dolthubGraphQLURL
	dolthubGraphQLURL = server.URL
	defer func() { dolthubGraphQLURL = oldURL }()

	t.Setenv("DOLTHUB_SESSION_TOKEN", "my-session")

	provider := NewDoltHubProvider("api-token")
	err := provider.Fork("org", "db", "fork-org")
	if err != nil {
		t.Errorf("Fork with session token: %v", err)
	}
}

func TestDoltHubProvider_Fork_SkipsCreationWhenForkAlreadyExists(t *testing.T) {
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/alice-dev/wl-commons/main") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"query_execution_status":"Success"}`))
			return
		}
		t.Fatalf("Fork should not attempt creation when the fork already exists: %s %s", r.Method, r.URL.String())
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	oldGraphQL := dolthubGraphQLURL
	dolthubAPIBase = apiServer.URL
	dolthubGraphQLURL = apiServer.URL + "/graphql"
	defer func() {
		dolthubAPIBase = oldAPI
		dolthubGraphQLURL = oldGraphQL
	}()

	t.Setenv("DOLTHUB_SESSION_TOKEN", "my-session")

	provider := NewDoltHubProvider("api-token")
	if err := provider.Fork("steveyegge", "wl-commons", "alice-dev"); err != nil {
		t.Fatalf("Fork should skip creation when the fork already exists: %v", err)
	}
}

func TestDoltHubProvider_ForkREST_Success(t *testing.T) {
	// REST fork: POST returns operation_name, poll returns success.
	pollCount := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "token api-token" {
			t.Errorf("expected auth header, got %q", r.Header.Get("authorization"))
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding request: %v", err)
			}
			if body["ownerName"] != "alice-dev" {
				t.Errorf("ownerName = %q, want %q", body["ownerName"], "alice-dev")
			}
			if body["parentOwnerName"] != "steveyegge" {
				t.Errorf("parentOwnerName = %q, want %q", body["parentOwnerName"], "steveyegge")
			}
			if body["parentDatabaseName"] != "wl-commons" {
				t.Errorf("parentDatabaseName = %q, want %q", body["parentDatabaseName"], "wl-commons")
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"Success","operation_name":"fork-op-123"}`))
			return
		}
		if r.Method == "GET" && r.URL.Query().Get("operationName") == "fork-op-123" {
			pollCount++
			if pollCount < 2 {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"status":"Pending"}`))
				return
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"owner_name":"alice-dev","database_name":"wl-commons"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	provider := NewDoltHubProvider("api-token")
	err := provider.forkREST("steveyegge", "wl-commons", "alice-dev")
	if err != nil {
		t.Errorf("forkREST should succeed: %v", err)
	}
}

func TestDoltHubProvider_ForkREST_PollStatusSuccess(t *testing.T) {
	// REST fork: poll returns status "Success" without owner_name/database_name.
	pollCount := 0
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"Success","operation_name":"fork-op-456"}`))
			return
		}
		if r.Method == "GET" && r.URL.Query().Get("operationName") == "fork-op-456" {
			pollCount++
			if pollCount < 2 {
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{"status":"Pending"}`))
				return
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"Success"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	provider := NewDoltHubProvider("api-token")
	err := provider.forkREST("steveyegge", "wl-commons", "alice-dev")
	if err != nil {
		t.Errorf("forkREST should succeed on status-based completion: %v", err)
	}
}

func TestDoltHubProvider_ForkREST_PollErrorFallsBackToDatabaseExists(t *testing.T) {
	oldInitialBackoff := forkPollInitialBackoff
	oldMaxBackoff := forkPollMaxBackoff
	oldTimeout := forkPollTimeout
	forkPollInitialBackoff = time.Millisecond
	forkPollMaxBackoff = 2 * time.Millisecond
	forkPollTimeout = 20 * time.Millisecond
	defer func() {
		forkPollInitialBackoff = oldInitialBackoff
		forkPollMaxBackoff = oldMaxBackoff
		forkPollTimeout = oldTimeout
	}()

	var existsChecks atomic.Int32
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"Success","operation_name":"fork-op-authz"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/fork" && r.URL.Query().Get("operationName") == "fork-op-authz" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/alice-dev/wl-commons/main") {
			existsChecks.Add(1)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"query_execution_status":"Success"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	provider := NewDoltHubProvider("api-token")
	err := provider.forkREST("steveyegge", "wl-commons", "alice-dev")
	if err != nil {
		t.Fatalf("forkREST should succeed when the fork exists after a poll auth error: %v", err)
	}
	if existsChecks.Load() == 0 {
		t.Fatal("expected forkREST to check whether the fork database exists after poll failure")
	}
}

func TestDoltHubProvider_ForkREST_AlreadyExists(t *testing.T) {
	// REST fork: POST returns "already exists" error → treated as success.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"status":"Error","message":"database already exists"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	provider := NewDoltHubProvider("api-token")
	err := provider.forkREST("steveyegge", "wl-commons", "alice-dev")
	if err != nil {
		t.Errorf("forkREST should succeed for already-exists: %v", err)
	}
}

func TestDoltHubProvider_ForkREST_AuthError(t *testing.T) {
	// REST fork: auth error → falls back to exists-check → ForkRequiredError.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"status":"Error","message":"unauthorized"}`))
			return
		}
		// Exists-check for fallback: fork doesn't exist.
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"query_execution_status":"Error"}`))
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	provider := NewDoltHubProvider("bad-token")
	err := provider.forkREST("steveyegge", "wl-commons", "alice-dev")
	if err == nil {
		t.Fatal("expected ForkRequiredError, got nil")
	}
	var forkErr *ForkRequiredError
	if !errors.As(err, &forkErr) {
		t.Fatalf("expected ForkRequiredError, got %T: %v", err, err)
	}
}

func TestDoltHubProvider_Fork_NoSession_UsesREST(t *testing.T) {
	// When no session token, Fork dispatches to forkREST (not ForkRequiredError).
	gotRESTFork := false
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			gotRESTFork = true
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"Success","operation_name":""}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	t.Setenv("DOLTHUB_SESSION_TOKEN", "")

	provider := NewDoltHubProvider("api-token")
	err := provider.Fork("steveyegge", "wl-commons", "alice-dev")
	if err != nil {
		t.Errorf("Fork should succeed via REST: %v", err)
	}
	if !gotRESTFork {
		t.Error("expected Fork to use REST API, but no POST /fork was received")
	}
}

func TestDoltHubProvider_Fork_NoSession_ForkExists(t *testing.T) {
	// REST fork fails with auth error, but fork already exists → success.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"status":"Error","message":"forbidden"}`))
			return
		}
		// Exists-check fallback: fork exists.
		if r.Header.Get("authorization") != "token api-token" {
			t.Errorf("expected auth header, got %q", r.Header.Get("authorization"))
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"query_execution_status":"Success"}`))
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	t.Setenv("DOLTHUB_SESSION_TOKEN", "")

	provider := NewDoltHubProvider("api-token")
	err := provider.Fork("upstream-org", "wl-commons", "my-fork-org")
	if err != nil {
		t.Errorf("Fork should succeed when fork exists: %v", err)
	}
}

func TestDoltHubProvider_Fork_NoSession_ForkNotFound(t *testing.T) {
	// REST fork fails, fork doesn't exist → ForkRequiredError.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/fork") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"status":"Error","message":"forbidden"}`))
			return
		}
		// Exists-check fallback: fork not found.
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"query_execution_status":"Error","query_execution_message":"no such repository"}`))
	}))
	defer apiServer.Close()

	oldAPI := dolthubAPIBase
	dolthubAPIBase = apiServer.URL
	defer func() { dolthubAPIBase = oldAPI }()

	t.Setenv("DOLTHUB_SESSION_TOKEN", "")

	provider := NewDoltHubProvider("api-token")
	err := provider.Fork("hop", "wl-commons", "my-fork-org")
	if err == nil {
		t.Fatal("expected ForkRequiredError, got nil")
	}

	var forkErr *ForkRequiredError
	if !errors.As(err, &forkErr) {
		t.Fatalf("expected ForkRequiredError, got %T: %v", err, err)
	}
	if forkErr.UpstreamOrg != "hop" {
		t.Errorf("UpstreamOrg = %q, want %q", forkErr.UpstreamOrg, "hop")
	}
	if forkErr.UpstreamDB != "wl-commons" {
		t.Errorf("UpstreamDB = %q, want %q", forkErr.UpstreamDB, "wl-commons")
	}
	if forkErr.ForkOrg != "my-fork-org" {
		t.Errorf("ForkOrg = %q, want %q", forkErr.ForkOrg, "my-fork-org")
	}
}

func TestForkRequiredError_ForkURL(t *testing.T) {
	err := &ForkRequiredError{UpstreamOrg: "hop", UpstreamDB: "wl-commons", ForkOrg: "alice"}
	want := "https://www.dolthub.com/repositories/hop/wl-commons"
	if got := err.ForkURL(); got != want {
		t.Errorf("ForkURL() = %q, want %q", got, want)
	}
}

func TestDoltHubProvider_DatabaseURL(t *testing.T) {
	provider := NewDoltHubProvider("token")
	got := provider.DatabaseURL("steveyegge", "wl-commons")
	want := "https://doltremoteapi.dolthub.com/steveyegge/wl-commons"
	if got != want {
		t.Errorf("DatabaseURL = %q, want %q", got, want)
	}
}

func TestDoltHubProvider_Type(t *testing.T) {
	provider := NewDoltHubProvider("token")
	if got := provider.Type(); got != "dolthub" {
		t.Errorf("Type() = %q, want %q", got, "dolthub")
	}
}

func TestDoltHubProvider_FindPR(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "closed"},
				{"pull_id": "3", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/alice/fix-login",
			"from_branch_owner": "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/3", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/bob/add-feature",
			"from_branch_owner": "bob",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL

	provider := NewDoltHubProvider("token")

	// Find existing PR.
	url, id := provider.FindPR("org", "db", "bob", "wl/bob/add-feature")
	if id != "3" {
		t.Errorf("expected PR id 3, got %q", id)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}

	// No match.
	url, id = provider.FindPR("org", "db", "charlie", "wl/charlie/nope")
	if id != "" || url != "" {
		t.Errorf("expected empty result for non-matching PR, got url=%q id=%q", url, id)
	}
}

func TestDoltHubProvider_FindPR_Pagination(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pageToken") == "page2" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pulls": []map[string]any{
					{"pull_id": "5", "state": "open"},
				},
			})
		} else {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"pulls": []map[string]any{
					{"pull_id": "10", "state": "closed"},
				},
				"next_page_token": "page2",
			})
		}
	})
	mux.HandleFunc("/org/db/pulls/5", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL

	provider := NewDoltHubProvider("token")
	url, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001")
	if id != "5" {
		t.Errorf("expected PR id 5 from page 2, got %q", id)
	}
	if url == "" {
		t.Error("expected non-empty URL for paginated PR")
	}
}

func TestDoltHubProvider_FindPR_SkipsBadDetailAndKeepsSearching(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("boom"))
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob",
			"author":            "bob",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	url, id := provider.FindPR("org", "db", "bob", "wl/bob/w-002")
	if id != "2" || url == "" {
		t.Fatalf("FindPR() = (%q, %q), want PR 2", url, id)
	}
}

func TestDoltHubProvider_FindPR_UsesCachedIndex(t *testing.T) {
	var listCalls atomic.Int32
	var detailCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		detailCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		detailCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob",
			"author":            "bob",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	if _, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); id != "1" {
		t.Fatalf("first FindPR() id = %q, want 1", id)
	}
	if _, id := provider.FindPR("org", "db", "bob", "wl/bob/w-002"); id != "2" {
		t.Fatalf("second FindPR() id = %q, want 2", id)
	}
	if _, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); id != "1" {
		t.Fatalf("cached FindPR() id = %q, want 1", id)
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("pull list calls = %d, want 1", got)
	}
	if got := detailCalls.Load(); got != 2 {
		t.Fatalf("pull detail calls = %d, want 2", got)
	}
}

func TestDoltHubProvider_FindPR_DuplicateBranchUsesFirstPRAndAvoidsAmbiguousBranchCache(t *testing.T) {
	var listCalls atomic.Int32
	var detailCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	for _, pullID := range []string{"1", "2"} {
		mux.HandleFunc("/org/db/pulls/"+pullID, func(w http.ResponseWriter, _ *http.Request) {
			detailCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"from_branch":       "wl/bob/w-002",
				"from_branch_owner": "bob",
				"author":            "bob",
			})
		})
	}

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	firstURL, firstID := provider.FindPR("org", "db", "bob", "wl/bob/w-002")
	secondURL, secondID := provider.FindPR("org", "db", "bob", "wl/bob/w-002")
	if firstID != "1" || secondID != "1" {
		t.Fatalf("FindPR() duplicate branch ids = (%q, %q), want both 1", firstID, secondID)
	}
	if firstURL == "" || secondURL == "" {
		t.Fatal("expected non-empty PR URLs for duplicate-branch lookup")
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("pull list calls = %d, want 1", got)
	}
	if got := detailCalls.Load(); got != 2 {
		t.Fatalf("pull detail calls = %d, want 2", got)
	}
}

func TestDoltHubProvider_CreatePR_PrimesFindPRCache(t *testing.T) {
	var listCalls atomic.Int32
	var detailCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls/77", func(w http.ResponseWriter, _ *http.Request) {
		detailCalls.Add(1)
		t.Fatal("FindPR() should use the create-path cache without reading PR detail")
	})
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			listCalls.Add(1)
			t.Fatal("unexpected non-POST pull request")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"_id": "77",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	prURL, err := provider.CreatePR("alice", "org", "db", "wl/alice/w-003", "title", "body")
	if err != nil {
		t.Fatalf("CreatePR() error = %v", err)
	}
	if !strings.Contains(prURL, "/pulls/77") {
		t.Fatalf("CreatePR() url = %q, want /pulls/77", prURL)
	}
	if _, id := provider.FindPR("org", "db", "alice", "wl/alice/w-003"); id != "77" {
		t.Fatalf("FindPR() id = %q, want 77", id)
	}
	if got := listCalls.Load(); got != 0 {
		t.Fatalf("pull list calls = %d, want 0", got)
	}
	if got := detailCalls.Load(); got != 0 {
		t.Fatalf("pull detail calls = %d, want 0", got)
	}
}

func TestDoltHubProvider_ClosePR_InvalidatesFindPRCache(t *testing.T) {
	var listCalls atomic.Int32
	var detailCalls atomic.Int32
	open := true

	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		pulls := []map[string]any{}
		if open {
			pulls = append(pulls, map[string]any{"pull_id": "1", "state": "open"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pulls": pulls})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			open = false
			w.WriteHeader(http.StatusOK)
			return
		}
		detailCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice",
			"author":            "alice",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	if _, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); id != "1" {
		t.Fatalf("warm FindPR() id = %q, want 1", id)
	}
	if err := provider.ClosePR("org", "db", "1"); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if url, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); url != "" || id != "" {
		t.Fatalf("FindPR() after close = (%q, %q), want empty", url, id)
	}
	if got := listCalls.Load(); got != 2 {
		t.Fatalf("pull list calls = %d, want 2", got)
	}
	if got := detailCalls.Load(); got != 1 {
		t.Fatalf("pull detail calls = %d, want 1", got)
	}
}

func TestDoltHubProvider_FindPR_CacheExpiresAfterExternalClose(t *testing.T) {
	var listCalls atomic.Int32
	var detailCalls atomic.Int32
	open := true

	oldTTL := prCacheTTL
	prCacheTTL = 20 * time.Millisecond
	defer func() { prCacheTTL = oldTTL }()

	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		pulls := []map[string]any{}
		if open {
			pulls = append(pulls, map[string]any{"pull_id": "1", "state": "open"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pulls": pulls})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		detailCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice",
			"author":            "alice",
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	if _, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); id != "1" {
		t.Fatalf("warm FindPR() id = %q, want 1", id)
	}

	open = false
	time.Sleep(3 * prCacheTTL)

	if url, id := provider.FindPR("org", "db", "alice", "wl/alice/w-001"); url != "" || id != "" {
		t.Fatalf("FindPR() after external close = (%q, %q), want empty", url, id)
	}
	if got := listCalls.Load(); got != 2 {
		t.Fatalf("pull list calls = %d, want 2 after cache expiry", got)
	}
	if got := detailCalls.Load(); got != 1 {
		t.Fatalf("pull detail calls = %d, want 1", got)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upstream-org/wl-commons/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
				{"pull_id": "3", "state": "closed"},
			},
		})
	})
	mux.HandleFunc("/upstream-org/wl-commons/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/fix-login",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/upstream-org/wl-commons/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/add-feature",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	// Fork queries return dolt_diff results — only actually-changed rows.
	// Also serve completions queries for in_review entries.
	mux.HandleFunc("/alice-fork/wl-commons/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "FROM completions") {
			// alice's PR is "claimed", not in_review — no completions query expected.
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]string{}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "fix-login", "status": "claimed", "claimed_by": "alice", "diff_type": "modified"},
			},
		})
	})
	mux.HandleFunc("/bob-fork/wl-commons/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "FROM completions") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rows": []map[string]string{
					{"completed_by": "bob", "evidence": "https://github.com/bob/pr/42"},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "add-feature", "status": "in_review", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("upstream-org", "wl-commons")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}

	if len(ids) != 2 {
		t.Fatalf("expected 2 pending IDs, got %d: %v", len(ids), ids)
	}
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].RigHandle != "alice" {
		t.Errorf("expected fix-login → alice, got %+v", pending)
	}
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].Status != "claimed" {
		t.Errorf("expected fix-login status=claimed, got %+v", pending)
	}
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].ClaimedBy != "alice" {
		t.Errorf("expected fix-login claimed_by=alice, got %+v", pending)
	}
	if pending := ids["add-feature"]; len(pending) != 1 || pending[0].RigHandle != "bob" {
		t.Errorf("expected add-feature → bob, got %+v", pending)
	}
	if pending := ids["add-feature"]; len(pending) != 1 || pending[0].Status != "in_review" {
		t.Errorf("expected add-feature status=in_review, got %+v", pending)
	}
	// Verify completions fields are populated for in_review entry.
	if pending := ids["add-feature"]; len(pending) != 1 || pending[0].CompletedBy != "bob" {
		t.Errorf("expected add-feature CompletedBy=bob, got %+v", pending)
	}
	if pending := ids["add-feature"]; len(pending) != 1 || pending[0].Evidence != "https://github.com/bob/pr/42" {
		t.Errorf("expected add-feature Evidence, got %+v", pending)
	}
	// Verify claimed entry has no completions fields.
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].CompletedBy != "" {
		t.Errorf("expected fix-login CompletedBy empty, got %+v", pending)
	}
	// Verify URLs are populated.
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].PRURL == "" {
		t.Error("expected non-empty PRURL for fix-login")
	}
	if pending := ids["fix-login"]; len(pending) != 1 || pending[0].BranchURL == "" {
		t.Error("expected non-empty BranchURL for fix-login")
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_BatchesCompletionQueriesPerBranch(t *testing.T) {
	mux := http.NewServeMux()
	var completionCalls atomic.Int32
	var diffCalls atomic.Int32

	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{{"pull_id": "1", "state": "open"}},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/review",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "FROM completions") {
			completionCalls.Add(1)
			if !strings.Contains(q, "'w-001'") || !strings.Contains(q, "'w-002'") {
				t.Fatalf("batched completions query missing wanted IDs: %s", q)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"rows": []map[string]string{
					{"wanted_id": "w-001", "completed_by": "bob", "evidence": "https://example.com/1"},
					{"wanted_id": "w-002", "completed_by": "bob", "evidence": "https://example.com/2"},
				},
			})
			return
		}
		diffCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "in_review", "claimed_by": "bob", "diff_type": "modified"},
				{"id": "w-002", "status": "completed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if got := diffCalls.Load(); got != 1 {
		t.Fatalf("diff query count = %d, want 1", got)
	}
	if got := completionCalls.Load(); got != 1 {
		t.Fatalf("completion query count = %d, want 1", got)
	}
	if pending := ids["w-001"]; len(pending) != 1 || pending[0].CompletedBy != "bob" || pending[0].Evidence != "https://example.com/1" {
		t.Fatalf("w-001 pending = %+v, want batched completion data", pending)
	}
	if pending := ids["w-002"]; len(pending) != 1 || pending[0].CompletedBy != "bob" || pending[0].Evidence != "https://example.com/2" {
		t.Fatalf("w-002 pending = %+v, want batched completion data", pending)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_DeduplicatesBranchQueriesForDuplicatePRs(t *testing.T) {
	mux := http.NewServeMux()
	var diffCalls atomic.Int32

	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	for _, pullID := range []string{"1", "2"} {
		mux.HandleFunc("/org/db/pulls/"+pullID, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"from_branch":       "wl/bob/w-001",
				"from_branch_owner": "bob-fork",
				"author":            "bob",
			})
		})
	}
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Query().Get("q"), "FROM completions") {
			_ = json.NewEncoder(w).Encode(map[string]any{"rows": []map[string]string{}})
			return
		}
		diffCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if got := diffCalls.Load(); got != 1 {
		t.Fatalf("diff query count = %d, want 1", got)
	}
	pending := ids["w-001"]
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending states for duplicate PRs, got %+v", pending)
	}
	prURLs := map[string]bool{}
	for _, state := range pending {
		prURLs[state.PRURL] = true
	}
	if len(prURLs) != 2 {
		t.Fatalf("expected distinct PR URLs for duplicate PRs, got %+v", pending)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_ForkQueryFails_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	// Fork query returns 404 (fork deleted) for PR 1. Fail closed rather than
	// returning a partial pending set that can be cached as authoritative.
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err == nil || !strings.Contains(err.Error(), "reading fork diff for PR 1") {
		t.Fatalf("ListPendingWantedIDs() error = %v, want fork diff failure", err)
	}
	if ids != nil {
		t.Fatalf("ids = %+v, want nil on fork diff failure", ids)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_PRDetailFails_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("boom"))
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err == nil || !strings.Contains(err.Error(), "reading PR 1 detail") {
		t.Fatalf("ListPendingWantedIDs() error = %v, want PR detail failure", err)
	}
	if ids != nil {
		t.Fatalf("ids = %+v, want nil on PR detail failure", ids)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_UpstreamSnapshotFails_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "main",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/org/db/main", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "in_review", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err == nil || !strings.Contains(err.Error(), "reading upstream snapshot") {
		t.Fatalf("ListPendingWantedIDs() error = %v, want upstream snapshot failure", err)
	}
	if ids != nil {
		t.Fatalf("ids = %+v, want nil on upstream snapshot failure", ids)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_MissingForkSnapshotSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "main",
			"from_branch_owner": "missing-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/org/db/main", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "open", "claimed_by": ""},
			},
		})
	})
	mux.HandleFunc("/missing-fork/db/main", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"query_execution_status":"Error","query_execution_message":"no such repository"}`))
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 pending ID after skipping missing fork snapshot, got %d: %v", len(ids), ids)
	}
	if got := ids["w-002"]; len(got) != 1 || got[0].RigHandle != "bob" || got[0].Status != "claimed" {
		t.Fatalf("unexpected pending state after skip: %+v", ids["w-002"])
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_MissingForkDiffSkipped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "missing-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-002",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/missing-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"query_execution_status":"Error","query_execution_message":"no such repository"}`))
	})
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-002", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 pending ID after skipping missing fork diff, got %d: %v", len(ids), ids)
	}
	if got := ids["w-002"]; len(got) != 1 || got[0].RigHandle != "bob" || got[0].Status != "claimed" {
		t.Fatalf("unexpected pending state after skip: %+v", ids["w-002"])
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_CompletionQueryFails_GracefulDegradation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/w-001",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	requestCount := 0
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "FROM completions") {
			// Completions query fails.
			w.WriteHeader(500)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "in_review", "claimed_by": "alice", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	// The entry should still exist — just without completion data.
	if len(ids) != 1 {
		t.Fatalf("expected 1 pending ID, got %d", len(ids))
	}
	pending := ids["w-001"]
	if len(pending) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(pending))
	}
	if pending[0].Status != "in_review" {
		t.Errorf("expected status=in_review, got %s", pending[0].Status)
	}
	// Completions fields should be empty (graceful degradation).
	if pending[0].CompletedBy != "" {
		t.Errorf("expected empty CompletedBy on failure, got %q", pending[0].CompletedBy)
	}
	if pending[0].Evidence != "" {
		t.Errorf("expected empty Evidence on failure, got %q", pending[0].Evidence)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_BatchedCompletionFailure_GracefulDegradation(t *testing.T) {
	mux := http.NewServeMux()
	var completionCalls atomic.Int32

	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{{"pull_id": "1", "state": "open"}},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/review",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query().Get("q")
		if strings.Contains(q, "FROM completions") {
			completionCalls.Add(1)
			if !strings.Contains(q, "'w-001'") || !strings.Contains(q, "'w-002'") {
				t.Fatalf("batched completions query missing wanted IDs: %s", q)
			}
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "in_review", "claimed_by": "alice", "diff_type": "modified"},
				{"id": "w-002", "status": "completed", "claimed_by": "alice", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	oldAPIBase, oldRepoBase := dolthubAPIBase, dolthubRepoBase
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"
	defer func() {
		dolthubAPIBase = oldAPIBase
		dolthubRepoBase = oldRepoBase
	}()

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if got := completionCalls.Load(); got == 0 {
		t.Fatal("expected at least one batched completion query attempt")
	}
	for _, wantedID := range []string{"w-001", "w-002"} {
		pending := ids[wantedID]
		if len(pending) != 1 {
			t.Fatalf("%s entries = %+v, want 1", wantedID, pending)
		}
		if pending[0].CompletedBy != "" || pending[0].Evidence != "" {
			t.Fatalf("%s completion fields = %+v, want graceful degradation", wantedID, pending[0])
		}
	}
}

func TestDoltHubProvider_dolthubGet_StopsRetryingOnCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	provider := NewDoltHubProviderWithClient(server.Client()).WithContext(ctx)
	start := time.Now()
	_, err := provider.dolthubGet(server.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dolthubGet() error = %v, want context.DeadlineExceeded", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("dolthubGet() took %v, want prompt cancellation", elapsed)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_StaleEntriesFiltered(t *testing.T) {
	// Stale fork state should be filtered:
	// 1. status "open" = untouched item (unless the row is newly added)
	// 2. claimed_by someone other than PR author = inherited claim
	// Only intentional actions (claimed_by == author) pass through.
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
				{"pull_id": "2", "state": "open"},
				{"pull_id": "3", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/register/stale-user",
			"from_branch_owner": "stale-fork",
			"author":            "stale-user",
		})
	})
	mux.HandleFunc("/org/db/pulls/2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/bob/w-001",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	mux.HandleFunc("/org/db/pulls/3", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/register/charlie",
			"from_branch_owner": "charlie-fork",
			"author":            "charlie",
		})
	})
	// stale-user: dolt_diff shows w-001 at "open" (stale untouched).
	mux.HandleFunc("/stale-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "open", "claimed_by": "", "diff_type": "modified"},
			},
		})
	})
	// bob: dolt_diff shows w-001 at "claimed" by bob (intentional action).
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "claimed", "claimed_by": "bob", "diff_type": "modified"},
			},
		})
	})
	// charlie: dolt_diff shows w-001 at "claimed" by alice (inherited, not charlie's).
	mux.HandleFunc("/charlie-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-001", "status": "claimed", "claimed_by": "alice", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	// Only bob's "claimed" entry should appear:
	// - stale-user filtered (status "open")
	// - charlie filtered (claimed_by "alice" != author "charlie")
	if len(ids) != 1 {
		t.Fatalf("expected 1 pending ID (stale filtered), got %d: %v", len(ids), ids)
	}
	pending := ids["w-001"]
	if len(pending) != 1 {
		t.Fatalf("expected 1 entry for w-001, got %d", len(pending))
	}
	if pending[0].RigHandle != "bob" {
		t.Errorf("expected rig_handle=bob, got %s", pending[0].RigHandle)
	}
	if pending[0].Status != "claimed" {
		t.Errorf("expected status=claimed, got %s", pending[0].Status)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_NewOpenItemIncluded(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "wl/alice/w-new",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-new", "status": "open", "claimed_by": "", "diff_type": "added"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}

	pending := ids["w-new"]
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending entry for w-new, got %+v", pending)
	}
	if pending[0].Status != "open" {
		t.Errorf("expected status=open, got %+v", pending[0])
	}
	if pending[0].RigHandle != "alice" {
		t.Errorf("expected rig_handle=alice, got %+v", pending[0])
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_NoDiffs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "feature/other-work",
			"from_branch_owner": "someone",
			"author":            "someone",
		})
	})
	// Fork branch has no diffs vs its own main → dolt_diff returns empty.
	mux.HandleFunc("/someone/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected 0 pending IDs when fork matches upstream, got %d", len(ids))
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_NonStandardBranch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "complete/w-com-001",
			"from_branch_owner": "alice-fork",
			"author":            "alice",
		})
	})
	// Fork branch dolt_diff shows w-com-001 was actually modified on the branch.
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-com-001", "status": "completed", "claimed_by": "alice", "diff_type": "modified"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("expected 1 pending ID, got %d", len(ids))
	}
	pending := ids["w-com-001"]
	if len(pending) != 1 {
		t.Fatalf("expected 1 entry for w-com-001, got %d", len(pending))
	}
	if pending[0].RigHandle != "alice" {
		t.Errorf("expected rig_handle=alice, got %s", pending[0].RigHandle)
	}
	if pending[0].Status != "completed" {
		t.Errorf("expected status=completed, got %s", pending[0].Status)
	}
}

func TestDoltHubProvider_ListPendingWantedIDs_PRFromMain(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/org/db/pulls", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/pulls/") {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"pulls": []map[string]any{
				{"pull_id": "1", "state": "open"},
			},
		})
	})
	mux.HandleFunc("/org/db/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"from_branch":       "main",
			"from_branch_owner": "bob-fork",
			"author":            "bob",
		})
	})
	// Upstream baseline — needed for PRs from "main" (snapshot fallback).
	mux.HandleFunc("/org/db/main", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-com-001", "status": "open", "claimed_by": ""},
				{"id": "w-com-002", "status": "open", "claimed_by": ""},
			},
		})
	})
	// Fork main has multiple changes (common for manual DoltHub edits).
	// Snapshot comparison against upstream main catches these.
	mux.HandleFunc("/bob-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rows": []map[string]string{
				{"id": "w-com-001", "status": "claimed", "claimed_by": "bob"},
				{"id": "w-com-002", "status": "completed", "claimed_by": "bob"},
			},
		})
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	dolthubAPIBase = server.URL
	dolthubRepoBase = server.URL + "/repositories"

	provider := NewDoltHubProvider("token")
	ids, err := provider.ListPendingWantedIDs("org", "db")
	if err != nil {
		t.Fatalf("ListPendingWantedIDs() error: %v", err)
	}
	// Both items changed — data-diff catches them even from a "main" branch PR.
	if len(ids) != 2 {
		t.Fatalf("expected 2 pending IDs, got %d: %v", len(ids), ids)
	}
	if pending := ids["w-com-001"]; len(pending) != 1 || pending[0].RigHandle != "bob" {
		t.Errorf("expected w-com-001 → bob, got %+v", pending)
	}
	if pending := ids["w-com-002"]; len(pending) != 1 || pending[0].Status != "completed" {
		t.Errorf("expected w-com-002 status=completed, got %+v", pending)
	}
}
