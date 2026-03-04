package api

import (
	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/sdk"
)

// --- Response types ---

// WantedSummaryJSON is the JSON representation of a browse list item.
type WantedSummaryJSON struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Project      string `json:"project,omitempty"`
	Type         string `json:"type,omitempty"`
	Priority     int    `json:"priority"`
	PostedBy     string `json:"posted_by,omitempty"`
	ClaimedBy    string `json:"claimed_by,omitempty"`
	Status       string `json:"status"`
	EffortLevel  string `json:"effort_level"`
	PendingCount int    `json:"pending_count,omitempty"`
}

// BrowseResponse is the JSON response for GET /api/wanted.
type BrowseResponse struct {
	Items []WantedSummaryJSON `json:"items"`
}

// WantedItemJSON is the JSON representation of a full wanted item.
type WantedItemJSON struct {
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

// CompletionJSON is the JSON representation of a completion record.
type CompletionJSON struct {
	ID          string `json:"id"`
	WantedID    string `json:"wanted_id"`
	CompletedBy string `json:"completed_by"`
	Evidence    string `json:"evidence,omitempty"`
	StampID     string `json:"stamp_id,omitempty"`
	ValidatedBy string `json:"validated_by,omitempty"`
}

// StampJSON is the JSON representation of a reputation stamp.
type StampJSON struct {
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

// UpstreamPRJSON is the JSON representation of a pending upstream PR.
type UpstreamPRJSON struct {
	RigHandle string `json:"rig_handle"`
	Status    string `json:"status"`
	ClaimedBy string `json:"claimed_by,omitempty"`
	Branch    string `json:"branch,omitempty"`
	BranchURL string `json:"branch_url,omitempty"`
	PRURL     string `json:"pr_url,omitempty"`
	Delta     string `json:"delta,omitempty"`
}

// DetailResponse is the JSON response for GET /api/wanted/{id}.
type DetailResponse struct {
	Item          *WantedItemJSON  `json:"item"`
	Completion    *CompletionJSON  `json:"completion,omitempty"`
	Stamp         *StampJSON       `json:"stamp,omitempty"`
	Branch        string           `json:"branch,omitempty"`
	BranchURL     string           `json:"branch_url,omitempty"`
	MainStatus    string           `json:"main_status,omitempty"`
	PRURL         string           `json:"pr_url,omitempty"`
	Delta         string           `json:"delta,omitempty"`
	Actions       []string         `json:"actions"`
	BranchActions []string         `json:"branch_actions"`
	Mode          string           `json:"mode"`
	UpstreamPRs   []UpstreamPRJSON `json:"upstream_prs,omitempty"`
}

// MutationResponse is the JSON response for mutation endpoints.
type MutationResponse struct {
	Detail *DetailResponse `json:"detail,omitempty"`
	Branch string          `json:"branch,omitempty"`
	Hint   string          `json:"hint,omitempty"`
}

// DashboardResponse is the JSON response for GET /api/dashboard.
type DashboardResponse struct {
	Claimed   []WantedSummaryJSON `json:"claimed"`
	InReview  []WantedSummaryJSON `json:"in_review"`
	Completed []WantedSummaryJSON `json:"completed"`
}

// UpstreamInfoJSON is the JSON representation of an upstream in the config response.
type UpstreamInfoJSON struct {
	Upstream string `json:"upstream"`
	ForkOrg  string `json:"fork_org"`
	ForkDB   string `json:"fork_db"`
	Mode     string `json:"mode"`
}

// ConfigResponse is the JSON response for GET /api/config.
type ConfigResponse struct {
	RigHandle string             `json:"rig_handle"`
	Mode      string             `json:"mode"`
	Hosted    bool               `json:"hosted,omitempty"`
	Connected bool               `json:"connected,omitempty"`
	Upstream  string             `json:"upstream,omitempty"`
	Upstreams []UpstreamInfoJSON `json:"upstreams,omitempty"`
}

// LeaderboardEntryJSON is the JSON representation of a leaderboard entry.
type LeaderboardEntryJSON struct {
	RigHandle     string   `json:"rig_handle"`
	Completions   int      `json:"completions"`
	AvgQuality    float64  `json:"avg_quality"`
	AvgReliab     float64  `json:"avg_reliability"`
	AvgCreativity float64  `json:"avg_creativity"`
	TopSkills     []string `json:"top_skills,omitempty"`
}

// LeaderboardResponse is the JSON response for GET /api/leaderboard.
type LeaderboardResponse struct {
	Entries []LeaderboardEntryJSON `json:"entries"`
}

// ErrorResponse is the JSON error envelope.
type ErrorResponse struct {
	Error string `json:"error"`
}

// --- Request types ---

// PostRequest is the JSON body for POST /api/wanted.
type PostRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Type        string   `json:"type"`
	Priority    int      `json:"priority"`
	EffortLevel string   `json:"effort_level"`
	Tags        []string `json:"tags"`
}

// UpdateRequest is the JSON body for PATCH /api/wanted/{id}.
type UpdateRequest struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Type        string   `json:"type"`
	Priority    *int     `json:"priority"` // pointer so 0 is distinguishable from unset
	EffortLevel string   `json:"effort_level"`
	Tags        []string `json:"tags"`
	TagsSet     bool     `json:"tags_set"`
}

// DoneRequest is the JSON body for POST /api/wanted/{id}/done.
type DoneRequest struct {
	Evidence string `json:"evidence"`
}

// AcceptRequest is the JSON body for POST /api/wanted/{id}/accept.
type AcceptRequest struct {
	Quality     int      `json:"quality"`
	Reliability int      `json:"reliability"`
	Severity    string   `json:"severity"`
	SkillTags   []string `json:"skill_tags"`
	Message     string   `json:"message"`
}

// RejectRequest is the JSON body for POST /api/wanted/{id}/reject.
type RejectRequest struct {
	Reason string `json:"reason"`
}

// SettingsRequest is the JSON body for PUT /api/settings.
type SettingsRequest struct {
	Mode    string `json:"mode"`
	Signing bool   `json:"signing"`
}

// PRResponse is the JSON response for POST /api/branches/{branch}/pr.
type PRResponse struct {
	URL string `json:"url"`
}

// DiffResponse is the JSON response for GET /api/branches/{branch}/diff.
type DiffResponse struct {
	Diff string `json:"diff"`
}

// --- Converters ---

func toWantedItemJSON(item *commons.WantedItem) *WantedItemJSON {
	if item == nil {
		return nil
	}
	return &WantedItemJSON{
		ID:          item.ID,
		Title:       item.Title,
		Description: item.Description,
		Project:     item.Project,
		Type:        item.Type,
		Priority:    item.Priority,
		Tags:        item.Tags,
		PostedBy:    item.PostedBy,
		ClaimedBy:   item.ClaimedBy,
		Status:      item.Status,
		EffortLevel: item.EffortLevel,
		CreatedAt:   item.CreatedAt,
		UpdatedAt:   item.UpdatedAt,
	}
}

func toCompletionJSON(c *commons.CompletionRecord) *CompletionJSON {
	if c == nil {
		return nil
	}
	return &CompletionJSON{
		ID:          c.ID,
		WantedID:    c.WantedID,
		CompletedBy: c.CompletedBy,
		Evidence:    c.Evidence,
		StampID:     c.StampID,
		ValidatedBy: c.ValidatedBy,
	}
}

func toStampJSON(s *commons.Stamp) *StampJSON {
	if s == nil {
		return nil
	}
	return &StampJSON{
		ID:          s.ID,
		Author:      s.Author,
		Subject:     s.Subject,
		Quality:     s.Quality,
		Reliability: s.Reliability,
		Severity:    s.Severity,
		ContextID:   s.ContextID,
		ContextType: s.ContextType,
		SkillTags:   s.SkillTags,
		Message:     s.Message,
	}
}

func toDetailResponse(d *sdk.DetailResult, mode string) *DetailResponse {
	if d == nil {
		return nil
	}
	actions := make([]string, len(d.Actions))
	for i, t := range d.Actions {
		actions[i] = commons.TransitionName(t)
	}
	var upstreamPRs []UpstreamPRJSON
	for _, p := range d.UpstreamPRs {
		delta := ""
		if p.Status != "" && d.Item != nil && p.Status != d.Item.Status {
			delta = d.Item.Status + " → " + p.Status
		}
		upstreamPRs = append(upstreamPRs, UpstreamPRJSON{
			RigHandle: p.RigHandle,
			Status:    p.Status,
			ClaimedBy: p.ClaimedBy,
			Branch:    p.Branch,
			BranchURL: p.BranchURL,
			PRURL:     p.PRURL,
			Delta:     delta,
		})
	}

	return &DetailResponse{
		Item:          toWantedItemJSON(d.Item),
		Completion:    toCompletionJSON(d.Completion),
		Stamp:         toStampJSON(d.Stamp),
		Branch:        d.Branch,
		BranchURL:     d.BranchURL,
		MainStatus:    d.MainStatus,
		PRURL:         d.PRURL,
		Delta:         d.Delta,
		Actions:       actions,
		BranchActions: d.BranchActions,
		Mode:          mode,
		UpstreamPRs:   upstreamPRs,
	}
}

func toMutationResponse(r *sdk.MutationResult, mode string) *MutationResponse {
	if r == nil {
		return nil
	}
	return &MutationResponse{
		Detail: toDetailResponse(r.Detail, mode),
		Branch: r.Branch,
		Hint:   r.Hint,
	}
}

func toSummaryJSON(s commons.WantedSummary, pendingCount int) WantedSummaryJSON {
	return WantedSummaryJSON{
		ID:           s.ID,
		Title:        s.Title,
		Description:  s.Description,
		Project:      s.Project,
		Type:         s.Type,
		Priority:     s.Priority,
		PostedBy:     s.PostedBy,
		ClaimedBy:    s.ClaimedBy,
		Status:       s.Status,
		EffortLevel:  s.EffortLevel,
		PendingCount: pendingCount,
	}
}

func toBrowseResponse(r *sdk.BrowseResult) *BrowseResponse {
	items := make([]WantedSummaryJSON, len(r.Items))
	for i, s := range r.Items {
		items[i] = toSummaryJSON(s, r.PendingIDs[s.ID])
	}
	return &BrowseResponse{Items: items}
}

func toLeaderboardResponse(entries []commons.LeaderboardEntry) *LeaderboardResponse {
	items := make([]LeaderboardEntryJSON, len(entries))
	for i, e := range entries {
		items[i] = LeaderboardEntryJSON{
			RigHandle:     e.RigHandle,
			Completions:   e.Completions,
			AvgQuality:    e.AvgQuality,
			AvgReliab:     e.AvgReliab,
			AvgCreativity: e.AvgCreativity,
			TopSkills:     e.TopSkills,
		}
	}
	return &LeaderboardResponse{Entries: items}
}

func toDashboardResponse(d *commons.DashboardData) *DashboardResponse {
	convert := func(items []commons.WantedSummary) []WantedSummaryJSON {
		result := make([]WantedSummaryJSON, len(items))
		for i, s := range items {
			result[i] = toSummaryJSON(s, 0)
		}
		return result
	}
	return &DashboardResponse{
		Claimed:   convert(d.Claimed),
		InReview:  convert(d.InReview),
		Completed: convert(d.Completed),
	}
}
