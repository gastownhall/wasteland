//go:build integration

package offline

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigGetProviderType(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			stdout, stderr, err := runWL(t, env, "config", "get", "provider-type")
			if err != nil {
				t.Fatalf("wl config get provider-type failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			got := strings.TrimSpace(stdout)
			want := string(backend)
			if got != want {
				t.Errorf("provider-type = %q, want %q", got, want)
			}
		})
	}
}

func TestReviewMarkdown(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Switch to PR mode and post.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Markdown review test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)
			branch := "wl/" + forkOrg + "/" + wantedID

			// Review with --md.
			stdout, stderr, err := runWL(t, env, "review", branch, "--md")
			if err != nil {
				t.Fatalf("wl review --md failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "## wl review") {
				t.Errorf("expected '## wl review' header, got: %s", stdout)
			}
			if !strings.Contains(stdout, "### Summary") {
				t.Errorf("expected '### Summary' section, got: %s", stdout)
			}
			if !strings.Contains(stdout, "### Changes") {
				t.Errorf("expected '### Changes' section, got: %s", stdout)
			}
		})
	}
}

func TestReviewJSON(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Switch to PR mode and post.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "JSON review test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)
			branch := "wl/" + forkOrg + "/" + wantedID

			// Review with --json.
			stdout, stderr, err := runWL(t, env, "review", branch, "--json")
			if err != nil {
				t.Fatalf("wl review --json failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// If there's output, it should be valid JSON.
			// (dolt diff -r json may produce empty output on some versions.)
			trimmed := strings.TrimSpace(stdout)
			if trimmed != "" && !json.Valid([]byte(trimmed)) {
				t.Errorf("review --json output is not valid JSON: %s", trimmed)
			}
		})
	}
}

func TestPRModePost(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Switch to PR mode.
			setMode(t, env, upstream, "pr")

			// Post in PR mode.
			stdout, stderr, err := runWL(t, env, "post",
				"--title", "PR mode test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "Branch:") {
				t.Errorf("expected Branch output in PR mode, got: %s", stdout)
			}

			wantedID := extractWantedID(t, stdout)

			// Verify a branch was created.
			expectedBranch := "wl/" + forkOrg + "/" + wantedID
			raw := doltSQL(t, dbDir, "SELECT name FROM dolt_branches WHERE name='"+expectedBranch+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Errorf("expected branch %q to exist", expectedBranch)
			}
		})
	}
}

func TestPRModeClaim(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnvInMode(t, backend, "wild-west")
			dbDir := forkCloneDir(t, env)

			// Post in wild-west mode.
			stdout, _, err := runWL(t, env, "post",
				"--title", "PR claim test",
				"--type", "bug",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// Switch to PR mode.
			setMode(t, env, upstream, "pr")

			// Claim in PR mode.
			stdout, stderr, err := runWL(t, env, "claim", wantedID, "--no-push")
			if err != nil {
				t.Fatalf("wl claim failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "Branch:") {
				t.Errorf("expected Branch output in PR mode, got: %s", stdout)
			}

			// Verify a branch was created.
			expectedBranch := "wl/" + forkOrg + "/" + wantedID
			raw := doltSQL(t, dbDir, "SELECT name FROM dolt_branches WHERE name='"+expectedBranch+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Errorf("expected branch %q to exist", expectedBranch)
			}
		})
	}
}

func TestPRModeReturnToMain(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Switch to PR mode.
			setMode(t, env, upstream, "pr")

			// Post.
			stdout, _, err := runWL(t, env, "post",
				"--title", "Return to main test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			_ = extractWantedID(t, stdout)

			// Active branch should be main after the command completes.
			raw := doltSQL(t, dbDir, "SELECT active_branch()")
			rows := parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatal("could not query active branch")
			}
			branch := strings.TrimSpace(rows[1][0])
			if branch != "main" {
				t.Errorf("active branch = %q, want %q", branch, "main")
			}
		})
	}
}

func TestWildWestModeUnchanged(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnvInMode(t, backend, "wild-west")
			dbDir := forkCloneDir(t, env)

			// Post in explicit wild-west mode.
			stdout, _, err := runWL(t, env, "post",
				"--title", "Wild-west unchanged test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}

			if strings.Contains(stdout, "Branch:") {
				t.Errorf("wild-west mode should not print Branch line, got: %s", stdout)
			}

			wantedID := extractWantedID(t, stdout)

			// No mutation branches should have been created (wl/register/* from join is OK).
			raw := doltSQL(t, dbDir, "SELECT COUNT(*) FROM dolt_branches WHERE name LIKE 'wl/%' AND name NOT LIKE 'wl/register/%'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || strings.TrimSpace(rows[1][0]) != "0" {
				t.Errorf("expected 0 mutation branches in wild-west mode, got: %v", rows)
			}

			// Item should exist on main.
			raw = doltSQL(t, dbDir, "SELECT id FROM wanted WHERE id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 {
				t.Errorf("wanted item %s should exist on main", wantedID)
			}
		})
	}
}

func TestReviewListBranches(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Switch to PR mode and post.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Review list test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// wl review (no args) should list the branch.
			stdout, stderr, err := runWL(t, env, "review")
			if err != nil {
				t.Fatalf("wl review failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			expectedBranch := "wl/" + forkOrg + "/" + wantedID
			if !strings.Contains(stdout, expectedBranch) {
				t.Errorf("expected review to list %q, got: %s", expectedBranch, stdout)
			}
		})
	}
}

func TestReviewShowsDiff(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Switch to PR mode and post.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Review diff test",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)

			// wl review <branch> --stat should show diff.
			branch := "wl/" + forkOrg + "/" + wantedID
			stdout, stderr, err := runWL(t, env, "review", branch, "--stat")
			if err != nil {
				t.Fatalf("wl review --stat failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			if !strings.Contains(stdout, "wanted") {
				t.Errorf("expected diff stat to mention 'wanted' table, got: %s", stdout)
			}
		})
	}
}

func TestMergeBasic(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// Switch to PR mode and post.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Merge test item",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)
			branch := "wl/" + forkOrg + "/" + wantedID

			// Item should NOT be on main yet.
			raw := doltSQL(t, dbDir, "SELECT COUNT(*) FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) >= 2 && strings.TrimSpace(rows[1][0]) != "0" {
				t.Errorf("item should not be on main before merge, got count: %s", rows[1][0])
			}

			// Merge.
			stdout, stderr, err := runWL(t, env, "merge", branch, "--no-push")
			if err != nil {
				t.Fatalf("wl merge failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "Merged") {
				t.Errorf("expected 'Merged' message, got: %s", stdout)
			}
			if !strings.Contains(stdout, "deleted") {
				t.Errorf("expected branch deletion message, got: %s", stdout)
			}

			// Item should now be on main.
			raw = doltSQL(t, dbDir, "SELECT id, title FROM wanted WHERE id='"+wantedID+"'")
			rows = parseCSV(t, raw)
			if len(rows) < 2 {
				t.Fatalf("wanted item %s not found on main after merge", wantedID)
			}
			if rows[1][1] != "Merge test item" {
				t.Errorf("title = %q, want %q", rows[1][1], "Merge test item")
			}

			// Branch should be gone.
			raw = doltSQL(t, dbDir, "SELECT COUNT(*) FROM dolt_branches WHERE name='"+branch+"'")
			rows = parseCSV(t, raw)
			if len(rows) >= 2 && strings.TrimSpace(rows[1][0]) != "0" {
				t.Errorf("branch %s should be deleted after merge", branch)
			}
		})
	}
}

func TestMergeKeepBranch(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Keep branch test",
				"--type", "bug",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)
			branch := "wl/" + forkOrg + "/" + wantedID

			// Merge with --keep-branch.
			stdout, stderr, err := runWL(t, env, "merge", branch, "--keep-branch", "--no-push")
			if err != nil {
				t.Fatalf("wl merge failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Branch should still exist.
			raw := doltSQL(t, dbDir, "SELECT COUNT(*) FROM dolt_branches WHERE name='"+branch+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || strings.TrimSpace(rows[1][0]) != "1" {
				t.Errorf("branch %s should still exist with --keep-branch", branch)
			}
		})
	}
}

func TestMergeNonExistentBranch(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			_, _, err := runWL(t, env, "merge", "wl/fake/w-nonexistent", "--no-push")
			if err == nil {
				t.Fatal("merge of non-existent branch should fail")
			}
		})
	}
}

func TestMergeFullLifecycle(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)
			dbDir := forkCloneDir(t, env)

			// PR mode: post → review --stat → merge → verify on main.
			setMode(t, env, upstream, "pr")

			stdout, _, err := runWL(t, env, "post",
				"--title", "Full lifecycle PR",
				"--type", "feature",
				"--no-push",
			)
			if err != nil {
				t.Fatalf("wl post failed: %v", err)
			}
			wantedID := extractWantedID(t, stdout)
			branch := "wl/" + forkOrg + "/" + wantedID

			// Review should show diff.
			stdout, stderr, err := runWL(t, env, "review", branch, "--stat")
			if err != nil {
				t.Fatalf("wl review --stat failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if !strings.Contains(stdout, "wanted") {
				t.Errorf("review stat should mention 'wanted', got: %s", stdout)
			}

			// Merge.
			stdout, stderr, err = runWL(t, env, "merge", branch, "--no-push")
			if err != nil {
				t.Fatalf("wl merge failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify on main.
			raw := doltSQL(t, dbDir, "SELECT title FROM wanted WHERE id='"+wantedID+"'")
			rows := parseCSV(t, raw)
			if len(rows) < 2 || rows[1][0] != "Full lifecycle PR" {
				t.Errorf("item not found on main after merge")
			}
		})
	}
}

func TestConfigSetGetMode(t *testing.T) {
	for _, backend := range backends {
		t.Run(string(backend), func(t *testing.T) {
			env := joinedEnv(t, backend)

			// Default mode should be PR.
			stdout, stderr, err := runWL(t, env, "config", "get", "mode")
			if err != nil {
				t.Fatalf("wl config get mode failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "pr" {
				t.Errorf("default mode = %q, want %q", strings.TrimSpace(stdout), "pr")
			}

			// Set to wild-west.
			stdout, stderr, err = runWL(t, env, "config", "set", "mode", "wild-west")
			if err != nil {
				t.Fatalf("wl config set mode wild-west failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify it's set.
			stdout, stderr, err = runWL(t, env, "config", "get", "mode")
			if err != nil {
				t.Fatalf("wl config get mode failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "wild-west" {
				t.Errorf("mode = %q, want %q", strings.TrimSpace(stdout), "wild-west")
			}

			// Set back to PR.
			stdout, stderr, err = runWL(t, env, "config", "set", "mode", "pr")
			if err != nil {
				t.Fatalf("wl config set mode pr failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
			}

			// Verify.
			stdout, _, err = runWL(t, env, "config", "get", "mode")
			if err != nil {
				t.Fatalf("wl config get mode failed: %v", err)
			}
			if strings.TrimSpace(stdout) != "pr" {
				t.Errorf("mode = %q, want %q", strings.TrimSpace(stdout), "pr")
			}
		})
	}
}
