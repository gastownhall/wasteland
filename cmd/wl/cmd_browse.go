package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/federation"
	"github.com/julianknutsen/wasteland/internal/sdk"
	"github.com/julianknutsen/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newBrowseCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		project   string
		status    string
		itemType  string
		priority  int
		limit     int
		jsonOut   bool
		ephemeral bool
		postedBy  string
		claimedBy string
		search    string
		view      string
	)

	cmd := &cobra.Command{
		Use:   "browse",
		Short: "Browse wanted items on the commons board",
		Args:  cobra.NoArgs,
		Long: `Browse the Wasteland wanted board.

Pulls the latest upstream changes into your local clone and queries it.
Use --ephemeral to clone to a temp dir instead (slower, for edge cases).

In PR mode, branch mutations are merged into the results (same as the web UI).
Use --view to control which branches are included:
  mine      Only your branches (default)
  all       All rigs' branches
  upstream  No branch overlay, pure main data

EXAMPLES:
  wl browse                          # All open wanted items
  wl browse --project gastown        # Filter by project
  wl browse --type bug               # Only bugs
  wl browse --status claimed         # Claimed items
  wl browse --priority 0             # Critical priority only
  wl browse --limit 5               # Show 5 items
  wl browse --json                   # JSON output
  wl browse --view all               # Include all rigs' branch mutations
  wl browse --posted-by alice        # Items posted by alice
  wl browse --claimed-by bob         # Items claimed by bob
  wl browse --search auth            # Search in title
  wl browse --ephemeral              # Clone upstream (slow)`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBrowse(cmd, stdout, stderr, commons.BrowseFilter{
				Status:    status,
				Project:   project,
				Type:      itemType,
				Priority:  priority,
				Limit:     limit,
				PostedBy:  postedBy,
				ClaimedBy: claimedBy,
				Search:    search,
				View:      view,
			}, jsonOut, ephemeral)
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "Filter by project (e.g., gastown, beads, hop)")
	cmd.Flags().StringVar(&status, "status", "open", "Filter by status (open, claimed, in_review, completed, withdrawn)")
	cmd.Flags().StringVar(&itemType, "type", "", "Filter by type (feature, bug, design, rfc, docs, inference)")
	cmd.Flags().IntVar(&priority, "priority", -1, "Filter by priority (0=critical, 2=medium, 4=backlog)")
	cmd.Flags().IntVar(&limit, "limit", 50, "Maximum items to display")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ephemeral, "ephemeral", false, "Clone upstream to temp dir instead of querying local (slow)")
	cmd.Flags().StringVar(&postedBy, "posted-by", "", "Filter by poster's rig handle")
	cmd.Flags().StringVar(&claimedBy, "claimed-by", "", "Filter by claimer's rig handle")
	cmd.Flags().StringVar(&search, "search", "", "Search in title")
	cmd.Flags().StringVar(&view, "view", "", "Branch view: mine (default), all, or upstream")
	_ = cmd.RegisterFlagCompletionFunc("project", completeProjectNames)
	_ = cmd.RegisterFlagCompletionFunc("status", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"open", "claimed", "in_review", "completed", "withdrawn"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("type", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"feature", "bug", "design", "rfc", "docs", "inference"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("view", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"mine", "all", "upstream"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func runBrowse(cmd *cobra.Command, stdout, stderr io.Writer, filter commons.BrowseFilter, jsonOut, ephemeral bool) error {
	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	if err := requireDolt(); err != nil {
		return err
	}

	if ephemeral {
		query := commons.BuildBrowseQuery(filter)
		return runBrowseEphemeral(stdout, cfg, query, jsonOut)
	}

	if err := runBrowseLocal(stdout, stderr, cfg, filter, jsonOut); err != nil {
		return err
	}
	warnIfStale(stdout, cfg)
	return nil
}

func runBrowseLocal(stdout, stderr io.Writer, cfg *federation.Config, filter commons.BrowseFilter, jsonOut bool) error {
	spinnerOut := stdout
	if jsonOut {
		spinnerOut = stderr
	}
	sp := style.StartSpinner(spinnerOut, "Syncing with upstream...")
	err := commons.PullUpstream(cfg.LocalDir)
	if err != nil {
		sp.Stop()
		return fmt.Errorf("pulling upstream: %w", err)
	}

	// Fetch origin branches and create local tracking branches so
	// BrowseWantedBranchAware can see PR-mode mutations via AS OF.
	if cfg.ResolveMode() == federation.ModePR {
		_ = commons.FetchRemote(cfg.LocalDir, "origin")
		_ = commons.TrackOriginBranches(cfg.LocalDir, "wl/")
	}
	sp.Stop()

	db := openDB(cfg.LocalDir)
	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: cfg.RigHandle,
		Mode:      cfg.ResolveMode(),
	})

	result, err := client.Browse(filter)
	if err != nil {
		return fmt.Errorf("querying wanted board: %w", err)
	}

	if jsonOut {
		return renderBrowseJSON(stdout, result)
	}
	return renderBrowseSummaries(stdout, result)
}

func renderBrowseSummaries(stdout io.Writer, result *sdk.BrowseResult) error {
	items := result.Items
	if len(items) == 0 {
		fmt.Fprintln(stdout, "No wanted items found matching your filters.")
		return nil
	}

	tbl := style.NewTable(
		style.Column{Name: "ID", Width: 12},
		style.Column{Name: "TITLE", Width: 40},
		style.Column{Name: "PROJECT", Width: 12},
		style.Column{Name: "TYPE", Width: 10},
		style.Column{Name: "PRI", Width: 4, Align: style.AlignRight},
		style.Column{Name: "POSTED BY", Width: 16},
		style.Column{Name: "STATUS", Width: 10},
		style.Column{Name: "EFFORT", Width: 8},
	)

	for _, item := range items {
		pri := wlFormatPriority(fmt.Sprintf("%d", item.Priority))
		tbl.AddRow(item.ID, item.Title, item.Project, item.Type, pri, item.PostedBy, item.Status, item.EffortLevel)
	}

	fmt.Fprintf(stdout, "Wanted items (%d):\n\n", len(items))
	fmt.Fprint(stdout, tbl.Render())

	return nil
}

func renderBrowseJSON(stdout io.Writer, result *sdk.BrowseResult) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result.Items)
}

func runBrowseEphemeral(stdout io.Writer, cfg *federation.Config, query string, jsonOut bool) error {
	doltPath, _ := exec.LookPath("dolt")

	_, commonsDB, _ := federation.ParseUpstream(cfg.Upstream)
	cloneURL := cfg.UpstreamURL
	if cloneURL == "" {
		cloneURL = cfg.Upstream
	}

	tmpDir, err := os.MkdirTemp("", "wl-browse-*")
	if err != nil {
		return fmt.Errorf("creating temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	cloneDir := filepath.Join(tmpDir, commonsDB)

	fmt.Fprintf(stdout, "Cloning %s...\n", style.Bold.Render(cfg.Upstream))

	cloneCmd := exec.Command(doltPath, "clone", cloneURL, cloneDir)
	cloneCmd.Stderr = os.Stderr
	if err := cloneCmd.Run(); err != nil {
		return fmt.Errorf("cloning %s: %w", cfg.Upstream, err)
	}
	fmt.Fprintf(stdout, "%s Cloned successfully\n\n", style.Bold.Render("✓"))

	if jsonOut {
		sqlCmd := exec.Command(doltPath, "sql", "-q", query, "-r", "json")
		sqlCmd.Dir = cloneDir
		sqlCmd.Stdout = stdout
		sqlCmd.Stderr = os.Stderr
		return sqlCmd.Run()
	}

	return renderBrowseTable(stdout, doltPath, cloneDir, query)
}

func renderBrowseTable(stdout io.Writer, doltPath, cloneDir, query string) error {
	sqlCmd := exec.Command(doltPath, "sql", "-q", query, "-r", "csv")
	sqlCmd.Dir = cloneDir
	output, err := sqlCmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("query failed: %s", string(exitErr.Stderr))
		}
		return fmt.Errorf("running query: %w", err)
	}

	return renderBrowseCSV(stdout, string(output))
}

func renderBrowseCSV(stdout io.Writer, csvData string) error {
	rows := wlParseCSV(csvData)
	if len(rows) <= 1 {
		fmt.Fprintln(stdout, "No wanted items found matching your filters.")
		return nil
	}

	tbl := style.NewTable(
		style.Column{Name: "ID", Width: 12},
		style.Column{Name: "TITLE", Width: 40},
		style.Column{Name: "PROJECT", Width: 12},
		style.Column{Name: "TYPE", Width: 10},
		style.Column{Name: "PRI", Width: 4, Align: style.AlignRight},
		style.Column{Name: "POSTED BY", Width: 16},
		style.Column{Name: "STATUS", Width: 10},
		style.Column{Name: "EFFORT", Width: 8},
	)

	for _, row := range rows[1:] {
		if len(row) < 9 {
			continue
		}
		pri := wlFormatPriority(row[4])
		tbl.AddRow(row[0], row[1], row[2], row[3], pri, row[5], row[7], row[8])
	}

	fmt.Fprintf(stdout, "Wanted items (%d):\n\n", len(rows)-1)
	fmt.Fprint(stdout, tbl.Render())

	return nil
}

func wlParseCSV(data string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, wlParseCSVLine(line))
	}
	return rows
}

func wlParseCSVLine(line string) []string {
	var fields []string
	var field strings.Builder
	inQuote := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"' && !inQuote:
			inQuote = true
		case ch == '"' && inQuote:
			if i+1 < len(line) && line[i+1] == '"' {
				field.WriteByte('"')
				i++
			} else {
				inQuote = false
			}
		case ch == ',' && !inQuote:
			fields = append(fields, field.String())
			field.Reset()
		default:
			field.WriteByte(ch)
		}
	}
	fields = append(fields, field.String())
	return fields
}

func wlFormatPriority(pri string) string {
	switch pri {
	case "0":
		return style.Error.Render("P0")
	case "1":
		return style.Warning.Render("P1")
	case "2":
		return "P2"
	case "3":
		return style.Dim.Render("P3")
	case "4":
		return style.Dim.Render("P4")
	default:
		return pri
	}
}
