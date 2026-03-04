package sdk

import (
	"github.com/julianknutsen/wasteland/internal/commons"
)

// BrowseResult holds the items returned by Browse along with branch metadata.
type BrowseResult struct {
	Items      []commons.WantedSummary
	PendingIDs map[string]int // wanted IDs with pending changes; value is the count of PRs/branches
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
}

// Browse queries the wanted board with filters, applying branch overlays in PR mode.
func (c *Client) Browse(filter commons.BrowseFilter) (*BrowseResult, error) {
	items, pendingIDs, err := commons.BrowseWantedBranchAware(c.db, c.mode, c.rigHandle, filter)
	if err != nil {
		return nil, err
	}

	// In "all" view, merge upstream PR IDs if the callback is set.
	var upstreamIDs map[string]string
	view := filter.View
	if view == "" {
		view = "all"
	}
	if view == "all" && c.ListPendingItems != nil {
		upstreamIDs, err = c.ListPendingItems()
		if err == nil {
			for id := range upstreamIDs {
				if pendingIDs[id] == 0 {
					pendingIDs[id] = 1
				}
			}
		}
	}

	// Overlay pending PR state onto items not already changed on main.
	// Without this, items show a "pending" badge but status stays "open"
	// which is contradictory — pending implies a state change happened.
	for i := range items {
		if items[i].ClaimedBy == "" && pendingIDs[items[i].ID] > 0 {
			if pendingIDs[items[i].ID] > 1 {
				items[i].ClaimedBy = "Multiple (pending)"
			} else if rig := upstreamIDs[items[i].ID]; rig != "" {
				items[i].ClaimedBy = rig + " (pending)"
			}
			// A pending claim means the item is effectively claimed, not open.
			if items[i].Status == "open" {
				items[i].Status = "claimed"
			}
		}
	}

	return &BrowseResult{Items: items, PendingIDs: pendingIDs}, nil
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
	return result, nil
}

func (c *Client) detailWildWest(wantedID string) (*DetailResult, error) {
	item, completion, stamp, err := commons.QueryFullDetail(c.db, wantedID)
	if err != nil {
		return nil, err
	}
	return &DetailResult{
		Item:       item,
		Completion: completion,
		Stamp:      stamp,
		Actions:    commons.AvailableTransitions(item, c.rigHandle),
	}, nil
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
