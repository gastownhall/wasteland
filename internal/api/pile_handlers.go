package api

import (
	"errors"
	"net/http"

	"github.com/gastownhall/wasteland/internal/pile"
)

const maxProfileSearchLimit = 100

// handleProfile serves GET /api/profile/{handle}
// Returns a discriminated profile response. If hop/the-pile has a boot_block
// for the handle, emits kind=character_sheet with the full developer profile.
// Otherwise falls back to kind=stamp_feed assembled from hop/wl-commons.
// 404 only when both sources are empty.
// No auth required — profile lookups are public read-only data.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	handle := r.PathValue("handle")
	if handle == "" {
		writeError(w, http.StatusBadRequest, "handle is required")
		return
	}

	if s.pile == nil {
		writeError(w, http.StatusServiceUnavailable, "profile service not configured")
		return
	}

	resp, err := pile.QueryProfileResponse(s.pile, s.commons, s.githubHandleLookup(), handle)
	// When the pile misses and commons is not wired, the caller can't
	// distinguish "truly unknown handle" from "fallback source is
	// unconfigured" — surface this as 503 instead of a misleading 404.
	if errors.Is(err, pile.ErrProfileNotFound) && s.commons == nil {
		writeError(w, http.StatusServiceUnavailable, "profile fallback source not configured")
		return
	}
	if err != nil {
		if errors.Is(err, pile.ErrProfileNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusBadGateway, "upstream profile service error")
		}
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleProfileSearch serves GET /api/profile?q=search
// Searches for profiles matching the query string.
func (s *Server) handleProfileSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	if s.pile == nil {
		writeError(w, http.StatusServiceUnavailable, "profile service not configured")
		return
	}

	limit := parseIntParam(r, "limit", 20)
	if limit > maxProfileSearchLimit {
		limit = maxProfileSearchLimit
	}
	results, err := pile.SearchProfiles(s.pile, q, limit)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream profile service error")
		return
	}
	writeJSON(w, http.StatusOK, results)
}
