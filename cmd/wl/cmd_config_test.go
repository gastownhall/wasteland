package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/spf13/cobra"
)

func TestValidateMode_Valid(t *testing.T) {
	for _, mode := range []string{federation.ModeWildWest, federation.ModePR} {
		if err := validateMode(mode); err != nil {
			t.Errorf("validateMode(%q) = %v, want nil", mode, err)
		}
	}
}

func TestValidateMode_Invalid(t *testing.T) {
	for _, mode := range []string{"", "chaos", "merge", "WILD-WEST"} {
		if err := validateMode(mode); err == nil {
			t.Errorf("validateMode(%q) = nil, want error", mode)
		}
	}
}

func TestValidConfigKeys(t *testing.T) {
	if !validConfigKeys["mode"] {
		t.Error("expected 'mode' to be a valid config key")
	}
	if validConfigKeys["nonexistent"] {
		t.Error("'nonexistent' should not be a valid config key")
	}
}

func TestValidConfigKeys_GitHubRepo(t *testing.T) {
	if !validConfigKeys["github-repo"] {
		t.Error("expected 'github-repo' to be a valid config key")
	}
}

func TestValidateGitHubRepo_Valid(t *testing.T) {
	for _, repo := range []string{"owner/repo", "steveyegge/wl-commons", "a/b"} {
		if err := validateGitHubRepo(repo); err != nil {
			t.Errorf("validateGitHubRepo(%q) = %v, want nil", repo, err)
		}
	}
}

func TestValidateGitHubRepo_Invalid(t *testing.T) {
	for _, repo := range []string{"", "noslash", "/bad", "bad/", "/"} {
		if err := validateGitHubRepo(repo); err == nil {
			t.Errorf("validateGitHubRepo(%q) = nil, want error", repo)
		}
	}
}

func TestValidConfigKeys_ProviderType(t *testing.T) {
	if !validConfigKeys["provider-type"] {
		t.Error("expected 'provider-type' to be a valid config key")
	}
}

// --- Handler-level tests for runConfigGet / runConfigSet ---

func saveTestConfig(t *testing.T, cfg *federation.Config) {
	t.Helper()
	store := federation.NewConfigStore()
	if err := store.Save(cfg); err != nil {
		t.Fatalf("saving test config: %v", err)
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("wasteland", "", "")
	return cmd
}

func TestRunConfigGet_Mode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		Mode: "pr", JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "mode")
	if err != nil {
		t.Fatalf("runConfigGet(mode) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "pr" {
		t.Errorf("runConfigGet(mode) = %q, want %q", got, "pr")
	}
}

func TestRunConfigGet_ModeDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "mode")
	if err != nil {
		t.Fatalf("runConfigGet(mode) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "pr" {
		t.Errorf("runConfigGet(mode default) = %q, want %q", got, "pr")
	}
}

func TestRunConfigGet_ProviderType(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		ProviderType: "github", JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "provider-type")
	if err != nil {
		t.Fatalf("runConfigGet(provider-type) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "github" {
		t.Errorf("runConfigGet(provider-type) = %q, want %q", got, "github")
	}
}

func TestRunConfigGet_ProviderTypeDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "provider-type")
	if err != nil {
		t.Fatalf("runConfigGet(provider-type) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "dolthub" {
		t.Errorf("runConfigGet(provider-type default) = %q, want %q", got, "dolthub")
	}
}

func TestRunConfigGet_GitHubRepo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		GitHubRepo: "steveyegge/wl-commons", JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "github-repo")
	if err != nil {
		t.Fatalf("runConfigGet(github-repo) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "steveyegge/wl-commons" {
		t.Errorf("runConfigGet(github-repo) = %q, want %q", got, "steveyegge/wl-commons")
	}
}

func TestRunConfigGet_UnknownKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "nonexistent")
	if err == nil {
		t.Fatal("runConfigGet(nonexistent) expected error")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("error = %q, want to contain 'unknown config key'", err.Error())
	}
}

func TestRunConfigGet_NotJoined(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "mode")
	if err == nil {
		t.Fatal("runConfigGet when not joined expected error")
	}
}

func TestRunConfigSet_Mode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "mode", "pr")
	if err != nil {
		t.Fatalf("runConfigSet(mode, pr) error: %v", err)
	}
	if !strings.Contains(stdout.String(), "mode = pr") {
		t.Errorf("output = %q, want to contain 'mode = pr'", stdout.String())
	}

	// Verify the mode persists.
	store := federation.NewConfigStore()
	loaded, err := store.Load("hop/wl-commons")
	if err != nil {
		t.Fatalf("loading config after set: %v", err)
	}
	if loaded.Mode != "pr" {
		t.Errorf("saved Mode = %q, want %q", loaded.Mode, "pr")
	}
}

func TestRunConfigSet_ModeInvalid(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "mode", "chaos")
	if err == nil {
		t.Fatal("runConfigSet(mode, chaos) expected error")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("error = %q, want to contain 'invalid mode'", err.Error())
	}
}

func TestRunConfigSet_ProviderTypeReadOnly(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "provider-type", "github")
	if err == nil {
		t.Fatal("runConfigSet(provider-type) expected error (read-only)")
	}
	if !strings.Contains(err.Error(), "read-only") {
		t.Errorf("error = %q, want to contain 'read-only'", err.Error())
	}
}

func TestRunConfigSet_GitHubRepo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "github-repo", "org/repo")
	if err != nil {
		t.Fatalf("runConfigSet(github-repo) error: %v", err)
	}

	store := federation.NewConfigStore()
	loaded, err := store.Load("hop/wl-commons")
	if err != nil {
		t.Fatalf("loading config after set: %v", err)
	}
	if loaded.GitHubRepo != "org/repo" { //nolint:staticcheck // backward compat
		t.Errorf("saved GitHubRepo = %q, want %q", loaded.GitHubRepo, "org/repo") //nolint:staticcheck // backward compat
	}
}

func TestRunConfigSet_GitHubRepoInvalid(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "github-repo", "noslash")
	if err == nil {
		t.Fatal("runConfigSet(github-repo, noslash) expected error")
	}
	if !strings.Contains(err.Error(), "invalid github-repo") {
		t.Errorf("error = %q, want to contain 'invalid github-repo'", err.Error())
	}
}

func TestRunConfigSet_UnknownKey(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "bogus", "value")
	if err == nil {
		t.Fatal("runConfigSet(bogus) expected error")
	}
	if !strings.Contains(err.Error(), "unknown config key") {
		t.Errorf("error = %q, want to contain 'unknown config key'", err.Error())
	}
}

func TestValidConfigKeys_Signing(t *testing.T) {
	if !validConfigKeys["signing"] {
		t.Error("expected 'signing' to be a valid config key")
	}
}

func TestRunConfigGet_Signing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		Signing: true, JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "signing")
	if err != nil {
		t.Fatalf("runConfigGet(signing) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "true" {
		t.Errorf("runConfigGet(signing) = %q, want %q", got, "true")
	}
}

func TestRunConfigGet_SigningDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigGet(configCmd(), &stdout, &stderr, "signing")
	if err != nil {
		t.Fatalf("runConfigGet(signing) error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "false" {
		t.Errorf("runConfigGet(signing default) = %q, want %q", got, "false")
	}
}

func TestRunConfigSet_Signing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "signing", "true")
	if err != nil {
		t.Fatalf("runConfigSet(signing, true) error: %v", err)
	}
	if !strings.Contains(stdout.String(), "signing = true") {
		t.Errorf("output = %q, want to contain 'signing = true'", stdout.String())
	}

	// Verify the value persists.
	store := federation.NewConfigStore()
	loaded, err := store.Load("hop/wl-commons")
	if err != nil {
		t.Fatalf("loading config after set: %v", err)
	}
	if !loaded.Signing {
		t.Error("saved Signing = false, want true")
	}
}

func TestRunConfigSet_SigningInvalid(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream: "hop/wl-commons", ForkOrg: "alice", ForkDB: "wl-commons",
		JoinedAt: time.Now(),
	})

	var stdout, stderr bytes.Buffer
	err := runConfigSet(configCmd(), &stdout, &stderr, "signing", "yes")
	if err == nil {
		t.Fatal("runConfigSet(signing, yes) expected error")
	}
	if !strings.Contains(err.Error(), "invalid signing value") {
		t.Errorf("error = %q, want to contain 'invalid signing value'", err.Error())
	}
}
