package main

import (
	"fmt"
	"io"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/herald"
	"github.com/gastownhall/wasteland/internal/xdg"
	"github.com/spf13/cobra"
)

func newHeraldCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "herald",
		Short: "Watch the wanted board for changes",
		Long: `Herald polls the wanted board and reports new, removed, or status-changed items.

On first run it snapshots the current board. Subsequent runs diff against the
saved state and print changes. Use --watch to poll continuously.

State is persisted as JSON in the XDG data directory.

Examples:
  wl herald              # one-shot diff since last run
  wl herald --watch      # poll every 60s (Ctrl-C to stop)
  wl herald --interval 30s --watch`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			watch, _ := cmd.Flags().GetBool("watch")
			intervalStr, _ := cmd.Flags().GetString("interval")
			interval, err := time.ParseDuration(intervalStr)
			if err != nil {
				return fmt.Errorf("invalid --interval: %w", err)
			}
			return runHerald(cmd, stdout, stderr, watch, interval)
		},
	}
	cmd.Flags().Bool("watch", false, "Poll continuously instead of one-shot")
	cmd.Flags().String("interval", "60s", "Poll interval (e.g. 30s, 2m)")
	return cmd
}

func runHerald(cmd *cobra.Command, stdout, stderr io.Writer, watch bool, interval time.Duration) error {
	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return err
	}
	db, err := openDBFromConfig(cfg)
	if err != nil {
		return err
	}

	lister := &wantedBoardLister{db: db}
	notifier := &herald.LogNotifier{Printf: func(format string, args ...any) {
		fmt.Fprintf(stdout, format, args...)
	}}
	statePath := herald.DefaultStatePath(xdg.DataDir())
	store := herald.NewStateStore(statePath)

	if !watch {
		return herald.Poll(lister, notifier, store)
	}

	fmt.Fprintf(stderr, "Watching wanted board (every %s, Ctrl-C to stop)...\n", interval)
	for {
		if err := herald.Poll(lister, notifier, store); err != nil {
			fmt.Fprintf(stderr, "herald: poll error: %v\n", err)
		}
		time.Sleep(interval)
	}
}

// wantedBoardLister adapts the commons browse query into the herald.Lister interface.
type wantedBoardLister struct {
	db commons.DB
}

func (l *wantedBoardLister) ListItems() ([]herald.Item, error) {
	summaries, err := commons.BrowseWanted(l.db, commons.BrowseFilter{
		Limit: 500,
		Sort:  commons.SortNewest,
	})
	if err != nil {
		return nil, err
	}
	items := make([]herald.Item, len(summaries))
	for i, s := range summaries {
		items[i] = herald.Item{
			ID:     s.ID,
			Title:  s.Title,
			Status: s.Status,
		}
	}
	return items, nil
}
