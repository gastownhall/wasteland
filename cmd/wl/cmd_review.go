package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/julianknutsen/wasteland/internal/backend"
	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/federation"
	"github.com/julianknutsen/wasteland/internal/remote"
	"github.com/julianknutsen/wasteland/internal/sdk"
	"github.com/julianknutsen/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newReviewCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		jsonOut  bool
		mdOut    bool
		statOut  bool
		createPR bool
	)

	cmd := &cobra.Command{
		Use:   "review [branch]",
		Short: "Review PR-mode branches",
		Long: `List or review PR-mode branches.

Without arguments, lists all wl/* branches.
With a branch name, shows the diff between main and the branch.

Output formats (mutually exclusive):
  (default)    Full diff piped to stdout
  --stat       Summary statistics
  --json       JSON diff output
  --md         Markdown-formatted diff for pasting into PRs
  --create-pr  Push branch and open a pull request on the upstream provider

Examples:
  wl review                          # list wl/* branches
  wl review wl/my-rig/w-abc123       # terminal diff
  wl review wl/my-rig/w-abc123 --stat
  wl review wl/my-rig/w-abc123 --md
  wl review wl/my-rig/w-abc123 --create-pr`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var branch string
			if len(args) == 1 {
				branch = args[0]
			}
			return runReview(cmd, stdout, stderr, branch, jsonOut, mdOut, statOut, createPR)
		},
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output diff as JSON")
	cmd.Flags().BoolVar(&mdOut, "md", false, "Output diff as Markdown")
	cmd.Flags().BoolVar(&statOut, "stat", false, "Output diff statistics")
	cmd.Flags().BoolVar(&createPR, "create-pr", false, "Push branch and open a PR on the upstream provider")

	return cmd
}

func runReview(cmd *cobra.Command, stdout, _ io.Writer, branch string, jsonOut, mdOut, statOut, createPR bool) error {
	// Validate mutually exclusive flags.
	flagCount := 0
	if jsonOut {
		flagCount++
	}
	if mdOut {
		flagCount++
	}
	if statOut {
		flagCount++
	}
	if createPR {
		flagCount++
	}
	if flagCount > 1 {
		return fmt.Errorf("--json, --md, --stat, and --create-pr are mutually exclusive")
	}

	if createPR && branch == "" {
		return fmt.Errorf("--create-pr requires a branch argument")
	}

	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	// Remote mode: use API for branch listing and PR creation.
	if cfg.ResolveBackend() != federation.BackendLocal {
		if branch == "" {
			return listReviewBranchesRemote(stdout, cfg)
		}
		if createPR {
			db, err := openDBFromConfig(cfg)
			if err != nil {
				return err
			}
			prURL, err := createPRForBranchRemote(cfg, db, branch)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "\n%s %s\n", style.Bold.Render("PR:"), prURL)
			return nil
		}
		// Show diff via API.
		db, err := openDBFromConfig(cfg)
		if err != nil {
			return err
		}
		if rdb, ok := db.(*backend.RemoteDB); ok {
			diff, err := rdb.Diff(branch)
			if err != nil {
				return fmt.Errorf("diff: %w", err)
			}
			fmt.Fprint(stdout, diff)
			return nil
		}
		return fmt.Errorf("diff not supported for this backend")
	}

	if branch == "" {
		return listReviewBranches(stdout, cfg.LocalDir)
	}

	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		return fmt.Errorf("dolt not found in PATH — install from https://docs.dolthub.com/introduction/installation")
	}

	base := diffBase(cfg.LocalDir, doltPath)

	if createPR {
		switch cfg.ResolveProviderType() {
		case "github":
			return runGitHubPR(stdout, cfg, doltPath, branch, base)
		case "dolthub":
			return runDoltHubPR(stdout, cfg, doltPath, branch, base)
		default:
			return fmt.Errorf("--create-pr: provider %q does not support pull requests", cfg.ResolveProviderType())
		}
	}

	return showDiff(stdout, cfg.LocalDir, doltPath, branch, base, jsonOut, mdOut, statOut)
}

// diffBase returns "upstream/main" if the upstream remote exists and can be
// fetched, otherwise "main". In fork mode the upstream remote points to the
// canonical commons, so diffs show what the upstream maintainer would see. In
// direct mode there is no upstream remote and origin IS upstream, so we fall
// back to local main. A fetch is required so that the upstream/main ref is
// available locally (dolt remote add does not fetch).
func diffBase(dbDir, doltPath string) string {
	cmd := exec.Command(doltPath, "remote", "-v")
	cmd.Dir = dbDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "main"
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "upstream") {
			fetch := exec.Command(doltPath, "fetch", "upstream")
			fetch.Dir = dbDir
			if err := fetch.Run(); err != nil {
				return "main"
			}
			return "upstream/main"
		}
	}
	return "main"
}

func listReviewBranchesRemote(stdout io.Writer, cfg *federation.Config) error {
	db, err := openDBFromConfig(cfg)
	if err != nil {
		return err
	}
	branches, err := db.Branches("wl/")
	if err != nil {
		return fmt.Errorf("listing branches: %w", err)
	}
	if len(branches) == 0 {
		fmt.Fprintln(stdout, "No review branches found.")
		return nil
	}
	fmt.Fprintf(stdout, "%s\n", style.Bold.Render("Review branches:"))
	for _, b := range branches {
		fmt.Fprintf(stdout, "  %s\n", b)
	}
	return nil
}

func listReviewBranches(stdout io.Writer, dbDir string) error {
	branches, err := commons.ListBranches(dbDir, "wl/")
	if err != nil {
		return fmt.Errorf("listing branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Fprintln(stdout, "No review branches found.")
		return nil
	}

	fmt.Fprintf(stdout, "%s\n", style.Bold.Render("Review branches:"))
	for _, b := range branches {
		fmt.Fprintf(stdout, "  %s\n", b)
	}
	return nil
}

func showDiff(stdout io.Writer, dbDir, doltPath, branch, base string, jsonOut, mdOut, statOut bool) error {
	if statOut {
		cmd := exec.Command(doltPath, "diff", "--stat", base+"..."+branch)
		cmd.Dir = dbDir
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("dolt diff --stat: %w", err)
		}
		return nil
	}

	if jsonOut {
		cmd := exec.Command(doltPath, "diff", "-r", "json", base+"..."+branch)
		cmd.Dir = dbDir
		cmd.Stdout = stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("dolt diff -r json: %w", err)
		}
		return nil
	}

	if mdOut {
		return renderMarkdownDiff(stdout, dbDir, doltPath, branch, base)
	}

	// Default: full terminal diff.
	cmd := exec.Command(doltPath, "diff", base+"..."+branch)
	cmd.Dir = dbDir
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dolt diff: %w", err)
	}
	return nil
}

func renderMarkdownDiff(stdout io.Writer, dbDir, doltPath, branch, base string) error {
	fmt.Fprintf(stdout, "## wl review: %s\n\n", branch)

	// Summary (stat).
	fmt.Fprintln(stdout, "### Summary")
	fmt.Fprintln(stdout, "```")

	statCmd := exec.Command(doltPath, "diff", "--stat", base+"..."+branch)
	statCmd.Dir = dbDir
	statOut, err := statCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(stdout, "(no changes)\n")
	} else {
		fmt.Fprint(stdout, strings.TrimRight(string(statOut), "\n")+"\n")
	}
	fmt.Fprintln(stdout, "```")
	fmt.Fprintln(stdout)

	// Changes (SQL diff).
	fmt.Fprintln(stdout, "### Changes")
	fmt.Fprintln(stdout, "```sql")

	diffCmd := exec.Command(doltPath, "diff", "-r", "sql", base+"..."+branch)
	diffCmd.Dir = dbDir
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(stdout, "-- (no SQL changes)\n")
	} else {
		fmt.Fprint(stdout, strings.TrimRight(string(diffOut), "\n")+"\n")
	}
	fmt.Fprintln(stdout, "```")

	return nil
}

// --- GitHub PR shell ---

func runGitHubPR(stdout io.Writer, cfg *federation.Config, doltPath, branch, base string) error {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return fmt.Errorf("gh not found in PATH — install from https://cli.github.com")
	}

	// In GitHub mode, origin is already GitHub; force-push dolt branch there.
	// Force is safe — this is a wl/* branch on the user's own fork.
	if err := commons.PushBranchToRemoteForce(cfg.LocalDir, "origin", branch, true, stdout); err != nil {
		return fmt.Errorf("pushing to GitHub fork: %w", err)
	}

	// Generate markdown diff.
	var mdBuf bytes.Buffer
	if err := renderMarkdownDiff(&mdBuf, cfg.LocalDir, doltPath, branch, base); err != nil {
		return fmt.Errorf("generating markdown diff: %w", err)
	}

	// Get wanted title for PR title.
	title := wantedTitleFromBranch(doltPath, cfg.LocalDir, branch)
	prTitle := fmt.Sprintf("[wl] %s", title)

	// Create git-native branch on fork + cross-fork PR to upstream.
	client := newGHClient(ghPath)
	prURL, err := createGitHubPR(client, cfg.Upstream, cfg.ForkOrg, cfg.ForkDB, branch, prTitle, mdBuf.String(), stdout)
	if err != nil {
		return err
	}

	fmt.Fprintf(stdout, "\n%s %s\n", style.Bold.Render("PR:"), prURL)
	return nil
}

func runDoltHubPR(stdout io.Writer, cfg *federation.Config, doltPath, branch, base string) error {
	token := os.Getenv("DOLTHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("DOLTHUB_TOKEN environment variable is required for DoltHub PRs")
	}

	// Force-push dolt branch to origin.
	// Force is safe — this is a wl/* branch on the user's own fork.
	if err := commons.PushBranchToRemoteForce(cfg.LocalDir, "origin", branch, true, stdout); err != nil {
		return fmt.Errorf("pushing to DoltHub fork: %w", err)
	}

	// Generate markdown diff for PR body.
	var mdBuf bytes.Buffer
	if err := renderMarkdownDiff(&mdBuf, cfg.LocalDir, doltPath, branch, base); err != nil {
		return fmt.Errorf("generating markdown diff: %w", err)
	}

	// Get wanted title for PR title.
	title := wantedTitleFromBranch(doltPath, cfg.LocalDir, branch)
	prTitle := fmt.Sprintf("[wl] %s", title)

	// Parse upstream into org + db.
	upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
	if err != nil {
		return fmt.Errorf("parsing upstream: %w", err)
	}

	// Create PR via DoltHub REST API, or update if one already exists.
	provider := remote.NewDoltHubProvider(token)
	prURL, err := provider.CreatePR(cfg.ForkOrg, upstreamOrg, db, branch, prTitle, mdBuf.String())
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			existingURL, existingID := provider.FindPR(upstreamOrg, db, cfg.ForkOrg, branch)
			if existingID != "" {
				if updateErr := provider.UpdatePR(upstreamOrg, db, existingID, prTitle, mdBuf.String()); updateErr != nil {
					fmt.Fprintf(stdout, "  warning: could not update existing PR: %v\n", updateErr)
				} else {
					fmt.Fprintf(stdout, "  Updated existing PR.\n")
				}
				fmt.Fprintf(stdout, "\n%s %s\n", style.Bold.Render("PR:"), existingURL)
				return nil
			}
			// Could not find the existing PR — return the pulls page.
			prURL = fmt.Sprintf("%s/%s/%s/pulls", "https://www.dolthub.com/repositories", upstreamOrg, db)
			fmt.Fprintf(stdout, "  PR already exists for this branch.\n")
			fmt.Fprintf(stdout, "\n%s %s\n", style.Bold.Render("PR:"), prURL)
			return nil
		}
		return fmt.Errorf("creating DoltHub PR: %w", err)
	}

	fmt.Fprintf(stdout, "\n%s %s\n", style.Bold.Render("PR:"), prURL)
	return nil
}

func createGitHubPR(client GitHubPRClient, upstreamRepo, forkOrg, forkDB, wlBranch, title, mdBody string, stdout io.Writer) (string, error) {
	forkRepo := forkOrg + "/" + forkDB
	wantedID := extractWantedID(wlBranch)
	markerPath := ".wasteland/" + wantedID + ".md"

	// 1. Get fork's default branch SHA.
	fmt.Fprintln(stdout, "  Getting fork HEAD...")
	headSHA, err := client.GetRef(forkRepo, "heads/main")
	if err != nil {
		return "", fmt.Errorf("getting fork HEAD: %w", err)
	}

	// 2. Get base tree SHA from the commit.
	baseTreeSHA, err := client.GetCommitTree(forkRepo, headSHA)
	if err != nil {
		return "", fmt.Errorf("getting base commit: %w", err)
	}

	// 3. Create blob with marker file content.
	fmt.Fprintln(stdout, "  Creating marker file...")
	blobSHA, err := client.CreateBlob(forkRepo, mdBody, "utf-8")
	if err != nil {
		return "", fmt.Errorf("creating blob: %w", err)
	}

	// 4. Create tree with marker file.
	treeSHA, err := client.CreateTree(forkRepo, baseTreeSHA, []TreeEntry{{
		Path: markerPath,
		Mode: "100644",
		Type: "blob",
		SHA:  blobSHA,
	}})
	if err != nil {
		return "", fmt.Errorf("creating tree: %w", err)
	}

	// 5. Create commit on fork.
	fmt.Fprintln(stdout, "  Creating commit...")
	commitSHA, err := client.CreateCommit(forkRepo, fmt.Sprintf("wl review: %s", wlBranch), treeSHA, []string{headSHA})
	if err != nil {
		return "", fmt.Errorf("creating commit: %w", err)
	}

	// 6. Create or update ref on fork.
	fmt.Fprintln(stdout, "  Pushing branch to fork...")
	if err := client.CreateRef(forkRepo, "refs/heads/"+wlBranch, commitSHA); err != nil {
		// Ref may already exist — force-update it.
		if err := client.UpdateRef(forkRepo, "heads/"+wlBranch, commitSHA, true); err != nil {
			return "", fmt.Errorf("creating/updating ref: %w", err)
		}
	}

	// 7. Create cross-fork PR or update existing.
	fmt.Fprintln(stdout, "  Opening PR...")
	head := forkOrg + ":" + wlBranch

	existingURL, existingNumber := client.FindPR(upstreamRepo, head)
	if existingNumber != "" {
		_ = client.UpdatePR(upstreamRepo, existingNumber, map[string]string{"body": mdBody})
		return existingURL, nil
	}

	prURL, err := client.CreatePR(upstreamRepo, title, mdBody, head, "main")
	if err != nil {
		return "", fmt.Errorf("creating PR: %w", err)
	}
	return prURL, nil
}

// findExistingPR checks for an open PR on upstream with the given head ref.
// Returns the PR URL and number, or empty strings if none found.
func findExistingPR(ghPath, upstreamRepo, head string) (url, number string) {
	cmd := exec.Command(ghPath, "pr", "list", "--repo", upstreamRepo, "--head", head, "--state", "open", "--json", "number,url")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", ""
	}
	var prs []struct {
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(out, &prs); err != nil || len(prs) == 0 {
		return "", ""
	}
	return prs[0].URL, fmt.Sprintf("%d", prs[0].Number)
}

// ghAPICall executes a GitHub API call via the gh CLI.
func ghAPICall(ghPath, method, endpoint, body string) ([]byte, error) {
	args := []string{"api", endpoint}
	if method != "GET" {
		args = append(args, "-X", method)
	}
	if body != "" {
		args = append(args, "--input", "-")
	}
	cmd := exec.Command(ghPath, args...)
	if body != "" {
		cmd.Stdin = strings.NewReader(body)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("gh api %s %s: %w (%s)", method, endpoint, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// extractWantedID extracts the wanted ID from a branch name (wl/<rig>/<id> → <id>).
func extractWantedID(branch string) string {
	parts := strings.SplitN(branch, "/", 3)
	if len(parts) < 3 {
		return branch
	}
	return parts[2]
}

// wantedTitleFromBranch queries the wanted table for the item title.
// Falls back to the branch name if the query fails.
func wantedTitleFromBranch(doltPath, dbDir, branch string) string {
	wantedID := extractWantedID(branch)
	query := fmt.Sprintf(
		"SELECT title FROM wanted AS OF '%s' WHERE id = '%s' LIMIT 1",
		commons.EscapeSQL(branch),
		commons.EscapeSQL(wantedID),
	)
	cmd := exec.Command(doltPath, "sql", "-r", "csv", "-q", query)
	cmd.Dir = dbDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return branch
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		return branch
	}
	return strings.TrimSpace(lines[1])
}

// submitPRReview submits a review on the GitHub PR for the given branch.
// event must be "APPROVE" or "REQUEST_CHANGES".
func submitPRReview(client GitHubPRClient, upstreamRepo, forkOrg, branch, event, comment string) (string, error) {
	head := forkOrg + ":" + branch
	prURL, number := client.FindPR(upstreamRepo, head)
	if number == "" {
		return "", fmt.Errorf("no open PR found for branch %s", branch)
	}

	if err := client.SubmitReview(upstreamRepo, number, event, comment); err != nil {
		return "", fmt.Errorf("submitting review: %w", err)
	}
	return prURL, nil
}

// parseReviewStatus parses GitHub review list JSON into approval state.
// It tracks the latest review state per user and returns two independent bools.
func parseReviewStatus(data []byte) (hasApproval, hasChangesRequested bool) {
	var reviews []struct {
		User  struct{ Login string } `json:"user"`
		State string                 `json:"state"`
	}
	if err := json.Unmarshal(data, &reviews); err != nil {
		return false, false
	}

	latest := map[string]string{}
	for _, r := range reviews {
		switch r.State {
		case "APPROVED", "CHANGES_REQUESTED":
			latest[r.User.Login] = r.State
		}
	}

	for _, state := range latest {
		switch state {
		case "APPROVED":
			hasApproval = true
		case "CHANGES_REQUESTED":
			hasChangesRequested = true
		}
	}
	return hasApproval, hasChangesRequested
}

// prApprovalStatus checks the review status of a GitHub PR. Best-effort.
// Silently returns (false, false) on any error.
func prApprovalStatus(client GitHubPRClient, upstreamRepo, forkOrg, branch string) (hasApproval, hasChangesRequested bool) {
	head := forkOrg + ":" + branch
	_, number := client.FindPR(upstreamRepo, head)
	if number == "" {
		return false, false
	}

	data, err := client.ListReviews(upstreamRepo, number)
	if err != nil {
		return false, false
	}
	return parseReviewStatus(data)
}

// closeGitHubPR finds and closes an open GitHub PR for the given branch.
// Best-effort: failures print warnings but don't block the merge.
func closeGitHubPR(client GitHubPRClient, upstreamRepo, forkOrg, forkDB, branch string, stdout io.Writer) {
	head := forkOrg + ":" + branch
	prURL, number := client.FindPR(upstreamRepo, head)
	if number == "" {
		return
	}

	if err := client.ClosePR(upstreamRepo, number); err != nil {
		fmt.Fprintf(stdout, "  warning: failed to close PR %s: %v\n", prURL, err)
		return
	}

	// Add a closing comment.
	_ = client.AddComment(upstreamRepo, number, "Merged via `wl merge`.")

	// Delete the branch on the fork.
	forkRepo := forkOrg + "/" + forkDB
	if err := client.DeleteRef(forkRepo, "heads/"+branch); err != nil {
		fmt.Fprintf(stdout, "  warning: failed to delete GitHub branch %s: %v\n", branch, err)
	}

	fmt.Fprintf(stdout, "  Closed PR %s\n", prURL)
}

// createPRForBranch creates an upstream PR for the given branch.
// Returns the PR URL on success. Used by both the CLI --create-pr flag
// and the TUI submit PR flow.
func createPRForBranch(cfg *federation.Config, branch string) (string, error) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		return "", fmt.Errorf("dolt not found in PATH")
	}
	base := diffBase(cfg.LocalDir, doltPath)

	switch cfg.ResolveProviderType() {
	case "github":
		return createPRForBranchGitHub(cfg, doltPath, branch, base)
	case "dolthub":
		return createPRForBranchDoltHub(cfg, doltPath, branch, base)
	default:
		return "", fmt.Errorf("provider %q does not support pull requests", cfg.ResolveProviderType())
	}
}

func createPRForBranchGitHub(cfg *federation.Config, doltPath, branch, base string) (string, error) {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return "", fmt.Errorf("gh not found in PATH — install from https://cli.github.com")
	}

	// Force-push dolt branch to origin.
	if err := commons.PushBranchToRemoteForce(cfg.LocalDir, "origin", branch, true, io.Discard); err != nil {
		return "", fmt.Errorf("pushing to GitHub fork: %w", err)
	}

	// Generate markdown diff.
	var mdBuf bytes.Buffer
	if err := renderMarkdownDiff(&mdBuf, cfg.LocalDir, doltPath, branch, base); err != nil {
		return "", fmt.Errorf("generating markdown diff: %w", err)
	}

	title := wantedTitleFromBranch(doltPath, cfg.LocalDir, branch)
	prTitle := fmt.Sprintf("[wl] %s", title)

	client := newGHClient(ghPath)
	return createGitHubPR(client, cfg.Upstream, cfg.ForkOrg, cfg.ForkDB, branch, prTitle, mdBuf.String(), io.Discard)
}

func createPRForBranchDoltHub(cfg *federation.Config, doltPath, branch, base string) (string, error) {
	token := os.Getenv("DOLTHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("DOLTHUB_TOKEN environment variable is required for DoltHub PRs")
	}

	// Force-push dolt branch to origin.
	if err := commons.PushBranchToRemoteForce(cfg.LocalDir, "origin", branch, true, io.Discard); err != nil {
		return "", fmt.Errorf("pushing to DoltHub fork: %w", err)
	}

	var mdBuf bytes.Buffer
	if err := renderMarkdownDiff(&mdBuf, cfg.LocalDir, doltPath, branch, base); err != nil {
		return "", fmt.Errorf("generating markdown diff: %w", err)
	}

	title := wantedTitleFromBranch(doltPath, cfg.LocalDir, branch)
	prTitle := fmt.Sprintf("[wl] %s", title)

	upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
	if err != nil {
		return "", fmt.Errorf("parsing upstream: %w", err)
	}

	provider := remote.NewDoltHubProvider(token)
	prURL, err := provider.CreatePR(cfg.ForkOrg, upstreamOrg, db, branch, prTitle, mdBuf.String())
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			existingURL, existingID := provider.FindPR(upstreamOrg, db, cfg.ForkOrg, branch)
			if existingID != "" {
				_ = provider.UpdatePR(upstreamOrg, db, existingID, prTitle, mdBuf.String())
				return existingURL, nil
			}
			return fmt.Sprintf("%s/%s/%s/pulls", "https://www.dolthub.com/repositories", upstreamOrg, db), nil
		}
		return "", fmt.Errorf("creating DoltHub PR: %w", err)
	}
	return prURL, nil
}

// createPRForBranchRemote creates a DoltHub PR in remote mode. The branch already
// exists on the fork (the write API auto-pushes), so no local dolt is needed.
func createPRForBranchRemote(cfg *federation.Config, cdb commons.DB, branch string) (string, error) {
	if cfg.ResolveProviderType() != "dolthub" {
		if cfg.IsGitHub() {
			return "", fmt.Errorf("GitHub PRs require local dolt; ensure --local-db is set")
		}
		return "", fmt.Errorf("remote backend only supports DoltHub PRs")
	}

	token := os.Getenv("DOLTHUB_TOKEN")
	if token == "" {
		return "", fmt.Errorf("DOLTHUB_TOKEN environment variable is required for DoltHub PRs")
	}

	upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
	if err != nil {
		return "", fmt.Errorf("parsing upstream: %w", err)
	}

	// Build PR title from the wanted item title.
	wantedID := extractWantedID(branch)
	prTitle := fmt.Sprintf("[wl] %s", wantedID)
	if item, _, _, qerr := commons.QueryFullDetailAsOf(cdb, wantedID, branch); qerr == nil && item != nil {
		prTitle = fmt.Sprintf("[wl] %s", item.Title)
	}

	// Build PR description from the branch diff.
	var prBody string
	if rdb, ok := cdb.(*backend.RemoteDB); ok {
		if diff, derr := rdb.Diff(branch); derr == nil {
			prBody = diff
		}
	}

	provider := remote.NewDoltHubProvider(token)
	prURL, err := provider.CreatePR(cfg.ForkOrg, upstreamOrg, db, branch, prTitle, prBody)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			existingURL, existingID := provider.FindPR(upstreamOrg, db, cfg.ForkOrg, branch)
			if existingID != "" {
				_ = provider.UpdatePR(upstreamOrg, db, existingID, prTitle, prBody)
				return existingURL, nil
			}
			return fmt.Sprintf("%s/%s/%s/pulls", "https://www.dolthub.com/repositories", upstreamOrg, db), nil
		}
		return "", fmt.Errorf("creating DoltHub PR: %w", err)
	}
	return prURL, nil
}

// checkPRForBranch checks if an upstream PR already exists for the given branch.
// Returns the PR URL or empty string. Best-effort: returns "" on any error.
func checkPRForBranch(cfg *federation.Config, branch string) string {
	switch cfg.ResolveProviderType() {
	case "github":
		ghPath, err := exec.LookPath("gh")
		if err != nil {
			return ""
		}
		client := newGHClient(ghPath)
		head := cfg.ForkOrg + ":" + branch
		url, _ := client.FindPR(cfg.Upstream, head)
		return url
	case "dolthub":
		token := os.Getenv("DOLTHUB_TOKEN")
		if token == "" {
			return ""
		}
		upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
		if err != nil {
			return ""
		}
		provider := remote.NewDoltHubProvider(token)
		url, _ := provider.FindPR(upstreamOrg, db, cfg.ForkOrg, branch)
		return url
	default:
		return ""
	}
}

// closePRForBranch finds and closes the PR associated with the given branch.
// Returns nil on success or if no PR exists.
func closePRForBranch(cfg *federation.Config, branch string) error {
	switch cfg.ResolveProviderType() {
	case "github":
		ghPath, err := exec.LookPath("gh")
		if err != nil {
			return nil // no gh CLI, best-effort
		}
		client := newGHClient(ghPath)
		head := cfg.ForkOrg + ":" + branch
		_, number := client.FindPR(cfg.Upstream, head)
		if number == "" {
			return nil
		}
		return client.ClosePR(cfg.Upstream, number)
	case "dolthub":
		token := os.Getenv("DOLTHUB_TOKEN")
		if token == "" {
			return nil
		}
		upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
		if err != nil {
			return nil
		}
		provider := remote.NewDoltHubProvider(token)
		_, prID := provider.FindPR(upstreamOrg, db, cfg.ForkOrg, branch)
		if prID == "" {
			return nil
		}
		return provider.ClosePR(upstreamOrg, db, prID)
	default:
		return nil
	}
}

// listPendingItemsFromPRs returns a callback that lists wanted IDs with open
// upstream PRs. Uses a 30-second TTL cache to avoid hammering the API.
// Returns nil if the provider type does not support PR listing.
func listPendingItemsFromPRs(cfg *federation.Config) func() (map[string][]sdk.PendingItem, error) {
	switch cfg.ResolveProviderType() {
	case "dolthub":
		return dolthubListPendingItems(cfg)
	case "github":
		ghPath, err := exec.LookPath("gh")
		if err != nil {
			return nil
		}
		return ghListPendingItems(ghPath, cfg.Upstream)
	default:
		return nil
	}
}

func dolthubListPendingItems(cfg *federation.Config) func() (map[string][]sdk.PendingItem, error) {
	token := commons.DoltHubToken()
	if token == "" {
		return nil
	}
	upstreamOrg, db, err := federation.ParseUpstream(cfg.Upstream)
	if err != nil {
		return nil
	}

	var (
		mu       sync.Mutex
		cached   map[string][]sdk.PendingItem
		cachedAt time.Time
		cacheTTL = 30 * time.Second
	)

	return func() (map[string][]sdk.PendingItem, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && time.Since(cachedAt) < cacheTTL {
			return cached, nil
		}
		provider := remote.NewDoltHubProvider(token)
		states, err := provider.ListPendingWantedIDs(upstreamOrg, db)
		if err != nil {
			return nil, err
		}
		result := make(map[string][]sdk.PendingItem, len(states))
		for id, pending := range states {
			items := make([]sdk.PendingItem, len(pending))
			for i, p := range pending {
				items[i] = sdk.PendingItem{
					RigHandle: p.RigHandle,
					Status:    p.Status,
					ClaimedBy: p.ClaimedBy,
					Branch:    p.Branch,
					BranchURL: p.BranchURL,
					PRURL:     p.PRURL,
				}
			}
			result[id] = items
		}
		cached = result
		cachedAt = time.Now()
		return cached, nil
	}
}

func ghListPendingItems(ghPath, upstreamRepo string) func() (map[string][]sdk.PendingItem, error) {
	var (
		mu       sync.Mutex
		cached   map[string][]sdk.PendingItem
		cachedAt time.Time
		cacheTTL = 30 * time.Second
	)
	return func() (map[string][]sdk.PendingItem, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != nil && time.Since(cachedAt) < cacheTTL {
			return cached, nil
		}
		out, err := exec.Command(ghPath, "api",
			fmt.Sprintf("repos/%s/pulls?state=open&per_page=100", upstreamRepo),
		).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("listing GitHub PRs: %w", err)
		}
		var prs []struct {
			Head struct {
				Ref string `json:"ref"`
			} `json:"head"`
		}
		if err := json.Unmarshal(out, &prs); err != nil {
			return nil, fmt.Errorf("parsing GitHub PRs: %w", err)
		}
		ids := make(map[string][]sdk.PendingItem)
		for _, pr := range prs {
			parts := strings.SplitN(pr.Head.Ref, "/", 3)
			if len(parts) == 3 && parts[0] == "wl" {
				ids[parts[2]] = append(ids[parts[2]], sdk.PendingItem{
					RigHandle: parts[1],
				})
			}
		}
		cached = ids
		cachedAt = time.Now()
		return cached, nil
	}
}

// branchURLCallback returns a callback that builds a DoltHub branch URL.
// Returns nil if the provider is not DoltHub or fork info is missing.
func branchURLCallback(cfg *federation.Config) func(string) string {
	if cfg.ForkOrg == "" || cfg.ForkDB == "" {
		return nil
	}
	switch cfg.ResolveProviderType() {
	case "dolthub":
		return func(branch string) string {
			return fmt.Sprintf("https://www.dolthub.com/repositories/%s/%s/data/%s",
				cfg.ForkOrg, cfg.ForkDB, strings.ReplaceAll(branch, "/", "%2F"))
		}
	case "github":
		return func(branch string) string {
			return fmt.Sprintf("https://github.com/%s/%s/tree/%s",
				cfg.ForkOrg, cfg.ForkDB, strings.ReplaceAll(branch, "/", "%2F"))
		}
	default:
		return nil
	}
}
