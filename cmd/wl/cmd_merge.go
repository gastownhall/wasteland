package main

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newMergeCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		noPush     bool
		keepBranch bool
	)

	cmd := &cobra.Command{
		Use:   "merge <branch>",
		Short: "Merge a reviewed branch into main",
		Long: `Merge a wl/* branch into main after review.

Performs a Dolt merge, pushes main to upstream and origin, and deletes
the branch (unless --keep-branch is set).

Examples:
  wl merge wl/my-rig/w-abc123
  wl merge wl/my-rig/w-abc123 --keep-branch
  wl merge wl/my-rig/w-abc123 --no-push`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMerge(cmd, stdout, stderr, args[0], noPush, keepBranch)
		},
	}

	cmd.Flags().BoolVar(&noPush, "no-push", false, "Skip pushing to remotes")
	cmd.Flags().BoolVar(&keepBranch, "keep-branch", false, "Don't delete branch after merge")
	cmd.ValidArgsFunction = completeBranchNames

	return cmd
}

func runMerge(cmd *cobra.Command, stdout, _ io.Writer, branch string, noPush, keepBranch bool) error {
	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	// Remote mode: use RemoteDB.MergeBranch via the write API.
	if cfg.ResolveBackend() != federation.BackendLocal {
		if noPush {
			return fmt.Errorf("--no-push is not supported in remote mode (remote merges are immediate)")
		}
		return runMergeRemote(stdout, cfg, branch, keepBranch)
	}

	exists, err := commons.BranchExists(cfg.LocalDir, branch)
	if err != nil {
		return fmt.Errorf("checking branch: %w", err)
	}
	if !exists {
		return fmt.Errorf("branch %q does not exist", branch)
	}

	// Best-effort: check PR approval status before merging.
	if cfg.IsGitHub() {
		if ghPath, err := exec.LookPath("gh"); err == nil {
			client := newGHClient(ghPath)
			hasApproval, hasChangesRequested := prApprovalStatus(client, cfg.Upstream, cfg.ForkOrg, branch)
			if msg := mergeApprovalWarning(hasApproval, hasChangesRequested); msg != "" {
				fmt.Fprintf(stdout, "  %s %s\n", style.Warning.Render("⚠"), msg)
			}
		}
	}

	if err := commons.CheckoutMain(cfg.LocalDir); err != nil {
		return fmt.Errorf("checking out main: %w", err)
	}

	if err := commons.MergeBranch(cfg.LocalDir, branch); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "%s Merged %s into main\n", style.Bold.Render("✓"), branch)

	if !keepBranch {
		if err := commons.DeleteBranch(cfg.LocalDir, branch); err != nil {
			fmt.Fprintf(stdout, "  warning: failed to delete branch %s: %v\n", branch, err)
		} else {
			fmt.Fprintf(stdout, "  Branch %s deleted\n", branch)
		}
	}

	if !noPush {
		if err := commons.PushAllRemotes(cfg.LocalDir, stdout); err != nil {
			fmt.Fprintf(stdout, "\n  %s %s\n", style.Warning.Render(style.IconWarn),
				"Push failed — merge saved locally. Run 'wl sync' to retry.")
		}
	}

	// Best-effort: auto-close the corresponding GitHub PR shell.
	if cfg.IsGitHub() {
		if ghPath, err := exec.LookPath("gh"); err == nil {
			closeGitHubPR(newGHClient(ghPath), cfg.Upstream, cfg.ForkOrg, cfg.ForkDB, branch, stdout)
		}
	}

	return nil
}

func runMergeRemote(stdout io.Writer, cfg *federation.Config, branch string, keepBranch bool) error {
	db, err := openDBFromConfig(cfg)
	if err != nil {
		return err
	}

	sp := style.StartSpinner(stdout, "Merging branch via API...")
	err = db.MergeBranch(branch)
	sp.Stop()
	if err != nil {
		return fmt.Errorf("merging branch: %w", err)
	}

	fmt.Fprintf(stdout, "%s Merged %s into main\n", style.Bold.Render("✓"), branch)

	if !keepBranch {
		if err := db.DeleteBranch(branch); err != nil {
			fmt.Fprintf(stdout, "  warning: failed to delete branch %s: %v\n", branch, err)
		} else {
			fmt.Fprintf(stdout, "  Branch %s deleted\n", branch)
		}
	}

	return nil
}

// mergeApprovalWarning returns a warning message based on PR approval state.
// Returns "" if the PR is approved with no outstanding change requests.
func mergeApprovalWarning(hasApproval, hasChangesRequested bool) string {
	if hasChangesRequested {
		return "PR has outstanding change requests"
	}
	if !hasApproval {
		return "PR has no approvals"
	}
	return ""
}
