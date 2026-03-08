package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gastownhall/wasteland/internal/api"
	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/hosted"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/gastownhall/wasteland/web"
	"github.com/getsentry/sentry-go"
	sentryhttp "github.com/getsentry/sentry-go/http"
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

// listenAndServeGraceful starts the server and shuts down gracefully on
// SIGINT/SIGTERM, giving in-flight requests up to 10 seconds to complete.
func listenAndServeGraceful(srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case sig := <-quit:
		slog.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			return fmt.Errorf("graceful shutdown failed: %w", err)
		}
		slog.Info("server stopped")
		return nil
	}
}

func initSentry(environment string) {
	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		return
	}
	if err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          version,
		TracesSampleRate: 0.2,
	}); err != nil {
		slog.Error("sentry init failed", "error", err)
	}
}

func runServe(cmd *cobra.Command, stdout, stderr io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	slog.SetDefault(logger)

	initSentry("self-sovereign")
	defer sentry.Flush(2 * time.Second)

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
		CloseUpstreamPR:  closeUpstreamPRCallback(cfg),
	})

	server := api.New(client)

	scoreboardCache := api.NewScoreboardCache(db, 5*time.Minute)
	server.SetScoreboard(scoreboardCache)
	scoreboardCache.Start()
	defer scoreboardCache.Stop()

	detailCache := api.NewCachedEndpoint(newDetailRefresh(db), 5*time.Minute)
	server.SetScoreboardDetail(detailCache)
	detailCache.Start()
	defer detailCache.Stop()

	dumpCache := api.NewCachedEndpoint(newDumpRefresh(db), 5*time.Minute)
	server.SetScoreboardDump(dumpCache)
	dumpCache.Start()
	defer dumpCache.Stop()

	rateLimiter := api.NewRateLimiter(120, 120, time.Minute)
	defer rateLimiter.Stop()
	generalRL := api.RateLimit(rateLimiter)
	bodyLimit := api.MaxBytesBody(64 << 10) // 64 KB
	sentryMiddleware := sentryhttp.New(sentryhttp.Options{Repanic: true})
	handler := sentryMiddleware.Handle(api.RequestLog(logger)(api.SecurityHeaders(generalRL(bodyLimit(api.SPAHandler(server, web.Assets))))))
	if devMode {
		handler = api.CORSMiddleware(handler)
	}

	addr := fmt.Sprintf(":%d", port)
	slog.Info("server started", "mode", "self-sovereign", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: handler, MaxHeaderBytes: 1 << 20} //nolint:gosec // bind addr is user-controlled via --port flag
	return listenAndServeGraceful(srv)
}

func runServeHosted(cmd *cobra.Command, stdout, _ io.Writer) error {
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	slog.SetDefault(logger)

	port := resolvePort(cmd)
	devMode, _ := cmd.Flags().GetBool("dev")

	environment := os.Getenv("WL_ENVIRONMENT")
	if environment == "" {
		environment = "production"
	}
	initSentry(environment)
	defer sentry.Flush(2 * time.Second)

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

	// Public read-only RemoteDB against hop/wl-commons (no token needed).
	publicDB := backend.NewRemoteDB("", "hop", "wl-commons", "hop", "wl-commons", "")

	// Scoreboard cache.
	scoreboardCache := api.NewScoreboardCache(publicDB, 5*time.Minute)
	apiServer.SetScoreboard(scoreboardCache)
	scoreboardCache.Start()
	defer scoreboardCache.Stop()

	hostedDetailCache := api.NewCachedEndpoint(newDetailRefresh(publicDB), 5*time.Minute)
	apiServer.SetScoreboardDetail(hostedDetailCache)
	hostedDetailCache.Start()
	defer hostedDetailCache.Stop()

	hostedDumpCache := api.NewCachedEndpoint(newDumpRefresh(publicDB), 5*time.Minute)
	apiServer.SetScoreboardDump(hostedDumpCache)
	hostedDumpCache.Start()
	defer hostedDumpCache.Stop()

	// Anonymous client for unauthenticated public reads (browse, detail, etc.).
	// Uses a background-refreshing cache so no user request blocks on DoltHub.
	pendingCache := newPendingItemsCache("hop", "wl-commons", 2*time.Minute)
	defer pendingCache.Stop()
	anonClient := sdk.New(sdk.ClientConfig{
		DB:               publicDB,
		Mode:             federation.ModePR,
		ListPendingItems: pendingCache.Get,
	})
	apiServer.SetPublicClient(anonClient)

	// Build the hosted server and compose handlers.
	hostedServer := hosted.NewServer(resolver, sessions, nangoClient, sessionSecret, environment)

	hostedRateLimiter := api.NewRateLimiter(120, 120, time.Minute)
	defer hostedRateLimiter.Stop()
	generalRL := api.RateLimit(hostedRateLimiter)
	bodyLimit := api.MaxBytesBody(64 << 10) // 64 KB
	sentryMiddleware := sentryhttp.New(sentryhttp.Options{Repanic: true})
	handler := sentryMiddleware.Handle(api.RequestLog(logger)(api.SecurityHeaders(generalRL(bodyLimit(hostedServer.Handler(apiServer, web.Assets))))))
	if devMode {
		handler = api.CORSMiddleware(handler)
	}

	addr := fmt.Sprintf(":%d", port)
	slog.Info("server started", "mode", "hosted", "addr", addr)
	slog.Info("nango configured", "integration_id", nangoClient.IntegrationID())
	srv := &http.Server{Addr: addr, Handler: handler, MaxHeaderBytes: 1 << 20} //nolint:gosec // bind addr is user-controlled via --port flag
	return listenAndServeGraceful(srv)
}

// newDetailRefresh returns a refresh callback for the scoreboard detail cache.
func newDetailRefresh(db commons.DB) func() ([]byte, error) {
	return func() ([]byte, error) {
		entries, err := commons.QueryScoreboardDetail(db, 100)
		if err != nil {
			return nil, err
		}
		return json.Marshal(api.ToScoreboardDetailResponse(entries))
	}
}

// newDumpRefresh returns a refresh callback for the scoreboard dump cache.
func newDumpRefresh(db commons.DB) func() ([]byte, error) {
	return func() ([]byte, error) {
		dump, err := commons.QueryScoreboardDump(db)
		if err != nil {
			return nil, err
		}
		return json.Marshal(api.ToScoreboardDumpResponse(dump))
	}
}

// pendingItemsCache refreshes pending PR data in the background so user
// requests never block on DoltHub API calls.
type pendingItemsCache struct {
	mu     sync.RWMutex
	cached map[string][]sdk.PendingItem
	stop   chan struct{}
}

func newPendingItemsCache(upstreamOrg, db string, interval time.Duration) *pendingItemsCache {
	c := &pendingItemsCache{stop: make(chan struct{})}
	provider := remote.NewDoltHubProvider("")

	refresh := func() {
		states, err := provider.ListPendingWantedIDs(upstreamOrg, db)
		if err != nil {
			slog.Warn("pending items refresh failed", "error", err)
			return
		}
		result := make(map[string][]sdk.PendingItem, len(states))
		for id, pending := range states {
			items := make([]sdk.PendingItem, len(pending))
			for i, p := range pending {
				items[i] = sdk.PendingItem{
					RigHandle:   p.RigHandle,
					Status:      p.Status,
					ClaimedBy:   p.ClaimedBy,
					Branch:      p.Branch,
					BranchURL:   p.BranchURL,
					PRURL:       p.PRURL,
					CompletedBy: p.CompletedBy,
					Evidence:    p.Evidence,
				}
			}
			result[id] = items
		}
		c.mu.Lock()
		c.cached = result
		c.mu.Unlock()
	}

	// Pre-warm on startup.
	go refresh()

	// Background refresh loop.
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				refresh()
			case <-c.stop:
				return
			}
		}
	}()

	return c
}

func (c *pendingItemsCache) Get() (map[string][]sdk.PendingItem, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached, nil
}

func (c *pendingItemsCache) Stop() {
	close(c.stop)
}
