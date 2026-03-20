// Package federation implements the Wasteland federation protocol.
//
// The Wasteland is a federation of Gas Towns via DoltHub. Each rig has a
// sovereign fork of a shared commons database. Rigs register by writing
// to the commons' rigs table, and contribute wanted work items and
// completions through DoltHub's fork/PR/merge primitives.
package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/xdg"
)

// ErrNotJoined indicates the rig has not joined a wasteland.
var ErrNotJoined = errors.New("rig has not joined a wasteland")

// Mode constants for the wasteland workflow.
const (
	ModeWildWest = "wild-west"
	ModePR       = "pr"
)

// ErrAmbiguous indicates multiple wastelands are joined and --wasteland is required.
var ErrAmbiguous = errors.New("multiple wastelands joined; use --wasteland to select one")

// Config holds the wasteland configuration for a rig.
type Config struct {
	// Upstream is the DoltHub path of the upstream commons (e.g., "steveyegge/wl-commons").
	Upstream string `json:"upstream"`

	// ProviderType is the upstream provider ("dolthub", "file", "git", "github").
	ProviderType string `json:"provider_type,omitempty"`

	// UpstreamURL is the resolved dolt-compatible remote URL for the upstream.
	// Used by browse for ephemeral clones.
	UpstreamURL string `json:"upstream_url,omitempty"`

	// ForkOrg is the DoltHub org where the fork lives (e.g., "alice-dev").
	ForkOrg string `json:"fork_org"`

	// ForkDB is the database name of the fork (e.g., "wl-commons").
	ForkDB string `json:"fork_db"`

	// LocalDir is the absolute path to the local clone of the fork.
	LocalDir string `json:"local_dir"`

	// RigHandle is the rig's handle in the registry.
	RigHandle string `json:"rig_handle"`

	// HopURI is the rig's HOP protocol URI (e.g., "hop://alice@example.com/alice-rig/").
	HopURI string `json:"hop_uri,omitempty"`

	// JoinedAt is when the rig joined the wasteland.
	JoinedAt time.Time `json:"joined_at"`

	// Mode is the workflow mode: "" or "pr" (default) or "wild-west".
	Mode string `json:"mode,omitempty"`

	// Backend is the database backend: "remote" (DoltHub API, default) or "local" (dolt CLI).
	Backend string `json:"backend,omitempty"`

	// Signing enables GPG-signed Dolt commits when true.
	Signing bool `json:"signing,omitempty"`

	// LastSyncAt records when the local clone was last synced with upstream.
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`

	// GitHubRepo is the upstream GitHub repo for PR shells (e.g., "steveyegge/wl-commons").
	//
	// Deprecated: use ProviderType == "github" instead.
	GitHubRepo string `json:"github_repo,omitempty"`
}

// ResolveMode returns the effective mode, defaulting to PR mode.
func (c *Config) ResolveMode() string {
	if c.Mode == "" || c.Mode == ModePR {
		return ModePR
	}
	return c.Mode
}

// ValidateMode validates a workflow mode value.
// Empty mode is allowed because PR mode is the default when mode is unset.
func ValidateMode(mode string) error {
	switch mode {
	case "", ModePR, ModeWildWest:
		return nil
	default:
		return fmt.Errorf("mode %q must be %q or %q", mode, ModePR, ModeWildWest)
	}
}

// Backend constants.
const (
	BackendRemote = "remote"
	BackendLocal  = "local"
)

// ResolveBackend returns the effective backend.
// Explicit "local" or "remote" values are returned as-is.
// When unset, defaults to "local" if LocalDir is configured (backward compat),
// "remote" otherwise (new remote-only users).
func (c *Config) ResolveBackend() string {
	switch c.Backend {
	case BackendLocal:
		return BackendLocal
	case BackendRemote:
		return BackendRemote
	default:
		if c.LocalDir != "" {
			return BackendLocal
		}
		return BackendRemote
	}
}

// ResolveProviderType returns the effective provider type.
// Falls back to "dolthub" for backward compatibility with old configs.
func (c *Config) ResolveProviderType() string {
	if c.ProviderType != "" {
		return c.ProviderType
	}
	return "dolthub"
}

// IsGitHub returns true if the provider type is "github".
func (c *Config) IsGitHub() bool {
	return c.ResolveProviderType() == "github"
}

// ParseUpstream parses an upstream path like "steveyegge/wl-commons" into org and db.
func ParseUpstream(upstream string) (org, db string, err error) {
	parts := strings.SplitN(upstream, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid upstream path %q: expected format 'org/database'", upstream)
	}
	return parts[0], parts[1], nil
}

// LocalCloneDir returns the local clone directory for a specific wasteland commons.
func LocalCloneDir(upstreamOrg, upstreamDB string) string {
	return filepath.Join(xdg.DataDir(), upstreamOrg, upstreamDB)
}

// escapeSQLString escapes backslashes and single quotes for SQL string literals.
func escapeSQLString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", "''")
}

// DoltCLI abstracts dolt CLI subprocess operations.
type DoltCLI interface {
	Clone(remoteURL, targetDir string) error
	Init(localDir string) error
	SQLExec(localDir, query string) error
	StageAndCommit(localDir, message string, signed bool) error
	AddRemote(localDir, name, remoteURL string) error
	RegisterRig(localDir, handle, dolthubOrg, displayName, ownerEmail, version string, signed bool) error
	Push(localDir string) error
	PushBranch(localDir, branch string, force bool) error
	CheckoutBranch(localDir, branch string) error
	CheckoutMain(localDir string) error
	AddUpstreamRemote(localDir, remoteURL string) error
}

// ConfigStore abstracts wasteland config persistence.
type ConfigStore interface {
	Load(upstream string) (*Config, error)
	Save(cfg *Config) error
	Delete(upstream string) error
	List() ([]string, error)
}

// ResolveConfig resolves the active wasteland config.
// If explicit is non-empty, loads that specific upstream config.
// If exactly one wasteland is joined, returns it.
// If zero are joined, returns ErrNotJoined.
// If multiple are joined, returns ErrAmbiguous.
func ResolveConfig(store ConfigStore, explicit string) (*Config, error) {
	if explicit != "" {
		cfg, err := store.Load(explicit)
		if err != nil {
			return nil, fmt.Errorf("loading config for %s: %w", explicit, err)
		}
		return cfg, nil
	}

	upstreams, err := store.List()
	if err != nil {
		return nil, fmt.Errorf("listing wastelands: %w", err)
	}

	switch len(upstreams) {
	case 0:
		return nil, fmt.Errorf("%w (run 'wl join <upstream>')", ErrNotJoined)
	case 1:
		cfg, err := store.Load(upstreams[0])
		if err != nil {
			return nil, fmt.Errorf("loading config for %s: %w", upstreams[0], err)
		}
		return cfg, nil
	default:
		var list strings.Builder
		for _, u := range upstreams {
			fmt.Fprintf(&list, "  - %s\n", u)
		}
		return nil, fmt.Errorf("%w:\n%s", ErrAmbiguous, list.String())
	}
}

// Service coordinates wasteland operations with injectable dependencies.
type Service struct {
	Remote     remote.Provider
	CLI        DoltCLI
	Config     ConfigStore
	OnProgress func(step string) // optional callback for progress reporting
}

// JoinResult holds the output of a successful Join operation.
type JoinResult struct {
	Config *Config
	PRURL  string // non-empty when a PR was created (fork mode only)
}

// Join orchestrates the wasteland join workflow: fork -> clone -> add upstream -> register -> push -> save config.
// When direct is true, skips forking and clones upstream directly — useful for maintainers
// with write access to the upstream commons.
func (s *Service) Join(upstream, forkOrg, handle, displayName, ownerEmail, version string, signed, direct bool) (*JoinResult, error) {
	upstreamOrg, upstreamDB, err := ParseUpstream(upstream)
	if err != nil {
		return nil, err
	}

	// Check if already joined to this specific upstream (idempotent).
	if existing, err := s.Config.Load(upstream); err == nil {
		return &JoinResult{Config: existing}, nil
	} else if !errors.Is(err, ErrNotJoined) {
		return nil, fmt.Errorf("loading wasteland config: %w", err)
	}

	localDir := LocalCloneDir(upstreamOrg, upstreamDB)
	progress := s.OnProgress
	if progress == nil {
		progress = func(string) {}
	}

	upstreamURL := s.Remote.DatabaseURL(upstreamOrg, upstreamDB)

	if direct {
		// Direct mode: clone upstream as origin, no fork or upstream remote.
		progress("Cloning upstream directly...")
		if err := s.CLI.Clone(upstreamURL, localDir); err != nil {
			return nil, fmt.Errorf("cloning upstream: %w", err)
		}
	} else {
		progress("Forking commons...")
		if err := s.Remote.Fork(upstreamOrg, upstreamDB, forkOrg); err != nil {
			return nil, fmt.Errorf("forking commons: %w", err)
		}

		progress("Cloning fork locally...")
		forkURL := s.Remote.DatabaseURL(forkOrg, upstreamDB)
		if err := cloneWithRetry(s.CLI, forkURL, localDir, progress); err != nil {
			return nil, fmt.Errorf("cloning fork: %w", err)
		}

		progress("Adding upstream remote...")
		if err := s.CLI.AddUpstreamRemote(localDir, upstreamURL); err != nil {
			return nil, fmt.Errorf("adding upstream remote: %w", err)
		}
	}

	// In fork mode, register on a branch and create a PR.
	// In direct mode, register and push on main.
	branch := fmt.Sprintf("wl/register/%s", handle)
	if !direct {
		progress("Creating branch " + branch + "...")
		if err := s.CLI.CheckoutBranch(localDir, branch); err != nil {
			return nil, fmt.Errorf("creating registration branch: %w", err)
		}
	}

	progress("Registering rig...")
	if err := s.CLI.RegisterRig(localDir, handle, forkOrg, displayName, ownerEmail, version, signed); err != nil {
		return nil, fmt.Errorf("registering rig: %w", err)
	}

	var prURL string
	if direct {
		progress("Pushing to upstream...")
		if err := s.CLI.Push(localDir); err != nil {
			return nil, fmt.Errorf("pushing: %w", err)
		}
	} else {
		progress("Pushing branch to fork...")
		if err := s.CLI.PushBranch(localDir, branch, true); err != nil {
			return nil, fmt.Errorf("pushing branch: %w", err)
		}

		// Return to main so the local clone is on the default branch.
		if err := s.CLI.CheckoutMain(localDir); err != nil {
			return nil, fmt.Errorf("returning to main: %w", err)
		}

		progress("Opening pull request...")
		title := fmt.Sprintf("Register rig: %s", handle)
		body := fmt.Sprintf("Register rig **%s** (%s) in the commons.", handle, displayName)
		prURL, err = s.Remote.CreatePR(forkOrg, upstreamOrg, upstreamDB, branch, title, body)
		if err != nil {
			progress(fmt.Sprintf("warning: could not create PR: %v", err))
			prURL = ""
		}
	}

	hopURI := fmt.Sprintf("hop://%s/%s/", ownerEmail, handle)
	cfg := &Config{
		Upstream:     upstream,
		ProviderType: s.Remote.Type(),
		UpstreamURL:  upstreamURL,
		ForkOrg:      forkOrg,
		ForkDB:       upstreamDB,
		LocalDir:     localDir,
		Backend:      BackendLocal,
		Mode:         ModePR,
		RigHandle:    handle,
		HopURI:       hopURI,
		JoinedAt:     time.Now(),
	}
	if err := s.Config.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving wasteland config: %w", err)
	}

	return &JoinResult{Config: cfg, PRURL: prURL}, nil
}

// CreateOptions holds parameters for creating a new wasteland commons.
type CreateOptions struct {
	Upstream    string // "org/db" path
	Handle      string // rig handle for creator
	DisplayName string
	OwnerEmail  string
	Version     string // wl version
	SchemaSQL   string // DDL passed in by caller (avoids federation→schema import)
	Name        string // optional wasteland display name for _meta
	LocalOnly   bool
	Signed      bool
}

// CreateResult holds the output of a successful Create operation.
type CreateResult struct {
	Config *Config
}

// Create orchestrates creating a new wasteland commons: init -> schema -> commit -> register -> push -> save config.
func (s *Service) Create(opts CreateOptions) (*CreateResult, error) {
	org, db, err := ParseUpstream(opts.Upstream)
	if err != nil {
		return nil, err
	}

	// Idempotent: if config already exists, return early.
	if existing, err := s.Config.Load(opts.Upstream); err == nil {
		return &CreateResult{Config: existing}, nil
	} else if !errors.Is(err, ErrNotJoined) {
		return nil, fmt.Errorf("loading wasteland config: %w", err)
	}

	localDir := LocalCloneDir(org, db)
	progress := s.OnProgress
	if progress == nil {
		progress = func(string) {}
	}

	progress("Initializing dolt database...")
	if err := s.CLI.Init(localDir); err != nil {
		return nil, fmt.Errorf("initializing database: %w", err)
	}

	sqlScript := opts.SchemaSQL
	if opts.Name != "" {
		sqlScript += fmt.Sprintf("\nINSERT IGNORE INTO _meta (`key`, value) VALUES ('wasteland_name', '%s');\n",
			escapeSQLString(opts.Name))
	}

	progress("Applying commons schema...")
	if err := s.CLI.SQLExec(localDir, sqlScript); err != nil {
		return nil, fmt.Errorf("applying schema: %w", err)
	}

	progress("Committing initial schema...")
	if err := s.CLI.StageAndCommit(localDir, "Initialize commons schema v1.0", opts.Signed); err != nil {
		return nil, fmt.Errorf("committing schema: %w", err)
	}

	progress("Registering rig...")
	if err := s.CLI.RegisterRig(localDir, opts.Handle, org, opts.DisplayName, opts.OwnerEmail, opts.Version, opts.Signed); err != nil {
		return nil, fmt.Errorf("registering rig: %w", err)
	}

	if !opts.LocalOnly {
		remoteURL := s.Remote.DatabaseURL(org, db)

		progress("Adding origin remote...")
		if err := s.CLI.AddRemote(localDir, "origin", remoteURL); err != nil {
			return nil, fmt.Errorf("adding origin remote: %w", err)
		}

		progress("Pushing to remote...")
		if err := s.CLI.Push(localDir); err != nil {
			return nil, fmt.Errorf("pushing: %w", err)
		}
	}

	hopURI := fmt.Sprintf("hop://%s/%s/", opts.OwnerEmail, opts.Handle)
	cfg := &Config{
		Upstream:  opts.Upstream,
		ForkOrg:   org,
		ForkDB:    db,
		LocalDir:  localDir,
		Backend:   BackendLocal,
		Mode:      ModePR,
		RigHandle: opts.Handle,
		HopURI:    hopURI,
		JoinedAt:  time.Now(),
	}
	if !opts.LocalOnly {
		cfg.ProviderType = s.Remote.Type()
		cfg.UpstreamURL = s.Remote.DatabaseURL(org, db)
	}

	progress("Saving config...")
	if err := s.Config.Save(cfg); err != nil {
		return nil, fmt.Errorf("saving wasteland config: %w", err)
	}

	return &CreateResult{Config: cfg}, nil
}

// cloneWithRetry retries the clone with exponential backoff. A newly forked
// database may not be immediately available on the dolt remote API.
func cloneWithRetry(cli DoltCLI, remoteURL, targetDir string, progress func(string)) error {
	backoff := 2 * time.Second
	deadline := time.Now().Add(2 * time.Minute)

	err := cli.Clone(remoteURL, targetDir)
	for err != nil && time.Now().Before(deadline) {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "permission") && !strings.Contains(msg, "could not access") && !strings.Contains(msg, "not found") {
			return err
		}
		progress(fmt.Sprintf("  Fork not yet available, retrying in %s...", backoff))
		time.Sleep(backoff)
		if backoff < 16*time.Second {
			backoff *= 2
		}
		err = cli.Clone(remoteURL, targetDir)
	}
	return err
}

// execDoltCLI implements DoltCLI using real dolt subprocess calls.
type execDoltCLI struct{}

func (e *execDoltCLI) Clone(remoteURL, targetDir string) error {
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		return fmt.Errorf("creating parent directory: %w", err)
	}

	if _, err := os.Stat(filepath.Join(targetDir, ".dolt")); err == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "clone", remoteURL, targetDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt clone %s: %w (%s)", remoteURL, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) Init(localDir string) error {
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "init")
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt init: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) SQLExec(localDir, query string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "sql", "-q", query)
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt sql: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) StageAndCommit(localDir, message string, signed bool) error {
	ctxAdd, cancelAdd := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelAdd()
	addCmd := exec.CommandContext(ctxAdd, "dolt", "add", "-A")
	addCmd.Dir = localDir
	if output, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dolt add: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	commitArgs := []string{"commit"}
	if signed {
		commitArgs = append(commitArgs, "-S")
	}
	commitArgs = append(commitArgs, "-m", message)
	ctxCommit, cancelCommit := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCommit()
	commitCmd := exec.CommandContext(ctxCommit, "dolt", commitArgs...)
	commitCmd.Dir = localDir
	output, err := commitCmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "no changes added") {
			return nil
		}
		return fmt.Errorf("dolt commit: %w (%s)", err, msg)
	}
	return nil
}

func (e *execDoltCLI) AddRemote(localDir, name, remoteURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "remote", "add", name, remoteURL)
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(strings.ToLower(msg), "already exists") {
			return nil
		}
		return fmt.Errorf("dolt remote add %s: %w (%s)", name, err, msg)
	}
	return nil
}

func (e *execDoltCLI) RegisterRig(localDir, handle, dolthubOrg, displayName, ownerEmail, version string, signed bool) error {
	hopURI := fmt.Sprintf("hop://%s/%s/", ownerEmail, handle)
	sql := fmt.Sprintf(
		`INSERT INTO rigs (handle, display_name, dolthub_org, hop_uri, owner_email, gt_version, trust_level, registered_at, last_seen) `+
			`VALUES ('%s', '%s', '%s', '%s', '%s', '%s', 1, NOW(), NOW()) `+
			`ON DUPLICATE KEY UPDATE display_name = '%s', dolthub_org = '%s', hop_uri = '%s', owner_email = '%s', gt_version = '%s', last_seen = NOW()`,
		escapeSQLString(handle),
		escapeSQLString(displayName),
		escapeSQLString(dolthubOrg),
		escapeSQLString(hopURI),
		escapeSQLString(ownerEmail),
		escapeSQLString(version),
		escapeSQLString(displayName),
		escapeSQLString(dolthubOrg),
		escapeSQLString(hopURI),
		escapeSQLString(ownerEmail),
		escapeSQLString(version),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "sql", "-q", sql)
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("inserting rig registration: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	ctxAdd, cancelAdd := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelAdd()
	addCmd := exec.CommandContext(ctxAdd, "dolt", "add", ".")
	addCmd.Dir = localDir
	if output, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("dolt add: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	commitArgs := []string{"commit"}
	if signed {
		commitArgs = append(commitArgs, "-S")
	}
	commitArgs = append(commitArgs, "-m", fmt.Sprintf("Register rig: %s", handle))
	ctxCommit, cancelCommit := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCommit()
	commitCmd := exec.CommandContext(ctxCommit, "dolt", commitArgs...)
	commitCmd.Dir = localDir
	output, err = commitCmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "nothing to commit") || strings.Contains(lower, "no changes added") {
			// Rig already registered (ON DUPLICATE KEY UPDATE hit with no changes) — success.
			return nil
		}
		if signed && strings.Contains(lower, "invalid user id") {
			return fmt.Errorf("GPG signing failed: no signing key configured\n\n" +
				"Set your GPG key for dolt:\n" +
				"  gpg --list-secret-keys --keyid-format long    # find your key ID (hex after algorithm)\n" +
				"  dolt config --global --add sqlserver.global.signingkey <your-gpg-key-id>")
		}
		return fmt.Errorf("dolt commit: %w (%s)", err, msg)
	}

	return nil
}

func (e *execDoltCLI) Push(localDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "push", "origin", "main")
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt push: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) PushBranch(localDir, branch string, force bool) error {
	args := []string{"push"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, "origin", branch)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", args...)
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt push branch %s: %w (%s)", branch, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) CheckoutBranch(localDir, branch string) error {
	// Create the branch if it doesn't exist, then checkout.
	ctxCreate, cancelCreate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCreate()
	createCmd := exec.CommandContext(ctxCreate, "dolt", "branch", branch)
	createCmd.Dir = localDir
	if output, err := createCmd.CombinedOutput(); err != nil {
		msg := strings.ToLower(strings.TrimSpace(string(output)))
		if !strings.Contains(msg, "already exists") {
			return fmt.Errorf("dolt branch %s: %w (%s)", branch, err, msg)
		}
	}
	ctxCheckout, cancelCheckout := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCheckout()
	cmd := exec.CommandContext(ctxCheckout, "dolt", "checkout", branch)
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt checkout %s: %w (%s)", branch, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) CheckoutMain(localDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "dolt", "checkout", "main")
	cmd.Dir = localDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("dolt checkout main: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (e *execDoltCLI) AddUpstreamRemote(localDir, remoteURL string) error {
	ctxCheck, cancelCheck := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelCheck()
	checkCmd := exec.CommandContext(ctxCheck, "dolt", "remote", "-v")
	checkCmd.Dir = localDir
	output, err := checkCmd.CombinedOutput()
	if err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "upstream") {
				return nil
			}
		}
	}

	ctxAdd, cancelAdd := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelAdd()
	cmd := exec.CommandContext(ctxAdd, "dolt", "remote", "add", "upstream", remoteURL)
	cmd.Dir = localDir
	output, err = cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if strings.Contains(strings.ToLower(msg), "already exists") {
			return nil
		}
		return fmt.Errorf("dolt remote add upstream: %w (%s)", err, msg)
	}
	return nil
}

// fileConfigStore implements ConfigStore using a directory of per-wasteland JSON files.
// Config files live at {configDir}/wastelands/{org}/{db}.json.
type fileConfigStore struct{}

func (f *fileConfigStore) configPath(upstream string) (string, error) {
	org, db, err := ParseUpstream(upstream)
	if err != nil {
		return "", err
	}
	return filepath.Join(xdg.ConfigDir(), "wastelands", org, db+".json"), nil
}

func (f *fileConfigStore) Load(upstream string) (*Config, error) {
	path, err := f.configPath(upstream)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotJoined
		}
		return nil, fmt.Errorf("reading wasteland config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing wasteland config: %w", err)
	}
	if err := ValidateMode(cfg.Mode); err != nil {
		return nil, fmt.Errorf("invalid wasteland config: %w", err)
	}
	return &cfg, nil
}

func (f *fileConfigStore) Save(cfg *Config) error {
	if err := ValidateMode(cfg.Mode); err != nil {
		return fmt.Errorf("invalid wasteland config: %w", err)
	}
	path, err := f.configPath(cfg.Upstream)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling wasteland config: %w", err)
	}
	data = append(data, '\n')

	// Atomic write: temp file in same dir, then rename.
	tmp, err := os.CreateTemp(dir, ".wl-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming config: %w", err)
	}
	return nil
}

func (f *fileConfigStore) Delete(upstream string) error {
	path, err := f.configPath(upstream)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotJoined, upstream)
		}
		return fmt.Errorf("deleting wasteland config: %w", err)
	}
	// Clean up empty parent directory (org dir).
	orgDir := filepath.Dir(path)
	entries, err := os.ReadDir(orgDir)
	if err == nil && len(entries) == 0 {
		_ = os.Remove(orgDir)
	}
	return nil
}

func (f *fileConfigStore) List() ([]string, error) {
	wasteDir := filepath.Join(xdg.ConfigDir(), "wastelands")
	if _, err := os.Stat(wasteDir); os.IsNotExist(err) {
		return nil, nil
	}

	var upstreams []string
	orgEntries, err := os.ReadDir(wasteDir)
	if err != nil {
		return nil, fmt.Errorf("reading wastelands directory: %w", err)
	}
	for _, orgEntry := range orgEntries {
		if !orgEntry.IsDir() {
			continue
		}
		orgDir := filepath.Join(wasteDir, orgEntry.Name())
		dbEntries, err := os.ReadDir(orgDir)
		if err != nil {
			continue
		}
		for _, dbEntry := range dbEntries {
			name := dbEntry.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			db := strings.TrimSuffix(name, ".json")
			upstreams = append(upstreams, orgEntry.Name()+"/"+db)
		}
	}
	return upstreams, nil
}

// NewConfigStore creates a ConfigStore backed by the filesystem.
func NewConfigStore() ConfigStore {
	return &fileConfigStore{}
}

// NewService creates a Service with real (production) dependencies.
func NewService(provider remote.Provider) *Service {
	return &Service{
		Remote: provider,
		CLI:    &execDoltCLI{},
		Config: &fileConfigStore{},
	}
}

// NewServiceWith creates a Service with an explicit ConfigStore.
func NewServiceWith(provider remote.Provider, store ConfigStore) *Service {
	return &Service{
		Remote: provider,
		CLI:    &execDoltCLI{},
		Config: store,
	}
}
