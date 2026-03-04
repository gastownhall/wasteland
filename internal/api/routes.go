package api

// registerRoutes wires all API endpoints onto the server mux.
func (s *Server) registerRoutes() {
	// Read endpoints.
	s.mux.HandleFunc("GET /api/wanted", s.handleBrowse)
	s.mux.HandleFunc("GET /api/wanted/{id}", s.handleDetail)
	s.mux.HandleFunc("GET /api/dashboard", s.handleDashboard)
	s.mux.HandleFunc("GET /api/config", s.handleConfig)
	s.mux.HandleFunc("GET /api/leaderboard", s.handleLeaderboard)

	// Mutation endpoints.
	s.mux.HandleFunc("POST /api/wanted", s.handlePost)
	s.mux.HandleFunc("PATCH /api/wanted/{id}", s.handleUpdate)
	s.mux.HandleFunc("DELETE /api/wanted/{id}", s.handleDelete)
	s.mux.HandleFunc("POST /api/wanted/{id}/claim", s.handleClaim)
	s.mux.HandleFunc("POST /api/wanted/{id}/unclaim", s.handleUnclaim)
	s.mux.HandleFunc("POST /api/wanted/{id}/done", s.handleDone)
	s.mux.HandleFunc("POST /api/wanted/{id}/accept", s.handleAccept)
	s.mux.HandleFunc("POST /api/wanted/{id}/reject", s.handleReject)
	s.mux.HandleFunc("POST /api/wanted/{id}/close", s.handleClose)

	// Branch endpoints — action comes before the {branch...} wildcard
	// since Go's ServeMux requires the wildcard at the end of the pattern.
	s.mux.HandleFunc("POST /api/branches/apply/{branch...}", s.handleApplyBranch)
	s.mux.HandleFunc("DELETE /api/branches/{branch...}", s.handleDiscardBranch)
	s.mux.HandleFunc("POST /api/branches/pr/{branch...}", s.handleSubmitPR)
	s.mux.HandleFunc("GET /api/branches/diff/{branch...}", s.handleBranchDiff)

	// Public endpoints (read-only, no auth).
	s.mux.HandleFunc("GET /api/scoreboard", s.handleScoreboard)
	s.mux.HandleFunc("OPTIONS /api/scoreboard", s.handleScoreboard)

	// Profile endpoints (read-only, no auth).
	s.mux.HandleFunc("GET /api/profile/{handle}", s.handleProfile)
	s.mux.HandleFunc("GET /api/profile", s.handleProfileSearch)

	// Settings endpoints.
	s.mux.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	s.mux.HandleFunc("POST /api/sync", s.handleSync)
}
