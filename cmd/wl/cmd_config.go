package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/julianknutsen/wasteland/internal/federation"
	"github.com/spf13/cobra"
)

func newConfigCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Get or set wasteland configuration",
		Long: `View or modify wasteland configuration settings.

Use 'wl config get <key>' to read a setting.
Use 'wl config set <key> <value>' to change a setting.

Supported keys:
  mode            Workflow mode: wild-west (default) or pr
  signing         Enable GPG-signed Dolt commits: true or false
  provider-type   Upstream provider type (read-only, set during 'wl join')
  github-repo     (deprecated) Upstream GitHub repo for PR shells`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newConfigGetCmd(stdout, stderr),
		newConfigSetCmd(stdout, stderr),
	)

	return cmd
}

func newConfigGetCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Get a configuration value",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return []string{"mode", "signing", "provider-type", "github-repo"}, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(cmd, stdout, stderr, args[0])
		},
	}
}

func newConfigSetCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				return []string{"mode", "signing", "github-repo"}, cobra.ShellCompDirectiveNoFileComp
			case 1:
				switch args[0] {
				case "mode":
					return []string{"wild-west", "pr"}, cobra.ShellCompDirectiveNoFileComp
				case "signing":
					return []string{"true", "false"}, cobra.ShellCompDirectiveNoFileComp
				}
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd, stdout, stderr, args[0], args[1])
		},
	}
}

// validConfigKeys lists the keys that can be read/written via wl config.
var validConfigKeys = map[string]bool{
	"mode":          true,
	"signing":       true,
	"github-repo":   true,
	"provider-type": true,
}

func runConfigGet(cmd *cobra.Command, stdout, _ io.Writer, key string) error {
	if !validConfigKeys[key] {
		return fmt.Errorf("unknown config key %q (supported: mode, signing, provider-type, github-repo)", key)
	}

	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	switch key {
	case "mode":
		fmt.Fprintln(stdout, cfg.ResolveMode())
	case "signing":
		fmt.Fprintln(stdout, cfg.Signing)
	case "provider-type":
		fmt.Fprintln(stdout, cfg.ResolveProviderType())
	case "github-repo":
		fmt.Fprintln(stdout, cfg.GitHubRepo) //nolint:staticcheck // backward compat
	}
	return nil
}

func runConfigSet(cmd *cobra.Command, stdout, _ io.Writer, key, value string) error {
	if !validConfigKeys[key] {
		return fmt.Errorf("unknown config key %q (supported: mode, signing, provider-type, github-repo)", key)
	}

	switch key {
	case "provider-type":
		return fmt.Errorf("provider-type is read-only (set during 'wl join')")
	case "mode":
		if err := validateMode(value); err != nil {
			return err
		}
	case "signing":
		if err := validateSigning(value); err != nil {
			return err
		}
	case "github-repo":
		if err := validateGitHubRepo(value); err != nil {
			return err
		}
	}

	explicit, _ := cmd.Flags().GetString("wasteland")
	store := federation.NewConfigStore()
	cfg, err := federation.ResolveConfig(store, explicit)
	if err != nil {
		return hintWrap(err)
	}

	switch key {
	case "mode":
		cfg.Mode = value
	case "signing":
		cfg.Signing = value == "true"
	case "github-repo":
		cfg.GitHubRepo = value //nolint:staticcheck // backward compat
	}

	if err := store.Save(cfg); err != nil {
		return fmt.Errorf("saving wasteland config: %w", err)
	}

	fmt.Fprintf(stdout, "%s = %s\n", key, value)
	return nil
}

func validateGitHubRepo(value string) error {
	parts := strings.SplitN(value, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid github-repo %q: expected format \"owner/repo\"", value)
	}
	return nil
}

func validateSigning(value string) error {
	switch value {
	case "true", "false":
		return nil
	default:
		return fmt.Errorf("invalid signing value %q: must be \"true\" or \"false\"", value)
	}
}

func validateMode(value string) error {
	switch value {
	case federation.ModeWildWest, federation.ModePR:
		return nil
	default:
		return fmt.Errorf("invalid mode %q: must be %q or %q", value, federation.ModeWildWest, federation.ModePR)
	}
}
