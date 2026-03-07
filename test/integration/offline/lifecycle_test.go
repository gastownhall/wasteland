//go:build integration

package offline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	upstreamOrg = "test-org"
	upstreamDB  = "wl-commons"
	upstream    = upstreamOrg + "/" + upstreamDB
	forkOrg     = "test-fork"
)

// joinedEnv creates a test env, sets up an upstream, and runs wl join.
// Returns the env ready for post/claim/done commands.
func joinedEnv(t *testing.T, backend backendKind) *testEnv {
	t.Helper()
	env := newTestEnv(t, backend)
	env.createUpstreamStore(t, upstreamOrg, upstreamDB)
	env.joinWasteland(t, upstream, forkOrg)
	return env
}

func joinedLifecycleEnv(t *testing.T, backend backendKind) *testEnv {
	t.Helper()
	return joinedEnvInMode(t, backend, "wild-west")
}

// forkCloneDir returns the fork clone dir from the wasteland config.
func forkCloneDir(t *testing.T, env *testEnv) string {
	t.Helper()
	cfg := env.loadConfig(t, upstream)
	dir, ok := cfg["local_dir"].(string)
	if !ok || dir == "" {
		t.Fatal("local_dir is empty in config")
	}
	return dir
}

func TestJoinCreatesConfig(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			cfg := env.loadConfig(t, upstream)
			if cfg["upstream"] != upstream {
				t.Errorf("upstream = %q, want %q", cfg["upstream"], upstream)
			}
			if cfg["fork_org"] != forkOrg {
				t.Errorf("fork_org = %q, want %q", cfg["fork_org"], forkOrg)
			}
			if cfg["rig_handle"] != forkOrg {
				t.Errorf("rig_handle = %q, want %q (defaults to fork org)", cfg["rig_handle"], forkOrg)
			}
			localDir, _ := cfg["local_dir"].(string)
			if localDir == "" {
				t.Fatal("local_dir is empty")
			}

			// Verify provider_type matches the backend.
			providerType, _ := cfg["provider_type"].(string)
			switch backend {
			case fileBackend:
				if providerType != "file" {
					t.Errorf("provider_type = %q, want %q", providerType, "file")
				}
			case gitBackend:
				if providerType != "git" {
					t.Errorf("provider_type = %q, want %q", providerType, "git")
				}
			case githubBackend:
				if providerType != "github" {
					t.Errorf("provider_type = %q, want %q", providerType, "github")
				}
			}

			// Verify upstream_url is set and has expected format.
			upstreamURL, _ := cfg["upstream_url"].(string)
			if upstreamURL == "" {
				t.Fatal("upstream_url is empty")
			}
			if !strings.HasPrefix(upstreamURL, "file://") {
				t.Errorf("upstream_url should start with file://, got %q", upstreamURL)
			}
			switch backend {
			case gitBackend, githubBackend:
				if !strings.HasSuffix(upstreamURL, ".git") {
					t.Errorf("upstream_url for %s should end with .git, got %q", backend, upstreamURL)
				}
			case fileBackend:
				if strings.HasSuffix(upstreamURL, ".git") {
					t.Errorf("upstream_url for file should not end with .git, got %q", upstreamURL)
				}
			}
		})
	}
}

func TestJoinAlreadyJoined(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Second join to same upstream should succeed (no-op).
			args := append([]string{"join", upstream}, env.remoteArgs()...)
			args = append(args, "--fork-org", forkOrg)
			stdout, _, err := runWL(t, env, args...)
			if err != nil {
				t.Fatalf("second join should succeed: %v", err)
			}
			if !strings.Contains(stdout, "Already joined") {
				t.Errorf("expected 'Already joined' message, got: %s", stdout)
			}
		})
	}
}

func TestSchemaInit(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Fork clone should already have the schema from upstream.
			raw := doltSQL(t, dbDir, "SHOW TABLES")
			rows := parseCSV(t, raw)

			tables := make(map[string]bool)
			for _, row := range rows[1:] {
				if len(row) > 0 {
					tables[row[0]] = true
				}
			}

			expected := []string{"_meta", "rigs", "wanted", "completions", "stamps", "badges", "chain_meta"}
			for _, name := range expected {
				if !tables[name] {
					t.Errorf("missing table %q; got tables: %v", name, tables)
				}
			}
		})
	}
}

func TestPostWanted(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			stdout, stderr, err := runWL(t, env, "post",
				"--title", "Test feature request",
				"--type", "feature",
				"--priority", "1",
				"--effort", "large",
				"--tags", "go,test",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			wantedID := extractWantedID(t, stdout)

			// Verify the item in the database.
			raw := doltSQL(t, dbDir, "SELECT id, title, type, priority, status, effort_level, posted_by FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found in database", wantedID)
			}

			row := rows[1]
			// row: id, title, type, priority, status, effort_level, posted_by
			if row[0] != wantedID {
				t.Errorf("id = %q, want %q", row[0], wantedID)
			}
			if row[1] != "Test feature request" {
				t.Errorf("title = %q, want %q", row[1], "Test feature request")
			}
			if row[2] != "feature" {
				t.Errorf("type = %q, want %q", row[2], "feature")
			}
			if row[3] != "1" {
				t.Errorf("priority = %q, want %q", row[3], "1")
			}
			if row[4] != "open" {
				t.Errorf("status = %q, want %q", row[4], "open")
			}
			if row[5] != "large" {
				t.Errorf("effort_level = %q, want %q", row[5], "large")
			}
			if row[6] != forkOrg {
				t.Errorf("posted_by = %q, want %q", row[6], forkOrg)
			}
		})
	}
}

func TestClaimWanted(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post an item first.
			stdout, _, err := runWL(t, env, "post", "--title", "Claim test item", "--type", "bug", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Claim it.
			stdout, stderr, err := runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify status and claimed_by.
			raw := doltSQL(t, dbDir, "SELECT status, COALESCE(claimed_by,'') FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found after claim", wantedID)
			}
			if rows[1][0] != "claimed" {
				t.Errorf("status = %q, want %q", rows[1][0], "claimed")
			}
			if rows[1][1] != forkOrg {
				t.Errorf("claimed_by = %q, want %q", rows[1][1], forkOrg)
			}
		})
	}
}

func TestClaimAlreadyClaimed(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post and claim.
			stdout, _, err := runWL(t, env, "post", "--title", "Double claim test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("first claim failed: %v", err)
			}

			// Second claim should fail.
			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err == nil {
				t.Fatal("second claim should have failed")
			}

			// Status should still be claimed.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "claimed" {
				t.Errorf("status should still be 'claimed' after double claim attempt")
			}
		})
	}
}

func TestDoneFullLifecycle(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post.
			stdout, _, err := runWL(t, env, "post", "--title", "Done lifecycle test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Claim.
			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Done.
			stdout, stderr, err := runWL(t, env, "done", wantedID, "--evidence", "https://github.com/test/pr/1", "--no-push")
			if err != nil {
				t.Fatalf("wl done failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify status is in_review.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "in_review" {
				t.Errorf("status = %q, want %q", rows[1][0], "in_review")
			}

			// Verify completion record exists.
			raw = doltSQL(t, dbDir, "SELECT wanted_id, completed_by FROM completions WHERE wanted_id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatal("no completion record found")
			}
			if rows[1][0] != wantedID {
				t.Errorf("completion wanted_id = %q, want %q", rows[1][0], wantedID)
			}
			if rows[1][1] != forkOrg {
				t.Errorf("completion completed_by = %q, want %q", rows[1][1], forkOrg)
			}
		})
	}
}

func TestDoneWrongClaimer(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post and claim as the default rig (forkOrg handle).
			stdout, _, err := runWL(t, env, "post", "--title", "Wrong claimer test", "--type", "bug", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Rewrite config to a different rig handle.
			writeConfig(t, env, upstream, "rig-b")

			// Done as rig-b should fail (claimed by forkOrg).
			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "fake", "--no-push")
			if err == nil {
				t.Fatal("done by wrong claimer should have failed")
			}

			// Status should still be claimed.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "claimed" {
				t.Errorf("status should still be 'claimed' after wrong-claimer done attempt")
			}
		})
	}
}

func TestDoneUnclaimed(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post but don't claim.
			stdout, _, err := runWL(t, env, "post", "--title", "Unclaimed done test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Done on open item should fail.
			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "fake", "--no-push")
			if err == nil {
				t.Fatal("done on unclaimed item should have failed")
			}

			// Verify still open.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "open" {
				var got string
				if len(rows) >= 2 {
					got = rows[1][0]
				}
				t.Errorf("status = %q, want %q", got, "open")
			}
		})
	}
}

func TestPostOutput(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)

			stdout, _, err := runWL(t, env, "post", "--title", "Output format test", "--type", "docs", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}

			if !strings.Contains(stdout, "Posted wanted item") {
				t.Errorf("output missing 'Posted wanted item': %s", stdout)
			}
			if !strings.Contains(stdout, "w-") {
				t.Errorf("output missing wanted ID: %s", stdout)
			}
			if !strings.Contains(stdout, "Output format test") {
				t.Errorf("output missing title: %s", stdout)
			}
		})
	}
}

func TestAcceptFullLifecycle(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post as forkOrg (poster).
			stdout, _, err := runWL(t, env, "post", "--title", "Accept lifecycle test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Switch to worker-rig for claim + done.
			writeConfig(t, env, upstream, "worker-rig")

			// Claim as worker-rig.
			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Done as worker-rig.
			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "https://github.com/test/pr/1", "--no-push")
			if err != nil {
				t.Fatalf("wl done failed: %v", err)
			}

			// Switch back to forkOrg (poster) for accept.
			writeConfig(t, env, upstream, forkOrg)

			// Accept as poster.
			stdout, stderr, err := runWL(t, env, "accept", wantedID, "--quality", "4", "--reliability", "3", "--severity", "branch", "--skills", "go,test", "--message", "great work", "--no-push")
			if err != nil {
				t.Fatalf("wl accept failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Accepted") {
				t.Errorf("expected 'Accepted' message, got: %s", stdout)
			}

			// Verify wanted status is completed.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "completed" {
				t.Errorf("status = %q, want %q", rows[1][0], "completed")
			}

			// Verify stamp was created.
			raw = doltSQL(t, dbDir, "SELECT author, subject, severity FROM stamps WHERE context_id IN (SELECT id FROM completions WHERE wanted_id='"+wantedID+"')")
			rows = parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatal("no stamp record found")
			}
			if rows[1][0] != forkOrg {
				t.Errorf("stamp author = %q, want %q", rows[1][0], forkOrg)
			}
			if rows[1][1] != "worker-rig" {
				t.Errorf("stamp subject = %q, want %q", rows[1][1], "worker-rig")
			}
			if rows[1][2] != "branch" {
				t.Errorf("stamp severity = %q, want %q", rows[1][2], "branch")
			}

			// Verify completion was validated.
			raw = doltSQL(t, dbDir, "SELECT COALESCE(validated_by, '') FROM completions WHERE wanted_id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != forkOrg {
				var got string
				if len(rows) >= 2 {
					got = rows[1][0]
				}
				t.Errorf("completion validated_by = %q, want %q", got, forkOrg)
			}
		})
	}
}

func TestAcceptSelfReject(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)

			// Post → claim → done as forkOrg.
			stdout, _, err := runWL(t, env, "post", "--title", "Self accept test", "--type", "bug", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "evidence", "--no-push")
			if err != nil {
				t.Fatalf("wl done failed: %v", err)
			}

			// Accept as same rig should fail.
			_, _, err = runWL(t, env, "accept", wantedID, "--quality", "3", "--no-push")
			if err == nil {
				t.Fatal("accept by same rig should have failed")
			}
		})
	}
}

func TestRejectFullLifecycle(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post as forkOrg (poster).
			stdout, _, err := runWL(t, env, "post", "--title", "Reject lifecycle test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Switch to worker-rig for claim + done.
			writeConfig(t, env, upstream, "worker-rig")

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "https://github.com/test/pr/1", "--no-push")
			if err != nil {
				t.Fatalf("wl done failed: %v", err)
			}

			// Switch back to forkOrg (poster) for reject.
			writeConfig(t, env, upstream, forkOrg)

			stdout, stderr, err := runWL(t, env, "reject", wantedID, "--reason", "tests failing", "--no-push")
			if err != nil {
				t.Fatalf("wl reject failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Rejected") {
				t.Errorf("expected 'Rejected' message, got: %s", stdout)
			}

			// Verify status reverted to claimed.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "claimed" {
				t.Errorf("status = %q, want %q", rows[1][0], "claimed")
			}

			// Verify completion record was deleted.
			raw = doltSQL(t, dbDir, "SELECT COUNT(*) FROM completions WHERE wanted_id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "0" {
				var got string
				if len(rows) >= 2 {
					got = rows[1][0]
				}
				t.Errorf("completion count = %q, want %q", got, "0")
			}

			// Worker re-submits.
			writeConfig(t, env, upstream, "worker-rig")

			_, _, err = runWL(t, env, "done", wantedID, "--evidence", "https://github.com/test/pr/2", "--no-push")
			if err != nil {
				t.Fatalf("wl done (re-submit) failed: %v", err)
			}

			// Poster accepts.
			writeConfig(t, env, upstream, forkOrg)

			stdout, stderr, err = runWL(t, env, "accept", wantedID, "--quality", "4", "--no-push")
			if err != nil {
				t.Fatalf("wl accept failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Accepted") {
				t.Errorf("expected 'Accepted' message, got: %s", stdout)
			}

			// Verify final status is completed.
			raw = doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "completed" {
				t.Errorf("status = %q, want %q", rows[1][0], "completed")
			}
		})
	}
}

func TestUpdateWanted(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post.
			stdout, _, err := runWL(t, env, "post", "--title", "Update test item", "--type", "feature", "--priority", "2", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Update title and priority.
			stdout, stderr, err := runWL(t, env, "update", wantedID, "--title", "Updated title", "--priority", "1", "--effort", "large", "--no-push")
			if err != nil {
				t.Fatalf("wl update failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Updated") {
				t.Errorf("expected 'Updated' message, got: %s", stdout)
			}

			// Verify fields changed in database.
			raw := doltSQL(t, dbDir, "SELECT title, priority, effort_level FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found", wantedID)
			}
			if rows[1][0] != "Updated title" {
				t.Errorf("title = %q, want %q", rows[1][0], "Updated title")
			}
			if rows[1][1] != "1" {
				t.Errorf("priority = %q, want %q", rows[1][1], "1")
			}
			if rows[1][2] != "large" {
				t.Errorf("effort_level = %q, want %q", rows[1][2], "large")
			}
		})
	}
}

func TestUpdateClaimedFails(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)

			// Post and claim.
			stdout, _, err := runWL(t, env, "post", "--title", "Claimed update test", "--type", "bug", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Update on claimed item should fail.
			_, _, err = runWL(t, env, "update", wantedID, "--title", "New title", "--no-push")
			if err == nil {
				t.Fatal("update on claimed item should have failed")
			}
		})
	}
}

func TestUnclaimWanted(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post an item.
			stdout, _, err := runWL(t, env, "post", "--title", "Unclaim test item", "--type", "bug", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Claim it.
			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Unclaim it.
			stdout, stderr, err := runWL(t, env, "unclaim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl unclaim failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Unclaimed") {
				t.Errorf("expected 'Unclaimed' message, got: %s", stdout)
			}

			// Verify status is open and claimed_by is empty.
			raw := doltSQL(t, dbDir, "SELECT status, COALESCE(claimed_by,'') FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found after unclaim", wantedID)
			}
			if rows[1][0] != "open" {
				t.Errorf("status = %q, want %q", rows[1][0], "open")
			}
			if rows[1][1] != "" {
				t.Errorf("claimed_by = %q, want empty", rows[1][1])
			}
		})
	}
}

func TestUnclaimByPoster(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post as forkOrg (poster).
			stdout, _, err := runWL(t, env, "post", "--title", "Poster unclaim test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Switch to worker-rig and claim.
			writeConfig(t, env, upstream, "worker-rig")

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Switch back to forkOrg (poster) and unclaim.
			writeConfig(t, env, upstream, forkOrg)

			stdout, stderr, err := runWL(t, env, "unclaim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl unclaim (poster) failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Unclaimed") {
				t.Errorf("expected 'Unclaimed' message, got: %s", stdout)
			}

			// Verify status is open and claimed_by is empty.
			raw := doltSQL(t, dbDir, "SELECT status, COALESCE(claimed_by,'') FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found after unclaim", wantedID)
			}
			if rows[1][0] != "open" {
				t.Errorf("status = %q, want %q", rows[1][0], "open")
			}
			if rows[1][1] != "" {
				t.Errorf("claimed_by = %q, want empty", rows[1][1])
			}
		})
	}
}

func TestDeleteWanted(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Post.
			stdout, _, err := runWL(t, env, "post", "--title", "Delete test item", "--type", "docs", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Delete.
			stdout, stderr, err := runWL(t, env, "delete", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl delete failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Withdrawn") {
				t.Errorf("expected 'Withdrawn' message, got: %s", stdout)
			}

			// Verify status is withdrawn.
			raw := doltSQL(t, dbDir, "SELECT status FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "withdrawn" {
				t.Errorf("status = %q, want %q", rows[1][0], "withdrawn")
			}
		})
	}
}

func TestDeleteClaimedFails(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedLifecycleEnv(t, backend)

			// Post and claim.
			stdout, _, err := runWL(t, env, "post", "--title", "Claimed delete test", "--type", "feature", "--no-push")
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			_, _, err = runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v", err)
			}

			// Delete on claimed item should fail.
			_, _, err = runWL(t, env, "delete", wantedID, "--no-push")
			if err == nil {
				t.Fatal("delete on claimed item should have failed")
			}
		})
	}
}

// writeConfig overwrites the wasteland config with a different rig handle.
// Used by TestDoneWrongClaimer and TestAcceptFullLifecycle to simulate a different rig.
func writeConfig(t *testing.T, env *testEnv, upstreamPath, rigHandle string) {
	t.Helper()
	cfg := env.loadConfig(t, upstreamPath)
	cfg["rig_handle"] = rigHandle

	parts := strings.SplitN(upstreamPath, "/", 2)
	configPath := filepath.Join(env.ConfigDir, "wastelands", parts[0], parts[1]+".json")

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}
	if err := os.WriteFile(configPath, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("writing config: %v", err)
	}
}

// --- Multi-wasteland tests ---

func TestMultiWastelandJoin(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)

			// Create two upstream stores.
			env.createUpstreamStore(t, "org-a", "wl-commons")
			env.createUpstreamStore(t, "org-b", "other-db")

			// Join both.
			env.joinWasteland(t, "org-a/wl-commons", "fork-a")
			env.joinWasteland(t, "org-b/other-db", "fork-b")

			// wl list should show both.
			stdout, stderr, err := runWL(t, env, "list")
			if err != nil {
				t.Fatalf("wl list failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "org-a/wl-commons") {
				t.Errorf("list output missing org-a/wl-commons: %s", stdout)
			}
			if !strings.Contains(stdout, "org-b/other-db") {
				t.Errorf("list output missing org-b/other-db: %s", stdout)
			}
			if !strings.Contains(stdout, "2") {
				t.Errorf("list output should mention count 2: %s", stdout)
			}
		})
	}
}

func TestMultiWasteland_RequiresFlag(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)

			// Create and join two upstreams.
			env.createUpstreamStore(t, "org-a", "wl-commons")
			env.createUpstreamStore(t, "org-b", "other-db")
			env.joinWasteland(t, "org-a/wl-commons", "fork-a")
			env.joinWasteland(t, "org-b/other-db", "fork-b")

			// Post without --wasteland should fail (ambiguous).
			_, _, err := runWL(t, env, "post", "--title", "Test", "--type", "feature", "--no-push")
			if err == nil {
				t.Fatal("post without --wasteland should fail with multiple wastelands")
			}

			// Post with --wasteland should succeed.
			stdout, stderr, err := runWL(t, env, "post",
				"--wasteland", "org-a/wl-commons",
				"--title", "Multi-WL post test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("post with --wasteland failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Posted wanted item") {
				t.Errorf("expected post success, got: %s", stdout)
			}
		})
	}
}

func TestLeave(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := newTestEnv(t, backend)

			// Create and join.
			env.createUpstreamStore(t, upstreamOrg, upstreamDB)
			env.joinWasteland(t, upstream, forkOrg)

			// Leave.
			stdout, stderr, err := runWL(t, env, "leave")
			if err != nil {
				t.Fatalf("wl leave failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Left wasteland") {
				t.Errorf("expected 'Left wasteland' message, got: %s", stdout)
			}

			// List should show empty.
			stdout, _, err = runWL(t, env, "list")
			if err != nil {
				t.Fatalf("wl list failed after leave: %v", err)
			}
			if !strings.Contains(stdout, "No wastelands joined") {
				t.Errorf("expected 'No wastelands joined', got: %s", stdout)
			}
		})
	}
}
