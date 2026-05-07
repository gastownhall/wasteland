package main

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/gastownhall/wasteland/internal/api"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/gastownhall/wasteland/internal/tui"
)

type fakeSelfHostedServer struct {
	client *sdk.Client
	env    string
}

func (s *fakeSelfHostedServer) SetEnvironment(environment string)       { s.env = environment }
func (s *fakeSelfHostedServer) SetCommonsQuerier(pile.RowQuerier)       {}
func (s *fakeSelfHostedServer) SetScoreboard(*api.CachedEndpoint)       {}
func (s *fakeSelfHostedServer) SetScoreboardDetail(*api.CachedEndpoint) {}
func (s *fakeSelfHostedServer) SetScoreboardDump(*api.CachedEndpoint)   {}
func (s *fakeSelfHostedServer) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func withSelfHostedAPIServerOverride(t *testing.T, fn func(*sdk.Client) selfHostedAPIServer) {
	t.Helper()
	old := newSelfHostedAPIServer
	newSelfHostedAPIServer = fn
	t.Cleanup(func() {
		newSelfHostedAPIServer = old
	})
}

func TestRunTUI_LocalClientCallbacks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOLTHUB_TOKEN", "token")
	saveTestConfig(t, &federation.Config{
		Upstream:     "hop/wl-commons",
		ForkOrg:      "alice",
		ForkDB:       "wl-commons",
		LocalDir:     t.TempDir(),
		RigHandle:    "alice",
		Backend:      federation.BackendLocal,
		ProviderType: "dolthub",
		JoinedAt:     time.Now(),
	})
	installReviewDolt(t)

	withLocalWorkflowDBOverride(t, func(string, string) localWorkflowDB {
		return fakeLocalWorkflowDB{
			pushMainFn: func(io.Writer) error { return nil },
			scriptedDB: scriptedDB{
				syncFunc: func() error { return nil },
			},
		}
	})

	var captured tui.Config
	withTeaOverrides(t,
		func(cfg tui.Config) bubbletea.Model {
			captured = cfg
			return nil
		},
		func(bubbletea.Model, ...bubbletea.ProgramOption) teaProgram {
			return fakeTeaProgram{}
		},
	)

	var closedPR string
	withPushBranchOverride(t, func(string, string, string, bool, io.Writer) error { return nil })
	withDoltHubPRProviderOverride(t, func(string) doltHubPRProvider {
		return fakeDoltHubPRProvider{
			createPRFn: func(_, _, _, branch, title, body string) (string, error) {
				if branch != "wl/alice/w-go-1" || !strings.Contains(title, "Fix auth") || !strings.Contains(body, "UPDATE wanted") {
					t.Fatalf("got %q %q %q", branch, title, body)
				}
				return "https://dolthub.example/pr/tui-local", nil
			},
			findPRFn: func(string, string, string, string) (string, string) {
				return "https://dolthub.example/pr/tui-local", "17"
			},
			closePRFn: func(_, _, prID string) error {
				closedPR = prID
				return nil
			},
		}
	})

	cmd := commandWithWasteland("hop/wl-commons")
	_ = cmd.Flags().Set("local-db", "true")
	if err := runTUI(cmd, io.Discard, io.Discard); err != nil {
		t.Fatalf("runTUI() error = %v", err)
	}

	diff, err := captured.Client.BranchDiff("wl/alice/w-go-1")
	if err != nil {
		t.Fatalf("BranchDiff() error = %v", err)
	}
	if !strings.Contains(diff, "UPDATE wanted") {
		t.Fatalf("diff = %q", diff)
	}

	url, err := captured.Client.SubmitPR("wl/alice/w-go-1")
	if err != nil {
		t.Fatalf("SubmitPR() error = %v", err)
	}
	if url != "https://dolthub.example/pr/tui-local" {
		t.Fatalf("url = %q", url)
	}

	if got := captured.Client.CheckPR("wl/alice/w-go-1"); got != "https://dolthub.example/pr/tui-local" {
		t.Fatalf("CheckPR() = %q", got)
	}
	if err := captured.Client.ClosePR("wl/alice/w-go-1"); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if closedPR != "17" {
		t.Fatalf("closedPR = %q", closedPR)
	}

	if err := captured.Client.SaveSettings(federation.ModeWildWest, true); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	cfg, err := federation.NewConfigStore().Load("hop/wl-commons")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mode != federation.ModeWildWest || !cfg.Signing || captured.Client.Mode() != federation.ModeWildWest {
		t.Fatalf("saved cfg = %+v, client mode = %q", cfg, captured.Client.Mode())
	}
}

func TestRunTUI_RemoteClientCallbacks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOLTHUB_TOKEN", "token")
	saveTestConfig(t, &federation.Config{
		Upstream:     "hop/wl-commons",
		ForkOrg:      "alice",
		ForkDB:       "wl-commons",
		RigHandle:    "alice",
		Backend:      federation.BackendRemote,
		ProviderType: "dolthub",
		JoinedAt:     time.Now(),
	})

	withRemoteWorkflowDBOverride(t, func(string, string, string, string, string, string) remoteWorkflowDB {
		return fakeRemoteWorkflowDB{
			scriptedDB: scriptedDB{
				syncFunc: func() error { return nil },
				queryFunc: func(query, ref string) (string, error) {
					if ref == "" {
						return "", nil
					}
					if ref != "wl/alice/w-1" || !strings.Contains(query, "WHERE w.id='w-1'") {
						t.Fatalf("query = %q ref = %q", query, ref)
					}
					return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at,completion_id,completion_wanted_id,completed_by,evidence,completion_stamp_id,validated_by,stamp_record_id,stamp_author,stamp_subject,stamp_valence,stamp_severity,stamp_context_id,stamp_context_type,stamp_skill_tags,stamp_message\nw-1,Fix auth,,,,2,,alice,,claimed,medium,,,,,,,,,,,,,,,\n", nil
				},
			},
			diffFn: func(branch string) (string, error) {
				if branch != "wl/alice/w-1" {
					t.Fatalf("branch = %q", branch)
				}
				return "remote diff", nil
			},
		}
	})

	var captured tui.Config
	withTeaOverrides(t,
		func(cfg tui.Config) bubbletea.Model {
			captured = cfg
			return nil
		},
		func(bubbletea.Model, ...bubbletea.ProgramOption) teaProgram {
			return fakeTeaProgram{}
		},
	)

	var closedPR string
	withDoltHubPRProviderOverride(t, func(string) doltHubPRProvider {
		return fakeDoltHubPRProvider{
			createPRFn: func(_, _, _, branch, title, body string) (string, error) {
				if branch != "wl/alice/w-1" || title != "[wl] Fix auth" || body != "" {
					t.Fatalf("got %q %q %q", branch, title, body)
				}
				return "https://dolthub.example/pr/tui-remote", nil
			},
			findPRFn: func(string, string, string, string) (string, string) {
				return "https://dolthub.example/pr/tui-remote", "29"
			},
			closePRFn: func(_, _, prID string) error {
				closedPR = prID
				return nil
			},
		}
	})

	if err := runTUI(commandWithWasteland("hop/wl-commons"), io.Discard, io.Discard); err != nil {
		t.Fatalf("runTUI() error = %v", err)
	}

	diff, err := captured.Client.BranchDiff("wl/alice/w-1")
	if err != nil {
		t.Fatalf("BranchDiff() error = %v", err)
	}
	if diff != "remote diff" {
		t.Fatalf("diff = %q", diff)
	}

	url, err := captured.Client.SubmitPR("wl/alice/w-1")
	if err != nil {
		t.Fatalf("SubmitPR() error = %v", err)
	}
	if url != "https://dolthub.example/pr/tui-remote" {
		t.Fatalf("url = %q", url)
	}

	if got := captured.Client.CheckPR("wl/alice/w-1"); got != "https://dolthub.example/pr/tui-remote" {
		t.Fatalf("CheckPR() = %q", got)
	}
	if err := captured.Client.ClosePR("wl/alice/w-1"); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if closedPR != "29" {
		t.Fatalf("closedPR = %q", closedPR)
	}
}

func TestRunServe_UsesWLEnvironmentOverride(t *testing.T) {
	var capturedServer *fakeSelfHostedServer
	withSelfHostedAPIServerOverride(t, func(client *sdk.Client) selfHostedAPIServer {
		capturedServer = &fakeSelfHostedServer{client: client}
		return capturedServer
	})
	withServeListenOverride(t, func(*http.Server) error { return nil })
	withLocalWorkflowDBOverride(t, func(string, string) localWorkflowDB {
		return fakeLocalWorkflowDB{
			scriptedDB: scriptedDB{
				syncFunc:  func() error { return nil },
				queryFunc: func(string, string) (string, error) { return "", nil },
			},
		}
	})

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("WL_ENVIRONMENT", "staging")
	saveTestConfig(t, &federation.Config{
		Upstream:  "hop/wl-commons",
		ForkOrg:   "alice",
		ForkDB:    "wl-commons",
		LocalDir:  t.TempDir(),
		RigHandle: "alice",
		Backend:   federation.BackendLocal,
		JoinedAt:  time.Now(),
	})
	installFakeDolt(t, "#!/bin/sh\nexit 0\n")

	cmd := commandWithWasteland("hop/wl-commons")
	cmd.Flags().Int("port", 8999, "")
	cmd.Flags().Bool("dev", false, "")
	_ = cmd.Flags().Set("local-db", "true")
	if err := runServe(cmd, io.Discard, io.Discard); err != nil {
		t.Fatalf("runServe() error = %v", err)
	}
	if capturedServer == nil || capturedServer.client == nil {
		t.Fatal("self-hosted API server did not receive client")
	}
	if capturedServer.env != "staging" {
		t.Fatalf("environment = %q, want %q", capturedServer.env, "staging")
	}
}

func TestRunServe_LocalClientCallbacks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOLTHUB_TOKEN", "token")
	saveTestConfig(t, &federation.Config{
		Upstream:     "hop/wl-commons",
		ForkOrg:      "alice",
		ForkDB:       "wl-commons",
		LocalDir:     t.TempDir(),
		RigHandle:    "alice",
		Backend:      federation.BackendLocal,
		ProviderType: "dolthub",
		JoinedAt:     time.Now(),
	})
	installReviewDolt(t)

	withLocalWorkflowDBOverride(t, func(string, string) localWorkflowDB {
		return fakeLocalWorkflowDB{
			pushMainFn: func(io.Writer) error { return nil },
			scriptedDB: scriptedDB{
				syncFunc:  func() error { return nil },
				queryFunc: func(string, string) (string, error) { return "", nil },
			},
		}
	})

	var capturedClient *sdk.Client
	withSelfHostedAPIServerOverride(t, func(client *sdk.Client) selfHostedAPIServer {
		capturedClient = client
		return &fakeSelfHostedServer{client: client}
	})
	withServeListenOverride(t, func(*http.Server) error { return nil })

	var closedPR string
	withPushBranchOverride(t, func(string, string, string, bool, io.Writer) error { return nil })
	withDoltHubPRProviderOverride(t, func(string) doltHubPRProvider {
		return fakeDoltHubPRProvider{
			createPRFn: func(_, _, _, branch, title, body string) (string, error) {
				if branch != "wl/alice/w-go-1" || !strings.Contains(title, "Fix auth") || !strings.Contains(body, "UPDATE wanted") {
					t.Fatalf("got %q %q %q", branch, title, body)
				}
				return "https://dolthub.example/pr/serve-local", nil
			},
			findPRFn: func(string, string, string, string) (string, string) {
				return "https://dolthub.example/pr/serve-local", "37"
			},
			closePRFn: func(_, _, prID string) error {
				closedPR = prID
				return nil
			},
		}
	})

	cmd := commandWithWasteland("hop/wl-commons")
	cmd.Flags().Int("port", 8999, "")
	cmd.Flags().Bool("dev", false, "")
	_ = cmd.Flags().Set("local-db", "true")
	if err := runServe(cmd, io.Discard, io.Discard); err != nil {
		t.Fatalf("runServe() error = %v", err)
	}

	diff, err := capturedClient.BranchDiff("wl/alice/w-go-1")
	if err != nil {
		t.Fatalf("BranchDiff() error = %v", err)
	}
	if !strings.Contains(diff, "UPDATE wanted") {
		t.Fatalf("diff = %q", diff)
	}

	url, err := capturedClient.SubmitPR("wl/alice/w-go-1")
	if err != nil {
		t.Fatalf("SubmitPR() error = %v", err)
	}
	if url != "https://dolthub.example/pr/serve-local" {
		t.Fatalf("url = %q", url)
	}
	if got := capturedClient.CheckPR("wl/alice/w-go-1"); got != "https://dolthub.example/pr/serve-local" {
		t.Fatalf("CheckPR() = %q", got)
	}
	if err := capturedClient.ClosePR("wl/alice/w-go-1"); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if closedPR != "37" {
		t.Fatalf("closedPR = %q", closedPR)
	}

	if err := capturedClient.SaveSettings(federation.ModeWildWest, true); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	cfg, err := federation.NewConfigStore().Load("hop/wl-commons")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Mode != federation.ModeWildWest || !cfg.Signing {
		t.Fatalf("saved cfg = %+v", cfg)
	}
}

func TestRunServe_RemoteClientCallbacks(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOLTHUB_TOKEN", "token")
	saveTestConfig(t, &federation.Config{
		Upstream:     "hop/wl-commons",
		ForkOrg:      "alice",
		ForkDB:       "wl-commons",
		RigHandle:    "alice",
		Backend:      federation.BackendRemote,
		ProviderType: "dolthub",
		JoinedAt:     time.Now(),
	})

	withRemoteWorkflowDBOverride(t, func(string, string, string, string, string, string) remoteWorkflowDB {
		return fakeRemoteWorkflowDB{
			scriptedDB: scriptedDB{
				syncFunc: func() error { return nil },
				queryFunc: func(query, ref string) (string, error) {
					if ref == "" {
						return "", nil
					}
					if ref != "wl/alice/w-1" || !strings.Contains(query, "WHERE w.id='w-1'") {
						t.Fatalf("query = %q ref = %q", query, ref)
					}
					return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at,completion_id,completion_wanted_id,completed_by,evidence,completion_stamp_id,validated_by,stamp_record_id,stamp_author,stamp_subject,stamp_valence,stamp_severity,stamp_context_id,stamp_context_type,stamp_skill_tags,stamp_message\nw-1,Fix auth,,,,2,,alice,,claimed,medium,,,,,,,,,,,,,,,\n", nil
				},
			},
			diffFn: func(branch string) (string, error) {
				if branch != "wl/alice/w-1" {
					t.Fatalf("branch = %q", branch)
				}
				return "serve remote diff", nil
			},
		}
	})

	var capturedClient *sdk.Client
	withSelfHostedAPIServerOverride(t, func(client *sdk.Client) selfHostedAPIServer {
		capturedClient = client
		return &fakeSelfHostedServer{client: client}
	})
	withServeListenOverride(t, func(*http.Server) error { return nil })

	var closedPR string
	withDoltHubPRProviderOverride(t, func(string) doltHubPRProvider {
		return fakeDoltHubPRProvider{
			createPRFn: func(_, _, _, branch, title, body string) (string, error) {
				if branch != "wl/alice/w-1" || title != "[wl] Fix auth" || body != "" {
					t.Fatalf("got %q %q %q", branch, title, body)
				}
				return "https://dolthub.example/pr/serve-remote", nil
			},
			findPRFn: func(string, string, string, string) (string, string) {
				return "https://dolthub.example/pr/serve-remote", "41"
			},
			closePRFn: func(_, _, prID string) error {
				closedPR = prID
				return nil
			},
		}
	})

	cmd := commandWithWasteland("hop/wl-commons")
	cmd.Flags().Int("port", 8999, "")
	cmd.Flags().Bool("dev", false, "")
	if err := runServe(cmd, &bytes.Buffer{}, io.Discard); err != nil {
		t.Fatalf("runServe() error = %v", err)
	}

	diff, err := capturedClient.BranchDiff("wl/alice/w-1")
	if err != nil {
		t.Fatalf("BranchDiff() error = %v", err)
	}
	if diff != "serve remote diff" {
		t.Fatalf("diff = %q", diff)
	}

	url, err := capturedClient.SubmitPR("wl/alice/w-1")
	if err != nil {
		t.Fatalf("SubmitPR() error = %v", err)
	}
	if url != "https://dolthub.example/pr/serve-remote" {
		t.Fatalf("url = %q", url)
	}
	if got := capturedClient.CheckPR("wl/alice/w-1"); got != "https://dolthub.example/pr/serve-remote" {
		t.Fatalf("CheckPR() = %q", got)
	}
	if err := capturedClient.ClosePR("wl/alice/w-1"); err != nil {
		t.Fatalf("ClosePR() error = %v", err)
	}
	if closedPR != "41" {
		t.Fatalf("closedPR = %q", closedPR)
	}
}

func TestRunServe_UsesInjectedSelfHostedServer(t *testing.T) {
	var capturedServer *fakeSelfHostedServer
	withSelfHostedAPIServerOverride(t, func(client *sdk.Client) selfHostedAPIServer {
		capturedServer = &fakeSelfHostedServer{client: client}
		return capturedServer
	})
	withServeListenOverride(t, func(*http.Server) error { return nil })
	withLocalWorkflowDBOverride(t, func(string, string) localWorkflowDB {
		return fakeLocalWorkflowDB{
			scriptedDB: scriptedDB{
				syncFunc:  func() error { return nil },
				queryFunc: func(string, string) (string, error) { return "", nil },
			},
		}
	})

	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	saveTestConfig(t, &federation.Config{
		Upstream:  "hop/wl-commons",
		ForkOrg:   "alice",
		ForkDB:    "wl-commons",
		LocalDir:  t.TempDir(),
		RigHandle: "alice",
		Backend:   federation.BackendLocal,
		JoinedAt:  time.Now(),
	})
	installFakeDolt(t, "#!/bin/sh\nexit 0\n")

	cmd := commandWithWasteland("hop/wl-commons")
	cmd.Flags().Int("port", 8999, "")
	cmd.Flags().Bool("dev", false, "")
	_ = cmd.Flags().Set("local-db", "true")
	if err := runServe(cmd, io.Discard, io.Discard); err != nil {
		t.Fatalf("runServe() error = %v", err)
	}
	if capturedServer == nil || capturedServer.client == nil {
		t.Fatal("self-hosted API server did not receive client")
	}
	if capturedServer.env != "self-sovereign" {
		t.Fatalf("environment = %q, want %q", capturedServer.env, "self-sovereign")
	}
}
