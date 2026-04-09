package sdk

import (
	"context"
	"errors"
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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

var sdkTracer = otel.Tracer("github.com/gastownhall/wasteland/internal/sdk")

func allowsUpstreamPending(status string) bool {
	switch status {
	case "open", "claimed", "in_review":
		return true
	default:
		return false
	}
}

type dbContextBinder interface {
	WithContext(ctx context.Context) commons.DB
}

func bindDBContext(ctx context.Context, db commons.DB) commons.DB {
	if binder, ok := db.(dbContextBinder); ok {
		return binder.WithContext(ctx)
	}
	return db
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
	// PendingReadIncomplete reports that the primary item was loaded, but the
	// pending metadata fetch failed and the result was degraded.
	PendingReadIncomplete bool
}

// Browse queries the wanted board with filters, applying branch overlays in PR mode.
func (c *Client) Browse(filter commons.BrowseFilter) (*BrowseResult, error) {
	return c.BrowseContext(context.Background(), filter)
}

// BrowseContext queries the wanted board with request-scoped tracing.
func (c *Client) BrowseContext(ctx context.Context, filter commons.BrowseFilter) (*BrowseResult, error) {
	view := filter.View
	if view == "" {
		if c.mode == "pr" {
			view = "mine"
		} else {
			view = "all"
		}
	}

	ctx, span := sdkTracer.Start(ctx, "sdk.browse", trace.WithAttributes(
		attribute.String("mode", c.mode),
		attribute.String("view", view),
	))
	defer span.End()

	queryCtx, querySpan := sdkTracer.Start(ctx, "sdk.browse.branch_aware_query")
	items, pendingIDs, err := commons.BrowseWantedBranchAware(bindDBContext(queryCtx, c.db), c.mode, c.rigHandle, filter)
	if err != nil {
		querySpan.RecordError(err)
		querySpan.End()
		span.RecordError(err)
		return nil, err
	}
	querySpan.End()

	// In non-upstream views, merge pending PR state if the callback is set.
	var upstreamItems map[string][]PendingItem
	var visiblePendingItems map[string][]PendingItem
	if view != "upstream" && (c.ListPendingItems != nil || c.ListPendingItemsContext != nil) {
		pendingCtx, pendingSpan := sdkTracer.Start(ctx, "sdk.browse.list_pending_items")
		upstreamItems, err = c.listPendingItemsContext(pendingCtx)
		if err != nil {
			pendingSpan.RecordError(err)
			pendingSpan.End()
			if !c.allowBestEffortPendingRead(ctx, err) {
				span.RecordError(err)
				return nil, err
			}
		} else {
			pendingSpan.SetAttributes(attribute.Int("wanted_ids", len(upstreamItems)))
			visiblePendingItems = upstreamItems
			if view == "mine" {
				visiblePendingItems = filterPendingItemsForRig(upstreamItems, c.rigHandle)
			}
			pendingSpan.End()
		}
	}

	seen := make(map[string]bool, len(items))
	filteredUpstreamItems := make(map[string][]PendingItem)
	pendingVisibility := make(map[string]bool)
	pendingAllowed := func(id, fallbackStatus string) (bool, error) {
		if allowed, ok := pendingVisibility[id]; ok {
			return allowed, nil
		}
		status, found, err := commons.QueryItemStatus(bindDBContext(ctx, c.db), id, "")
		if err != nil {
			return false, err
		}
		if !found {
			status = fallbackStatus
		}
		allowed := allowsUpstreamPending(status)
		pendingVisibility[id] = allowed
		return allowed, nil
	}
	// Overlay furthest upstream state onto items.
	for i := range items {
		seen[items[i].ID] = true
		pending := upstreamItems[items[i].ID]
		if len(pending) == 0 {
			continue
		}
		allowed, err := pendingAllowed(items[i].ID, items[i].Status)
		if err != nil {
			span.RecordError(err)
			return nil, err
		}
		if !allowed {
			continue
		}
		filteredUpstreamItems[items[i].ID] = pending
		pendingIDs[items[i].ID] += len(pending)
		overlayPendingClaimedBy(&items[i], pending)
	}

	overlayCtx, overlaySpan := sdkTracer.Start(ctx, "sdk.browse.overlay_pending_branch_only")
	defer overlaySpan.End()
	for id, pending := range visiblePendingItems {
		if seen[id] || len(pending) == 0 {
			continue
		}
		best := bestPendingState(pending)
		if best.Branch == "" {
			continue
		}
		allowed, err := pendingAllowed(id, best.Status)
		if err != nil {
			overlaySpan.RecordError(err)
			span.RecordError(err)
			return nil, err
		}
		if !allowed {
			continue
		}
		item, err := c.loadPendingBrowseItemContext(overlayCtx, id, best)
		if err != nil {
			overlaySpan.RecordError(err)
			if c.allowBestEffortPendingRead(ctx, err) {
				continue
			}
			span.RecordError(err)
			return nil, err
		}
		if !matchesPendingBrowseFilter(item, best.Status, best.ClaimedBy, filter) {
			continue
		}
		filteredUpstreamItems[id] = upstreamItems[id]
		pendingIDs[id] += len(pending)
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
	overlaySpan.SetAttributes(attribute.Int("branch_only_items", len(items)-len(seen)))

	return &BrowseResult{Items: items, PendingIDs: pendingIDs, UpstreamPending: filteredUpstreamItems}, nil
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
	if f.Search != "" {
		s := strings.ToLower(f.Search)
		if strings.Contains(strings.ToLower(item.Title), s) {
			return true
		}
		if strings.Contains(strings.ToLower(item.Description), s) {
			return true
		}
		for _, tag := range item.Tags {
			if strings.Contains(strings.ToLower(tag), s) {
				return true
			}
		}
		return false
	}
	return true
}

// Detail fetches the complete state of a wanted item including actions.
func (c *Client) Detail(wantedID string) (*DetailResult, error) {
	return c.DetailContext(context.Background(), wantedID)
}

// DetailContext fetches the complete state of a wanted item with request tracing.
func (c *Client) DetailContext(ctx context.Context, wantedID string) (*DetailResult, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.detail", trace.WithAttributes(
		attribute.String("mode", c.mode),
	))
	defer span.End()
	if c.mode == "pr" {
		return c.detailPRContext(ctx, wantedID)
	}
	return c.detailWildWestContext(ctx, wantedID)
}

func (c *Client) detailPRContext(ctx context.Context, wantedID string) (*DetailResult, error) {
	resolveCtx, resolveSpan := sdkTracer.Start(ctx, "sdk.detail.resolve_item_state")
	state, err := commons.ResolveItemState(bindDBContext(resolveCtx, c.db), c.rigHandle, wantedID)
	if err != nil {
		resolveSpan.RecordError(err)
	}
	resolveSpan.End()
	if err != nil {
		return nil, err
	}
	effective := state.Effective()
	pendingReadIncomplete := false
	var upstreamPRs []PendingItem
	if shouldFetchUpstreamPRs(state, effective) {
		upstreamPRs, err = c.fetchUpstreamPRsContext(ctx, wantedID)
		if err != nil {
			trace.SpanFromContext(ctx).RecordError(err)
			// Pending-only lookups must fail closed so branch-only items do not
			// silently disappear behind incomplete pending state.
			if effective == nil {
				return nil, err
			}
			// Pending PR metadata is decorative once we already have an effective item.
			if !c.allowBestEffortPendingRead(ctx, err) {
				return nil, err
			}
			upstreamPRs = nil
			pendingReadIncomplete = effective != nil
		}
	}
	if effective == nil {
		if len(upstreamPRs) > 0 {
			result, err := c.detailFromPendingContext(ctx, wantedID, upstreamPRs)
			if err == nil {
				return result, nil
			}
			trace.SpanFromContext(ctx).RecordError(err)
			return nil, err
		}
		// Fall back to main query if resolve found nothing.
		return c.detailWildWestContext(ctx, wantedID)
	}

	result := &DetailResult{
		Item:                  effective,
		Completion:            state.Completion,
		Stamp:                 state.Stamp,
		Branch:                state.BranchName,
		Delta:                 state.Delta(),
		Actions:               commons.AvailableTransitions(effective, c.rigHandle),
		PendingReadIncomplete: pendingReadIncomplete,
	}
	if state.Main != nil {
		result.MainStatus = state.Main.Status
	}
	if state.BranchName != "" {
		result.PRURL = c.checkPRContext(ctx, state.BranchName)
	}
	if state.BranchName != "" && c.BranchURL != nil {
		result.BranchURL = c.BranchURL(state.BranchName)
	}
	result.BranchActions = c.computeBranchActions(result)
	result.UpstreamPRs = upstreamPRs
	result.Actions = c.withUpstreamSubmissionActions(result.Item, result.UpstreamPRs, result.Actions)
	return result, nil
}

func (c *Client) loadPendingBrowseItemContext(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.pending.load_item")
	defer span.End()
	if c.LoadPendingItemContext != nil {
		item, err := c.LoadPendingItemContext(ctx, wantedID, pending)
		if err == nil {
			return item, nil
		}
		span.RecordError(err)
		if isContextError(err) {
			return nil, err
		}
	} else if c.LoadPendingItem != nil {
		item, err := c.LoadPendingItem(wantedID, pending)
		if err == nil {
			return item, nil
		}
		span.RecordError(err)
	}
	return commons.QueryWantedDetailAsOf(bindDBContext(ctx, c.db), wantedID, pending.Branch)
}

func (c *Client) loadPendingDetailContext(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.pending.load_detail")
	defer span.End()
	if c.LoadPendingDetailContext != nil {
		item, completion, stamp, err := c.LoadPendingDetailContext(ctx, wantedID, pending)
		if err == nil {
			return item, completion, stamp, nil
		}
		span.RecordError(err)
		if isContextError(err) {
			return nil, nil, nil, err
		}
	} else if c.LoadPendingDetail != nil {
		item, completion, stamp, err := c.LoadPendingDetail(wantedID, pending)
		if err == nil {
			return item, completion, stamp, nil
		}
		span.RecordError(err)
	}
	return commons.QueryFullDetailAsOf(bindDBContext(ctx, c.db), wantedID, pending.Branch)
}

func (c *Client) detailFromPendingContext(ctx context.Context, wantedID string, pending []PendingItem) (*DetailResult, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.detail.pending_fallback")
	defer span.End()
	best := bestPendingState(pending)
	if best.Branch == "" {
		return c.detailWildWestContext(ctx, wantedID)
	}

	item, completion, stamp, err := c.loadPendingDetailContext(ctx, wantedID, best)
	if err != nil {
		span.RecordError(err)
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
	return c.detailWildWestContext(context.Background(), wantedID)
}

func (c *Client) detailWildWestContext(ctx context.Context, wantedID string) (*DetailResult, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.detail.query_full_detail")
	defer span.End()
	item, completion, stamp, err := commons.QueryFullDetail(bindDBContext(ctx, c.db), wantedID)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	result := &DetailResult{
		Item:       item,
		Completion: completion,
		Stamp:      stamp,
		Actions:    commons.AvailableTransitions(item, c.rigHandle),
	}
	if !allowsUpstreamPending(item.Status) {
		return result, nil
	}
	upstreamPRs, err := c.fetchUpstreamPRsContext(ctx, wantedID)
	if err != nil {
		span.RecordError(err)
		// Once we have the full item from the primary DB, upstream PR metadata is
		// non-essential decoration. Degrade to the resolved item instead of failing
		// the entire detail view on a transient pending-state read error.
		result.PendingReadIncomplete = true
		return result, nil
	}
	result.UpstreamPRs = upstreamPRs
	result.Actions = c.withUpstreamSubmissionActions(result.Item, result.UpstreamPRs, result.Actions)
	return result, nil
}

func shouldFetchUpstreamPRs(state *commons.ItemState, effective *commons.WantedItem) bool {
	if state != nil && state.Main != nil {
		return allowsUpstreamPending(state.Main.Status)
	}
	if effective != nil {
		return allowsUpstreamPending(effective.Status)
	}
	return true
}

func (c *Client) fetchUpstreamPRsContext(ctx context.Context, wantedID string) ([]PendingItem, error) {
	if c.ListPendingItems == nil && c.ListPendingItemsContext == nil {
		return nil, nil
	}
	ctx, span := sdkTracer.Start(ctx, "sdk.detail.list_pending_items")
	defer span.End()
	upstream, err := c.listPendingItemsContext(ctx)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}
	return upstream[wantedID], nil
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

func (c *Client) withUpstreamSubmissionActions(
	item *commons.WantedItem,
	upstreamPRs []PendingItem,
	actions []commons.Transition,
) []commons.Transition {
	if item == nil || len(upstreamPRs) == 0 {
		return actions
	}
	if item.PostedBy != c.rigHandle && !commons.Admins[c.rigHandle] {
		return actions
	}

	hasPendingSubmission := false
	hasReviewableSubmission := false
	for _, pending := range upstreamPRs {
		hasPendingSubmission = true
		if pending.Status == "in_review" {
			hasReviewableSubmission = true
		}
	}
	if hasReviewableSubmission {
		actions = appendTransitionIfMissing(actions, commons.TransitionAccept)
		actions = appendTransitionIfMissing(actions, commons.TransitionClose)
	}
	if hasPendingSubmission {
		actions = appendTransitionIfMissing(actions, commons.TransitionReject)
	}
	return actions
}

func appendTransitionIfMissing(actions []commons.Transition, target commons.Transition) []commons.Transition {
	for _, action := range actions {
		if action == target {
			return actions
		}
	}
	return append(actions, target)
}

// Dashboard fetches the personal dashboard for the current rig handle.
func (c *Client) Dashboard() (*commons.DashboardData, error) {
	return c.DashboardContext(context.Background())
}

// DashboardContext fetches the personal dashboard with request tracing.
func (c *Client) DashboardContext(ctx context.Context) (*commons.DashboardData, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.dashboard", trace.WithAttributes(
		attribute.String("mode", c.mode),
	))
	defer span.End()
	data, err := commons.QueryMyDashboardBranchAware(bindDBContext(ctx, c.db), c.mode, c.rigHandle)
	if err != nil {
		span.RecordError(err)
	}
	return data, err
}

// Leaderboard returns ranked rig stats aggregated from completions and stamps.
func (c *Client) Leaderboard(limit int) ([]commons.LeaderboardEntry, error) {
	return c.LeaderboardContext(context.Background(), limit)
}

// LeaderboardContext returns ranked rig stats with request tracing.
func (c *Client) LeaderboardContext(ctx context.Context, limit int) ([]commons.LeaderboardEntry, error) {
	ctx, span := sdkTracer.Start(ctx, "sdk.leaderboard")
	defer span.End()
	entries, err := commons.QueryLeaderboard(bindDBContext(ctx, c.db), limit)
	if err != nil {
		span.RecordError(err)
	}
	return entries, err
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func (c *Client) allowBestEffortPendingRead(ctx context.Context, err error) bool {
	return c.bestEffortPendingReads && err != nil && !strictPendingReads(ctx)
}

func (c *Client) listPendingItemsContext(ctx context.Context) (map[string][]PendingItem, error) {
	if c.ListPendingItemsContext != nil {
		return c.ListPendingItemsContext(ctx)
	}
	if c.ListPendingItems != nil {
		return c.ListPendingItems()
	}
	return nil, nil
}

func (c *Client) checkPRContext(ctx context.Context, branch string) string {
	if c.CheckPRContext != nil {
		return c.CheckPRContext(ctx, branch)
	}
	if c.CheckPR != nil {
		return c.CheckPR(branch)
	}
	return ""
}
