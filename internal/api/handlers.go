package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/sdk"
)

// resolveClient extracts the sdk.Client from the request. Returns false if
// the client cannot be resolved (writes a 401 error to w in that case).
// For GET requests, falls back to the anonymous public client if available.
func (s *Server) resolveClient(w http.ResponseWriter, r *http.Request) (*sdk.Client, bool) {
	client, err := s.clientFunc(r)
	if err != nil {
		if r.Method == http.MethodGet && s.publicClient != nil {
			return s.publicClient, true
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
	key := canonicalBrowseKey(r)
	data, err := s.browseCache.GetOrFetch(key, func() ([]byte, error) {
		filter := parseQueryFilter(r)
		result, err := client.Browse(filter)
		if err != nil {
			return nil, err
		}
		return json.Marshal(toBrowseResponse(result))
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=15, stale-while-revalidate=30")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	data, err := s.detailCache.GetOrFetch(id, func() ([]byte, error) {
		result, err := client.Detail(id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(toDetailResponse(result, client.Mode()))
	})
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=15, stale-while-revalidate=30")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	data, err := client.Dashboard()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
func (s *Server) invalidateReadCaches(itemID string) {
	s.browseCache.Invalidate()
	s.detailCache.InvalidateKey(itemID)
}

// invalidateAllCaches busts both browse and detail caches entirely.
func (s *Server) invalidateAllCaches() {
	s.browseCache.Invalidate()
	s.detailCache.Invalidate()
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
