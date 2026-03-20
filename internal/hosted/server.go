package hosted

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gastownhall/wasteland/internal/api"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/getsentry/sentry-go"
)

// Server provides hosted-mode handler composition.
type Server struct {
	resolver      *WorkspaceResolver
	sessions      *SessionStore
	nango         *NangoClient
	sessionSecret string
	forkRegistrar ForkRegistrar
	environment   string // "staging", "production", or "" (unset)
}

// NewServer creates a hosted Server.
func NewServer(resolver *WorkspaceResolver, sessions *SessionStore, nango *NangoClient, sessionSecret, environment string) *Server {
	return &Server{
		resolver:      resolver,
		sessions:      sessions,
		nango:         nango,
		sessionSecret: sessionSecret,
		forkRegistrar: &DoltHubForkRegistrar{},
		environment:   environment,
	}
}

// Handler composes the hosted endpoints with the API server and static assets.
func (s *Server) Handler(apiServer *api.Server, assets fs.FS) http.Handler {
	mux := http.NewServeMux()

	// Rate limiters: strict for auth mutations, general for all traffic.
	authRL := api.RateLimit(api.NewRateLimiter(10, 10, time.Minute))
	generalRL := api.RateLimit(api.NewRateLimiter(120, 120, time.Minute))

	// Health check for Railway / load balancers (no rate limit).
	// Pings DoltHub SQL API to verify upstream reachability.
	mux.HandleFunc("GET /healthz", healthHandler())

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
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
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
	if err := federation.ValidateMode(req.Mode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = federation.ModePR
	}

	// Read-modify-write: preserve existing wastelands, upsert the new one.
	apiKey, meta, err := s.nango.GetConnection(req.ConnectionID)
	if err != nil || meta == nil {
		meta = &UserMetadata{RigHandle: req.RigHandle}
	}

	// Verify the DoltHub API key works before proceeding.
	if err := ProbeDoltHubToken(apiKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "DoltHub API key is invalid — please reconnect your DoltHub account. Verify you created an API token (not a credential) in DoltHub.",
		})
		return
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
		sentry.CaptureException(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save config: " + err.Error()})
		return
	}

	// Best-effort: fork the upstream and register the rig on DoltHub.
	var setupWarning string
	if s.forkRegistrar != nil {
		setupWarning = s.forkRegistrar.EnsureForkAndRegister(
			apiKey, req.Upstream, req.ForkOrg, req.ForkDB, req.RigHandle, req.DisplayName, req.Email,
		)
		if setupWarning != "" {
			slog.Warn("fork registrar warning on connect", "warning", setupWarning, "connection_id", req.ConnectionID)
		}
	}

	// Create session.
	sessionID, err := s.sessions.Create(req.ConnectionID)
	if err != nil {
		slog.Error("failed to create session", "error", err, "connection_id", req.ConnectionID)
		sentry.CaptureException(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create session: " + err.Error()})
		return
	}
	SetSessionCookie(w, sessionID, req.ConnectionID, s.sessionSecret)

	resp := map[string]string{"status": "connected"}
	if setupWarning != "" {
		resp["setup_warning"] = setupWarning
	}
	writeJSON(w, http.StatusOK, resp)
}

// authStatusResponse is the JSON response for GET /api/auth/status.
type authStatusResponse struct {
	Authenticated bool              `json:"authenticated"`
	Connected     bool              `json:"connected"`
	RigHandle     string            `json:"rig_handle,omitempty"`
	Wastelands    []WastelandConfig `json:"wastelands,omitempty"`
	Environment   string            `json:"environment,omitempty"`
}

// handleAuthStatus returns the current session state.
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	sessionID, _, ok := ReadSessionCookie(r, s.sessionSecret)
	if !ok {
		writeJSON(w, http.StatusOK, authStatusResponse{Environment: s.environment})
		return
	}

	session, ok := s.sessions.Get(sessionID)
	if !ok {
		writeJSON(w, http.StatusOK, authStatusResponse{Environment: s.environment})
		return
	}

	if session.ConnectionID == "" {
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true, Environment: s.environment})
		return
	}

	// Fetch metadata from Nango.
	_, meta, err := s.nango.GetConnection(session.ConnectionID)
	if err != nil {
		// Nango call failed -- report as not connected so frontend can re-auth.
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true, Connected: false, Environment: s.environment})
		return
	}

	if meta == nil {
		writeJSON(w, http.StatusOK, authStatusResponse{Authenticated: true, Connected: false, Environment: s.environment})
		return
	}

	writeJSON(w, http.StatusOK, authStatusResponse{
		Authenticated: true,
		Connected:     true,
		RigHandle:     meta.RigHandle,
		Wastelands:    meta.Wastelands,
		Environment:   s.environment,
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
		sentry.CaptureException(err)
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
	ForkOrg     string `json:"fork_org"`
	ForkDB      string `json:"fork_db"`
	Upstream    string `json:"upstream"`
	Mode        string `json:"mode"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
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
	if err := federation.ValidateMode(req.Mode); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	mode := req.Mode
	if mode == "" {
		mode = federation.ModePR
	}

	// Fetch current metadata, upsert the new wasteland, write back.
	apiKey, meta, err := s.nango.GetConnection(session.ConnectionID)
	if err != nil {
		slog.Error("nango: failed to read metadata", "error", err, "connection_id", session.ConnectionID)
		sentry.CaptureException(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to read metadata: " + err.Error()})
		return
	}
	if meta == nil {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "no existing metadata"})
		return
	}

	// Verify the DoltHub API key still works before joining.
	if err := ProbeDoltHubToken(apiKey); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "DoltHub API key is invalid — please reconnect your DoltHub account. Verify you created an API token (not a credential) in DoltHub.",
		})
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
		sentry.CaptureException(err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save metadata: " + err.Error()})
		return
	}

	// Best-effort: fork the upstream and register the rig on DoltHub.
	var setupWarning string
	if s.forkRegistrar != nil {
		setupWarning = s.forkRegistrar.EnsureForkAndRegister(
			apiKey, req.Upstream, req.ForkOrg, req.ForkDB, meta.RigHandle, req.DisplayName, req.Email,
		)
		if setupWarning != "" {
			slog.Warn("fork registrar warning on join", "warning", setupWarning, "connection_id", session.ConnectionID)
		}
	}

	// Bust the workspace cache so the next request picks up the new wasteland.
	s.resolver.InvalidateConnection(session.ConnectionID)

	resp := map[string]string{"status": "joined"}
	if setupWarning != "" {
		resp["setup_warning"] = setupWarning
	}
	writeJSON(w, http.StatusOK, resp)
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
		sentry.CaptureException(err)
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
		sentry.CaptureException(err)
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

// healthHandler returns an HTTP handler that checks DoltHub API reachability.
// A GET to /healthz returns 200 with {"status":"ok","dolthub":"ok"} when
// DoltHub responds, or 200 with {"status":"ok","dolthub":"unreachable"} when it
// doesn't (the process is healthy even if upstream is degraded — Railway should
// not restart us for an upstream outage).
func healthHandler() http.HandlerFunc {
	client := &http.Client{Timeout: 3 * time.Second}
	const probe = "https://www.dolthub.com/api/v1alpha1/hop/wl-commons/main?q=SELECT%201"

	return func(w http.ResponseWriter, _ *http.Request) {
		dolthub := "ok"
		resp, err := client.Get(probe)
		if err != nil {
			dolthub = "unreachable"
			slog.Warn("healthz: DoltHub probe failed", "error", err)
		} else {
			_ = resp.Body.Close()
			if resp.StatusCode >= 500 {
				dolthub = "degraded"
				slog.Warn("healthz: DoltHub probe returned error", "status", resp.StatusCode)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"dolthub": dolthub,
		})
	}
}

// ProbeDoltHubToken verifies a DoltHub API key by running a lightweight query.
// Returns nil if the key is valid (or empty), error if DoltHub rejects it.
// Var so tests can override.
var ProbeDoltHubToken = func(apiKey string) error {
	if apiKey == "" {
		return nil
	}
	req, err := http.NewRequest("GET",
		"https://www.dolthub.com/api/v1alpha1/hop/wl-commons/main?q=SELECT%201", nil)
	if err != nil {
		return nil // don't block connect for request construction errors
	}
	req.Header.Set("authorization", "token "+apiKey)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil // network error — don't block connect for transient issues
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	if resp.StatusCode == http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		if strings.Contains(string(body), "invalid authorization") {
			return fmt.Errorf("DoltHub rejected the API key")
		}
	}
	return nil
}

// writeJSON writes a JSON response (duplicated here to avoid circular import
// with the api package, which provides the canonical version).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
