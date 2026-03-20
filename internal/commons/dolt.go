package commons

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// doltRetry runs fn up to 3 times with backoff (1s, 2s) between attempts.
func doltRetry(fn func() error) error {
	var err error
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * time.Second)
		}
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}

// DoltHubToken returns the DoltHub API token from the environment.
func DoltHubToken() string {
	return os.Getenv("DOLTHUB_TOKEN")
}

// DoltHubOrg returns the default DoltHub organization from the environment.
func DoltHubOrg() string {
	return os.Getenv("DOLTHUB_ORG")
}

// PushAllRemotes pushes the local main branch to both upstream and origin remotes.
// If a push is rejected (stale), it pulls to merge and retries.
// Returns an error if any remote push ultimately fails.
func PushAllRemotes(dbDir string, stdout io.Writer) error {
	var failures []string
	for _, remote := range []string{"upstream", "origin"} {
		if err := pushRemote(dbDir, remote); err != nil {
			fmt.Fprintf(stdout, "  Syncing with %s...\n", remote)
			if pullErr := pullRemote(dbDir, remote); pullErr != nil {
				fmt.Fprintf(stdout, "  warning: sync from %s failed: %v\n", remote, pullErr)
				failures = append(failures, remote)
				continue
			}
			if err := pushRemote(dbDir, remote); err != nil {
				fmt.Fprintf(stdout, "  warning: push to %s failed after sync: %v\n", remote, err)
				failures = append(failures, remote)
				continue
			}
		}
		fmt.Fprintf(stdout, "  Pushed to %s\n", remote)
	}
	if len(failures) > 0 {
		return fmt.Errorf("push failed for remotes: %s", strings.Join(failures, ", "))
	}
	return nil
}

func pushRemote(dbDir, remote string) error {
	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "push", remote, "main")
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt push %s main: %w (%s)", remote, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

func pullRemote(dbDir, remote string) error {
	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "pull", remote, "main")
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt pull %s main: %w (%s)", remote, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// PullUpstream pulls the latest changes from the upstream remote.
func PullUpstream(dbDir string) error {
	return pullRemote(dbDir, "upstream")
}

// ResetMainToUpstream fetches upstream and hard-resets local main to match.
// Used in PR mode where main should always mirror upstream exactly.
func ResetMainToUpstream(dbDir string) error {
	if err := FetchRemote(dbDir, "upstream"); err != nil {
		return err
	}
	return doltExec(dbDir, "reset", "--hard", "upstream/main")
}

// FetchRemote fetches the latest refs from a named remote without merging.
func FetchRemote(dbDir, remote string) error {
	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "fetch", remote)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt fetch %s: %w (%s)", remote, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// DoltSQLScript executes a SQL script against a dolt database directory.
func DoltSQLScript(dbDir, script string) error {
	tmpFile, err := os.CreateTemp("", "dolt-script-*.sql")
	if err != nil {
		return fmt.Errorf("creating temp SQL file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(script); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing SQL script: %w", err)
	}
	_ = tmpFile.Close()

	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "sql", "--file", tmpFile.Name())
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w (output: %s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// BranchName returns the conventional branch name for a PR-mode mutation.
func BranchName(rigHandle, wantedID string) string {
	return fmt.Sprintf("wl/%s/%s", rigHandle, wantedID)
}

// BranchExists checks whether a branch exists in the dolt database.
func BranchExists(dbDir, branch string) (bool, error) {
	out, err := DoltSQLQuery(dbDir, fmt.Sprintf(
		"SELECT COUNT(*) AS cnt FROM dolt_branches WHERE name = '%s'",
		strings.ReplaceAll(branch, "'", "''"),
	))
	if err != nil {
		return false, err
	}
	// CSV output: "cnt\n0\n" or "cnt\n1\n"
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return false, fmt.Errorf("unexpected dolt_branches output: %s", out)
	}
	return strings.TrimSpace(lines[1]) != "0", nil
}

// CheckoutBranch creates the branch if it doesn't exist, then checks it out.
// Uses dolt CLI commands (not SQL DOLT_CHECKOUT) because the SQL stored
// procedure is session-scoped and does not persist across dolt sql invocations.
// When creating a new branch, prefers origin's remote tracking branch as the
// start point so the local branch starts with the remote's data.
func CheckoutBranch(dbDir, branch string) error {
	exists, err := BranchExists(dbDir, branch)
	if err != nil {
		return fmt.Errorf("checking branch %s: %w", branch, err)
	}
	if !exists {
		// Prefer creating from origin's tracking branch if it exists,
		// so the local branch starts with the remote's data (e.g., wanted items
		// submitted via PRs). Falls back to creating from HEAD (main).
		remoteBranch := "remotes/origin/" + branch
		if remoteExists, _ := RemoteBranchExists(dbDir, remoteBranch); remoteExists {
			if err := doltExec(dbDir, "branch", branch, remoteBranch); err != nil {
				return fmt.Errorf("creating branch %s from %s: %w", branch, remoteBranch, err)
			}
		} else {
			if err := doltExec(dbDir, "branch", branch); err != nil {
				return fmt.Errorf("creating branch %s: %w", branch, err)
			}
		}
	}
	return doltExec(dbDir, "checkout", branch)
}

// RemoteBranchExists checks whether a remote tracking branch exists in the
// dolt_remote_branches system table.
func RemoteBranchExists(dbDir, remoteBranch string) (bool, error) {
	escaped := strings.ReplaceAll(remoteBranch, "'", "''")
	out, err := DoltSQLQuery(dbDir, fmt.Sprintf(
		"SELECT COUNT(*) AS cnt FROM dolt_remote_branches WHERE name = '%s'", escaped))
	if err != nil {
		return false, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return false, nil
	}
	return strings.TrimSpace(lines[1]) != "0", nil
}

// MergeRemoteTracking merges the origin remote tracking branch into the
// currently checked-out local branch. If the remote tracking branch doesn't
// exist or the merge fails, this is a best-effort no-op. Used in PR mode to
// bring stale local branches up to date with origin's data.
func MergeRemoteTracking(dbDir, branch string) error {
	remoteBranch := "remotes/origin/" + branch
	exists, err := RemoteBranchExists(dbDir, remoteBranch)
	if err != nil || !exists {
		return nil
	}
	return doltExec(dbDir, "merge", remoteBranch)
}

// CheckoutBranchFrom checks out a branch if it exists, or creates it from
// startPoint if it doesn't. Used in PR mode so new branches start from a
// clean upstream-aligned main, while existing branches (with pending
// multi-step mutations like claim→done→accept) are preserved.
func CheckoutBranchFrom(dbDir, branch, startPoint string) error {
	exists, err := BranchExists(dbDir, branch)
	if err != nil {
		return fmt.Errorf("checking branch %s: %w", branch, err)
	}
	if !exists {
		if err := doltExec(dbDir, "branch", branch, startPoint); err != nil {
			return fmt.Errorf("creating branch %s from %s: %w", branch, startPoint, err)
		}
	}
	return doltExec(dbDir, "checkout", branch)
}

// CheckoutMain switches the working directory back to the main branch.
func CheckoutMain(dbDir string) error {
	return doltExec(dbDir, "checkout", "main")
}

// doltExec runs a dolt CLI command in the given database directory.
func doltExec(dbDir string, args ...string) error {
	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", args...)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// PushBranch force-pushes a named branch to origin.
// Force is always used because wl/* branches on the user's own fork may
// have diverged history after redo operations (unclaim then re-claim, etc.).
func PushBranch(dbDir, branch string, stdout io.Writer) error {
	err := doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "push", "--force", "origin", branch)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("push branch %s: %w (%s)", branch, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(stdout, "  warning: %v\n", err)
		return err
	}
	fmt.Fprintf(stdout, "  Pushed branch %s to origin\n", branch)
	return nil
}

// ListBranches returns branch names matching a prefix (e.g. "wl/").
func ListBranches(dbDir, prefix string) ([]string, error) {
	out, err := DoltSQLQuery(dbDir, fmt.Sprintf(
		"SELECT name FROM dolt_branches WHERE name LIKE '%s%%' ORDER BY name",
		EscapeLIKE(prefix),
	))
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil, nil // header only, no branches
	}
	var branches []string
	for _, line := range lines[1:] {
		name := strings.TrimSpace(line)
		if name != "" {
			branches = append(branches, name)
		}
	}
	return branches, nil
}

// TrackOriginBranches creates local tracking branches for any
// remotes/origin/{prefix}* branches that don't already exist locally.
// This makes origin branch data available to AS OF queries.
func TrackOriginBranches(dbDir, prefix string) error {
	remotePrefix := "remotes/origin/" + prefix

	// Get remote branches.
	out, err := DoltSQLQuery(dbDir, fmt.Sprintf(
		"SELECT name FROM dolt_remote_branches WHERE name LIKE '%s%%' ORDER BY name",
		EscapeLIKE(remotePrefix),
	))
	if err != nil {
		return nil // best-effort; remote may not exist
	}
	var remoteBranches []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n")[1:] {
		name := strings.TrimSpace(line)
		if name != "" {
			remoteBranches = append(remoteBranches, name)
		}
	}
	if len(remoteBranches) == 0 {
		return nil
	}

	// Get local branches to avoid duplicates.
	localBranches, _ := ListBranches(dbDir, prefix)
	localSet := make(map[string]bool, len(localBranches))
	for _, b := range localBranches {
		localSet[b] = true
	}

	// Create local tracking branches for any missing ones.
	for _, remote := range remoteBranches {
		local := strings.TrimPrefix(remote, "remotes/origin/")
		if localSet[local] {
			continue
		}
		// dolt branch <local> <remote-ref>
		cmd := exec.Command("dolt", "branch", local, remote)
		cmd.Dir = dbDir
		_ = cmd.Run() // best-effort
	}

	// Also prune local branches whose remote counterpart no longer exists.
	remoteSet := make(map[string]bool, len(remoteBranches))
	for _, r := range remoteBranches {
		remoteSet[strings.TrimPrefix(r, "remotes/origin/")] = true
	}
	for _, local := range localBranches {
		if !strings.HasPrefix(local, prefix) {
			continue
		}
		if !remoteSet[local] {
			_ = DeleteBranch(dbDir, local) // best-effort
		}
	}

	return nil
}

// MergeBranch merges a branch into main. If the merge produces conflicts
// it aborts and returns an error. The caller must already be on main.
func MergeBranch(dbDir, branch string) error {
	escaped := strings.ReplaceAll(branch, "'", "''")
	err := DoltSQLScript(dbDir, fmt.Sprintf(
		"CALL DOLT_CHECKOUT('main');\nCALL DOLT_MERGE('%s');", escaped,
	))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "conflict") {
			_ = DoltSQLScript(dbDir, "CALL DOLT_MERGE('--abort');")
			return fmt.Errorf("merge conflict on branch %s: resolve manually or delete the branch", branch)
		}
		return fmt.Errorf("merging branch %s: %w", branch, err)
	}
	return nil
}

// DeleteBranch deletes a local branch.
func DeleteBranch(dbDir, branch string) error {
	escaped := strings.ReplaceAll(branch, "'", "''")
	return DoltSQLScript(dbDir, fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s');", escaped))
}

// DeleteRemoteBranch deletes a branch on a named remote using refspec syntax.
func DeleteRemoteBranch(dbDir, remote, branch string) error {
	return doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "push", remote, ":"+branch)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt push %s :%s: %w (%s)", remote, branch, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
}

// EnsureGitHubRemote adds a "github" Dolt remote pointing to the rig's
// GitHub fork (e.g. https://github.com/alice-dev/wl-commons.git).
// Idempotent: if "github" remote already exists, no-op.
func EnsureGitHubRemote(dbDir, forkOrg, forkDB string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	checkCmd := exec.CommandContext(ctx, "dolt", "remote", "-v")
	checkCmd.Dir = dbDir
	output, err := checkCmd.CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "github") {
				return nil
			}
		}
	}

	remoteURL := fmt.Sprintf("https://github.com/%s/%s.git", forkOrg, forkDB)
	addCmd := exec.CommandContext(ctx, "dolt", "remote", "add", "github", remoteURL)
	addCmd.Dir = dbDir
	output, err = addCmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(strings.ToLower(msg), "already exists") {
			return nil
		}
		return fmt.Errorf("dolt remote add github: %w (%s)", err, msg)
	}
	return nil
}

// PushBranchToRemote pushes a branch to a named remote.
func PushBranchToRemote(dbDir, remote, branch string, stdout io.Writer) error {
	return PushBranchToRemoteForce(dbDir, remote, branch, false, stdout)
}

// PushBranchToRemoteForce pushes a branch to a named remote, optionally with --force.
func PushBranchToRemoteForce(dbDir, remote, branch string, force bool, stdout io.Writer) error {
	err := doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		args := []string{"push"}
		if force {
			args = append(args, "--force")
		}
		args = append(args, remote, branch)
		cmd := exec.CommandContext(ctx, "dolt", args...)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt push %s %s: %w (%s)", remote, branch, err, strings.TrimSpace(string(output)))
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "  Pushed branch %s to %s\n", branch, remote)
	return nil
}

// ListWantedIDs returns wanted item IDs, optionally filtered by status.
func ListWantedIDs(db DB, statusFilter string) ([]string, error) {
	query := "SELECT id FROM wanted"
	if statusFilter != "" {
		query += fmt.Sprintf(" WHERE status = '%s'", EscapeSQL(statusFilter))
	}
	query += " ORDER BY created_at DESC LIMIT 50"
	out, err := db.Query(query, "")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return nil, nil
	}
	var ids []string
	for _, line := range lines[1:] {
		id := strings.TrimSpace(line)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ResolveWantedID resolves a wanted ID or unambiguous prefix to a full ID.
func ResolveWantedID(db DB, idOrPrefix string) (string, error) {
	query := fmt.Sprintf("SELECT id FROM wanted WHERE id LIKE '%s%%' LIMIT 3", EscapeLIKE(idOrPrefix))
	out, err := db.Query(query, "")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return "", fmt.Errorf("no wanted item matching %q", idOrPrefix)
	}
	var matches []string
	for _, line := range lines[1:] {
		id := strings.TrimSpace(line)
		if id != "" {
			matches = append(matches, id)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no wanted item matching %q", idOrPrefix)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("ambiguous prefix %q matches: %s", idOrPrefix, strings.Join(matches, ", "))
	}
	return matches[0], nil
}

// QueryItemStatus returns the status of a wanted item at a specific ref.
// If ref is empty, queries the working copy.
// Returns (status, true, nil) if found, ("", false, nil) if not found,
// or ("", false, err) if the query failed.
func QueryItemStatus(db DB, wantedID, ref string) (string, bool, error) {
	query := fmt.Sprintf(
		"SELECT status FROM wanted WHERE id = '%s'",
		EscapeSQL(wantedID),
	)
	out, err := db.Query(query, ref)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return "", false, nil
	}
	return strings.TrimSpace(lines[1]), true, nil
}

// QueryItemStatusAsOf is a convenience wrapper that returns "" on not-found or error.
//
// Deprecated: prefer QueryItemStatus for explicit error handling.
func QueryItemStatusAsOf(db DB, wantedID, ref string) string {
	status, _, _ := QueryItemStatus(db, wantedID, ref)
	return status
}

// DoltSQLQuery executes a SQL query and returns the raw CSV output.
func DoltSQLQuery(dbDir, query string) (string, error) {
	var result string
	err := doltRetry(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "dolt", "sql", "-r", "csv", "-q", query)
		cmd.Dir = dbDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("dolt sql query failed: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		result = string(output)
		return nil
	})
	return result, err
}
