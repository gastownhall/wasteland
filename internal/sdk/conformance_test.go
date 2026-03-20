//go:build integration

package sdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/schema"
)

var doltPath string

func TestMain(m *testing.M) {
	var err error
	doltPath, err = exec.LookPath("dolt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt not found in PATH — skipping conformance tests\n")
		os.Exit(0)
	}

	// Isolated dolt HOME so tests don't touch user config.
	tmpHome, err := os.MkdirTemp("", "sdk-conformance-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp home: %v\n", err)
		os.Exit(1)
	}
	doltCfg := filepath.Join(tmpHome, ".dolt")
	if err := os.MkdirAll(doltCfg, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "creating dolt config dir: %v\n", err)
		os.Exit(1)
	}
	cfg := `{"user.name":"conformance-test","user.email":"test@example.com","user.creds":""}` + "\n"
	if err := os.WriteFile(filepath.Join(doltCfg, "config_global.json"), []byte(cfg), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "writing dolt config: %v\n", err)
		os.Exit(1)
	}

	os.Setenv("HOME", tmpHome)
	os.Setenv("DOLT_ROOT_PATH", tmpHome)

	code := m.Run()
	os.RemoveAll(tmpHome)
	os.Exit(code)
}

// --- backend factories ---

type dbFactory struct {
	name  string
	setup func(t *testing.T) commons.DB
}

func dbFactories() []dbFactory {
	return []dbFactory{
		{"fakeDB", func(t *testing.T) commons.DB { return newFakeDB() }},
		{"LocalDB", func(t *testing.T) commons.DB { return setupLocalDB(t) }},
	}
}

func setupLocalDB(t *testing.T) commons.DB {
	t.Helper()
	dir := t.TempDir()

	// dolt init
	cmd := exec.Command(doltPath, "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dolt init: %v\n%s", err, out)
	}

	// Run schema DDL + commit.
	initScript := schema.SQL + "\nCALL DOLT_ADD('-A');\nCALL DOLT_COMMIT('-m', 'init schema');\n"
	if err := commons.DoltSQLScript(dir, initScript); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	return backend.NewLocalDB(dir, backend.WildWestSync)
}

// --- seeding helpers ---

func seedConformanceItem(t *testing.T, db commons.DB, id, title, postedBy string) {
	t.Helper()
	item := &commons.WantedItem{
		ID: id, Title: title, PostedBy: postedBy,
		EffortLevel: "medium",
	}
	dml, err := commons.InsertWantedDML(item)
	if err != nil {
		t.Fatalf("InsertWantedDML: %v", err)
	}
	if err := db.Exec("", "seed: "+id, false, dml); err != nil {
		t.Fatalf("seed item %s: %v", id, err)
	}
}

func seedClaimedItem(t *testing.T, db commons.DB, id, postedBy, claimedBy string) {
	t.Helper()
	seedConformanceItem(t, db, id, "Test "+id, postedBy)
	if err := db.Exec("", "claim: "+id, false, commons.ClaimWantedDML(id, claimedBy)); err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
}

func seedInReviewItem(t *testing.T, db commons.DB, id, postedBy, claimedBy string) (completionID string) {
	t.Helper()
	seedClaimedItem(t, db, id, postedBy, claimedBy)
	completionID = fmt.Sprintf("c-%s", id)
	stmts := commons.SubmitCompletionDML(completionID, id, claimedBy, "http://example.com/evidence", "")
	if err := db.Exec("", "done: "+id, false, stmts...); err != nil {
		t.Fatalf("done %s: %v", id, err)
	}
	return completionID
}

func seedCompletedItem(t *testing.T, db commons.DB, id, postedBy, claimedBy string) {
	t.Helper()
	seedInReviewItem(t, db, id, postedBy, claimedBy)
	// Close without stamp (simpler than accept for seeding).
	if err := db.Exec("", "close: "+id, false, commons.CloseWantedDML(id)); err != nil {
		t.Fatalf("close %s: %v", id, err)
	}
}

// --- assertion helpers ---

func assertItemStatus(t *testing.T, db commons.DB, wantedID, ref, want string) {
	t.Helper()
	status, found, err := commons.QueryItemStatus(db, wantedID, ref)
	if err != nil {
		t.Fatalf("QueryItemStatus(%s, %q): %v", wantedID, ref, err)
	}
	if !found {
		t.Fatalf("item %s not found on ref %q", wantedID, ref)
	}
	if status != want {
		t.Errorf("item %s status = %q, want %q (ref %q)", wantedID, status, want, ref)
	}
}

func assertNothingToCommit(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "nothing to commit") {
		t.Fatalf("expected 'nothing to commit' error, got: %v", err)
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- scenario definitions ---

type scenario struct {
	name string
	seed func(t *testing.T, db commons.DB)
	run  func(t *testing.T, db commons.DB)
}

func scenarios() []scenario {
	return []scenario{
		// --- positive mutations ---
		{
			name: "insert_wanted",
			seed: func(t *testing.T, db commons.DB) {},
			run: func(t *testing.T, db commons.DB) {
				item := &commons.WantedItem{
					ID: "w-ins", Title: "New item", PostedBy: "alice",
					EffortLevel: "medium",
				}
				dml, err := commons.InsertWantedDML(item)
				assertNoError(t, err)
				assertNoError(t, db.Exec("", "insert", false, dml))
				assertItemStatus(t, db, "w-ins", "", "open")
			},
		},
		{
			name: "claim_open",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-cl", "Claim me", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				assertNoError(t, db.Exec("", "claim", false, commons.ClaimWantedDML("w-cl", "bob")))
				assertItemStatus(t, db, "w-cl", "", "claimed")
			},
		},
		{
			name: "unclaim_claimed",
			seed: func(t *testing.T, db commons.DB) {
				seedClaimedItem(t, db, "w-uc", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				assertNoError(t, db.Exec("", "unclaim", false, commons.UnclaimWantedDML("w-uc")))
				assertItemStatus(t, db, "w-uc", "", "open")
			},
		},
		{
			name: "done_claimed",
			seed: func(t *testing.T, db commons.DB) {
				seedClaimedItem(t, db, "w-dn", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				stmts := commons.SubmitCompletionDML("c-dn", "w-dn", "bob", "http://example.com", "")
				assertNoError(t, db.Exec("", "done", false, stmts...))
				assertItemStatus(t, db, "w-dn", "", "in_review")
				// Verify completion exists.
				c, err := commons.QueryCompletion(db, "w-dn")
				assertNoError(t, err)
				if c.CompletedBy != "bob" {
					t.Errorf("completion.CompletedBy = %q, want %q", c.CompletedBy, "bob")
				}
			},
		},
		{
			name: "accept_in_review",
			seed: func(t *testing.T, db commons.DB) {
				seedInReviewItem(t, db, "w-ac", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				c, err := commons.QueryCompletion(db, "w-ac")
				assertNoError(t, err)
				stamp := &commons.Stamp{
					ID: "s-ac", Subject: c.ID, Quality: 5, Reliability: 5,
					Severity: "minor", ContextID: c.ID, ContextType: "completion",
				}
				stmts := commons.AcceptCompletionDML("w-ac", c.ID, "alice", "", stamp)
				assertNoError(t, db.Exec("", "accept", false, stmts...))
				assertItemStatus(t, db, "w-ac", "", "completed")
				// Verify stamp exists.
				s, err := commons.QueryStamp(db, "s-ac")
				assertNoError(t, err)
				if s.Author != "alice" {
					t.Errorf("stamp.Author = %q, want %q", s.Author, "alice")
				}
			},
		},
		{
			name: "reject_in_review",
			seed: func(t *testing.T, db commons.DB) {
				seedInReviewItem(t, db, "w-rj", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				stmts := commons.RejectCompletionDML("w-rj")
				assertNoError(t, db.Exec("", "reject", false, stmts...))
				assertItemStatus(t, db, "w-rj", "", "claimed")
				// Completion should be gone.
				_, err := commons.QueryCompletion(db, "w-rj")
				if err == nil {
					t.Error("expected completion to be deleted after reject")
				}
			},
		},
		{
			name: "close_in_review",
			seed: func(t *testing.T, db commons.DB) {
				seedInReviewItem(t, db, "w-cls", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				assertNoError(t, db.Exec("", "close", false, commons.CloseWantedDML("w-cls")))
				assertItemStatus(t, db, "w-cls", "", "completed")
			},
		},
		{
			name: "delete_open",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-del", "Delete me", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				assertNoError(t, db.Exec("", "delete", false, commons.DeleteWantedDML("w-del")))
				assertItemStatus(t, db, "w-del", "", "withdrawn")
			},
		},

		// --- negative mutations (expect "nothing to commit") ---
		{
			name: "claim_already_claimed",
			seed: func(t *testing.T, db commons.DB) {
				seedClaimedItem(t, db, "w-cc", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				err := db.Exec("", "claim again", false, commons.ClaimWantedDML("w-cc", "carol"))
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "claim_nonexistent",
			seed: func(t *testing.T, db commons.DB) {},
			run: func(t *testing.T, db commons.DB) {
				err := db.Exec("", "claim ghost", false, commons.ClaimWantedDML("w-none", "carol"))
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "unclaim_open",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-uo", "Unclaim open", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				err := db.Exec("", "unclaim open", false, commons.UnclaimWantedDML("w-uo"))
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "done_wrong_rig",
			seed: func(t *testing.T, db commons.DB) {
				seedClaimedItem(t, db, "w-dw", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				stmts := commons.SubmitCompletionDML("c-dw", "w-dw", "carol", "http://example.com", "")
				err := db.Exec("", "done wrong rig", false, stmts...)
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "done_open",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-do", "Done open", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				// Item is open, not claimed — both UPDATE (WHERE status='claimed')
				// and INSERT (WHERE status='in_review') match nothing.
				stmts := commons.SubmitCompletionDML("c-do", "w-do", "bob", "http://example.com", "")
				err := db.Exec("", "done open", false, stmts...)
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "close_completed",
			seed: func(t *testing.T, db commons.DB) {
				seedCompletedItem(t, db, "w-ccl", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				err := db.Exec("", "close completed", false, commons.CloseWantedDML("w-ccl"))
				assertNothingToCommit(t, err)
			},
		},
		{
			name: "delete_claimed",
			seed: func(t *testing.T, db commons.DB) {
				seedClaimedItem(t, db, "w-dc", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				err := db.Exec("", "delete claimed", false, commons.DeleteWantedDML("w-dc"))
				assertNothingToCommit(t, err)
			},
		},

		// --- roundtrip fidelity ---
		{
			name: "accept_stamp_roundtrip",
			seed: func(t *testing.T, db commons.DB) {
				seedInReviewItem(t, db, "w-sr", "alice", "bob")
			},
			run: func(t *testing.T, db commons.DB) {
				c, err := commons.QueryCompletion(db, "w-sr")
				assertNoError(t, err)
				stamp := &commons.Stamp{
					ID: "s-sr", Subject: c.ID, Quality: 5, Reliability: 3,
					Severity: "minor", ContextID: c.ID, ContextType: "completion",
				}
				stmts := commons.AcceptCompletionDML("w-sr", c.ID, "alice", "", stamp)
				assertNoError(t, db.Exec("", "accept", false, stmts...))
				// Stamp valence contains commas in JSON — verify fields after it survive.
				s, err := commons.QueryStamp(db, "s-sr")
				assertNoError(t, err)
				if s.Severity != "minor" {
					t.Errorf("stamp.Severity = %q, want %q", s.Severity, "minor")
				}
				if s.ContextID != c.ID {
					t.Errorf("stamp.ContextID = %q, want %q", s.ContextID, c.ID)
				}
			},
		},
		{
			name: "update_title",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-ut", "Old Title", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				dml, err := commons.UpdateWantedDML("w-ut", &commons.WantedUpdate{Title: "New Title"})
				assertNoError(t, err)
				assertNoError(t, db.Exec("", "update title", false, dml))
				// Verify title changed (query detail).
				item, err := commons.QueryWantedDetail(db, "w-ut")
				assertNoError(t, err)
				if item.Title != "New Title" {
					t.Errorf("title = %q, want %q", item.Title, "New Title")
				}
				if item.Status != "open" {
					t.Errorf("status = %q, want %q (should be unchanged)", item.Status, "open")
				}
			},
		},

		// --- multi-step ---
		{
			name: "full_lifecycle",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-lc", "Lifecycle", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				// open → claim
				assertNoError(t, db.Exec("", "claim", false, commons.ClaimWantedDML("w-lc", "bob")))
				assertItemStatus(t, db, "w-lc", "", "claimed")

				// claim → in_review
				stmts := commons.SubmitCompletionDML("c-lc", "w-lc", "bob", "http://example.com", "")
				assertNoError(t, db.Exec("", "done", false, stmts...))
				assertItemStatus(t, db, "w-lc", "", "in_review")

				// in_review → completed (accept)
				c, err := commons.QueryCompletion(db, "w-lc")
				assertNoError(t, err)
				stamp := &commons.Stamp{
					ID: "s-lc", Subject: c.ID, Quality: 5, Reliability: 5,
					Severity: "minor", ContextID: c.ID, ContextType: "completion",
				}
				acceptStmts := commons.AcceptCompletionDML("w-lc", c.ID, "alice", "", stamp)
				assertNoError(t, db.Exec("", "accept", false, acceptStmts...))
				assertItemStatus(t, db, "w-lc", "", "completed")
			},
		},
		{
			name: "branch_isolation",
			seed: func(t *testing.T, db commons.DB) {
				seedConformanceItem(t, db, "w-br", "Branch test", "alice")
			},
			run: func(t *testing.T, db commons.DB) {
				// Claim on branch — main should remain open.
				branch := "wl/bob/w-br"
				assertNoError(t, db.Exec(branch, "claim on branch", false, commons.ClaimWantedDML("w-br", "bob")))

				// Main unchanged.
				assertItemStatus(t, db, "w-br", "", "open")
				// Branch shows claimed.
				assertItemStatus(t, db, "w-br", branch, "claimed")
			},
		},
	}
}

// --- conformance runner ---

func TestConformance(t *testing.T) {
	for _, sc := range scenarios() {
		for _, f := range dbFactories() {
			t.Run(sc.name+"/"+f.name, func(t *testing.T) {
				db := f.setup(t)
				sc.seed(t, db)
				sc.run(t, db)
			})
		}
	}
}
