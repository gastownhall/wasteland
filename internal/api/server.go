// Package api provides the HTTP REST server for the Wasteland wanted board.
//
// It wraps sdk.Client to expose browse, detail, dashboard, mutation, and branch
// operations as JSON endpoints consumable by any HTTP client.
package api

import (
	"net/http"

	"github.com/julianknutsen/wasteland/internal/pile"
	"github.com/julianknutsen/wasteland/internal/sdk"
)

// ClientFunc resolves an sdk.Client from an HTTP request. In self-sovereign mode
// this returns a static client; in hosted mode it resolves per-user from session.
type ClientFunc func(r *http.Request) (*sdk.Client, error)

// WorkspaceFunc resolves an sdk.Workspace from an HTTP request.
type WorkspaceFunc func(r *http.Request) (*sdk.Workspace, error)

// Server is the HTTP API server wrapping an SDK client.
type Server struct {
	clientFunc    ClientFunc
	workspaceFunc WorkspaceFunc
	pile          pile.RowQuerier
	scoreboard    *ScoreboardCache
	publicClient  *sdk.Client // anonymous fallback for public reads (hosted mode)
	mux           *http.ServeMux
	hosted        bool // true when running in multi-tenant hosted mode
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
		clientFunc: fn,
		mux:        http.NewServeMux(),
		hosted:     true,
	}
	s.pile = pile.NewDefault()
	s.registerRoutes()
	return s
}

// NewHostedWorkspace creates a Server for multi-tenant hosted mode with workspace support.
func NewHostedWorkspace(clientFn ClientFunc, workspaceFn WorkspaceFunc) *Server {
	s := &Server{
		clientFunc:    clientFn,
		workspaceFunc: workspaceFn,
		mux:           http.NewServeMux(),
		hosted:        true,
	}
	s.pile = pile.NewDefault()
	s.registerRoutes()
	return s
}

// NewWithClientFunc creates a Server that resolves a client per-request.
func NewWithClientFunc(fn ClientFunc) *Server {
	s := &Server{
		clientFunc: fn,
		mux:        http.NewServeMux(),
	}
	s.pile = pile.NewDefault()
	s.registerRoutes()
	return s
}

// SetProfileQuerier replaces the profile data source (useful for testing).
func (s *Server) SetProfileQuerier(pq pile.RowQuerier) {
	s.pile = pq
}

// SetScoreboard sets the scoreboard cache for the public scoreboard endpoint.
func (s *Server) SetScoreboard(sc *ScoreboardCache) {
	s.scoreboard = sc
}

// SetPublicClient sets an anonymous SDK client for unauthenticated public reads.
func (s *Server) SetPublicClient(c *sdk.Client) {
	s.publicClient = c
}

// ScoreboardHandler returns an http.HandlerFunc for the scoreboard endpoint.
func (s *Server) ScoreboardHandler() http.HandlerFunc {
	return s.handleScoreboard
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
