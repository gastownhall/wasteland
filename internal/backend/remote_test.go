package backend

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	old := DoltHubAPIBase
	DoltHubAPIBase = srv.URL
	return srv, func() {
		DoltHubAPIBase = old
		srv.Close()
	}
}

func TestRemoteDB_Query_Main(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify path: /{readOwner}/{readDB}/main
		if !strings.Contains(r.URL.Path, "/upstream-org/wl-commons/main") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("authorization") != "token test-token" {
			t.Errorf("missing auth header")
		}
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "id", "columnType": "varchar(20)"},
				{"columnName": "status", "columnType": "varchar(20)"},
			},
			"rows": []map[string]string{
				{"id": "w-001", "status": "open"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	csv, err := db.Query("SELECT id, status FROM wanted", "")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), csv)
	}
	if lines[1] != "w-001,open" {
		t.Errorf("row = %q, want %q", lines[1], "w-001,open")
	}
}

func TestRemoteDB_Query_Branch(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Branch refs should route to the fork database.
		if !strings.Contains(r.URL.Path, "/fork-org/wl-commons/wl/alice/w-001") {
			t.Errorf("unexpected path for branch query: %s", r.URL.Path)
		}
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "status", "columnType": "varchar(20)"},
			},
			"rows": []map[string]string{
				{"status": "claimed"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	csv, err := db.Query("SELECT status FROM wanted WHERE id='w-001'", "wl/alice/w-001")
	if err != nil {
		t.Fatalf("Query error: %v", err)
	}

	if !strings.Contains(csv, "claimed") {
		t.Errorf("expected 'claimed' in output, got: %q", csv)
	}
}

func TestRemoteDB_Exec(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			// Branch existence check — branch does not exist yet.
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "cnt", "columnType": "int"},
				},
				"rows": []map[string]string{
					{"cnt": "0"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.Method == "POST" {
			// First write: branch doesn't exist, so write from main.
			if !strings.Contains(r.URL.RawPath, "/fork-org/wl-commons/write/main/wl%2Falice%2Fw-001") {
				t.Errorf("unexpected write path: %s (raw: %s)", r.URL.Path, r.URL.RawPath)
			}
			resp := map[string]string{
				"query_execution_status": "Success",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("unexpected method: %s", r.Method)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.Exec("wl/alice/w-001", "wl claim: w-001", false,
		"UPDATE wanted SET status='claimed' WHERE id='w-001'")
	if err != nil {
		t.Fatalf("Exec error: %v", err)
	}
}

func TestRemoteDB_Exec_SequentialMutation(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			// Branch existence check — branch already exists.
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "cnt", "columnType": "int"},
				},
				"rows": []map[string]string{
					{"cnt": "1"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		if r.Method == "POST" {
			// Second write: branch exists, so write from branch (not main).
			if !strings.Contains(r.URL.RawPath, "/write/wl%2Falice%2Fw-001/wl%2Falice%2Fw-001") {
				t.Errorf("expected write from branch, got: %s (raw: %s)", r.URL.Path, r.URL.RawPath)
			}
			resp := map[string]string{
				"query_execution_status": "Success",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("unexpected method: %s", r.Method)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.Exec("wl/alice/w-001", "wl done: w-001", false,
		"UPDATE wanted SET status='in_review' WHERE id='w-001'")
	if err != nil {
		t.Fatalf("Exec sequential error: %v", err)
	}
}

func TestRemoteDB_Exec_MainBranch(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Empty branch should default to main.
		if !strings.Contains(r.URL.Path, "/write/main/main") {
			t.Errorf("expected write/main/main path, got: %s", r.URL.Path)
		}
		resp := map[string]string{
			"query_execution_status": "Success",
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.Exec("", "wl claim: w-001", false,
		"UPDATE wanted SET status='claimed' WHERE id='w-001'")
	if err != nil {
		t.Fatalf("Exec error: %v", err)
	}
}

func TestRemoteDB_Branches(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "name", "columnType": "varchar(255)"},
			},
			"rows": []map[string]string{
				{"name": "wl/alice/w-001"},
				{"name": "wl/alice/w-002"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	branches, err := db.Branches("wl/alice/")
	if err != nil {
		t.Fatalf("Branches error: %v", err)
	}
	if len(branches) != 2 {
		t.Fatalf("expected 2 branches, got %d", len(branches))
	}
	if branches[0] != "wl/alice/w-001" {
		t.Errorf("branch[0] = %q, want %q", branches[0], "wl/alice/w-001")
	}
}

func TestRemoteDB_PushNoOps(t *testing.T) {
	t.Parallel()
	db := NewRemoteDB("token", "up", "db", "fork", "db")

	if err := db.PushBranch("branch", nil); err != nil {
		t.Errorf("PushBranch should be no-op, got: %v", err)
	}
	if err := db.PushMain(nil); err != nil {
		t.Errorf("PushMain should be no-op, got: %v", err)
	}
	if err := db.PushAllRemotes(nil); err != nil {
		t.Errorf("PushAllRemotes should be no-op, got: %v", err)
	}
	if err := db.CanWildWest(); err == nil {
		t.Error("CanWildWest should return error for remote DB")
	}
}

func TestRemoteDB_Exec_Poll(t *testing.T) {
	pollCount := 0
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			resp := map[string]string{
				"operation_name": "op-123",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// GET: distinguish branch-existence check from poll
		if strings.Contains(r.URL.Path, "/write") {
			// Poll
			pollCount++
			if pollCount < 2 {
				resp := map[string]string{
					"query_execution_status": "Running",
				}
				_ = json.NewEncoder(w).Encode(resp)
				return
			}
			resp := map[string]string{
				"query_execution_status": "Success",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// Branch existence check — branch doesn't exist
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "cnt", "columnType": "int"},
			},
			"rows": []map[string]string{
				{"cnt": "0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.Exec("some-branch", "test", false, "UPDATE wanted SET status='open'")
	if err != nil {
		t.Fatalf("Exec with poll error: %v", err)
	}
	if pollCount < 2 {
		t.Errorf("expected at least 2 polls, got %d", pollCount)
	}
}

func TestRemoteDB_Exec_Poll_DoneResDetails(t *testing.T) {
	pollCount := 0
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			resp := map[string]string{
				"operation_name": "users/testuser/userOperations/abc-123",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		// GET: distinguish branch-existence check from poll
		if strings.Contains(r.URL.Path, "/write") {
			// Poll — return the current DoltHub response format with done + res_details.
			pollCount++
			if pollCount < 2 {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"done": false,
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"done": true,
				"res_details": map[string]string{
					"query_execution_status":  "Success",
					"query_execution_message": "Query OK, 1 row affected.",
				},
			})
			return
		}
		// Branch existence check — branch doesn't exist
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "cnt", "columnType": "int"},
			},
			"rows": []map[string]string{
				{"cnt": "0"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.Exec("some-branch", "test", false, "INSERT INTO wanted VALUES (...)")
	if err != nil {
		t.Fatalf("Exec with done+res_details poll error: %v", err)
	}
	if pollCount < 2 {
		t.Errorf("expected at least 2 polls, got %d", pollCount)
	}
}

func TestRemoteDB_Query_Error(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "internal server error")
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	_, err := db.Query("SELECT 1", "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestRemoteDB_Sync_NoOp(t *testing.T) {
	// Sync is a no-op for remote — DoltHub's hosted SQL API does not support
	// remote operations (dolt_remotes, DOLT_FETCH). Reads go directly to the
	// upstream API so fork-level sync is unnecessary.
	t.Parallel()
	db := NewRemoteDB("token", "up", "db", "fork", "db")
	if err := db.Sync(); err != nil {
		t.Errorf("Sync should be no-op, got: %v", err)
	}
}

func TestRemoteDB_MergeBranch(t *testing.T) {
	var writtenSQL string

	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writtenSQL = r.URL.Query().Get("q")
			if !strings.Contains(r.URL.Path, "/write/main/main") {
				t.Errorf("expected write on main, got: %s", r.URL.Path)
			}
			resp := map[string]string{"query_execution_status": "Success"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("unexpected method: %s", r.Method)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	if err := db.MergeBranch("wl/alice/w-001"); err != nil {
		t.Fatalf("MergeBranch error: %v", err)
	}

	if !strings.Contains(writtenSQL, "DOLT_MERGE") {
		t.Errorf("expected DOLT_MERGE in SQL, got: %s", writtenSQL)
	}
	if !strings.Contains(writtenSQL, "wl/alice/w-001") {
		t.Errorf("expected branch name in SQL, got: %s", writtenSQL)
	}
}

func TestRemoteDB_DeleteBranch(t *testing.T) {
	var writtenSQL string
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writtenSQL = r.URL.Query().Get("q")
			resp := map[string]string{"query_execution_status": "Success"}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("unexpected method: %s", r.Method)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	if err := db.DeleteBranch("wl/alice/w-001"); err != nil {
		t.Fatalf("DeleteBranch error: %v", err)
	}
	if !strings.Contains(writtenSQL, "DOLT_BRANCH") {
		t.Errorf("expected DOLT_BRANCH in SQL, got: %s", writtenSQL)
	}
	if !strings.Contains(writtenSQL, "wl/alice/w-001") {
		t.Errorf("expected branch name in SQL, got: %s", writtenSQL)
	}
}

func TestRemoteDB_DeleteBranch_ReturnsError(t *testing.T) {
	// When DOLT_BRANCH fails, DeleteBranch should return the error so the
	// caller can fall back to clearing item data from the branch.
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			resp := map[string]string{
				"query_execution_status":  "Error",
				"query_execution_message": "stored procedure not found",
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		t.Errorf("unexpected method: %s", r.Method)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	err := db.DeleteBranch("wl/alice/w-001")
	if err == nil {
		t.Fatal("expected error when DOLT_BRANCH fails")
	}
	if !strings.Contains(err.Error(), "stored procedure not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoteDB_DeleteBranch_MainNoOp(t *testing.T) {
	// Deleting main or empty branch should be a no-op.
	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	if err := db.DeleteBranch("main"); err != nil {
		t.Fatalf("DeleteBranch(main) error: %v", err)
	}
	if err := db.DeleteBranch(""); err != nil {
		t.Fatalf("DeleteBranch('') error: %v", err)
	}
}

func TestRemoteDB_Diff(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")

		if strings.Contains(q, "dolt_diff_stat(") {
			// Return one changed table.
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "table_name", "columnType": "varchar(255)"},
					{"columnName": "rows_added", "columnType": "int"},
					{"columnName": "rows_modified", "columnType": "int"},
					{"columnName": "rows_deleted", "columnType": "int"},
				},
				"rows": []map[string]string{
					{"table_name": "wanted", "rows_added": "0", "rows_modified": "1", "rows_deleted": "0"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(q, "dolt_diff(") {
			// Return one modified row.
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "diff_type", "columnType": "varchar(20)"},
					{"columnName": "from_id", "columnType": "varchar(20)"},
					{"columnName": "to_id", "columnType": "varchar(20)"},
					{"columnName": "from_status", "columnType": "varchar(20)"},
					{"columnName": "to_status", "columnType": "varchar(20)"},
					{"columnName": "from_commit", "columnType": "varchar(64)"},
					{"columnName": "to_commit", "columnType": "varchar(64)"},
				},
				"rows": []map[string]string{
					{
						"diff_type":   "modified",
						"from_id":     "w-001",
						"to_id":       "w-001",
						"from_status": "open",
						"to_status":   "claimed",
						"from_commit": "abc123",
						"to_commit":   "def456",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(500)
		fmt.Fprint(w, "unexpected query: "+q)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	diff, err := db.Diff("wl/alice/w-001")
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	if !strings.Contains(diff, "wanted") {
		t.Errorf("expected 'wanted' table in diff, got: %q", diff)
	}
	if !strings.Contains(diff, "modified") {
		t.Errorf("expected 'modified' in diff, got: %q", diff)
	}
	if !strings.Contains(diff, "open") && !strings.Contains(diff, "claimed") {
		t.Errorf("expected status change in diff, got: %q", diff)
	}
}

func TestRemoteDB_Diff_FieldsWithCommas(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")

		if strings.Contains(q, "dolt_diff_stat(") {
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "table_name", "columnType": "varchar(255)"},
					{"columnName": "rows_added", "columnType": "int"},
					{"columnName": "rows_modified", "columnType": "int"},
					{"columnName": "rows_deleted", "columnType": "int"},
				},
				"rows": []map[string]string{
					{"table_name": "wanted", "rows_added": "0", "rows_modified": "1", "rows_deleted": "0"},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		if strings.Contains(q, "dolt_diff(") {
			// Return a row with commas in description and JSON array in tags.
			resp := map[string]any{
				"query_execution_status": "Success",
				"schema_fragment": []map[string]string{
					{"columnName": "diff_type", "columnType": "varchar(20)"},
					{"columnName": "from_id", "columnType": "varchar(20)"},
					{"columnName": "to_id", "columnType": "varchar(20)"},
					{"columnName": "from_status", "columnType": "varchar(20)"},
					{"columnName": "to_status", "columnType": "varchar(20)"},
					{"columnName": "from_description", "columnType": "text"},
					{"columnName": "to_description", "columnType": "text"},
					{"columnName": "from_tags", "columnType": "text"},
					{"columnName": "to_tags", "columnType": "text"},
				},
				"rows": []map[string]string{
					{
						"diff_type":        "modified",
						"from_id":          "w-001",
						"to_id":            "w-001",
						"from_status":      "open",
						"to_status":        "claimed",
						"from_description": "Design the YAML format for roles, capabilities, and constraints.",
						"to_description":   "Design the YAML format for roles, capabilities, and constraints.",
						"from_tags":        `["design","roles","YAML"]`,
						"to_tags":          `["design","roles","YAML"]`,
					},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.WriteHeader(500)
		fmt.Fprint(w, "unexpected query: "+q)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	diff, err := db.Diff("wl/alice/w-001")
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	// Status should show the change correctly.
	if !strings.Contains(diff, "open") || !strings.Contains(diff, "claimed") {
		t.Errorf("expected status change in diff, got: %q", diff)
	}
	// Tags with commas should not corrupt column alignment.
	if strings.Contains(diff, "→ claimed\n  project:") {
		t.Error("garbled output detected: tag commas corrupted column alignment")
	}
}

func TestRemoteDB_Diff_NoChanges(t *testing.T) {
	srv, cleanup := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		// Return empty diff_stat.
		resp := map[string]any{
			"query_execution_status": "Success",
			"schema_fragment": []map[string]string{
				{"columnName": "table_name", "columnType": "varchar(255)"},
				{"columnName": "rows_added", "columnType": "int"},
				{"columnName": "rows_modified", "columnType": "int"},
				{"columnName": "rows_deleted", "columnType": "int"},
			},
			"rows": []map[string]string{},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	db := NewRemoteDB("test-token", "upstream-org", "wl-commons", "fork-org", "wl-commons")
	db.client = srv.Client()

	diff, err := db.Diff("wl/alice/w-001")
	if err != nil {
		t.Fatalf("Diff error: %v", err)
	}

	if !strings.Contains(diff, "no changes") {
		t.Errorf("expected 'no changes' message, got: %q", diff)
	}
}
