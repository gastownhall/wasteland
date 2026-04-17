// Package api provides the HTTP REST server for the Wasteland wanted board.
//
// It wraps sdk.Client to expose browse, detail, dashboard, mutation, and branch
// operations as JSON endpoints consumable by any HTTP client.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/gastownhall/wasteland/internal/sdk"
)

// ClientFunc resolves an sdk.Client from an HTTP request. In self-sovereign mode
// this returns a static client; in hosted mode it resolves per-user from session.
type ClientFunc func(r *http.Request) (*sdk.Client, error)

// WorkspaceFunc resolves an sdk.Workspace from an HTTP request.
type WorkspaceFunc func(r *http.Request) (*sdk.Workspace, error)

// Server is the HTTP API server wrapping an SDK client.
type Server struct {
	clientFunc          ClientFunc
	workspaceFunc       WorkspaceFunc
	mutationInvalidator func(context.Context)
	pile                pile.RowQuerier
	commons             pile.RowQuerier
	ghCache             githubcache.Cache
	scoreboard          *CachedEndpoint
	scoreboardDetail    *CachedEndpoint
	scoreboardDump      *CachedEndpoint
	publicClient        *sdk.Client // anonymous fallback for public reads (hosted mode)
	browseCache         *ReadCache  // keyed by canonicalized query string
	detailCache         *ReadCache  // keyed by item ID
	environment         string
	mux                 *http.ServeMux
	hosted              bool // true when running in multi-tenant hosted mode
}

// New creates a Server backed by the given SDK client.
// This is the backwards-compatible constructor for self-sovereign mode.
func New(client *sdk.Client) *Server {
	return NewWithClientFunc(func(_ *http.Request) (*sdk.Client, error) {
		return client, nil
	})
}

// NewHosted creates a Server for multi-tenant hosted mode.
func NewHosted(fn ClientFunc) *Server {
	s := &Server{
		clientFunc:  fn,
		browseCache: NewReadCache(30*time.Second, 64),
		detailCache: NewReadCache(30*time.Second, 256),
		mux:         http.NewServeMux(),
		hosted:      true,
	}
	s.pile = pile.NewDefault()
	s.commons = pile.NewCommonsReader()
	s.ghCache = loadGitHubCache()
	s.registerRoutes()
	return s
}

// NewHostedWorkspace creates a Server for multi-tenant hosted mode with workspace support.
func NewHostedWorkspace(clientFn ClientFunc, workspaceFn WorkspaceFunc) *Server {
	s := &Server{
		clientFunc:    clientFn,
		workspaceFunc: workspaceFn,
		browseCache:   NewReadCache(30*time.Second, 64),
		detailCache:   NewReadCache(30*time.Second, 256),
		mux:           http.NewServeMux(),
		hosted:        true,
	}
	s.pile = pile.NewDefault()
	s.commons = pile.NewCommonsReader()
	s.ghCache = loadGitHubCache()
	s.registerRoutes()
	return s
}

// NewWithClientFunc creates a Server that resolves a client per-request.
func NewWithClientFunc(fn ClientFunc) *Server {
	s := &Server{
		clientFunc:  fn,
		browseCache: NewReadCache(30*time.Second, 64),
		detailCache: NewReadCache(30*time.Second, 256),
		mux:         http.NewServeMux(),
	}
	s.pile = pile.NewDefault()
	s.commons = pile.NewCommonsReader()
	s.ghCache = loadGitHubCache()
	s.registerRoutes()
	return s
}

// loadGitHubCache opens the on-disk github-handles cache. Failures are
// logged and swallowed: the profile handler tolerates a nil cache.
func loadGitHubCache() githubcache.Cache {
	c, err := githubcache.Load()
	if err != nil {
		slog.Warn("github-handles cache unavailable; profile links disabled",
			"error", err)
		return nil
	}
	return c
}

// SetProfileQuerier replaces the primary pile data source and clears the
// commons fallback source. Callers that want fallback behavior in tests must
// also call SetCommonsQuerier afterwards; otherwise the handler 404s on
// pile-misses instead of consulting a live wl-commons reader left over from
// construction.
func (s *Server) SetProfileQuerier(pq pile.RowQuerier) {
	s.pile = pq
	s.commons = nil
}

// SetCommonsQuerier replaces the wl-commons fallback data source used when
// a handle has no boot_block in the-pile (useful for testing).
func (s *Server) SetCommonsQuerier(cq pile.RowQuerier) {
	s.commons = cq
}

// SetGitHubCache replaces the rig-handle → GitHub username cache used by
// the stamp-feed profile view. Pass nil to disable cache lookups (the
// stamp feed then emits an empty github_url and the UI renders no link).
func (s *Server) SetGitHubCache(c githubcache.Cache) {
	s.ghCache = c
}

// githubHandleLookup returns a pile.GithubHandleLookup closure over the
// server's cache, or nil when no cache is configured. The closure returns
// only the GitHub username string so that internal/pile remains free of
// any dependency on internal/githubcache.
func (s *Server) githubHandleLookup() pile.GithubHandleLookup {
	if s.ghCache == nil {
		return nil
	}
	cache := s.ghCache
	return func(handle string) (string, bool) {
		entry, ok := cache.Get(handle)
		if !ok {
			return "", false
		}
		return entry.GitHub, true
	}
}

// SetScoreboard sets the scoreboard cache for the public scoreboard endpoint.
func (s *Server) SetScoreboard(sc *CachedEndpoint) {
	s.scoreboard = sc
}

// SetScoreboardDetail sets the scoreboard detail cache.
func (s *Server) SetScoreboardDetail(ce *CachedEndpoint) {
	s.scoreboardDetail = ce
}

// SetScoreboardDump sets the scoreboard dump cache.
func (s *Server) SetScoreboardDump(ce *CachedEndpoint) {
	s.scoreboardDump = ce
}

// SetPublicClient sets an anonymous SDK client for unauthenticated public reads.
func (s *Server) SetPublicClient(c *sdk.Client) {
	s.publicClient = c
}

// SetEnvironment sets the environment string surfaced to browser runtime config.
func (s *Server) SetEnvironment(environment string) {
	s.environment = environment
}

// SetMutationInvalidator registers a callback that runs after successful
// mutations invalidate API read caches. Hosted mode uses this to evict
// resolver-owned caches that live beneath the HTTP layer.
func (s *Server) SetMutationInvalidator(fn func(context.Context)) {
	s.mutationInvalidator = fn
}

// ScoreboardHandler returns an http.HandlerFunc for the scoreboard endpoint.
func (s *Server) ScoreboardHandler() http.HandlerFunc {
	return s.handleScoreboard
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
