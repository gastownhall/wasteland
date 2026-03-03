package hosted

import (
	"context"
	"net/http"
	"strings"

	"github.com/julianknutsen/wasteland/internal/sdk"
)

type contextKey string

const (
	clientContextKey    contextKey = "hosted-client"
	workspaceContextKey contextKey = "hosted-workspace"
)

// ClientFromContext extracts the sdk.Client injected by auth middleware.
func ClientFromContext(ctx context.Context) (*sdk.Client, bool) {
	client, ok := ctx.Value(clientContextKey).(*sdk.Client)
	return client, ok
}

// WorkspaceFromContext extracts the sdk.Workspace injected by auth middleware.
func WorkspaceFromContext(ctx context.Context) (*sdk.Workspace, bool) {
	ws, ok := ctx.Value(workspaceContextKey).(*sdk.Workspace)
	return ws, ok
}

// AuthMiddleware protects /api/* routes (excluding /api/auth/*).
// It resolves the session cookie, looks up the Nango connection, and injects
// the per-user sdk.Workspace and active sdk.Client into the request context.
func (s *Server) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for /api/auth/* endpoints.
		if strings.HasPrefix(r.URL.Path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}

		// Non-API routes (static files, SPA) don't need auth.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		// Read and verify session cookie.
		sessionID, connectionID, ok := ReadSessionCookie(r, s.sessionSecret)
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}

		session, ok := s.sessions.Get(sessionID)
		if !ok {
			// Session not in memory — try to re-hydrate from Nango.
			if connectionID == "" {
				// Old-format cookie without connectionID — can't re-hydrate.
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
				return
			}
			// Validate the connection is still active in Nango.
			if _, _, err := s.nango.GetConnection(connectionID); err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
				return
			}
			s.sessions.Restore(sessionID, connectionID)
			session, _ = s.sessions.Get(sessionID)
		}

		if session.ConnectionID == "" {
			writeJSON(w, http.StatusPreconditionFailed, map[string]string{"error": "DoltHub not connected"})
			return
		}

		// Resolve the per-user Workspace.
		workspace, err := s.resolver.Resolve(session)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "failed to resolve workspace: " + err.Error()})
			return
		}

		// Determine active upstream from X-Wasteland header.
		upstream := r.Header.Get("X-Wasteland")
		upstreams := workspace.Upstreams()

		if upstream == "" && len(upstreams) == 1 {
			// Single-wasteland fallback for backward compat.
			upstream = upstreams[0].Upstream
		}

		if upstream == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Wasteland header required"})
			return
		}

		client, err := workspace.Client(upstream)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown upstream: " + upstream})
			return
		}

		// Inject both workspace and client into context.
		ctx := r.Context()
		ctx = context.WithValue(ctx, workspaceContextKey, workspace)
		ctx = context.WithValue(ctx, clientContextKey, client)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
