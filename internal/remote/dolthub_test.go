package remote

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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

func TestDoltHubProvider_ListPendingWantedIDs_ForkQueryFails_Skipped(t *testing.T) {
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
	// Fork query returns 404 (fork deleted) — can't determine diffs.
	mux.HandleFunc("/alice-fork/db/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("not found"))
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
	// Fork query failed → no diffs can be determined → PR skipped.
	if len(ids) != 0 {
		t.Errorf("expected 0 pending IDs when fork query fails, got %d: %v", len(ids), ids)
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

func TestDoltHubProvider_ListPendingWantedIDs_StaleEntriesFiltered(t *testing.T) {
	// Stale fork state should be filtered:
	// 1. status "open" = untouched item
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
