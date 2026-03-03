package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/julianknutsen/wasteland/internal/api"
	"github.com/julianknutsen/wasteland/internal/backend"
	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/federation"
	"github.com/julianknutsen/wasteland/internal/hosted"
	"github.com/julianknutsen/wasteland/internal/sdk"
	"github.com/julianknutsen/wasteland/internal/style"
	"github.com/julianknutsen/wasteland/web"
	"github.com/spf13/cobra"
)

func newServeCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the web UI server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hostedMode, _ := cmd.Flags().GetBool("hosted")
			if hostedMode {
				return runServeHosted(cmd, stdout, stderr)
			}
			return runServe(cmd, stdout, stderr)
		},
	}
	cmd.Flags().Int("port", 8999, "Port to listen on")
	cmd.Flags().Bool("dev", false, "Enable CORS for development (Vite proxy)")
	cmd.Flags().Bool("hosted", false, "Run in multi-tenant hosted mode (Nango)")
	return cmd
}

// resolvePort returns the port from the --port flag, or from the PORT env var
// if set (Railway and similar PaaS platforms set PORT automatically).
func resolvePort(cmd *cobra.Command) int {
	port, _ := cmd.Flags().GetInt("port")
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			port = p
		}
	}
	return port
}

func runServe(cmd *cobra.Command, stdout, stderr io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	slog.SetDefault(logger)

	port := resolvePort(cmd)
	devMode, _ := cmd.Flags().GetBool("dev")

	cfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	var db commons.DB
	if cfg.ResolveBackend() == federation.BackendLocal {
		if err := requireDolt(); err != nil {
			return err
		}
		localDB := backend.NewLocalDB(cfg.LocalDir, cfg.ResolveMode())
		db = localDB

		sp := style.StartSpinner(stderr, "Syncing with upstream...")
		err = localDB.Sync()
		sp.Stop()
		if err != nil {
			return fmt.Errorf("syncing with upstream: %w", err)
		}

		if cfg.ResolveMode() == federation.ModePR {
			if err := localDB.PushMain(io.Discard); err != nil {
				slog.Warn("could not sync origin/main", "error", err)
			}
		}
	} else {
		token := commons.DoltHubToken()
		if token == "" {
			return fmt.Errorf("DOLTHUB_TOKEN required for remote mode — set it in your environment")
		}
		upOrg, upDB, err := federation.ParseUpstream(cfg.Upstream)
		if err != nil {
			return fmt.Errorf("parsing upstream: %w", err)
		}
		remoteDB := backend.NewRemoteDB(token, upOrg, upDB, cfg.ForkOrg, cfg.ForkDB, cfg.ResolveMode())
		db = remoteDB

		sp := style.StartSpinner(stderr, "Syncing fork with upstream...")
		err = remoteDB.Sync()
		sp.Stop()
		if err != nil {
			slog.Warn("fork sync skipped", "error", err)
		}
	}

	// Build LoadDiff callback based on backend type.
	loadDiff := func(branch string) (string, error) {
		if cfg.ResolveBackend() != federation.BackendLocal {
			if rdb, ok := db.(*backend.RemoteDB); ok {
				return rdb.Diff(branch)
			}
			return "", fmt.Errorf("diff view requires local backend")
		}
		doltPath, err := exec.LookPath("dolt")
		if err != nil {
			return "", err
		}
		base := diffBase(cfg.LocalDir, doltPath)
		var buf bytes.Buffer
		if err := renderMarkdownDiff(&buf, cfg.LocalDir, doltPath, branch, base); err != nil {
			return "", err
		}
		return buf.String(), nil
	}

	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: cfg.RigHandle,
		Mode:      cfg.ResolveMode(),
		Signing:   cfg.Signing,
		HopURI:    cfg.HopURI,
		SaveConfig: func(mode string, signing bool) error {
			store := federation.NewConfigStore()
			c, err := store.Load(cfg.Upstream)
			if err != nil {
				return err
			}
			c.Mode = mode
			c.Signing = signing
			return store.Save(c)
		},
		LoadDiff: loadDiff,
		CreatePR: func(branch string) (string, error) {
			if cfg.ResolveBackend() != federation.BackendLocal {
				return createPRForBranchRemote(cfg, db, branch)
			}
			return createPRForBranch(cfg, branch)
		},
		CheckPR: func(branch string) string {
			return checkPRForBranch(cfg, branch)
		},
		ClosePR: func(branch string) error {
			return closePRForBranch(cfg, branch)
		},
		ListPendingItems: listPendingItemsFromPRs(cfg),
		BranchURL:        branchURLCallback(cfg),
	})

	server := api.New(client)

	generalRL := api.RateLimit(api.NewRateLimiter(120, 120, time.Minute))
	bodyLimit := api.MaxBytesBody(64 << 10) // 64 KB
	handler := api.RequestLog(logger)(api.SecurityHeaders(generalRL(bodyLimit(api.SPAHandler(server, web.Assets)))))
	if devMode {
		handler = api.CORSMiddleware(handler)
	}

	addr := fmt.Sprintf(":%d", port)
	slog.Info("server started", "mode", "self-sovereign", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: handler, MaxHeaderBytes: 1 << 20} //nolint:gosec // bind addr is user-controlled via --port flag
	return srv.ListenAndServe()
}

func runServeHosted(cmd *cobra.Command, stdout, _ io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	slog.SetDefault(logger)

	port := resolvePort(cmd)
	devMode, _ := cmd.Flags().GetBool("dev")

	// Read required env vars.
	nangoSecretKey := os.Getenv("NANGO_SECRET_KEY")
	if nangoSecretKey == "" {
		return fmt.Errorf("NANGO_SECRET_KEY environment variable is required for hosted mode")
	}
	sessionSecret := os.Getenv("WL_SESSION_SECRET")
	if sessionSecret == "" {
		return fmt.Errorf("WL_SESSION_SECRET environment variable is required for hosted mode")
	}

	// Optional env vars with defaults.
	nangoBaseURL := os.Getenv("NANGO_BASE_URL")
	nangoIntegrationID := os.Getenv("NANGO_INTEGRATION_ID")

	// Build Nango client.
	nangoCfg := hosted.NangoConfig{
		BaseURL:       nangoBaseURL,
		SecretKey:     nangoSecretKey,
		IntegrationID: nangoIntegrationID,
	}
	nangoClient := hosted.NewNangoClient(nangoCfg)

	// Build session store and workspace resolver.
	sessions := hosted.NewSessionStore()
	resolver := hosted.NewWorkspaceResolver(nangoClient, sessions)

	// Build the API server with hosted workspace resolution.
	apiServer := api.NewHostedWorkspace(hosted.NewClientFunc(), hosted.NewWorkspaceFunc())

	// Build the hosted server and compose handlers.
	hostedServer := hosted.NewServer(resolver, sessions, nangoClient, sessionSecret)

	bodyLimit := api.MaxBytesBody(64 << 10) // 64 KB
	handler := api.RequestLog(logger)(api.SecurityHeaders(bodyLimit(hostedServer.Handler(apiServer, web.Assets))))
	if devMode {
		handler = api.CORSMiddleware(handler)
	}

	addr := fmt.Sprintf(":%d", port)
	slog.Info("server started", "mode", "hosted", "addr", addr)
	slog.Info("nango configured", "integration_id", nangoClient.IntegrationID())
	srv := &http.Server{Addr: addr, Handler: handler, MaxHeaderBytes: 1 << 20} //nolint:gosec // bind addr is user-controlled via --port flag
	return srv.ListenAndServe()
}
