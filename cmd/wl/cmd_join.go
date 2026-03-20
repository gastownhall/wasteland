package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/spf13/cobra"
)

const defaultUpstream = "hop/wl-commons"

func newJoinCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		handle      string
		displayName string
		email       string
		forkOrg     string
		remoteBase  string
		gitRemote   string
		github      bool
		githubLocal string
		signed      bool
		direct      bool
		localDB     bool
	)

	cmd := &cobra.Command{
		Use:   "join [upstream]",
		Short: "Join a wasteland by forking its commons",
		Long: `Join a wasteland community by forking its shared commons database.

By default, uses remote mode (DoltHub API) — no local dolt installation needed.
Use --local-db to clone the fork locally (requires dolt installed).

This command:
  1. Forks the upstream commons to your org (or checks that your fork exists)
  2. Registers your rig in the rigs table via the DoltHub API
  3. Opens a pull request for registration
  4. Saves wasteland configuration locally

The upstream argument defaults to 'hop/wl-commons' (the main wasteland).
You can specify a different org/database path to join other wastelands.

Getting started:
  1. Sign up at https://www.dolthub.com
  2. Create an API token at https://www.dolthub.com/settings/tokens
  3. Set environment variables:
       export DOLTHUB_TOKEN=<your-api-token>
       export DOLTHUB_ORG=<your-dolthub-username>
  4. Run: wl join

Examples:
  wl join
  wl join hop/wl-commons --handle my-rig
  wl join --local-db             # clone locally (requires dolt)`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			upstream := defaultUpstream
			if len(args) > 0 {
				upstream = args[0]
			}
			if localDB || remoteBase != "" || gitRemote != "" || github || githubLocal != "" {
				return runJoin(stdout, stderr, upstream, handle, displayName, email, forkOrg, remoteBase, gitRemote, github, githubLocal, signed, direct)
			}
			return runJoinRemote(stdout, stderr, upstream, handle, displayName, email, forkOrg)
		},
	}

	cmd.Flags().StringVar(&handle, "handle", "", "Rig handle for registration (default: fork org)")
	cmd.Flags().StringVar(&displayName, "display-name", "", "Display name for the rig registry")
	cmd.Flags().StringVar(&email, "email", "", "Registration email (default: GPG key email if --signed, else git config user.email)")
	cmd.Flags().StringVar(&forkOrg, "fork-org", "", "Fork organization (default: DOLTHUB_ORG)")
	cmd.Flags().StringVar(&remoteBase, "remote-base", "", "Base directory for file:// remotes (offline mode)")
	cmd.Flags().StringVar(&gitRemote, "git-remote", "", "Base directory for bare git remotes")
	cmd.Flags().BoolVar(&github, "github", false, "Use GitHub as the upstream provider")
	cmd.Flags().StringVar(&githubLocal, "github-local", "", "Local base directory for GitHub-compatible testing mode")
	cmd.Flags().BoolVar(&signed, "signed", false, "GPG-sign the rig registration commit")
	cmd.Flags().BoolVar(&direct, "direct", false, "Skip forking — clone and push to upstream directly (for maintainers)")
	cmd.Flags().BoolVar(&localDB, "local-db", false, "Use local dolt database (clone fork, requires dolt installed)")
	cmd.MarkFlagsMutuallyExclusive("remote-base", "git-remote", "github", "github-local")

	return cmd
}

func runJoin(stdout, stderr io.Writer, upstream, handle, displayName, email, forkOrg, remoteBase, gitRemote string, github bool, githubLocal string, signed, direct bool) error {
	if err := requireDolt(); err != nil {
		return err
	}

	// Parse upstream path (validate early)
	_, _, err := federation.ParseUpstream(upstream)
	if err != nil {
		return err
	}

	store := federation.NewConfigStore()

	// Fast path: check if already joined to this specific upstream.
	if existing, loadErr := store.Load(upstream); loadErr == nil {
		fmt.Fprintf(stdout, "%s Already joined wasteland: %s\n", style.Bold.Render("⚠"), upstream)
		fmt.Fprintf(stdout, "  Handle: %s\n", existing.RigHandle)
		fmt.Fprintf(stdout, "  Fork: %s/%s\n", existing.ForkOrg, existing.ForkDB)
		fmt.Fprintf(stdout, "  Local: %s\n", existing.LocalDir)
		return nil
	} else if !errors.Is(loadErr, federation.ErrNotJoined) {
		return fmt.Errorf("loading wasteland config: %w", loadErr)
	}

	// Resolve fork org: flag > env var
	if forkOrg == "" {
		forkOrg = commons.DoltHubOrg()
	}

	var provider remote.Provider

	switch {
	case remoteBase != "":
		// Offline file mode — file:// dolt remotes, no DoltHub credentials needed.
		if forkOrg == "" {
			return fmt.Errorf("--fork-org is required in offline mode (or set DOLTHUB_ORG)")
		}
		provider = remote.NewFileProvider(remoteBase)

	case gitRemote != "":
		// Git remote mode — bare git repos as dolt remotes, no DoltHub credentials needed.
		if forkOrg == "" {
			return fmt.Errorf("--fork-org is required in git remote mode (or set DOLTHUB_ORG)")
		}
		provider = remote.NewGitProvider(gitRemote)

	case github:
		// GitHub mode — uses gh CLI for forking, GitHub HTTPS URLs as dolt remotes.
		if forkOrg == "" {
			return fmt.Errorf("--fork-org is required in GitHub mode (or set DOLTHUB_ORG)")
		}
		provider = remote.NewGitHubProvider()

	case githubLocal != "":
		// GitHub-local mode — bare git repos that report type "github" for testing.
		if forkOrg == "" {
			return fmt.Errorf("--fork-org is required in GitHub-local mode (or set DOLTHUB_ORG)")
		}
		provider = remote.NewFakeGitHubProvider(githubLocal)

	default:
		// DoltHub mode — requires token and org.
		token := commons.DoltHubToken()
		if token == "" {
			return fmt.Errorf("DOLTHUB_TOKEN environment variable is required\n\nGet your token from https://www.dolthub.com/settings/tokens")
		}
		if forkOrg == "" {
			return fmt.Errorf("DOLTHUB_ORG environment variable is required\n\nSet this to your DoltHub organization name")
		}
		provider = remote.NewDoltHubProvider(token)
	}

	// Determine handle
	if handle == "" {
		handle = forkOrg
	}

	// Determine display name from flag or git config
	if displayName == "" {
		displayName = gitConfigValue("user.name")
	}

	// Determine email from flag, GPG key (if signed), or git config
	if email == "" && signed {
		email = gpgKeyEmail()
	}
	if email == "" {
		email = gitConfigValue("user.email")
	}

	wlVersion := "dev"

	svc := federation.NewServiceWith(provider, store)
	svc.OnProgress = func(step string) {
		fmt.Fprintf(stdout, "  %s\n", step)
	}

	dbName := upstream[strings.Index(upstream, "/")+1:]
	fmt.Fprintf(stdout, "Joining wasteland %s (fork to %s/%s)...\n", upstream, forkOrg, dbName)
	result, err := svc.Join(upstream, forkOrg, handle, displayName, email, wlVersion, signed, direct)
	if err != nil {
		var forkErr *remote.ForkRequiredError
		if errors.As(err, &forkErr) {
			printForkInstructions(stdout, forkErr)
			return errExit
		}
		fmt.Fprintf(stderr, "wl join: %v\n", err)
		return errExit
	}

	cfg := result.Config
	fmt.Fprintf(stdout, "\n%s Joined wasteland: %s\n", style.Bold.Render("✓"), upstream)
	fmt.Fprintf(stdout, "  Handle: %s\n", cfg.RigHandle)
	fmt.Fprintf(stdout, "  Fork: %s/%s\n", cfg.ForkOrg, cfg.ForkDB)
	fmt.Fprintf(stdout, "  Local: %s\n", cfg.LocalDir)
	if result.PRURL != "" {
		fmt.Fprintf(stdout, "  PR: %s\n", style.Bold.Render(result.PRURL))
	}
	fmt.Fprintf(stdout, "\n  %s\n", style.Dim.Render("Next: wl browse  — browse the wanted board"))
	return nil
}

// runJoinRemote joins a wasteland in remote mode: fork + register via DoltHub API, no local dolt needed.
func runJoinRemote(stdout, _ io.Writer, upstream, handle, displayName, email, forkOrg string) error {
	// Parse upstream path (validate early)
	upstreamOrg, upstreamDB, err := federation.ParseUpstream(upstream)
	if err != nil {
		return err
	}

	store := federation.NewConfigStore()

	// Fast path: check if already joined to this specific upstream.
	if existing, loadErr := store.Load(upstream); loadErr == nil {
		fmt.Fprintf(stdout, "%s Already joined wasteland: %s\n", style.Bold.Render("⚠"), upstream)
		fmt.Fprintf(stdout, "  Handle: %s\n", existing.RigHandle)
		fmt.Fprintf(stdout, "  Fork: %s/%s\n", existing.ForkOrg, existing.ForkDB)
		fmt.Fprintf(stdout, "  Backend: %s\n", existing.ResolveBackend())
		return nil
	} else if !errors.Is(loadErr, federation.ErrNotJoined) {
		return fmt.Errorf("loading wasteland config: %w", loadErr)
	}

	// Resolve fork org: flag > env var
	if forkOrg == "" {
		forkOrg = commons.DoltHubOrg()
	}

	token := commons.DoltHubToken()
	if token == "" {
		return fmt.Errorf("DOLTHUB_TOKEN environment variable is required\n\nGet your token from https://www.dolthub.com/settings/tokens")
	}
	if forkOrg == "" {
		return fmt.Errorf("DOLTHUB_ORG environment variable is required\n\nSet this to your DoltHub organization name")
	}

	// Determine handle
	if handle == "" {
		handle = forkOrg
	}

	// Determine display name from flag or git config
	if displayName == "" {
		displayName = gitConfigValue("user.name")
	}

	// Determine email from flag or git config
	if email == "" {
		email = gitConfigValue("user.email")
	}

	provider := remote.NewDoltHubProvider(token)

	fmt.Fprintf(stdout, "Joining wasteland %s (fork to %s/%s, remote mode)...\n", upstream, forkOrg, upstreamDB)

	// 1. Fork upstream on DoltHub.
	fmt.Fprintf(stdout, "  Forking commons...\n")
	if err := provider.Fork(upstreamOrg, upstreamDB, forkOrg); err != nil {
		var forkErr *remote.ForkRequiredError
		if errors.As(err, &forkErr) {
			printForkInstructions(stdout, forkErr)
			return errExit
		}
		return fmt.Errorf("forking commons: %w", err)
	}

	// 2. Register rig via RemoteDB.Exec on a registration branch.
	fmt.Fprintf(stdout, "  Registering rig via API...\n")
	db := backend.NewRemoteDB(token, upstreamOrg, upstreamDB, forkOrg, upstreamDB, federation.ModePR)
	branch := fmt.Sprintf("wl/register/%s", handle)
	regSQL := commons.BuildRegistrationSQL(handle, forkOrg, displayName, email, "dev")
	if err := db.Exec(branch, "", false, regSQL); err != nil {
		return fmt.Errorf("registering rig: %w", err)
	}

	// 3. Create PR via DoltHub provider.
	fmt.Fprintf(stdout, "  Opening pull request...\n")
	title := fmt.Sprintf("Register rig: %s", handle)
	body := fmt.Sprintf("Register rig **%s** (%s) in the commons.", handle, displayName)
	prURL, err := provider.CreatePR(forkOrg, upstreamOrg, upstreamDB, branch, title, body)
	if err != nil {
		fmt.Fprintf(stdout, "  warning: could not create PR: %v\n", err)
		prURL = ""
	}

	// 4. Save config with Backend: "remote", LocalDir: "".
	hopURI := fmt.Sprintf("hop://%s/%s/", email, handle)
	cfg := &federation.Config{
		Upstream:     upstream,
		ProviderType: "dolthub",
		UpstreamURL:  provider.DatabaseURL(upstreamOrg, upstreamDB),
		ForkOrg:      forkOrg,
		ForkDB:       upstreamDB,
		LocalDir:     "",
		Backend:      federation.BackendRemote,
		Mode:         federation.ModePR,
		RigHandle:    handle,
		HopURI:       hopURI,
		JoinedAt:     time.Now(),
	}
	if err := store.Save(cfg); err != nil {
		return fmt.Errorf("saving wasteland config: %w", err)
	}

	fmt.Fprintf(stdout, "\n%s Joined wasteland: %s (remote mode)\n", style.Bold.Render("✓"), upstream)
	fmt.Fprintf(stdout, "  Handle: %s\n", cfg.RigHandle)
	fmt.Fprintf(stdout, "  Fork: %s/%s\n", cfg.ForkOrg, cfg.ForkDB)
	fmt.Fprintf(stdout, "  Backend: remote (DoltHub API)\n")
	if prURL != "" {
		fmt.Fprintf(stdout, "  PR: %s\n", style.Bold.Render(prURL))
	}
	fmt.Fprintf(stdout, "\n  %s\n", style.Dim.Render("Next: wl browse  — browse the wanted board"))
	return nil
}

func printForkInstructions(w io.Writer, err *remote.ForkRequiredError) {
	fmt.Fprintf(w, "\n%s Fork required\n\n", style.Bold.Render("!"))
	fmt.Fprintf(w, "  To join this wasteland, fork the commons on DoltHub:\n\n")
	fmt.Fprintf(w, "  1. Go to %s\n", style.Bold.Render(err.ForkURL()))
	fmt.Fprintf(w, "  2. Click %s (top right)\n", style.Bold.Render("Fork"))
	fmt.Fprintf(w, "  3. Select your organization: %s\n", style.Bold.Render(err.ForkOrg))
	fmt.Fprintf(w, "  4. Rerun: %s\n", style.Bold.Render("wl join"))
}

// gpgKeyEmail extracts an email from the GPG signing key's uid.
// If git config user.signingkey is set, only that key is queried.
// When a key has multiple UIDs, the last (oldest/primary) one is used.
// Returns empty string if GPG is not available or no keys are found.
func gpgKeyEmail() string {
	args := []string{"--list-secret-keys", "--with-colons"}
	if sigKey := gitConfigValue("user.signingkey"); sigKey != "" {
		args = append(args, sigKey)
	}
	cmd := exec.Command("gpg", args...)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	var email string
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "uid:") {
			// Colon-delimited format: uid:...:...:...:...:...:...:...:...:Name <email>:...
			fields := strings.Split(line, ":")
			if len(fields) > 9 {
				uid := fields[9]
				// Extract email from "Name <email>" format
				if start := strings.Index(uid, "<"); start >= 0 {
					if end := strings.Index(uid[start:], ">"); end >= 0 {
						email = uid[start+1 : start+end]
					}
				}
			}
		}
	}
	return email
}

// gitConfigValue retrieves a value from git config. Returns empty string on error.
func gitConfigValue(key string) string {
	cmd := exec.Command("git", "config", key)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
