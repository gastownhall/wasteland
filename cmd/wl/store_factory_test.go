package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/spf13/cobra"
)

// noopDB is a minimal commons.DB implementation for tests.
type noopDB struct{}

func (noopDB) Query(string, string) (string, error)       { return "", nil }
func (noopDB) Exec(string, string, bool, ...string) error { return nil }
func (noopDB) Branches(string) ([]string, error)          { return nil, nil }
func (noopDB) DeleteBranch(string) error                  { return nil }
func (noopDB) PushBranch(string, io.Writer) error         { return nil }
func (noopDB) PushMain(io.Writer) error                   { return nil }
func (noopDB) Sync() error                                { return nil }
func (noopDB) MergeBranch(string) error                   { return nil }
func (noopDB) DeleteRemoteBranch(string) error            { return nil }
func (noopDB) PushAllRemotes(io.Writer) error             { return nil }
func (noopDB) CanWildWest() error                         { return nil }

// withFakeSDK overrides newSDKClient and resolveWantedArg for test isolation.
// The returned SDK client uses a noopDB that succeeds on all mutations.
func withFakeSDK(t *testing.T) {
	t.Helper()
	db := noopDB{}
	oldSDKClient := newSDKClient
	newSDKClient = func(cfg *federation.Config, _ bool) (*sdk.Client, error) {
		return sdk.New(sdk.ClientConfig{
			DB:        db,
			RigHandle: cfg.RigHandle,
			Mode:      "wild-west",
			NoPush:    true, // never push in tests
		}), nil
	}
	oldDBFromConfig := openDBFromConfig
	openDBFromConfig = func(*federation.Config) (commons.DB, error) { return db, nil }
	oldResolve := resolveWantedArg
	resolveWantedArg = func(_ *federation.Config, id string) (string, error) { return id, nil }
	t.Cleanup(func() {
		newSDKClient = oldSDKClient
		openDBFromConfig = oldDBFromConfig
		resolveWantedArg = oldResolve
	})
}

// saveWasteland creates a minimal wasteland config on disk for handler tests.
func saveWasteland(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	store := federation.NewConfigStore()
	if err := store.Save(&federation.Config{
		Upstream:  "hop/wl-commons",
		ForkOrg:   "alice",
		ForkDB:    "wl-commons",
		LocalDir:  "/tmp/test/wl-commons",
		RigHandle: "alice",
		JoinedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("saving config: %v", err)
	}
}

func wastelandCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Flags().String("wasteland", "", "")
	return cmd
}

// --- Handler-level tests using SDK mock ---

func TestRunStatus_Handler(t *testing.T) {
	saveWasteland(t)
	withFakeSDK(t)

	// Seed the noopDB with data via SQL exec — noopDB returns empty results,
	// so the handler will get a "not found" from the SDK Detail call.
	// For this test we just verify the handler runs without panicking.
	var stdout, stderr bytes.Buffer
	err := runStatus(wastelandCmd(), &stdout, &stderr, "w-handler")
	// noopDB returns empty data, so Detail will return a nil item → "not found"
	if err == nil {
		t.Log("runStatus() succeeded (noopDB returned data)")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("runStatus() unexpected error: %v", err)
	}
}

func TestRunStatus_Handler_NotJoined(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := runStatus(wastelandCmd(), &stdout, &stderr, "w-abc")
	if err == nil {
		t.Fatal("runStatus() expected error when not joined")
	}
}
