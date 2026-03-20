package main

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newSyncCmd(stdout, stderr io.Writer) *cobra.Command {
	var dryRun bool
	var upgrade bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Pull upstream changes into local wl-commons fork",
		Args:  cobra.NoArgs,
		Long: `Sync your local wl-commons fork with the upstream hop/wl-commons.

If you have a local fork of wl-commons (created by wl join), this pulls
the latest changes from upstream.

Schema version changes are detected automatically:
  - MINOR version bumps (e.g. 1.1 → 1.2) are applied automatically
  - MAJOR version bumps (e.g. 1.x → 2.x) require --upgrade to proceed

EXAMPLES:
  wl sync                # Pull upstream changes
  wl sync --dry-run      # Show what would change
  wl sync --upgrade      # Allow major schema version upgrades`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSync(cmd, stdout, stderr, dryRun, upgrade)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without pulling")
	cmd.Flags().BoolVar(&upgrade, "upgrade", false, "Allow major schema version upgrades")

	return cmd
}

func runSync(cmd *cobra.Command, stdout, stderr io.Writer, dryRun, upgrade bool) error {
	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	// Remote mode: reads are always fresh from the DoltHub API.
	if cfg.ResolveBackend() != federation.BackendLocal {
		fmt.Fprintf(stdout, "Remote mode: reads are always fresh from the DoltHub API.\n")
		return nil
	}

	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		return fmt.Errorf("dolt not found in PATH — install from https://docs.dolthub.com/introduction/installation")
	}

	forkDir := cfg.LocalDir

	if forkDir == "" {
		return fmt.Errorf("no local wl-commons fork found\n\nJoin a wasteland first: wl join <org/db>")
	}

	fmt.Fprintf(stdout, "Local fork: %s\n", style.Dim.Render(forkDir))

	// Fetch upstream before version check (non-destructive).
	fetchCmd := exec.Command(doltPath, "fetch", "upstream")
	fetchCmd.Dir = forkDir
	fetchCmd.Stderr = stderr
	if err := fetchCmd.Run(); err != nil {
		return fmt.Errorf("fetching upstream: %w", err)
	}

	// Check schema versions.
	db := backend.NewLocalDB(forkDir, cfg.Mode)
	versionMsg, err := checkSchemaVersion(db, stdout, upgrade, dryRun)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintf(stdout, "\n%s Dry run — checking upstream for changes...\n", style.Bold.Render("~"))

		diffCmd := exec.Command(doltPath, "diff", "--stat", "HEAD", "upstream/main")
		diffCmd.Dir = forkDir
		diffCmd.Stderr = stderr
		diffOut, _ := diffCmd.Output()
		if len(diffOut) > 0 {
			fmt.Fprint(stdout, string(diffOut))
		} else {
			fmt.Fprintf(stdout, "%s Already up to date.\n", style.Bold.Render("✓"))
		}
		return nil
	}

	fmt.Fprintf(stdout, "\nPulling from upstream...\n")

	pullCmd := exec.Command(doltPath, "pull", "upstream", "main")
	pullCmd.Dir = forkDir
	pullCmd.Stdout = stdout
	pullCmd.Stderr = stderr
	if err := pullCmd.Run(); err != nil {
		return fmt.Errorf("pulling from upstream: %w", err)
	}

	fmt.Fprintf(stdout, "\n%s Synced with upstream\n", style.Bold.Render("✓"))
	if versionMsg != "" {
		fmt.Fprintf(stdout, "  %s\n", versionMsg)
	}
	updateSyncTimestamp(cfg)

	// Show summary
	summaryQuery := `SELECT
		(SELECT COUNT(*) FROM wanted WHERE status = 'open') AS open_wanted,
		(SELECT COUNT(*) FROM wanted) AS total_wanted,
		(SELECT COUNT(*) FROM completions) AS total_completions,
		(SELECT COUNT(*) FROM stamps) AS total_stamps`

	summaryCmd := exec.Command(doltPath, "sql", "-q", summaryQuery, "-r", "csv")
	summaryCmd.Dir = forkDir
	out, err := summaryCmd.Output()
	if err == nil {
		rows := wlParseCSV(string(out))
		if len(rows) >= 2 && len(rows[1]) >= 4 {
			r := rows[1]
			fmt.Fprintf(stdout, "\n  Open wanted:       %s\n", r[0])
			fmt.Fprintf(stdout, "  Total wanted:      %s\n", r[1])
			fmt.Fprintf(stdout, "  Total completions: %s\n", r[2])
			fmt.Fprintf(stdout, "  Total stamps:      %s\n", r[3])
		}
	}

	return nil
}

// checkSchemaVersion compares local and upstream schema versions and returns
// a message to display after sync. Returns an error if a MAJOR bump is
// detected and neither --upgrade nor --dry-run was passed.
// In dry-run mode, major bumps produce a warning but don't block.
func checkSchemaVersion(db commons.DB, stdout io.Writer, upgrade, dryRun bool) (string, error) {
	localVer, err := commons.ReadSchemaVersion(db, "")
	if err != nil {
		// Non-fatal: if we can't read the version, proceed with sync.
		return "", nil
	}

	upstreamVer, err := commons.ReadSchemaVersion(db, "upstream/main")
	if err != nil {
		return "", nil
	}

	// If either version is missing, proceed without gating.
	if localVer.IsZero() || upstreamVer.IsZero() {
		return "", nil
	}

	delta := commons.CompareVersions(localVer, upstreamVer)

	switch delta {
	case commons.VersionSame:
		return "", nil

	case commons.VersionMinor:
		return fmt.Sprintf("Schema updated: %s → %s (backwards-compatible)", localVer, upstreamVer), nil

	case commons.VersionMajor:
		if upgrade {
			return fmt.Sprintf("Schema upgraded: %s → %s (major version)", localVer, upstreamVer), nil
		}
		fmt.Fprintf(stdout, "\n  %s Schema upgrade required: %s → %s\n\n",
			style.Warning.Render(style.IconWarn), localVer, upstreamVer)
		fmt.Fprintf(stdout, "  This is a MAJOR version change that may affect your local data.\n")
		fmt.Fprintf(stdout, "  Review the changelog before upgrading.\n\n")
		fmt.Fprintf(stdout, "  To proceed: wl sync --upgrade\n")
		if dryRun {
			return fmt.Sprintf("Schema upgrade required: %s → %s (major version)", localVer, upstreamVer), nil
		}
		return "", fmt.Errorf("major schema upgrade required (use --upgrade to proceed)")

	case commons.VersionAhead:
		return fmt.Sprintf("Note: local schema (%s) is ahead of upstream (%s)", localVer, upstreamVer), nil
	}

	return "", nil
}
