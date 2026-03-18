package main

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/gastownhall/wasteland/internal/tui"
	"github.com/spf13/cobra"
)

func newTUICmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal UI for browsing the wanted board",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runTUI(cmd, stdout, stderr)
		},
	}
	return cmd
}

func runTUI(cmd *cobra.Command, _, stderr io.Writer) error {
	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	var db commons.DB
	if cfg.ResolveBackend() == federation.BackendLocal {
		if err := requireDolt(); err != nil {
			return err
		}
		localDB := backend.NewLocalDB(cfg.LocalDir, cfg.ResolveMode())
		db = localDB

		// Sync before launching the TUI.
		sp := style.StartSpinner(stderr, "Syncing with upstream...")
		err = localDB.Sync()
		sp.Stop()
		if err != nil {
			return fmt.Errorf("syncing with upstream: %w", err)
		}

		// PR mode: force-push main to origin so it matches upstream.
		if cfg.ResolveMode() == federation.ModePR {
			if err := localDB.PushMain(io.Discard); err != nil {
				fmt.Fprintf(stderr, "  warning: could not sync origin/main: %v\n", err)
			}
		}
	} else {
		upOrg, upDB, err := federation.ParseUpstream(cfg.Upstream)
		if err != nil {
			return fmt.Errorf("parsing upstream: %w", err)
		}
		remoteDB := backend.NewRemoteDB(commons.DoltHubToken(), upOrg, upDB, cfg.ForkOrg, cfg.ForkDB, cfg.ResolveMode())
		db = remoteDB

		sp := style.StartSpinner(stderr, "Syncing fork with upstream...")
		err = remoteDB.Sync()
		sp.Stop()
		if err != nil {
			fmt.Fprintf(stderr, "  warning: fork sync skipped: %v\n", err)
		}
	}

	// Build LoadDiff callback based on backend type.
	loadDiff := func(branch string) (string, error) {
		if cfg.ResolveBackend() != federation.BackendLocal {
			if rdb, ok := db.(*backend.RemoteDB); ok {
				return rdb.Diff(branch)
			}
			return "", fmt.Errorf("diff view requires local backend")
		}
		doltPath, err := exec.LookPath("dolt")
		if err != nil {
			return "", err
		}
		base := diffBase(cfg.LocalDir, doltPath)
		var buf bytes.Buffer
		if err := renderMarkdownDiff(&buf, cfg.LocalDir, doltPath, branch, base); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: cfg.RigHandle,
		Mode:      cfg.ResolveMode(),
		Signing:   cfg.Signing,
		HopURI:    cfg.HopURI,
		LoadDiff:  loadDiff,
		CreatePR: func(branch string) (string, error) {
			if cfg.ResolveBackend() != federation.BackendLocal {
				return createPRForBranchRemote(cfg, db, branch)
			}
			return createPRForBranch(cfg, branch)
		},
		CheckPR: func(branch string) string {
			return checkPRForBranch(cfg, branch)
		},
		ClosePR: func(branch string) error {
			return closePRForBranch(cfg, branch)
		},
		SaveConfig: func(mode string, signing bool) error {
			store := federation.NewConfigStore()
			c, err := store.Load(cfg.Upstream)
			if err != nil {
				return err
			}
			c.Mode = mode
			c.Signing = signing
			return store.Save(c)
		},
		LoadPendingDetail: pendingDetailLoaderCallback(cfg),
		ListPendingItems:  listPendingItemsFromPRs(cfg),
		BranchURL:         branchURLCallback(cfg),
		CloseUpstreamPR:   closeUpstreamPRCallback(cfg),
	})

	m := tui.New(tui.Config{
		Client:       client,
		RigHandle:    cfg.RigHandle,
		Upstream:     cfg.Upstream,
		Mode:         cfg.ResolveMode(),
		Signing:      cfg.Signing,
		ProviderType: cfg.ResolveProviderType(),
		ForkOrg:      cfg.ForkOrg,
		ForkDB:       cfg.ForkDB,
		LocalDir:     cfg.LocalDir,
		JoinedAt:     cfg.JoinedAt.Format("2006-01-02"),
	})

	p := bubbletea.NewProgram(m, bubbletea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
