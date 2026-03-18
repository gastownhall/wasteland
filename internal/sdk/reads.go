package sdk

import (
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
)

// PendingItem represents state from a pending upstream PR's fork branch.
type PendingItem struct {
	RigHandle   string
	Status      string
	ClaimedBy   string
	Branch      string // e.g. "wl/alice/w-001"
	BranchURL   string // web URL for the fork branch
	PRURL       string // web URL for the upstream PR
	ForkOwner   string // owner of the fork that hosts Branch
	CompletedBy string // from fork branch completions table
	Evidence    string // from fork branch completions table
}

// stateRank defines lifecycle ordering for furthest-future state overlay.
var stateRank = map[string]int{
	"open": 0, "claimed": 1, "in_review": 2, "completed": 3,
}

// BrowseResult holds the items returned by Browse along with branch metadata.
type BrowseResult struct {
	Items           []commons.WantedSummary
	PendingIDs      map[string]int           // wanted IDs with pending changes; value is the count of PRs/branches
	UpstreamPending map[string][]PendingItem // for detail view consumption
}

// DetailResult holds the full picture of a wanted item for display.
type DetailResult struct {
	Item       *commons.WantedItem
	Completion *commons.CompletionRecord
	Stamp      *commons.Stamp
	Branch     string // mutation branch name ("" if none)
	BranchURL  string // web URL for the branch ("" if none)
	MainStatus string // status on main ("" if no branch)
	PRURL      string // existing PR URL ("" if none)
	Delta      string // human-readable delta label ("" if none)
	Actions    []commons.Transition
	// BranchActions are mode-aware branch operations: "submit_pr", "apply", "discard".
	// Computed by the SDK based on mode, branch state, delta, and existing PR.
	BranchActions []string
	UpstreamPRs   []PendingItem // pending upstream PRs for this item
}

// Browse queries the wanted board with filters, applying branch overlays in PR mode.
func (c *Client) Browse(filter commons.BrowseFilter) (*BrowseResult, error) {
	items, pendingIDs, err := commons.BrowseWantedBranchAware(c.db, c.mode, c.rigHandle, filter)
	if err != nil {
		return nil, err
	}

	// In non-upstream views, merge pending PR state if the callback is set.
	var upstreamItems map[string][]PendingItem
	view := filter.View
	if view == "" {
		view = "all"
	}
	if view != "upstream" && c.ListPendingItems != nil {
		upstreamItems, err = c.ListPendingItems()
		if err == nil {
			if view == "mine" {
				upstreamItems = filterPendingItemsForRig(upstreamItems, c.rigHandle)
			}
			for id, pending := range upstreamItems {
				pendingIDs[id] += len(pending)
			}
		}
	}

	seen := make(map[string]bool, len(items))
	// Overlay furthest upstream state onto items.
	for i := range items {
		seen[items[i].ID] = true
		pending := upstreamItems[items[i].ID]
		if len(pending) == 0 {
			continue
		}
		overlayPendingClaimedBy(&items[i], pending)
	}

	for id, pending := range upstreamItems {
		if seen[id] || len(pending) == 0 {
			continue
		}
		best := bestPendingState(pending)
		if best.Branch == "" {
			continue
		}
		item, _, _, err := c.loadPendingDetail(id, best)
		if err != nil {
			continue
		}
		if !matchesPendingBrowseFilter(item, best.Status, best.ClaimedBy, filter) {
			continue
		}
		summary := commons.WantedSummary{
			ID:          item.ID,
			Title:       item.Title,
			Description: item.Description,
			Project:     item.Project,
			Type:        item.Type,
			Priority:    item.Priority,
			PostedBy:    item.PostedBy,
			ClaimedBy:   item.ClaimedBy,
			Status:      best.Status,
			EffortLevel: item.EffortLevel,
		}
		overlayPendingClaimedBy(&summary, pending)
		items = append(items, summary)
	}

	return &BrowseResult{Items: items, PendingIDs: pendingIDs, UpstreamPending: upstreamItems}, nil
}

func filterPendingItemsForRig(items map[string][]PendingItem, rigHandle string) map[string][]PendingItem {
	if len(items) == 0 {
		return items
	}
	prefix := "wl/" + rigHandle + "/"
	filtered := make(map[string][]PendingItem)
	for id, pending := range items {
		for _, p := range pending {
			if strings.HasPrefix(p.Branch, prefix) || (p.Branch == "" && (p.RigHandle == rigHandle || p.ClaimedBy == rigHandle)) {
				filtered[id] = append(filtered[id], p)
			}
		}
	}
	return filtered
}

func bestPendingState(pending []PendingItem) PendingItem {
	best := pending[0]
	for _, p := range pending[1:] {
		if stateRank[p.Status] > stateRank[best.Status] {
			best = p
		}
	}
	return best
}

func overlayPendingClaimedBy(item *commons.WantedSummary, pending []PendingItem) {
	best := bestPendingState(pending)
	if best.Status == "open" {
		return
	}

	totalCandidates := len(pending)
	if item.ClaimedBy != "" {
		totalCandidates++
	}
	switch {
	case totalCandidates > 1:
		item.ClaimedBy = "Multiple (pending)"
	case best.ClaimedBy != "":
		item.ClaimedBy = best.ClaimedBy + " (pending)"
	case best.RigHandle != "":
		item.ClaimedBy = best.RigHandle + " (pending)"
	}
}

func matchesPendingBrowseFilter(item *commons.WantedItem, status, claimedBy string, f commons.BrowseFilter) bool {
	if f.Status != "" && status != f.Status {
		return false
	}
	if f.Type != "" && item.Type != f.Type {
		return false
	}
	if f.Project != "" && item.Project != f.Project {
		return false
	}
	if f.Priority >= 0 && item.Priority != f.Priority {
		return false
	}
	if f.PostedBy != "" && item.PostedBy != f.PostedBy {
		return false
	}
	if f.ClaimedBy != "" && claimedBy != f.ClaimedBy {
		return false
	}
	if f.Search != "" && !strings.Contains(strings.ToLower(item.Title), strings.ToLower(f.Search)) {
		return false
	}
	return true
}

// Detail fetches the complete state of a wanted item including actions.
func (c *Client) Detail(wantedID string) (*DetailResult, error) {
	if c.mode == "pr" {
		return c.detailPR(wantedID)
	}
	return c.detailWildWest(wantedID)
}

func (c *Client) detailPR(wantedID string) (*DetailResult, error) {
	state, err := commons.ResolveItemState(c.db, c.rigHandle, wantedID)
	if err != nil {
		return nil, err
	}
	effective := state.Effective()
	if effective == nil {
		upstreamPRs := c.fetchUpstreamPRs(wantedID)
		if len(upstreamPRs) > 0 {
			result, err := c.detailFromPending(wantedID, upstreamPRs)
			if err == nil {
				return result, nil
			}
			return nil, err
		}
		// Fall back to main query if resolve found nothing.
		return c.detailWildWest(wantedID)
	}

	result := &DetailResult{
		Item:       effective,
		Completion: state.Completion,
		Stamp:      state.Stamp,
		Branch:     state.BranchName,
		Delta:      state.Delta(),
		Actions:    commons.AvailableTransitions(effective, c.rigHandle),
	}
	if state.Main != nil {
		result.MainStatus = state.Main.Status
	}
	if state.BranchName != "" && c.CheckPR != nil {
		result.PRURL = c.CheckPR(state.BranchName)
	}
	if state.BranchName != "" && c.BranchURL != nil {
		result.BranchURL = c.BranchURL(state.BranchName)
	}
	result.BranchActions = c.computeBranchActions(result)
	result.UpstreamPRs = c.fetchUpstreamPRs(wantedID)
	return result, nil
}

func (c *Client) loadPendingDetail(wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
	if c.LoadPendingDetail != nil {
		item, completion, stamp, err := c.LoadPendingDetail(wantedID, pending)
		if err == nil {
			return item, completion, stamp, nil
		}
	}
	return commons.QueryFullDetailAsOf(c.db, wantedID, pending.Branch)
}

func (c *Client) detailFromPending(wantedID string, pending []PendingItem) (*DetailResult, error) {
	best := bestPendingState(pending)
	if best.Branch == "" {
		return c.detailWildWest(wantedID)
	}

	item, completion, stamp, err := c.loadPendingDetail(wantedID, best)
	if err != nil {
		return nil, err
	}

	return &DetailResult{
		Item:        item,
		Completion:  completion,
		Stamp:       stamp,
		Branch:      best.Branch,
		BranchURL:   best.BranchURL,
		PRURL:       best.PRURL,
		Delta:       commons.ComputeDelta("", item.Status, true),
		UpstreamPRs: pending,
	}, nil
}

func (c *Client) detailWildWest(wantedID string) (*DetailResult, error) {
	item, completion, stamp, err := commons.QueryFullDetail(c.db, wantedID)
	if err != nil {
		return nil, err
	}
	result := &DetailResult{
		Item:       item,
		Completion: completion,
		Stamp:      stamp,
		Actions:    commons.AvailableTransitions(item, c.rigHandle),
	}
	result.UpstreamPRs = c.fetchUpstreamPRs(wantedID)
	return result, nil
}

// fetchUpstreamPRs returns pending upstream PRs for a specific item.
func (c *Client) fetchUpstreamPRs(wantedID string) []PendingItem {
	if c.ListPendingItems == nil {
		return nil
	}
	upstream, err := c.ListPendingItems()
	if err != nil {
		return nil
	}
	return upstream[wantedID]
}

// ComputeBranchActions returns the mode-aware branch operations available
// given the current mode, branch name, delta label, existing PR URL, and
// whether the item's regular actions include "delete".
//
//   - PR mode with delta and no existing PR: ["submit_pr", "discard"]
//   - PR mode with delta and existing PR: ["discard"]
//   - Wild-west mode with delta: ["apply", "discard"]
//   - No branch or no delta: []
//   - "discard" is suppressed when hasDelete is true (delete cleans up the branch)
func ComputeBranchActions(mode, branch, delta, prURL string, hasDelete bool) []string {
	if branch == "" || delta == "" {
		return nil
	}
	var actions []string
	switch mode {
	case "pr":
		if prURL == "" {
			actions = append(actions, "submit_pr")
		}
	case "wild-west":
		actions = append(actions, "apply")
	default:
		// Unknown mode — return no actions rather than offering wrong operations.
		return nil
	}
	if !hasDelete {
		actions = append(actions, "discard")
	}
	return actions
}

func (c *Client) computeBranchActions(r *DetailResult) []string {
	hasDelete := false
	for _, a := range r.Actions {
		if commons.TransitionName(a) == "delete" {
			hasDelete = true
			break
		}
	}
	return ComputeBranchActions(c.mode, r.Branch, r.Delta, r.PRURL, hasDelete)
}

// Dashboard fetches the personal dashboard for the current rig handle.
func (c *Client) Dashboard() (*commons.DashboardData, error) {
	return commons.QueryMyDashboardBranchAware(c.db, c.mode, c.rigHandle)
}

// Leaderboard returns ranked rig stats aggregated from completions and stamps.
func (c *Client) Leaderboard(limit int) ([]commons.LeaderboardEntry, error) {
	return commons.QueryLeaderboard(c.db, limit)
}
