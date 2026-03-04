package api

import (
	"net/http"

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
	filter := parseQueryFilter(r)
	result, err := client.Browse(filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toBrowseResponse(result))
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	result, err := client.Detail(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toDetailResponse(result, client.Mode()))
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
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
	writeJSON(w, http.StatusOK, map[string]string{"status": "synced"})
}
