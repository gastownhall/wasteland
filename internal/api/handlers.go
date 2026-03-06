package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/getsentry/sentry-go"
)

// resolveClient extracts the sdk.Client from the request. Returns false if
// the client cannot be resolved (writes a 401 error to w in that case).
// For GET requests, falls back to the anonymous public client if available.
func (s *Server) resolveClient(w http.ResponseWriter, r *http.Request) (*sdk.Client, bool) {
	client, err := s.clientFunc(r)
	if err != nil {
		if r.Method == http.MethodGet && s.publicClient != nil {
			c := s.publicClient
			// Staging impersonation: if the user isn't authenticated but
			// is impersonating, swap the rig handle on the public client
			// so actions reflect the impersonated user's permissions.
			if impersonate := r.Header.Get("X-Impersonate"); impersonate != "" && s.hosted {
				c = c.WithRigHandle(impersonate)
			}
			return c, true
		}
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	return client, true
}

// --- Read handlers ---

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	key := client.RigHandle() + ":" + canonicalBrowseKey(r)
	data, err := s.browseCache.GetOrFetch(key, func() ([]byte, error) {
		filter := parseQueryFilter(r)
		result, err := client.Browse(filter)
		if err != nil {
			return nil, err
		}
		return json.Marshal(toBrowseResponse(result))
	})
	if err != nil {
		// Auth errors should not serve stale data — the user needs to reconnect.
		if isUpstreamAuthError(err) {
			writeError(w, http.StatusUnauthorized, "DoltHub credentials expired — please reconnect.")
			return
		}
		// Try to serve stale cache data with a warning instead of a hard error.
		stale := s.browseCache.GetStale(key)
		if stale != nil {
			slog.Warn("serving stale browse data due to upstream error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			// Inject a warning into the stale response.
			var resp BrowseResponse
			if json.Unmarshal(stale, &resp) == nil {
				resp.Warning = "Upstream database is temporarily unavailable. Showing cached data."
				if patched, merr := json.Marshal(resp); merr == nil {
					stale = patched
				}
			}
			_, _ = w.Write(stale)
			return
		}
		// No stale data — return a 503 with a clear outage message.
		msg := "Upstream database is temporarily unavailable — please try again in a moment."
		slog.Error("browse failed with no stale data", "error", err)
		sentry.CaptureException(err)
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: msg})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", s.cacheControl())
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Warn("failed to write browse response", "error", err)
	}
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	key := client.RigHandle() + ":" + id
	data, err := s.detailCache.GetOrFetch(key, func() ([]byte, error) {
		result, err := client.Detail(id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(toDetailResponse(result, client.Mode()))
	})
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		// Auth errors should not serve stale data — the user needs to reconnect.
		if isUpstreamAuthError(err) {
			writeError(w, http.StatusUnauthorized, "DoltHub credentials expired — please reconnect.")
			return
		}
		// Try stale cache before returning a hard error.
		if stale := s.detailCache.GetStale(key); stale != nil {
			slog.Warn("serving stale detail data due to upstream error", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(stale)
			return
		}
		msg := "Upstream database is temporarily unavailable — please try again in a moment."
		slog.Error("detail failed with no stale data", "error", err)
		sentry.CaptureException(err)
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Error: msg})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", s.cacheControl())
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		slog.Warn("failed to write detail response", "error", err)
	}
}

// cacheControl returns the appropriate Cache-Control header value.
// Hosted mode uses private caching since responses vary per user.
func (s *Server) cacheControl() string {
	if s.hosted {
		return "private, max-age=15, stale-while-revalidate=30"
	}
	return "public, max-age=15, stale-while-revalidate=30"
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	data, err := client.Dashboard()
	if err != nil {
		writeUpstreamError(w, err, "dashboard")
		return
	}
	writeJSON(w, http.StatusOK, toDashboardResponse(data))
}

func (s *Server) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	limit := parseIntParam(r, "limit", 20)
	entries, err := client.Leaderboard(limit)
	if err != nil {
		writeUpstreamError(w, err, "leaderboard")
		return
	}
	writeJSON(w, http.StatusOK, toLeaderboardResponse(entries))
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	resp := ConfigResponse{
		RigHandle: client.RigHandle(),
		Mode:      client.Mode(),
		Hosted:    s.hosted,
		Connected: s.hosted, // in hosted mode, reaching this handler means connected
	}

	// If workspace is available, include upstream list.
	if s.workspaceFunc != nil {
		ws, err := s.workspaceFunc(r)
		if err == nil && ws != nil {
			infos := ws.Upstreams()
			resp.Upstreams = make([]UpstreamInfoJSON, len(infos))
			for i, info := range infos {
				resp.Upstreams[i] = UpstreamInfoJSON{
					Upstream: info.Upstream,
					ForkOrg:  info.ForkOrg,
					ForkDB:   info.ForkDB,
					Mode:     info.Mode,
				}
			}
		}
	}

	// Include active upstream from header if present.
	if upstream := r.Header.Get("X-Wasteland"); upstream != "" {
		resp.Upstream = upstream
	}

	writeJSON(w, http.StatusOK, resp)
}

// canonicalBrowseKey produces a stable cache key from the known browse filter
// query params. url.Values.Encode() sorts keys alphabetically.
func canonicalBrowseKey(r *http.Request) string {
	q := r.URL.Query()
	canon := url.Values{}
	for _, k := range []string{"status", "type", "priority", "project", "search", "sort", "limit", "view", "long"} {
		if v := q.Get(k); v != "" {
			canon.Set(k, v)
		}
	}
	return canon.Encode()
}

// invalidateReadCaches busts browse and detail caches after a mutation.
// Detail cache keys are prefixed with RigHandle (e.g. "rig:itemID"), so
// we invalidate the entire detail cache to cover all user-specific entries.
func (s *Server) invalidateReadCaches(_ string) {
	s.browseCache.Invalidate()
	s.detailCache.Invalidate()
}

// invalidateAllCaches busts both browse and detail caches entirely.
func (s *Server) invalidateAllCaches() {
	s.browseCache.Invalidate()
	s.detailCache.Invalidate()
}

// isUpstreamAuthError returns true if the error is a DoltHub authentication
// failure (expired or invalid API key).
func isUpstreamAuthError(err error) bool {
	return strings.Contains(err.Error(), "invalid authorization")
}

// writeUpstreamError classifies DoltHub errors and writes an appropriate response:
//   - "invalid authorization" → 401 (triggers frontend re-auth)
//   - other upstream errors → 503 with sanitized message + Sentry capture
func writeUpstreamError(w http.ResponseWriter, err error, label string) {
	if isUpstreamAuthError(err) {
		writeError(w, http.StatusUnauthorized, "DoltHub credentials expired — please reconnect.")
		return
	}
	slog.Error(label+" failed", "error", err)
	sentry.CaptureException(err)
	writeError(w, http.StatusServiceUnavailable,
		"Upstream database is temporarily unavailable — please try again in a moment.")
}

// writeMutationError writes a 409 for ConflictError, 400 for everything else.
func writeMutationError(w http.ResponseWriter, err error) {
	var conflict *commons.ConflictError
	if errors.As(err, &conflict) {
		writeError(w, http.StatusConflict, conflict.Message)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// --- Mutation handlers ---

func (s *Server) handlePost(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	var req PostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	result, err := client.Post(sdk.PostInput{
		Title:       req.Title,
		Description: req.Description,
		Project:     req.Project,
		Type:        req.Type,
		Priority:    req.Priority,
		EffortLevel: req.EffortLevel,
		Tags:        req.Tags,
	})
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.browseCache.Invalidate()
	writeJSON(w, http.StatusCreated, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req UpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	fields := &commons.WantedUpdate{
		Title:       req.Title,
		Description: req.Description,
		Project:     req.Project,
		Type:        req.Type,
		Priority:    -1,
		EffortLevel: req.EffortLevel,
		Tags:        req.Tags,
		TagsSet:     req.TagsSet,
	}
	if req.Priority != nil {
		fields.Priority = *req.Priority
	}
	result, err := client.Update(id, fields)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := client.Delete(id)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := client.Claim(id)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleUnclaim(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := client.Unclaim(id)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req DoneRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Evidence == "" {
		writeError(w, http.StatusBadRequest, "evidence is required")
		return
	}
	result, err := client.Done(id, req.Evidence)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleAccept(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req AcceptRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := client.Accept(id, sdk.AcceptInput{
		Quality:     req.Quality,
		Reliability: req.Reliability,
		Severity:    req.Severity,
		SkillTags:   req.SkillTags,
		Message:     req.Message,
	})
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleAcceptUpstream(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req AcceptUpstreamRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.RigHandle == "" {
		writeError(w, http.StatusBadRequest, "rig_handle is required")
		return
	}
	result, err := client.AcceptUpstream(id, req.RigHandle, sdk.AcceptInput{
		Quality:     req.Quality,
		Reliability: req.Reliability,
		Severity:    req.Severity,
		SkillTags:   req.SkillTags,
		Message:     req.Message,
	})
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleRejectUpstream(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req RejectUpstreamRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.RigHandle == "" {
		writeError(w, http.StatusBadRequest, "rig_handle is required")
		return
	}
	if err := client.RejectUpstream(id, req.RigHandle); err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func (s *Server) handleCloseUpstream(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req CloseUpstreamRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.RigHandle == "" {
		writeError(w, http.StatusBadRequest, "rig_handle is required")
		return
	}
	result, err := client.CloseUpstream(id, req.RigHandle)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleReject(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	var req RejectRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	result, err := client.Reject(id, req.Reason)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

func (s *Server) handleClose(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := client.Close(id)
	if err != nil {
		writeMutationError(w, err)
		return
	}
	s.invalidateReadCaches(id)
	writeJSON(w, http.StatusOK, toMutationResponse(result, client.Mode()))
}

// --- Branch handlers ---

func (s *Server) handleApplyBranch(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	branch := r.PathValue("branch")
	if err := client.ApplyBranch(branch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateAllCaches()
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
}

func (s *Server) handleDiscardBranch(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	branch := r.PathValue("branch")
	if err := client.DiscardBranch(branch); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.invalidateAllCaches()
	writeJSON(w, http.StatusOK, map[string]string{"status": "discarded"})
}

func (s *Server) handleSubmitPR(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	branch := r.PathValue("branch")
	url, err := client.SubmitPR(branch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, PRResponse{URL: url})
}

func (s *Server) handleBranchDiff(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	branch := r.PathValue("branch")
	diff, err := client.BranchDiff(branch)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, DiffResponse{Diff: diff})
}

// --- Settings handlers ---

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	var req SettingsRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Mode != "wild-west" && req.Mode != "pr" {
		writeError(w, http.StatusBadRequest, "mode must be \"wild-west\" or \"pr\"")
		return
	}
	if err := client.SaveSettings(req.Mode, req.Signing); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	if err := client.Sync(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateAllCaches()
	writeJSON(w, http.StatusOK, map[string]string{"status": "synced"})
}
