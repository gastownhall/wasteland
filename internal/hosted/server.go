package hosted

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/julianknutsen/wasteland/internal/api"
	"github.com/julianknutsen/wasteland/internal/sdk"
)

// Server provides hosted-mode handler composition.
type Server struct {
	resolver      *WorkspaceResolver
	sessions      *SessionStore
	nango         *NangoClient
	sessionSecret string
}

// NewServer creates a hosted Server.
func NewServer(resolver *WorkspaceResolver, sessions *SessionStore, nango *NangoClient, sessionSecret string) *Server {
	return &Server{
		resolver:      resolver,
		sessions:      sessions,
		nango:         nango,
		sessionSecret: sessionSecret,
	}
}

// Handler composes the hosted endpoints with the API server and static assets.
func (s *Server) Handler(apiServer *api.Server, assets fs.FS) http.Handler {
	mux := http.NewServeMux()

	// Rate limiters: strict for auth mutations, general for all traffic.
	authRL := api.RateLimit(api.NewRateLimiter(10, 10, time.Minute))
	generalRL := api.RateLimit(api.NewRateLimiter(120, 120, time.Minute))

	// Health check for Railway / load balancers (no rate limit).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Auth endpoints (no auth middleware required, strict rate limit).
	mux.Handle("POST /api/auth/connect", authRL(http.HandlerFunc(s.handleConnect)))
	mux.Handle("GET /api/auth/status", authRL(http.HandlerFunc(s.handleAuthStatus)))
	mux.Handle("POST /api/auth/logout", authRL(http.HandlerFunc(s.handleLogout)))
	mux.Handle("POST /api/auth/connect-session", authRL(http.HandlerFunc(s.handleConnectSession)))
	mux.Handle("POST /api/auth/join", authRL(http.HandlerFunc(s.handleJoin)))
	mux.Handle("DELETE /api/auth/wastelands/{upstream...}", authRL(http.HandlerFunc(s.handleLeaveWasteland)))

	// Public scoreboard endpoint (no auth, bypasses middleware).
	mux.HandleFunc("GET /api/scoreboard", apiServer.ScoreboardHandler())
	mux.HandleFunc("OPTIONS /api/scoreboard", apiServer.ScoreboardHandler())

	// All other routes go through rate limit -> auth middleware -> SPA handler.
	mux.Handle("/", generalRL(s.AuthMiddleware(api.SPAHandler(apiServer, assets))))

	return mux
}

// connectRequest is the JSON body for POST /api/auth/connect.
type connectRequest struct {
	ConnectionID string `json:"connection_id"`
	RigHandle    string `json:"rig_handle"`
	ForkOrg      string `json:"fork_org"`
	ForkDB       string `json:"fork_db"`
	Upstream     string `json:"upstream"`
	Mode         string `json:"mode"`
}

// handleConnect is called after the frontend completes Nango auth.
// It writes the user config as Nango connection metadata, creates a session,
// and sets the session cookie.
func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.ConnectionID == "" || req.RigHandle == "" || req.ForkOrg == "" || req.ForkDB == "" || req.Upstream == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "connection_id, rig_handle, fork_org, fork_db, and upstream are required"})
		return
	}

	if err := validateConnectFields(req.RigHandle, req.ForkOrg, req.ForkDB, req.Upstream); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "pr"
	}

	// Read-modify-write: preserve existing wastelands, upsert the new one.
	_, meta, err := s.nango.GetConnection(req.ConnectionID)
	if err != nil || meta == nil {
		meta = &UserMetadata{RigHandle: req.RigHandle}
	}
	meta.RigHandle = req.RigHandle
	meta.UpsertWasteland(WastelandConfig{
		Upstream: req.Upstream,
		ForkOrg:  req.ForkOrg,
		ForkDB:   req.ForkDB,
		Mode:     mode,
	})
	if err := s.nango.SetMetadata(req.ConnectionID, meta); err != nil {
		slog.Error("nango: failed to save metadata", "error", err, "connection_id", req.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save config: " + err.Error()})
		return
	}

	// Create session.
	sessionID, err := s.sessions.Create(req.ConnectionID)
	if err != nil {
		slog.Error("failed to create session", "error", err, "connection_id", req.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session: " + err.Error()})
		return
	}
	SetSessionCookie(w, sessionID, req.ConnectionID, s.sessionSecret)

	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

// authStatusResponse is the JSON response for GET /api/auth/status.
type authStatusResponse struct {
	Authenticated bool              `json:"authenticated"`
	Connected     bool              `json:"connected"`
	RigHandle     string            `json:"rig_handle,omitempty"`
	Wastelands    []WastelandConfig `json:"wastelands,omitempty"`
}

// handleAuthStatus returns the current session state.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := ReadSessionCookie(r, s.sessionSecret)
	if !ok {
		writeJSON(w, http.StatusOK, authStatusResponse{})
		return
	}

	session, ok := s.sessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusOK, authStatusResponse{})
		return
	}

	if session.ConnectionID == "" {
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true})
		return
	}

	// Fetch metadata from Nango.
	_, meta, err := s.nango.GetConnection(session.ConnectionID)
	if err != nil {
		// Nango call failed -- report as not connected so frontend can re-auth.
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true, Connected: false})
		return
	}

	if meta == nil {
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true, Connected: false})
		return
	}

	writeJSON(w, http.StatusOK, authStatusResponse{
		Authenticated: true,
		Connected:     true,
		RigHandle:     meta.RigHandle,
		Wastelands:    meta.Wastelands,
	})
}

// handleLogout destroys the session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := ReadSessionCookie(r, s.sessionSecret)
	if ok {
		s.sessions.Delete(sessionID)
	}
	ClearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged out"})
}

// connectSessionRequest is the JSON body for POST /api/auth/connect-session.
type connectSessionRequest struct {
	EndUserID string `json:"end_user_id"`
}

// connectSessionResponse is the JSON response for POST /api/auth/connect-session.
type connectSessionResponse struct {
	Token         string `json:"token"`
	IntegrationID string `json:"integration_id"`
}

// handleConnectSession creates a Nango connect session token for the frontend SDK.
func (s *Server) handleConnectSession(w http.ResponseWriter, r *http.Request) {
	var req connectSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.EndUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "end_user_id is required"})
		return
	}

	token, err := s.nango.CreateConnectSession(req.EndUserID)
	if err != nil {
		slog.Error("nango: failed to create connect session", "error", err, "end_user_id", req.EndUserID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session: " + err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, connectSessionResponse{
		Token:         token,
		IntegrationID: s.nango.integrationID,
	})
}

// joinRequest is the JSON body for POST /api/auth/join.
type joinRequest struct {
	ForkOrg  string `json:"fork_org"`
	ForkDB   string `json:"fork_db"`
	Upstream string `json:"upstream"`
	Mode     string `json:"mode"`
}

// handleJoin adds a new wasteland to the user's metadata.
// Requires a valid session cookie (manually validated, not through middleware).
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := ReadSessionCookie(r, s.sessionSecret)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	session, ok := s.sessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
		return
	}

	if session.ConnectionID == "" {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "DoltHub not connected"})
		return
	}

	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if req.ForkOrg == "" || req.ForkDB == "" || req.Upstream == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fork_org, fork_db, and upstream are required"})
		return
	}

	if err := validateJoinFields(req.ForkOrg, req.ForkDB, req.Upstream); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = "pr"
	}

	// Fetch current metadata, upsert the new wasteland, write back.
	_, meta, err := s.nango.GetConnection(session.ConnectionID)
	if err != nil {
		slog.Error("nango: failed to read metadata", "error", err, "connection_id", session.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read metadata: " + err.Error()})
		return
	}
	if meta == nil {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "no existing metadata"})
		return
	}

	meta.UpsertWasteland(WastelandConfig{
		Upstream: req.Upstream,
		ForkOrg:  req.ForkOrg,
		ForkDB:   req.ForkDB,
		Mode:     mode,
	})

	if err := s.nango.SetMetadata(session.ConnectionID, meta); err != nil {
		slog.Error("nango: failed to save metadata", "error", err, "connection_id", session.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save metadata: " + err.Error()})
		return
	}

	// Bust the workspace cache so the next request picks up the new wasteland.
	s.resolver.InvalidateConnection(session.ConnectionID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}

// handleLeaveWasteland removes a wasteland from the user's metadata.
func (s *Server) handleLeaveWasteland(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := ReadSessionCookie(r, s.sessionSecret)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	session, ok := s.sessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
		return
	}

	if session.ConnectionID == "" {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "DoltHub not connected"})
		return
	}

	upstream := r.PathValue("upstream")
	if upstream == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upstream is required"})
		return
	}

	// Fetch current metadata, remove the wasteland, write back.
	_, meta, err := s.nango.GetConnection(session.ConnectionID)
	if err != nil {
		slog.Error("nango: failed to read metadata", "error", err, "connection_id", session.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read metadata: " + err.Error()})
		return
	}
	if meta == nil {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "no existing metadata"})
		return
	}

	if len(meta.Wastelands) <= 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot remove last wasteland"})
		return
	}

	if !meta.RemoveWasteland(upstream) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "upstream not found"})
		return
	}

	if err := s.nango.SetMetadata(session.ConnectionID, meta); err != nil {
		slog.Error("nango: failed to save metadata", "error", err, "connection_id", session.ConnectionID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save metadata: " + err.Error()})
		return
	}

	s.resolver.InvalidateConnection(session.ConnectionID)

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// NewClientFunc returns a ClientFunc that reads the client from request context.
// This bridges the hosted auth middleware with api.Server's ClientFunc pattern.
func NewClientFunc() api.ClientFunc {
	return func(r *http.Request) (*sdk.Client, error) {
		client, ok := ClientFromContext(r.Context())
		if !ok {
			return nil, errNotAuthenticated
		}
		return client, nil
	}
}

// NewWorkspaceFunc returns a WorkspaceFunc that reads the workspace from request context.
func NewWorkspaceFunc() api.WorkspaceFunc {
	return func(r *http.Request) (*sdk.Workspace, error) {
		ws, ok := WorkspaceFromContext(r.Context())
		if !ok {
			return nil, errNotAuthenticated
		}
		return ws, nil
	}
}

var errNotAuthenticated = &authError{"not authenticated"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

// writeJSON writes a JSON response (duplicated here to avoid circular import
// with the api package, which provides the canonical version).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
