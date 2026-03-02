// wl is the Wasteland CLI — federation protocol for Gas Towns.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/julianknutsen/wasteland/internal/federation"
	"github.com/julianknutsen/wasteland/internal/style"
	"github.com/spf13/cobra"
)

// Version metadata injected via ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// errExit is a sentinel error returned by cobra RunE functions to signal
// non-zero exit. The command has already written its own error to stderr.
var errExit = errors.New("exit")

// run executes the wl CLI with the given args.
func run(args []string, stdout, stderr io.Writer) int {
	root := newRootCmd(stdout, stderr)
	if args == nil {
		args = []string{}
	}
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		if !errors.Is(err, errExit) {
			fmt.Fprintf(stderr, "wl: %v\n", err)
			var hinted *HintedError
			if errors.As(err, &hinted) {
				fmt.Fprintf(stderr, "\n  Hint: %s\n", hinted.Hint)
			}
		}
		return 1
	}
	return 0
}

// newRootCmd creates the root cobra command with all subcommands.
func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:           "wl",
		Short:         "Wasteland — federation protocol for Gas Towns",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			fmt.Fprintf(stderr, "wl: unknown command %q\n", args[0]) //nolint:errcheck // best-effort stderr
			return errExit
		},
	}
	root.PersistentFlags().String("wasteland", "", "Upstream wasteland to use (e.g., org/db); required when multiple are joined")
	_ = root.RegisterFlagCompletionFunc("wasteland", completeWastelandNames)
	root.PersistentFlags().Bool("local-db", false, "Use local dolt database instead of DoltHub API")
	root.PersistentFlags().String("color", "auto", "Color output: always, auto, never")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		colorMode, _ := cmd.Flags().GetString("color")
		switch colorMode {
		case "always", "auto", "never":
			style.SetColorMode(colorMode)
			return nil
		default:
			return fmt.Errorf("invalid --color value %q: must be always, auto, or never", colorMode)
		}
	}
	root.AddCommand(
		newCreateCmd(stdout, stderr),
		newJoinCmd(stdout, stderr),
		newPostCmd(stdout, stderr),
		newClaimCmd(stdout, stderr),
		newUnclaimCmd(stdout, stderr),
		newDoneCmd(stdout, stderr),
		newAcceptCmd(stdout, stderr),
		newRejectCmd(stdout, stderr),
		newCloseCmd(stdout, stderr),
		newUpdateCmd(stdout, stderr),
		newDeleteCmd(stdout, stderr),
		newBrowseCmd(stdout, stderr),
		newMeCmd(stdout, stderr),
		newStatusCmd(stdout, stderr),
		newSyncCmd(stdout, stderr),
		newLeaveCmd(stdout, stderr),
		newListCmd(stdout, stderr),
		newConfigCmd(stdout, stderr),
		newReviewCmd(stdout, stderr),
		newApproveCmd(stdout, stderr),
		newRequestChangesCmd(stdout, stderr),
		newMergeCmd(stdout, stderr),
		newVerifyCmd(stdout, stderr),
		newTUICmd(stdout, stderr),
		newServeCmd(stdout, stderr),
		newDoctorCmd(stdout, stderr),
		newInferCmd(stdout, stderr),
		newLeaderboardCmd(stdout, stderr),
		newVersionCmd(stdout),
	)
	return root
}

// resolveWasteland resolves the active wasteland config from --wasteland flag or auto-selection.
// Default is remote (API-only). Pass --local-db to use the local dolt database.
func resolveWasteland(cmd *cobra.Command) (*federation.Config, error) {
	explicit, _ := cmd.Flags().GetString("wasteland")
	store := federation.NewConfigStore()
	cfg, err := federation.ResolveConfig(store, explicit)
	if err != nil {
		return nil, err
	}
	if localDB, _ := cmd.Flags().GetBool("local-db"); localDB {
		cfg.Backend = federation.BackendLocal
	} else {
		cfg.Backend = federation.BackendRemote
	}
	return cfg, nil
}
