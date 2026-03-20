package federation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseUpstream(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOrg   string
		wantDB    string
		wantError bool
	}{
		{"valid", "steveyegge/wl-commons", "steveyegge", "wl-commons", false},
		{"valid with hyphens", "alice-dev/wl-commons", "alice-dev", "wl-commons", false},
		{"no slash", "wl-commons", "", "", true},
		{"empty org", "/wl-commons", "", "", true},
		{"empty db", "steveyegge/", "", "", true},
		{"empty", "", "", "", true},
		{"multiple slashes", "a/b/c", "a", "b/c", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org, db, err := ParseUpstream(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("ParseUpstream(%q) expected error, got org=%q db=%q", tt.input, org, db)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseUpstream(%q) unexpected error: %v", tt.input, err)
				return
			}
			if org != tt.wantOrg {
				t.Errorf("ParseUpstream(%q) org = %q, want %q", tt.input, org, tt.wantOrg)
			}
			if db != tt.wantDB {
				t.Errorf("ParseUpstream(%q) db = %q, want %q", tt.input, db, tt.wantDB)
			}
		})
	}
}

func TestConfigSaveLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()
	cfg := &Config{
		Upstream:  "steveyegge/wl-commons",
		ForkOrg:   "alice-dev",
		ForkDB:    "wl-commons",
		LocalDir:  "/tmp/test/wl-commons",
		RigHandle: "alice-dev",
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("steveyegge/wl-commons")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.Upstream != cfg.Upstream {
		t.Errorf("Upstream = %q, want %q", loaded.Upstream, cfg.Upstream)
	}
	if loaded.ForkOrg != cfg.ForkOrg {
		t.Errorf("ForkOrg = %q, want %q", loaded.ForkOrg, cfg.ForkOrg)
	}
	if loaded.ForkDB != cfg.ForkDB {
		t.Errorf("ForkDB = %q, want %q", loaded.ForkDB, cfg.ForkDB)
	}
	if loaded.RigHandle != cfg.RigHandle {
		t.Errorf("RigHandle = %q, want %q", loaded.RigHandle, cfg.RigHandle)
	}

	// Verify file is in wastelands/{org}/{db}.json
	expectedPath := filepath.Join(tmpDir, "wasteland", "wastelands", "steveyegge", "wl-commons.json")
	if _, err := os.Stat(expectedPath); err != nil {
		t.Errorf("config file not at expected path %s: %v", expectedPath, err)
	}
}

func TestConfigLoadNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()
	_, err := store.Load("nonexistent/db")
	if err == nil {
		t.Error("Load expected error for missing config")
	}
}

func TestConfigLoadInvalidMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	path := filepath.Join(tmpDir, "wasteland", "wastelands", "org", "db.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data := []byte("{\n  \"upstream\": \"org/db\",\n  \"fork_org\": \"fork\",\n  \"fork_db\": \"db\",\n  \"mode\": \"typo\"\n}\n")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store := NewConfigStore()
	_, err := store.Load("org/db")
	if err == nil {
		t.Fatal("Load expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), `invalid wasteland config: mode "typo" must be "pr" or "wild-west"`) {
		t.Fatalf("Load error = %q", err)
	}
}

func TestConfigSaveInvalidMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()
	err := store.Save(&Config{
		Upstream: "org/db",
		ForkOrg:  "fork",
		ForkDB:   "db",
		Mode:     "typo",
	})
	if err == nil {
		t.Fatal("Save expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), `invalid wasteland config: mode "typo" must be "pr" or "wild-west"`) {
		t.Fatalf("Save error = %q", err)
	}
}

func TestFileConfigStore_List(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()

	// Empty at first.
	upstreams, err := store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(upstreams) != 0 {
		t.Errorf("expected 0 upstreams, got %d", len(upstreams))
	}

	// Save two configs.
	if err = store.Save(&Config{Upstream: "org1/db1", ForkOrg: "fork1", ForkDB: "db1"}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	if err = store.Save(&Config{Upstream: "org2/db2", ForkOrg: "fork2", ForkDB: "db2"}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	upstreams, err = store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(upstreams) != 2 {
		t.Fatalf("expected 2 upstreams, got %d", len(upstreams))
	}

	found := map[string]bool{}
	for _, u := range upstreams {
		found[u] = true
	}
	if !found["org1/db1"] {
		t.Error("expected org1/db1 in list")
	}
	if !found["org2/db2"] {
		t.Error("expected org2/db2 in list")
	}
}

func TestFileConfigStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()

	// Save then delete.
	if err := store.Save(&Config{Upstream: "org1/db1", ForkOrg: "fork1", ForkDB: "db1"}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	if err := store.Delete("org1/db1"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	// Should no longer be loadable.
	_, err := store.Load("org1/db1")
	if err == nil {
		t.Error("expected error loading deleted config")
	}

	// List should be empty.
	upstreams, err := store.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if len(upstreams) != 0 {
		t.Errorf("expected 0 upstreams after delete, got %d", len(upstreams))
	}

	// Delete of nonexistent should error.
	err = store.Delete("nonexistent/db")
	if err == nil {
		t.Error("expected error deleting nonexistent config")
	}
}

func TestSaveAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()
	cfg := &Config{
		Upstream:  "org/db",
		ForkOrg:   "fork",
		ForkDB:    "db",
		RigHandle: "rig1",
	}

	// First save.
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("org/db")
	if err != nil {
		t.Fatalf("Load after first save: %v", err)
	}
	if loaded.RigHandle != "rig1" {
		t.Errorf("RigHandle = %q, want %q", loaded.RigHandle, "rig1")
	}

	// Second save with different data replaces atomically.
	cfg.RigHandle = "rig2"
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save (second): %v", err)
	}

	loaded, err = store.Load("org/db")
	if err != nil {
		t.Fatalf("Load after second save: %v", err)
	}
	if loaded.RigHandle != "rig2" {
		t.Errorf("RigHandle = %q, want %q", loaded.RigHandle, "rig2")
	}

	// Verify no temp files left behind.
	dir := filepath.Dir(filepath.Join(tmpDir, "wasteland", "wastelands", "org", "db.json"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("stale temp file found: %s", e.Name())
		}
	}
}

func TestSaveAtomicWithLastSyncAt(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	store := NewConfigStore()
	now := time.Now().Truncate(time.Second)
	cfg := &Config{
		Upstream:   "org/db",
		ForkOrg:    "fork",
		ForkDB:     "db",
		LastSyncAt: &now,
	}

	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("org/db")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.LastSyncAt == nil {
		t.Fatal("LastSyncAt should not be nil after round-trip")
	}
	if !loaded.LastSyncAt.Equal(now) {
		t.Errorf("LastSyncAt = %v, want %v", *loaded.LastSyncAt, now)
	}
}

func TestEscapeSQLString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"it''s", "it''''s"},
		{"", ""},
	}
	for _, tt := range tests {
		got := escapeSQLString(tt.input)
		if got != tt.want {
			t.Errorf("escapeSQLString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLocalCloneDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")
	got := LocalCloneDir("steveyegge", "wl-commons")
	want := filepath.Join("/tmp/xdg-test", "wasteland", "steveyegge", "wl-commons")
	if got != want {
		t.Errorf("LocalCloneDir = %q, want %q", got, want)
	}
}

func TestResolveProviderType(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		want         string
	}{
		{"explicit dolthub", "dolthub", "dolthub"},
		{"explicit github", "github", "github"},
		{"explicit file", "file", "file"},
		{"explicit git", "git", "git"},
		{"empty defaults to dolthub", "", "dolthub"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{ProviderType: tc.providerType}
			got := cfg.ResolveProviderType()
			if got != tc.want {
				t.Errorf("ResolveProviderType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		want string
	}{
		{"empty defaults to pr", "", ModePR},
		{"explicit pr", ModePR, ModePR},
		{"explicit wild-west", ModeWildWest, ModeWildWest},
		{"invalid returned as-is", "typo", "typo"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Mode: tc.mode}
			got := cfg.ResolveMode()
			if got != tc.want {
				t.Errorf("ResolveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveBackend(t *testing.T) {
	tests := []struct {
		name     string
		backend  string
		localDir string
		want     string
	}{
		{"explicit local", "local", "/some/dir", "local"},
		{"explicit remote", "remote", "/some/dir", "remote"},
		{"explicit remote no dir", "remote", "", "remote"},
		{"empty with local dir defaults to local", "", "/some/dir", "local"},
		{"empty without local dir defaults to remote", "", "", "remote"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Backend: tc.backend, LocalDir: tc.localDir}
			got := cfg.ResolveBackend()
			if got != tc.want {
				t.Errorf("ResolveBackend() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsGitHub(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		want         bool
	}{
		{"github provider", "github", true},
		{"dolthub provider", "dolthub", false},
		{"file provider", "file", false},
		{"empty defaults to dolthub", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{ProviderType: tc.providerType}
			got := cfg.IsGitHub()
			if got != tc.want {
				t.Errorf("IsGitHub() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJoinSetsProviderTypeAndUpstreamURL(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	store := NewConfigStore()
	provider := &fakeProviderForConfig{
		typeStr: "github",
		urlFmt:  "https://github.com/%s/%s.git",
	}
	cli := &noopDoltCLI{}

	svc := &Service{Remote: provider, CLI: cli, Config: store}
	result, err := svc.Join("org/db", "myfork", "rig", "Display", "e@e.com", "dev", false, false)
	if err != nil {
		t.Fatalf("Join() error: %v", err)
	}
	if result.Config.ProviderType != "github" {
		t.Errorf("ProviderType = %q, want %q", result.Config.ProviderType, "github")
	}
	if result.Config.UpstreamURL != "https://github.com/org/db.git" {
		t.Errorf("UpstreamURL = %q, want %q", result.Config.UpstreamURL, "https://github.com/org/db.git")
	}

	// Verify it round-trips through config store.
	loaded, err := store.Load("org/db")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if loaded.ProviderType != "github" {
		t.Errorf("loaded ProviderType = %q, want %q", loaded.ProviderType, "github")
	}
	if loaded.UpstreamURL != "https://github.com/org/db.git" {
		t.Errorf("loaded UpstreamURL = %q, want %q", loaded.UpstreamURL, "https://github.com/org/db.git")
	}
}

// fakeProviderForConfig is a minimal remote.Provider for testing config fields.
type fakeProviderForConfig struct {
	typeStr string
	urlFmt  string
}

func (f *fakeProviderForConfig) DatabaseURL(org, db string) string {
	return fmt.Sprintf(f.urlFmt, org, db)
}
func (f *fakeProviderForConfig) Fork(_, _, _ string) error                        { return nil }
func (f *fakeProviderForConfig) CreatePR(_, _, _, _, _, _ string) (string, error) { return "", nil }
func (f *fakeProviderForConfig) Type() string                                     { return f.typeStr }

// noopDoltCLI is a DoltCLI that does nothing (for config-focused tests).
type noopDoltCLI struct{}

func (n *noopDoltCLI) Clone(_, _ string) error                           { return nil }
func (n *noopDoltCLI) Init(_ string) error                               { return nil }
func (n *noopDoltCLI) SQLExec(_, _ string) error                         { return nil }
func (n *noopDoltCLI) StageAndCommit(_, _ string, _ bool) error          { return nil }
func (n *noopDoltCLI) AddRemote(_, _, _ string) error                    { return nil }
func (n *noopDoltCLI) RegisterRig(_, _, _, _, _, _ string, _ bool) error { return nil }
func (n *noopDoltCLI) Push(_ string) error                               { return nil }
func (n *noopDoltCLI) PushBranch(_, _ string, _ bool) error              { return nil }
func (n *noopDoltCLI) CheckoutBranch(_, _ string) error                  { return nil }
func (n *noopDoltCLI) CheckoutMain(_ string) error                       { return nil }
func (n *noopDoltCLI) AddUpstreamRemote(_, _ string) error               { return nil }
