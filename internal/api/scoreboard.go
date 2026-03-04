package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/julianknutsen/wasteland/internal/commons"
)

// ScoreboardResponse is the JSON response for GET /api/scoreboard.
type ScoreboardResponse struct {
	Entries   []ScoreboardEntryJSON `json:"entries"`
	UpdatedAt string                `json:"updated_at"`
}

// ScoreboardEntryJSON is the JSON representation of a scoreboard entry.
type ScoreboardEntryJSON struct {
	RigHandle      string   `json:"rig_handle"`
	DisplayName    string   `json:"display_name,omitempty"`
	TrustTier      string   `json:"trust_tier"`
	StampCount     int      `json:"stamp_count"`
	WeightedScore  int      `json:"weighted_score"`
	UniqueTowns    int      `json:"unique_towns"`
	Completions    int      `json:"completions"`
	AvgQuality     float64  `json:"avg_quality"`
	AvgReliability float64  `json:"avg_reliability"`
	TopSkills      []string `json:"top_skills,omitempty"`
}

// ScoreboardCache manages a cached, periodically refreshed scoreboard.
type ScoreboardCache struct {
	mu        sync.RWMutex
	cached    []byte // pre-serialized JSON
	updatedAt time.Time
	db        commons.DB
	interval  time.Duration
	done      chan struct{}
}

// NewScoreboardCache creates a new scoreboard cache.
func NewScoreboardCache(db commons.DB, interval time.Duration) *ScoreboardCache {
	return &ScoreboardCache{
		db:       db,
		interval: interval,
		done:     make(chan struct{}),
	}
}

// Start begins the background refresh goroutine.
func (sc *ScoreboardCache) Start() {
	go sc.run()
}

// Stop halts the background refresh goroutine.
func (sc *ScoreboardCache) Stop() {
	close(sc.done)
}

func (sc *ScoreboardCache) run() {
	// Initial load.
	sc.refresh()

	ticker := time.NewTicker(sc.interval)
	defer ticker.Stop()

	for {
		select {
		case <-sc.done:
			return
		case <-ticker.C:
			sc.refresh()
		}
	}
}

func (sc *ScoreboardCache) refresh() {
	entries, err := commons.QueryScoreboard(sc.db, 100)
	if err != nil {
		slog.Warn("scoreboard refresh failed", "error", err)
		return
	}

	resp := toScoreboardResponse(entries)
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Warn("scoreboard marshal failed", "error", err)
		return
	}

	sc.mu.Lock()
	sc.cached = data
	sc.updatedAt = time.Now().UTC()
	sc.mu.Unlock()
}

// Get returns the cached JSON. If the cache is empty, triggers a synchronous load.
func (sc *ScoreboardCache) Get() []byte {
	sc.mu.RLock()
	data := sc.cached
	sc.mu.RUnlock()

	if data != nil {
		return data
	}

	// First request: synchronous load.
	sc.refresh()

	sc.mu.RLock()
	data = sc.cached
	sc.mu.RUnlock()
	return data
}

func toScoreboardResponse(entries []commons.ScoreboardEntry) *ScoreboardResponse {
	items := make([]ScoreboardEntryJSON, len(entries))
	for i, e := range entries {
		items[i] = ScoreboardEntryJSON{
			RigHandle:      e.RigHandle,
			DisplayName:    e.DisplayName,
			TrustTier:      e.TrustTier,
			StampCount:     e.StampCount,
			WeightedScore:  e.WeightedScore,
			UniqueTowns:    e.UniqueTowns,
			Completions:    e.Completions,
			AvgQuality:     e.AvgQuality,
			AvgReliability: e.AvgReliab,
			TopSkills:      e.TopSkills,
		}
	}
	return &ScoreboardResponse{
		Entries:   items,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// handleScoreboard serves the cached scoreboard JSON with CORS headers.
func (s *Server) handleScoreboard(w http.ResponseWriter, r *http.Request) {
	// CORS headers for public access.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if s.scoreboard == nil {
		writeError(w, http.StatusServiceUnavailable, "scoreboard not configured")
		return
	}

	data := s.scoreboard.Get()
	if data == nil {
		writeError(w, http.StatusServiceUnavailable, "scoreboard data unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
