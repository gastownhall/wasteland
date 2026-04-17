// Command harness runs an in-process wl backend with a seeded DoltHub
// double so that browser-based e2e tests can drive the web UI against a
// deterministic, auth-free server.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/gastownhall/wasteland/internal/commons"
)

const (
	defaultAddr = "127.0.0.1:8999"
	upstreamID  = "e2e/wl-commons"
)

type wantedItem struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Project     string   `json:"project,omitempty"`
	Type        string   `json:"type,omitempty"`
	Priority    int      `json:"priority"`
	Tags        []string `json:"tags,omitempty"`
	PostedBy    string   `json:"posted_by,omitempty"`
	ClaimedBy   string   `json:"claimed_by,omitempty"`
	Status      string   `json:"status"`
	EffortLevel string   `json:"effort_level"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

type completionRecord struct {
	ID          string `json:"id"`
	WantedID    string `json:"wanted_id"`
	CompletedBy string `json:"completed_by"`
	Evidence    string `json:"evidence,omitempty"`
	StampID     string `json:"stamp_id,omitempty"`
	ValidatedBy string `json:"validated_by,omitempty"`
}

type stampRecord struct {
	ID          string   `json:"id"`
	Author      string   `json:"author"`
	Subject     string   `json:"subject"`
	Quality     int      `json:"quality"`
	Reliability int      `json:"reliability"`
	Severity    string   `json:"severity"`
	ContextID   string   `json:"context_id,omitempty"`
	ContextType string   `json:"context_type,omitempty"`
	SkillTags   []string `json:"skill_tags,omitempty"`
	Message     string   `json:"message,omitempty"`
}

type pendingSubmission struct {
	RigHandle   string   `json:"rig_handle"`
	Status      string   `json:"status"`
	ClaimedBy   string   `json:"claimed_by,omitempty"`
	Branch      string   `json:"branch,omitempty"`
	BranchURL   string   `json:"branch_url,omitempty"`
	PRURL       string   `json:"pr_url,omitempty"`
	CompletedBy string   `json:"completed_by,omitempty"`
	Evidence    string   `json:"evidence,omitempty"`
	SkillTags   []string `json:"skill_tags,omitempty"`
}

type requestLog struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Impersonate string `json:"impersonate,omitempty"`
	Body        string `json:"body,omitempty"`
}

type stateSnapshot struct {
	Requests    []requestLog                   `json:"requests"`
	Items       map[string]wantedItem          `json:"items"`
	Completions map[string]completionRecord    `json:"completions"`
	Stamps      map[string]stampRecord         `json:"stamps"`
	Pending     map[string][]pendingSubmission `json:"pending"`
}

type appState struct {
	mu             sync.Mutex
	nextWantedID   int
	nextCompletion int
	nextStamp      int
	items          map[string]*wantedItem
	completions    map[string]*completionRecord
	stamps         map[string]*stampRecord
	pending        map[string]map[string]*pendingSubmission
	requests       []requestLog
}

type postInput struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	EffortLevel string   `json:"effort_level"`
	Tags        []string `json:"tags"`
}

type doneInput struct {
	Evidence string `json:"evidence"`
}

type acceptUpstreamInput struct {
	RigHandle   string   `json:"rig_handle"`
	Quality     int      `json:"quality"`
	Reliability int      `json:"reliability"`
	Severity    string   `json:"severity"`
	SkillTags   []string `json:"skill_tags"`
	Message     string   `json:"message"`
}

func main() {
	addr := flag.String("addr", defaultAddr, "listen address")
	flag.Parse()

	state := newAppState()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runtime-config", state.handleRuntimeConfig)
	mux.HandleFunc("GET /api/bootstrap", state.handleBootstrap)
	mux.HandleFunc("GET /api/wanted", state.handleBrowse)
	mux.HandleFunc("POST /api/wanted", state.handlePost)
	mux.HandleFunc("GET /api/wanted/{id}", state.handleDetail)
	mux.HandleFunc("POST /api/wanted/{id}/claim", state.handleClaim)
	mux.HandleFunc("POST /api/wanted/{id}/done", state.handleDone)
	mux.HandleFunc("POST /api/wanted/{id}/accept-upstream", state.handleAcceptUpstream)
	mux.HandleFunc("GET /api/leaderboard", state.handleLeaderboard)
	mux.HandleFunc("GET /api/scoreboard", state.handleScoreboard)
	mux.HandleFunc("GET /__test/state", state.handleTestState)
	mux.HandleFunc("POST /__test/reset", state.handleReset)

	server := &http.Server{
		Addr:    *addr,
		Handler: loggingMiddleware(mux),
	}

	log.Printf("e2e harness listening on %s", *addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("harness server failed: %v", err)
		os.Exit(1)
	}
}

func newAppState() *appState {
	state := &appState{}
	state.resetLocked()
	return state
}

func (s *appState) resetLocked() {
	s.nextWantedID = 1
	s.nextCompletion = 1
	s.nextStamp = 1
	s.items = make(map[string]*wantedItem)
	s.completions = make(map[string]*completionRecord)
	s.stamps = make(map[string]*stampRecord)
	s.pending = make(map[string]map[string]*pendingSubmission)
	s.requests = nil
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

func (s *appState) currentActor(r *http.Request) string {
	if actor := strings.TrimSpace(r.Header.Get("X-Impersonate")); actor != "" {
		return actor
	}
	return "alice"
}

func (s *appState) recordRequest(r *http.Request, body []byte) {
	s.requests = append(s.requests, requestLog{
		Method:      r.Method,
		Path:        r.URL.Path,
		Impersonate: strings.TrimSpace(r.Header.Get("X-Impersonate")),
		Body:        string(body),
	})
}

func (s *appState) handleRuntimeConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"environment":                "staging",
		"browser_tracing_enabled":    false,
		"browser_trace_endpoint":     "",
		"browser_trace_sample_ratio": 0,
	})
}

func (s *appState) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	actor := s.currentActor(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated":   true,
		"connected":       true,
		"rig_handle":      actor,
		"environment":     "staging",
		"active_upstream": upstreamID,
		"mode":            "pr",
		"wastelands": []map[string]any{
			{
				"upstream": upstreamID,
				"fork_org": actor,
				"fork_db":  "wl-commons",
				"mode":     "pr",
				"signing":  false,
			},
		},
	})
}

func (s *appState) handleBrowse(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, nil)

	items := make([]map[string]any, 0, len(s.items))
	for _, item := range orderedItems(s.items) {
		pending := s.pending[item.ID]
		pendingItems := make([]map[string]any, 0, len(pending))
		for _, submission := range orderedPending(pending) {
			pendingItems = append(pendingItems, map[string]any{
				"rig_handle": submission.RigHandle,
				"status":     submission.Status,
				"branch_url": submission.BranchURL,
				"pr_url":     submission.PRURL,
			})
		}
		claimedBy := item.ClaimedBy
		switch len(pendingItems) {
		case 0:
		case 1:
			if pendingItems[0]["status"] != "open" {
				claimedBy = fmt.Sprintf("%s (pending)", pendingItems[0]["rig_handle"])
			}
		default:
			claimedBy = "Multiple (pending)"
		}
		items = append(items, map[string]any{
			"id":            item.ID,
			"title":         item.Title,
			"description":   item.Description,
			"project":       item.Project,
			"type":          item.Type,
			"priority":      item.Priority,
			"posted_by":     item.PostedBy,
			"claimed_by":    claimedBy,
			"status":        item.Status,
			"effort_level":  item.EffortLevel,
			"pending_count": len(pendingItems),
			"pending_items": pendingItems,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *appState) handlePost(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}

	var input postInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, body)

	actor := s.currentActor(r)
	id := fmt.Sprintf("w-e2e-%03d", s.nextWantedID)
	s.nextWantedID++

	item := &wantedItem{
		ID:          id,
		Title:       strings.TrimSpace(input.Title),
		Description: strings.TrimSpace(input.Description),
		Project:     strings.TrimSpace(input.Project),
		Type:        defaultString(input.Type, "feature"),
		Priority:    defaultPriority(input.Priority),
		Tags:        slices.Clone(input.Tags),
		PostedBy:    actor,
		Status:      "open",
		EffortLevel: defaultString(input.EffortLevel, "medium"),
		CreatedAt:   "2026-04-08T00:00:00Z",
		UpdatedAt:   "2026-04-08T00:00:00Z",
	}
	s.items[id] = item

	writeJSON(w, http.StatusOK, map[string]any{
		"detail": s.buildDetailLocked(actor, id),
	})
}

func (s *appState) handleDetail(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, nil)

	id := r.PathValue("id")
	if _, ok := s.items[id]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	writeJSON(w, http.StatusOK, s.buildDetailLocked(s.currentActor(r), id))
}

func (s *appState) handleClaim(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, nil)

	id := r.PathValue("id")
	item, ok := s.items[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	actor := s.currentActor(r)
	submission := s.ensurePendingLocked(id, actor)
	submission.Status = "claimed"
	submission.ClaimedBy = actor
	submission.CompletedBy = ""
	submission.Evidence = ""
	item.UpdatedAt = "2026-04-08T00:01:00Z"

	writeJSON(w, http.StatusOK, map[string]any{
		"detail": s.buildDetailLocked(actor, id),
	})
}

func (s *appState) handleDone(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}

	var input doneInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, body)

	id := r.PathValue("id")
	if _, ok := s.items[id]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	actor := s.currentActor(r)
	submission := s.ensurePendingLocked(id, actor)
	submission.Status = "in_review"
	submission.ClaimedBy = actor
	submission.CompletedBy = actor
	submission.Evidence = strings.TrimSpace(input.Evidence)

	writeJSON(w, http.StatusOK, map[string]any{
		"detail": s.buildDetailLocked(actor, id),
	})
}

func (s *appState) handleAcceptUpstream(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}

	var input acceptUpstreamInput
	if err := json.Unmarshal(body, &input); err != nil {
		writeError(w, err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, body)

	id := r.PathValue("id")
	item, ok := s.items[id]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	actor := s.currentActor(r)
	if item.PostedBy != actor && !commons.Admins[actor] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	pendingByRig := s.pending[id]
	if pendingByRig == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending submission"})
		return
	}
	submission := pendingByRig[input.RigHandle]
	if submission == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no pending submission"})
		return
	}

	reliability := input.Reliability
	if reliability == 0 {
		reliability = input.Quality
	}

	completionID := fmt.Sprintf("c-e2e-%03d", s.nextCompletion)
	s.nextCompletion++
	stampID := fmt.Sprintf("s-e2e-%03d", s.nextStamp)
	s.nextStamp++

	item.Status = "completed"
	item.ClaimedBy = submission.CompletedBy
	item.UpdatedAt = "2026-04-08T00:02:00Z"

	s.completions[id] = &completionRecord{
		ID:          completionID,
		WantedID:    id,
		CompletedBy: submission.CompletedBy,
		Evidence:    submission.Evidence,
		StampID:     stampID,
		ValidatedBy: actor,
	}
	s.stamps[stampID] = &stampRecord{
		ID:          stampID,
		Author:      actor,
		Subject:     submission.CompletedBy,
		Quality:     input.Quality,
		Reliability: reliability,
		Severity:    defaultString(input.Severity, "leaf"),
		ContextID:   completionID,
		ContextType: "completion",
		SkillTags:   slices.Clone(input.SkillTags),
		Message:     strings.TrimSpace(input.Message),
	}
	delete(s.pending, id)

	writeJSON(w, http.StatusOK, map[string]any{
		"detail": s.buildDetailLocked(actor, id),
	})
}

func (s *appState) handleLeaderboard(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, nil)

	entries := make([]map[string]any, 0)
	for _, entry := range s.leaderboardAggregatesLocked() {
		entries = append(entries, map[string]any{
			"rig_handle":      entry.Handle,
			"completions":     entry.completions,
			"avg_quality":     entry.avgQuality(),
			"avg_reliability": entry.avgReliability(),
			"avg_creativity":  0,
			"top_skills":      []string{},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *appState) handleScoreboard(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recordRequest(r, nil)

	entries := make([]map[string]any, 0)
	for _, entry := range s.leaderboardAggregatesLocked() {
		entries = append(entries, map[string]any{
			"rig_handle":      entry.Handle,
			"display_name":    entry.Handle,
			"trust_tier":      trustTier(entry.completions),
			"stamp_count":     entry.completions,
			"weighted_score":  (entry.totalQuality * 100) + (entry.totalReliab * 10),
			"unique_towns":    1,
			"completions":     entry.completions,
			"avg_quality":     entry.avgQuality(),
			"avg_reliability": entry.avgReliability(),
			"avg_creativity":  0,
			"top_skills":      []string{},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries":    entries,
		"updated_at": "2026-04-08T00:02:00Z",
	})
}

func (s *appState) handleReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.resetLocked()
	writeJSON(w, http.StatusOK, map[string]string{"status": "reset"})
}

func (s *appState) handleTestState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := make(map[string][]pendingSubmission, len(s.pending))
	for wantedID, byRig := range s.pending {
		rows := make([]pendingSubmission, 0, len(byRig))
		for _, submission := range orderedPending(byRig) {
			rows = append(rows, *clonePending(submission))
		}
		pending[wantedID] = rows
	}

	items := make(map[string]wantedItem, len(s.items))
	for id, item := range s.items {
		items[id] = *cloneItem(item)
	}

	completions := make(map[string]completionRecord, len(s.completions))
	for id, completion := range s.completions {
		completions[id] = *cloneCompletion(completion)
	}

	stamps := make(map[string]stampRecord, len(s.stamps))
	for id, stamp := range s.stamps {
		stamps[id] = *cloneStamp(stamp)
	}

	writeJSON(w, http.StatusOK, stateSnapshot{
		Requests:    slices.Clone(s.requests),
		Items:       items,
		Completions: completions,
		Stamps:      stamps,
		Pending:     pending,
	})
}

func (s *appState) ensurePendingLocked(wantedID, actor string) *pendingSubmission {
	byRig := s.pending[wantedID]
	if byRig == nil {
		byRig = make(map[string]*pendingSubmission)
		s.pending[wantedID] = byRig
	}
	if byRig[actor] == nil {
		byRig[actor] = &pendingSubmission{
			RigHandle: actor,
			Branch:    fmt.Sprintf("wl/%s/%s", actor, wantedID),
			BranchURL: fmt.Sprintf("https://example.test/branches/wl/%s/%s", actor, wantedID),
			PRURL:     fmt.Sprintf("https://example.test/prs/%s/%s", actor, wantedID),
		}
	}
	return byRig[actor]
}

func (s *appState) buildDetailLocked(actor, wantedID string) map[string]any {
	item := cloneItem(s.items[wantedID])
	ownPending := clonePending(s.pending[wantedID][actor])
	var upstreamPRs []map[string]any
	for _, submission := range orderedPending(s.pending[wantedID]) {
		if submission.RigHandle == actor {
			continue
		}
		upstreamPRs = append(upstreamPRs, map[string]any{
			"is_upstream":  true,
			"rig_handle":   submission.RigHandle,
			"status":       submission.Status,
			"claimed_by":   submission.ClaimedBy,
			"branch":       submission.Branch,
			"branch_url":   submission.BranchURL,
			"pr_url":       submission.PRURL,
			"completed_by": submission.CompletedBy,
			"evidence":     submission.Evidence,
		})
	}

	mainStatus := ""
	branch := ""
	delta := ""
	if ownPending != nil {
		mainStatus = item.Status
		item.Status = ownPending.Status
		item.ClaimedBy = ownPending.ClaimedBy
		branch = ownPending.Branch
		delta = commons.ComputeDelta(mainStatus, ownPending.Status, true)
	}

	actions := availableActions(actor, item, len(upstreamPRs) > 0, pendingHasInReview(upstreamPRs))
	branchActions := []string{}
	if ownPending != nil {
		branchActions = []string{"submit_pr", "discard"}
	}

	detail := map[string]any{
		"item":           item,
		"main_status":    mainStatus,
		"branch":         branch,
		"branch_url":     branchURL(branch),
		"delta":          delta,
		"actions":        actions,
		"branch_actions": branchActions,
		"mode":           "pr",
		"upstream_prs":   upstreamPRs,
	}

	if ownPending == nil {
		if completion := s.completions[wantedID]; completion != nil {
			detail["completion"] = cloneCompletion(completion)
			if stamp := s.stamps[completion.StampID]; stamp != nil {
				detail["stamp"] = cloneStamp(stamp)
			}
		}
	}

	if branch == "" {
		detail["branch_url"] = ""
	}

	return detail
}

type leaderboardAggregate struct {
	Handle       string
	completions  int
	totalQuality int
	totalReliab  int
}

func (entry *leaderboardAggregate) avgQuality() float64 {
	if entry == nil || entry.completions == 0 {
		return 0
	}
	return float64(entry.totalQuality) / float64(entry.completions)
}

func (entry *leaderboardAggregate) avgReliability() float64 {
	if entry == nil || entry.completions == 0 {
		return 0
	}
	return float64(entry.totalReliab) / float64(entry.completions)
}

func (s *appState) leaderboardAggregatesLocked() []*leaderboardAggregate {
	agg := map[string]*leaderboardAggregate{}
	for _, completion := range s.completions {
		stamp := s.stamps[completion.StampID]
		if stamp == nil {
			continue
		}
		entry := agg[completion.CompletedBy]
		if entry == nil {
			entry = &leaderboardAggregate{Handle: completion.CompletedBy}
			agg[completion.CompletedBy] = entry
		}
		entry.completions++
		entry.totalQuality += stamp.Quality
		entry.totalReliab += stamp.Reliability
	}

	handles := make([]string, 0, len(agg))
	for handle := range agg {
		handles = append(handles, handle)
	}
	slices.SortStableFunc(handles, func(a, b string) int {
		left := agg[a]
		right := agg[b]
		switch {
		case left.completions != right.completions:
			return right.completions - left.completions
		case left.totalQuality != right.totalQuality:
			return right.totalQuality - left.totalQuality
		default:
			return strings.Compare(a, b)
		}
	})

	rows := make([]*leaderboardAggregate, 0, len(handles))
	for _, handle := range handles {
		rows = append(rows, agg[handle])
	}
	return rows
}

func availableActions(actor string, item *wantedItem, hasPending, pendingInReview bool) []string {
	commonsItem := &commons.WantedItem{
		ID:          item.ID,
		Title:       item.Title,
		PostedBy:    item.PostedBy,
		ClaimedBy:   item.ClaimedBy,
		Status:      item.Status,
		EffortLevel: item.EffortLevel,
	}

	actions := make([]string, 0, 6)
	for _, transition := range commons.AvailableTransitions(commonsItem, actor) {
		actions = append(actions, commons.TransitionName(transition))
	}

	if (item.PostedBy == actor || commons.Admins[actor]) && hasPending {
		actions = appendUnique(actions, "reject")
		if pendingInReview {
			actions = appendUnique(actions, "accept", "close")
		}
	}

	return actions
}

func appendUnique(items []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(items, value) {
			items = append(items, value)
		}
	}
	return items
}

func pendingHasInReview(rows []map[string]any) bool {
	for _, row := range rows {
		if row["status"] == "in_review" {
			return true
		}
	}
	return false
}

func orderedItems(items map[string]*wantedItem) []*wantedItem {
	ids := make([]string, 0, len(items))
	for id := range items {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	rows := make([]*wantedItem, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, items[id])
	}
	return rows
}

func orderedPending(byRig map[string]*pendingSubmission) []*pendingSubmission {
	if len(byRig) == 0 {
		return nil
	}
	rigs := make([]string, 0, len(byRig))
	for rig := range byRig {
		rigs = append(rigs, rig)
	}
	slices.Sort(rigs)

	rows := make([]*pendingSubmission, 0, len(rigs))
	for _, rig := range rigs {
		rows = append(rows, byRig[rig])
	}
	return rows
}

func branchURL(branch string) string {
	if branch == "" {
		return ""
	}
	return fmt.Sprintf("https://example.test/branches/%s", branch)
}

func trustTier(completions int) string {
	switch {
	case completions >= 10:
		return "gold"
	case completions >= 3:
		return "silver"
	case completions >= 1:
		return "bronze"
	default:
		return "seed"
	}
}

func cloneItem(item *wantedItem) *wantedItem {
	if item == nil {
		return nil
	}
	dup := *item
	dup.Tags = slices.Clone(item.Tags)
	return &dup
}

func cloneCompletion(completion *completionRecord) *completionRecord {
	if completion == nil {
		return nil
	}
	dup := *completion
	return &dup
}

func cloneStamp(stamp *stampRecord) *stampRecord {
	if stamp == nil {
		return nil
	}
	dup := *stamp
	dup.SkillTags = slices.Clone(stamp.SkillTags)
	return &dup
}

func clonePending(submission *pendingSubmission) *pendingSubmission {
	if submission == nil {
		return nil
	}
	dup := *submission
	dup.SkillTags = slices.Clone(submission.SkillTags)
	return &dup
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func defaultPriority(priority int) int {
	if priority < 0 {
		return 0
	}
	if priority == 0 {
		return 2
	}
	return priority
}

func readBody(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return ioReadAllLimit(r.Body, 1<<20)
}

func ioReadAllLimit(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("request body too large")
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}
