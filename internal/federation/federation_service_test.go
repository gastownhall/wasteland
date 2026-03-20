package federation

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestJoin_Success(t *testing.T) {
	t.Parallel()
	log := NewCallLog()
	provider := NewFakeProvider()
	provider.Log = log
	cli := NewFakeDoltCLI()
	cli.Log = log
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err != nil {
		t.Fatalf("Join() error: %v", err)
	}

	cfg := result.Config
	if cfg.Upstream != "steveyegge/wl-commons" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "steveyegge/wl-commons")
	}
	if cfg.RigHandle != "alice-rig" {
		t.Errorf("RigHandle = %q, want %q", cfg.RigHandle, "alice-rig")
	}
	if cfg.Mode != ModePR {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModePR)
	}

	if !provider.Forked["steveyegge/wl-commons->alice-dev"] {
		t.Error("expected fork to be created")
	}
	if len(cli.Cloned) != 1 {
		t.Errorf("expected 1 clone, got %d", len(cli.Cloned))
	}
	if !cli.Registered["alice-rig"] {
		t.Error("expected rig to be registered")
	}
	if len(cli.Pushed) != 1 {
		t.Errorf("expected 1 push, got %d", len(cli.Pushed))
	}
	if len(cli.Remotes) != 1 {
		t.Errorf("expected 1 remote, got %d", len(cli.Remotes))
	}

	saved, err := cfgStore.Load("steveyegge/wl-commons")
	if err != nil {
		t.Fatalf("config not saved: %v", err)
	}
	if saved.Upstream != cfg.Upstream {
		t.Errorf("saved config doesn't match returned config")
	}

	// Verify call ordering: fork, clone, remote, checkout-branch, register, push-branch, checkout-main, create-pr
	expectedOrder := []string{"Fork", "Clone", "AddUpstreamRemote", "CheckoutBranch", "RegisterRig", "PushBranch", "CheckoutMain", "CreatePR"}
	if len(log.Calls) < len(expectedOrder) {
		t.Fatalf("expected at least %d calls in unified log, got %d: %v", len(expectedOrder), len(log.Calls), log.Calls)
	}
	for i, want := range expectedOrder {
		if i >= len(log.Calls) {
			break
		}
		got := log.Calls[i]
		if !strings.HasPrefix(got, want) {
			t.Errorf("unified log[%d] = %q, want prefix %q", i, got, want)
		}
	}

	// Verify PR was created.
	if result.PRURL == "" {
		t.Error("expected PR URL to be set in fork mode")
	}
	if len(provider.PRs) != 1 {
		t.Errorf("expected 1 PR, got %d", len(provider.PRs))
	}
}

func TestJoin_ForkFails(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	provider.ForkErr = fmt.Errorf("DoltHub API error (HTTP 403): forbidden")
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	_, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err == nil {
		t.Fatal("Join() expected error when fork fails")
	}
	if len(cli.Cloned) != 0 {
		t.Error("clone should not be called when fork fails")
	}
}

func TestJoin_CloneFails(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cli.CloneErr = fmt.Errorf("dolt clone failed: network timeout")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	_, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err == nil {
		t.Fatal("Join() expected error when clone fails")
	}
	if !provider.Forked["steveyegge/wl-commons->alice-dev"] {
		t.Error("fork should have been created before clone failed")
	}
	if len(cli.Pushed) != 0 {
		t.Error("push should not be called when clone fails")
	}
}

func TestJoin_CloneRetriesOnPermissionDenied(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cli.CloneErr = fmt.Errorf("dolt clone: could not access dolt url: permission denied")
	cli.CloneErrCount = 2 // succeed on 2nd attempt
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	_, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err != nil {
		t.Fatalf("Join() should succeed after retry: %v", err)
	}
	// Clone should have been called at least twice.
	cloneCalls := 0
	for _, c := range cli.Calls {
		if strings.Contains(c, "Clone(") {
			cloneCalls++
		}
	}
	if cloneCalls < 2 {
		t.Errorf("expected at least 2 clone attempts, got %d", cloneCalls)
	}
}

func TestJoin_AlreadyJoined(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.Configs["steveyegge/wl-commons"] = &Config{
		Upstream:  "steveyegge/wl-commons",
		ForkOrg:   "alice-dev",
		ForkDB:    "wl-commons",
		RigHandle: "alice-rig",
	}

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err != nil {
		t.Fatalf("Join() should succeed (no-op) when already joined: %v", err)
	}
	if result.Config.RigHandle != "alice-rig" {
		t.Errorf("returned config RigHandle = %q, want %q", result.Config.RigHandle, "alice-rig")
	}
	if len(provider.Calls) != 0 {
		t.Errorf("expected 0 provider calls for already-joined, got %d", len(provider.Calls))
	}
	if len(cli.Calls) != 0 {
		t.Errorf("expected 0 CLI calls for already-joined, got %d", len(cli.Calls))
	}
}

func TestJoin_SecondUpstream_Succeeds(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.Configs["org1/commons"] = &Config{
		Upstream:  "org1/commons",
		ForkOrg:   "alice-dev",
		ForkDB:    "commons",
		RigHandle: "alice-rig",
	}

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Join("org2/commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err != nil {
		t.Fatalf("Join() should succeed for second upstream: %v", err)
	}
	if result.Config.Upstream != "org2/commons" {
		t.Errorf("Upstream = %q, want %q", result.Config.Upstream, "org2/commons")
	}

	// Both configs should exist.
	upstreams, err := cfgStore.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(upstreams) != 2 {
		t.Errorf("expected 2 configs, got %d", len(upstreams))
	}
}

func TestJoin_ConfigLoadError(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.LoadErr = fmt.Errorf("permission denied")

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	_, err := svc.Join("steveyegge/wl-commons", "alice-dev", "alice-rig", "Alice", "alice@example.com", "dev", false, false)
	if err == nil {
		t.Fatal("Join() expected error when config load fails")
	}
	if !strings.Contains(err.Error(), "loading wasteland config") {
		t.Fatalf("Join() error = %v", err)
	}
	if len(provider.Calls) != 0 {
		t.Errorf("expected no provider calls when config load fails, got %d", len(provider.Calls))
	}
	if len(cli.Calls) != 0 {
		t.Errorf("expected no CLI calls when config load fails, got %d", len(cli.Calls))
	}
}

func TestCreate_ConfigLoadError(t *testing.T) {
	t.Parallel()
	provider := NewFakeProvider()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.LoadErr = fmt.Errorf("permission denied")

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err == nil {
		t.Fatal("Create() expected error when config load fails")
	}
	if !strings.Contains(err.Error(), "loading wasteland config") {
		t.Fatalf("Create() error = %v", err)
	}
	if len(provider.Calls) != 0 {
		t.Errorf("expected no provider calls when config load fails, got %d", len(provider.Calls))
	}
	if len(cli.Calls) != 0 {
		t.Errorf("expected no CLI calls when config load fails, got %d", len(cli.Calls))
	}
}

func TestJoin_InvalidUpstream(t *testing.T) {
	t.Parallel()
	svc := &Service{
		Remote: NewFakeProvider(),
		CLI:    NewFakeDoltCLI(),
		Config: NewFakeConfigStore(),
	}

	_, err := svc.Join("invalid", "org", "handle", "name", "email", "v1", false, false)
	if err == nil {
		t.Fatal("Join() expected error for invalid upstream")
	}
}

func TestJoin_Direct_SkipsForkAndPR(t *testing.T) {
	t.Parallel()
	log := NewCallLog()
	provider := NewFakeProvider()
	provider.Log = log
	cli := NewFakeDoltCLI()
	cli.Log = log
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Join("hop/wl-commons", "hop", "hop-rig", "Hop", "hop@example.com", "dev", false, true)
	if err != nil {
		t.Fatalf("Join(direct=true) error: %v", err)
	}

	// Should not have forked or created a PR.
	if len(provider.Forked) != 0 {
		t.Error("expected no fork in direct mode")
	}
	if len(provider.PRs) != 0 {
		t.Error("expected no PR in direct mode")
	}
	if result.PRURL != "" {
		t.Errorf("expected empty PRURL in direct mode, got %q", result.PRURL)
	}

	// Should have cloned, registered, and pushed on main.
	expectedOrder := []string{"Clone", "RegisterRig", "Push"}
	if len(log.Calls) < len(expectedOrder) {
		t.Fatalf("expected at least %d calls, got %d: %v", len(expectedOrder), len(log.Calls), log.Calls)
	}
	for i, want := range expectedOrder {
		if i >= len(log.Calls) {
			break
		}
		if !strings.HasPrefix(log.Calls[i], want) {
			t.Errorf("log[%d] = %q, want prefix %q", i, log.Calls[i], want)
		}
	}
}

func TestCreate_Success(t *testing.T) {
	t.Parallel()
	log := NewCallLog()
	provider := NewFakeProvider()
	provider.Log = log
	cli := NewFakeDoltCLI()
	cli.Log = log
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Create(CreateOptions{
		Upstream:    "myorg/wl-commons",
		Handle:      "myrig",
		DisplayName: "Alice",
		OwnerEmail:  "alice@example.com",
		Version:     "dev",
		SchemaSQL:   "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	cfg := result.Config
	if cfg.Upstream != "myorg/wl-commons" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "myorg/wl-commons")
	}
	if cfg.RigHandle != "myrig" {
		t.Errorf("RigHandle = %q, want %q", cfg.RigHandle, "myrig")
	}
	if cfg.ForkOrg != "myorg" {
		t.Errorf("ForkOrg = %q, want %q", cfg.ForkOrg, "myorg")
	}
	if cfg.ForkDB != "wl-commons" {
		t.Errorf("ForkDB = %q, want %q", cfg.ForkDB, "wl-commons")
	}
	if cfg.Backend != BackendLocal {
		t.Errorf("Backend = %q, want %q", cfg.Backend, BackendLocal)
	}
	if cfg.ProviderType != "fake" {
		t.Errorf("ProviderType = %q, want %q", cfg.ProviderType, "fake")
	}
	if cfg.Mode != ModePR {
		t.Errorf("Mode = %q, want %q", cfg.Mode, ModePR)
	}

	// Verify call ordering: Init → SQLExec → StageAndCommit → RegisterRig → AddRemote → Push
	expectedOrder := []string{"Init", "SQLExec", "StageAndCommit", "RegisterRig", "AddRemote", "Push"}
	if len(log.Calls) != len(expectedOrder) {
		t.Fatalf("expected %d calls, got %d: %v", len(expectedOrder), len(log.Calls), log.Calls)
	}
	for i, want := range expectedOrder {
		if !strings.HasPrefix(log.Calls[i], want) {
			t.Errorf("log[%d] = %q, want prefix %q", i, log.Calls[i], want)
		}
	}

	// Config was saved.
	saved, err := cfgStore.Load("myorg/wl-commons")
	if err != nil {
		t.Fatalf("config not saved: %v", err)
	}
	if saved.Upstream != cfg.Upstream {
		t.Errorf("saved config doesn't match returned config")
	}
}

func TestCreate_LocalOnly(t *testing.T) {
	t.Parallel()
	log := NewCallLog()
	provider := NewFakeProvider()
	provider.Log = log
	cli := NewFakeDoltCLI()
	cli.Log = log
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: provider, CLI: cli, Config: cfgStore}

	result, err := svc.Create(CreateOptions{
		Upstream:    "myorg/wl-commons",
		Handle:      "myrig",
		DisplayName: "Alice",
		OwnerEmail:  "alice@example.com",
		Version:     "dev",
		SchemaSQL:   "CREATE TABLE test (id INT PRIMARY KEY);",
		LocalOnly:   true,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// No AddRemote or Push calls.
	expectedOrder := []string{"Init", "SQLExec", "StageAndCommit", "RegisterRig"}
	if len(log.Calls) != len(expectedOrder) {
		t.Fatalf("expected %d calls, got %d: %v", len(expectedOrder), len(log.Calls), log.Calls)
	}
	for i, want := range expectedOrder {
		if !strings.HasPrefix(log.Calls[i], want) {
			t.Errorf("log[%d] = %q, want prefix %q", i, log.Calls[i], want)
		}
	}

	// Config still saved.
	if _, err := cfgStore.Load("myorg/wl-commons"); err != nil {
		t.Fatalf("config not saved: %v", err)
	}

	// LocalOnly config should not have provider type or upstream URL.
	cfg := result.Config
	if cfg.ProviderType != "" {
		t.Errorf("ProviderType = %q, want empty for local-only", cfg.ProviderType)
	}
	if cfg.UpstreamURL != "" {
		t.Errorf("UpstreamURL = %q, want empty for local-only", cfg.UpstreamURL)
	}
}

func TestCreate_WithName(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:    "myorg/wl-commons",
		Handle:      "myrig",
		DisplayName: "Alice",
		OwnerEmail:  "alice@example.com",
		Version:     "dev",
		SchemaSQL:   "CREATE TABLE test (id INT PRIMARY KEY);",
		Name:        "My Wasteland",
		LocalOnly:   true,
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	// SQLExec should contain the INSERT IGNORE INTO _meta statement.
	if len(cli.SQLExecs) != 1 {
		t.Fatalf("expected 1 SQL exec, got %d", len(cli.SQLExecs))
	}
	if !strings.Contains(cli.SQLExecs[0], "INSERT IGNORE INTO _meta") {
		t.Errorf("SQL = %q, want to contain INSERT IGNORE INTO _meta", cli.SQLExecs[0])
	}
	if !strings.Contains(cli.SQLExecs[0], "My Wasteland") {
		t.Errorf("SQL = %q, want to contain 'My Wasteland'", cli.SQLExecs[0])
	}
}

func TestCreate_AlreadyExists(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.Configs["myorg/wl-commons"] = &Config{
		Upstream:  "myorg/wl-commons",
		ForkOrg:   "myorg",
		ForkDB:    "wl-commons",
		RigHandle: "myrig",
	}

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	result, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err != nil {
		t.Fatalf("Create() should succeed (no-op) when already exists: %v", err)
	}
	if result.Config.RigHandle != "myrig" {
		t.Errorf("RigHandle = %q, want %q", result.Config.RigHandle, "myrig")
	}
	// Zero CLI calls.
	if len(cli.Calls) != 0 {
		t.Errorf("expected 0 CLI calls for already-exists, got %d: %v", len(cli.Calls), cli.Calls)
	}
}

func TestCreate_InvalidUpstream(t *testing.T) {
	t.Parallel()
	svc := &Service{Remote: NewFakeProvider(), CLI: NewFakeDoltCLI(), Config: NewFakeConfigStore()}

	_, err := svc.Create(CreateOptions{
		Upstream:  "invalid",
		Handle:    "rig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err == nil {
		t.Fatal("Create() expected error for invalid upstream")
	}
}

func TestCreate_InitFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cli.InitErr = fmt.Errorf("disk full")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
		LocalOnly: true,
	})
	if err == nil {
		t.Fatal("Create() expected error when Init fails")
	}
	// No SQLExec, Commit, Register, Push after Init failure.
	for _, call := range cli.Calls {
		if strings.HasPrefix(call, "SQLExec") || strings.HasPrefix(call, "StageAndCommit") ||
			strings.HasPrefix(call, "RegisterRig") || strings.HasPrefix(call, "Push") {
			t.Errorf("unexpected call after Init failure: %s", call)
		}
	}
}

func TestCreate_SQLExecFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cli.SQLExecErr = fmt.Errorf("syntax error")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "BAD SQL",
		LocalOnly: true,
	})
	if err == nil {
		t.Fatal("Create() expected error when SQLExec fails")
	}
	// Init should have been called, but not Commit, Register, or Push.
	for _, call := range cli.Calls {
		if strings.HasPrefix(call, "StageAndCommit") || strings.HasPrefix(call, "RegisterRig") || strings.HasPrefix(call, "Push") {
			t.Errorf("unexpected call after SQLExec failure: %s", call)
		}
	}
}

func TestCreate_CommitFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cli.CommitErr = fmt.Errorf("GPG signing failed")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
		LocalOnly: true,
	})
	if err == nil {
		t.Fatal("Create() expected error when StageAndCommit fails")
	}
	for _, call := range cli.Calls {
		if strings.HasPrefix(call, "RegisterRig") || strings.HasPrefix(call, "Push") {
			t.Errorf("unexpected call after Commit failure: %s", call)
		}
	}
}

func TestCreate_RegisterFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cli.RegisterErr = fmt.Errorf("rig registration failed")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err == nil {
		t.Fatal("Create() expected error when RegisterRig fails")
	}
	for _, call := range cli.Calls {
		if strings.HasPrefix(call, "Push") || strings.HasPrefix(call, "AddRemote") {
			t.Errorf("unexpected call after Register failure: %s", call)
		}
	}
}

func TestCreate_PushFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cli.PushErr = fmt.Errorf("push rejected")
	cfgStore := NewFakeConfigStore()

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
	})
	if err == nil {
		t.Fatal("Create() expected error when Push fails")
	}
	// Config should NOT be saved when push fails.
	if _, loadErr := cfgStore.Load("myorg/wl-commons"); loadErr == nil {
		t.Error("config should not be saved when push fails")
	}
}

func TestCreate_ConfigSaveFails(t *testing.T) {
	t.Parallel()
	cli := NewFakeDoltCLI()
	cfgStore := NewFakeConfigStore()
	cfgStore.SaveErr = fmt.Errorf("permission denied")

	svc := &Service{Remote: NewFakeProvider(), CLI: cli, Config: cfgStore}

	_, err := svc.Create(CreateOptions{
		Upstream:  "myorg/wl-commons",
		Handle:    "myrig",
		SchemaSQL: "CREATE TABLE test (id INT PRIMARY KEY);",
		LocalOnly: true,
	})
	if err == nil {
		t.Fatal("Create() expected error when config save fails")
	}
	if !strings.Contains(err.Error(), "saving wasteland config") {
		t.Errorf("error = %q, want to contain 'saving wasteland config'", err.Error())
	}
}

func TestResolveConfig_NoWastelands(t *testing.T) {
	t.Parallel()
	store := NewFakeConfigStore()

	_, err := ResolveConfig(store, "")
	if err == nil {
		t.Fatal("expected error for no wastelands")
	}
	if !errors.Is(err, ErrNotJoined) {
		t.Errorf("expected ErrNotJoined, got: %v", err)
	}
}

func TestResolveConfig_SingleAutoSelect(t *testing.T) {
	t.Parallel()
	store := NewFakeConfigStore()
	store.Configs["org/db"] = &Config{
		Upstream:  "org/db",
		RigHandle: "test-rig",
	}

	cfg, err := ResolveConfig(store, "")
	if err != nil {
		t.Fatalf("ResolveConfig() error: %v", err)
	}
	if cfg.Upstream != "org/db" {
		t.Errorf("Upstream = %q, want %q", cfg.Upstream, "org/db")
	}
}

func TestResolveConfig_MultipleAmbiguous(t *testing.T) {
	t.Parallel()
	store := NewFakeConfigStore()
	store.Configs["org1/db"] = &Config{Upstream: "org1/db"}
	store.Configs["org2/db"] = &Config{Upstream: "org2/db"}

	_, err := ResolveConfig(store, "")
	if err == nil {
		t.Fatal("expected error for multiple wastelands")
	}
	if !errors.Is(err, ErrAmbiguous) {
		t.Errorf("expected ErrAmbiguous, got: %v", err)
	}
}

func TestResolveConfig_ExplicitSelection(t *testing.T) {
	t.Parallel()
	store := NewFakeConfigStore()
	store.Configs["org1/db"] = &Config{Upstream: "org1/db", RigHandle: "rig1"}
	store.Configs["org2/db"] = &Config{Upstream: "org2/db", RigHandle: "rig2"}

	cfg, err := ResolveConfig(store, "org2/db")
	if err != nil {
		t.Fatalf("ResolveConfig() error: %v", err)
	}
	if cfg.RigHandle != "rig2" {
		t.Errorf("RigHandle = %q, want %q", cfg.RigHandle, "rig2")
	}
}

func TestResolveConfig_ExplicitNotFound(t *testing.T) {
	t.Parallel()
	store := NewFakeConfigStore()

	_, err := ResolveConfig(store, "nonexistent/db")
	if err == nil {
		t.Fatal("expected error for nonexistent explicit upstream")
	}
}
