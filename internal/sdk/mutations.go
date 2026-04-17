package sdk

import (
	"context"
	"fmt"

	"github.com/gastownhall/wasteland/internal/commons"
)

// AcceptInput holds the parameters for accepting a completion.
type AcceptInput struct {
	Quality     int
	Reliability int
	Severity    string
	SkillTags   []string
	Message     string
}

// UpstreamSubmissionSelector identifies one pending upstream submission.
// RigHandle preserves the existing CLI/API contract; PRURL disambiguates
// multiple open submissions from the same rig.
type UpstreamSubmissionSelector struct {
	RigHandle string
	PRURL     string
}

// PostInput holds the parameters for posting a new wanted item.
type PostInput struct {
	Title       string
	Description string
	Project     string
	Type        string
	Priority    int
	EffortLevel string
	Tags        []string
}

// Claim claims a wanted item for the current rig.
func (c *Client) Claim(wantedID string) (*MutationResult, error) {
	if result := c.prIdempotent(wantedID, "claimed"); result != nil {
		return result, nil
	}
	stmts := []string{commons.ClaimWantedDML(wantedID, c.rigHandle)}
	return c.mutate(wantedID, "wl claim: "+wantedID, stmts...)
}

// Unclaim reverts a claimed wanted item to open.
func (c *Client) Unclaim(wantedID string) (*MutationResult, error) {
	if result := c.prIdempotent(wantedID, "open"); result != nil {
		return result, nil
	}
	stmts := []string{commons.UnclaimWantedDML(wantedID)}
	return c.mutate(wantedID, "wl unclaim: "+wantedID, stmts...)
}

// Done submits completion evidence for a claimed wanted item.
func (c *Client) Done(wantedID, evidence string) (*MutationResult, error) {
	if result := c.prIdempotent(wantedID, "in_review"); result != nil {
		return result, nil
	}
	completionID := commons.GeneratePrefixedID("c", wantedID, c.rigHandle)
	stmts := commons.SubmitCompletionDML(completionID, wantedID, c.rigHandle, evidence, c.hopURI)
	return c.mutate(wantedID, "wl done: "+wantedID, stmts...)
}

// Accept validates a completion, creates a stamp, and marks the item completed.
func (c *Client) Accept(wantedID string, input AcceptInput) (*MutationResult, error) {
	result, subject, err := c.acceptLocked(wantedID, input)
	if err != nil || result == nil {
		return result, err
	}
	// Run the GitHub-handle cache hook after the mutex is released so a
	// slow network call cannot delay the next mutation. The hook swallows
	// all errors; Accept's return value is unaffected.
	c.populateGitHubCache(subject)
	return result, nil
}

// acceptLocked runs the Accept DML under c.mu and returns the stamp subject
// alongside the mutation result so the caller can drive the post-lock hook.
func (c *Client) acceptLocked(wantedID string, input AcceptInput) (*MutationResult, string, error) {
	// Hold the mutex for the entire operation to prevent concurrent Accept()
	// calls from both passing the idempotent check on the same completion.
	c.mu.Lock()
	defer c.mu.Unlock()

	if result := c.prIdempotentLocked(wantedID, "completed"); result != nil {
		return result, "", nil
	}

	// Look up the completion to get its ID and worker handle.
	completion, err := commons.QueryCompletion(c.db, wantedID)
	if err != nil {
		return nil, "", fmt.Errorf("querying completion: %w", err)
	}
	if completion == nil {
		return nil, "", fmt.Errorf("no completion found for item %s", wantedID)
	}
	if completion.CompletedBy == c.rigHandle {
		return nil, "", &commons.ConflictError{
			Message: fmt.Sprintf("cannot issue a stamp to yourself; use \"wl close %s\"", wantedID),
		}
	}

	stamp := &commons.Stamp{
		ID:          commons.GeneratePrefixedID("s", wantedID, c.rigHandle),
		Author:      c.rigHandle,
		Subject:     completion.CompletedBy,
		Quality:     input.Quality,
		Reliability: input.Reliability,
		Severity:    input.Severity,
		ContextID:   completion.ID,
		ContextType: "completion",
		SkillTags:   input.SkillTags,
		Message:     input.Message,
	}

	stmts := commons.AcceptCompletionDML(wantedID, completion.ID, c.rigHandle, c.hopURI, stamp)
	result, err := c.mutateLocked(wantedID, "wl accept: "+wantedID, stmts...)
	if err != nil {
		return nil, "", err
	}
	return result, stamp.Subject, nil
}

// AcceptUpstream adopts a fork submission, creating a completion and stamp on the poster's branch.
func (c *Client) AcceptUpstream(wantedID, submitterHandle string, input AcceptInput) (*MutationResult, error) {
	return c.AcceptUpstreamSelected(wantedID, UpstreamSubmissionSelector{RigHandle: submitterHandle}, input)
}

// AcceptUpstreamSelected adopts one specific fork submission, creating a
// completion and stamp on the poster's branch.
func (c *Client) AcceptUpstreamSelected(wantedID string, selector UpstreamSubmissionSelector, input AcceptInput) (*MutationResult, error) {
	result, subject, err := c.acceptUpstreamLocked(wantedID, selector, input)
	if err != nil || result == nil {
		return result, err
	}
	c.populateGitHubCache(subject)
	return result, nil
}

// acceptUpstreamLocked runs the AcceptUpstream DML under c.mu and returns
// the stamp subject so the caller can drive the post-lock hook.
func (c *Client) acceptUpstreamLocked(wantedID string, selector UpstreamSubmissionSelector, input AcceptInput) (*MutationResult, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if result := c.prIdempotentLocked(wantedID, "completed"); result != nil {
		return result, "", nil
	}

	if c.ListPendingItems == nil && c.ListPendingItemsContext == nil {
		return nil, "", fmt.Errorf("upstream PR listing not available")
	}

	pending, err := c.listPendingItemsContext(context.Background())
	if err != nil {
		return nil, "", fmt.Errorf("listing pending items: %w", err)
	}

	match, err := matchUpstreamSubmission(pending[wantedID], selector)
	if err != nil {
		return nil, "", err
	}

	if match.Status != "in_review" {
		return nil, "", fmt.Errorf("submission is not in review")
	}
	if match.CompletedBy == "" {
		return nil, "", fmt.Errorf("submission has no completion data")
	}
	if match.CompletedBy == c.rigHandle {
		submitterHandle := selector.RigHandle
		if submitterHandle == "" {
			submitterHandle = match.RigHandle
		}
		return nil, "", &commons.ConflictError{
			Message: fmt.Sprintf("cannot issue a stamp to yourself; use \"wl close-upstream %s %s\"", wantedID, submitterHandle),
		}
	}

	completionID := commons.GeneratePrefixedID("c", wantedID, match.CompletedBy)
	stamp := &commons.Stamp{
		ID:          commons.GeneratePrefixedID("s", wantedID, c.rigHandle),
		Author:      c.rigHandle,
		Subject:     match.CompletedBy,
		Quality:     input.Quality,
		Reliability: input.Reliability,
		Severity:    input.Severity,
		ContextID:   completionID,
		ContextType: "completion",
		SkillTags:   input.SkillTags,
		Message:     input.Message,
	}

	stmts := commons.AcceptUpstreamDML(wantedID, completionID, match.CompletedBy, match.Evidence, c.rigHandle, c.hopURI, stamp)
	result, err := c.mutateLocked(wantedID, "wl accept-upstream: "+wantedID, stmts...)
	if err != nil {
		return nil, "", err
	}
	return result, stamp.Subject, nil
}

// RejectUpstream declines a fork submission by closing its upstream DoltHub PR.
// No local state is modified — the item remains in its current status.
func (c *Client) RejectUpstream(wantedID, submitterHandle string) error {
	return c.RejectUpstreamSelected(wantedID, UpstreamSubmissionSelector{RigHandle: submitterHandle})
}

// RejectUpstreamSelected declines one fork submission by closing its upstream
// DoltHub PR. No local state is modified.
func (c *Client) RejectUpstreamSelected(wantedID string, selector UpstreamSubmissionSelector) error {
	match, err := c.findUpstreamSubmissionSelected(wantedID, selector)
	if err != nil {
		return err
	}
	if match.PRURL == "" {
		return fmt.Errorf("submission has no upstream PR to close")
	}
	if c.CloseUpstreamPR == nil {
		return fmt.Errorf("upstream PR closing not available")
	}
	return c.CloseUpstreamPR(match.PRURL)
}

// CloseUpstream adopts a fork submission without creating a stamp, then closes
// the upstream DoltHub PR.
func (c *Client) CloseUpstream(wantedID, submitterHandle string) (*MutationResult, error) {
	return c.CloseUpstreamSelected(wantedID, UpstreamSubmissionSelector{RigHandle: submitterHandle})
}

// CloseUpstreamSelected adopts one fork submission without creating a stamp,
// then closes the upstream DoltHub PR.
func (c *Client) CloseUpstreamSelected(wantedID string, selector UpstreamSubmissionSelector) (*MutationResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if result := c.prIdempotentLocked(wantedID, "completed"); result != nil {
		return result, nil
	}

	match, err := c.findUpstreamSubmissionSelected(wantedID, selector)
	if err != nil {
		return nil, err
	}

	if match.Status != "in_review" {
		return nil, fmt.Errorf("submission is not in review")
	}
	if match.CompletedBy == "" {
		return nil, fmt.Errorf("submission has no completion data")
	}

	completionID := commons.GeneratePrefixedID("c", wantedID, match.CompletedBy)
	stmts := commons.CloseUpstreamDML(wantedID, completionID, match.CompletedBy, match.Evidence, c.hopURI)
	result, err := c.mutateLocked(wantedID, "wl close-upstream: "+wantedID, stmts...)
	if err != nil {
		return nil, err
	}

	// Best-effort close the upstream PR.
	if match.PRURL != "" && c.CloseUpstreamPR != nil {
		_ = c.CloseUpstreamPR(match.PRURL)
	}
	return result, nil
}

// findUpstreamSubmission looks up a pending upstream submission by rig handle.
func (c *Client) findUpstreamSubmission(wantedID, submitterHandle string) (*PendingItem, error) {
	return c.findUpstreamSubmissionSelected(wantedID, UpstreamSubmissionSelector{RigHandle: submitterHandle})
}

func (c *Client) findUpstreamSubmissionSelected(wantedID string, selector UpstreamSubmissionSelector) (*PendingItem, error) {
	if c.ListPendingItems == nil && c.ListPendingItemsContext == nil {
		return nil, fmt.Errorf("upstream PR listing not available")
	}
	pending, err := c.listPendingItemsContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("listing pending items: %w", err)
	}
	return matchUpstreamSubmission(pending[wantedID], selector)
}

func matchUpstreamSubmission(items []PendingItem, selector UpstreamSubmissionSelector) (*PendingItem, error) {
	if selector.PRURL != "" {
		for i := range items {
			if items[i].PRURL != selector.PRURL {
				continue
			}
			if selector.RigHandle != "" && items[i].RigHandle != selector.RigHandle {
				return nil, fmt.Errorf("pending submission selector mismatch for %s", selector.PRURL)
			}
			match := items[i]
			return &match, nil
		}
		if selector.RigHandle != "" {
			return nil, fmt.Errorf("no pending submission from %s at %s", selector.RigHandle, selector.PRURL)
		}
		return nil, fmt.Errorf("no pending submission at %s", selector.PRURL)
	}
	if selector.RigHandle == "" {
		return nil, fmt.Errorf("no pending submission selector provided")
	}
	var matches []PendingItem
	for i := range items {
		if items[i].RigHandle == selector.RigHandle {
			matches = append(matches, items[i])
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no pending submission from %s", selector.RigHandle)
	case 1:
		match := matches[0]
		return &match, nil
	default:
		return nil, fmt.Errorf("multiple pending submissions from %s; select by pr_url", selector.RigHandle)
	}
}

// Reject rejects a completion, reverting the item from in_review to claimed.
func (c *Client) Reject(wantedID, reason string) (*MutationResult, error) {
	if result := c.prIdempotent(wantedID, "claimed"); result != nil {
		return result, nil
	}
	stmts := commons.RejectCompletionDML(wantedID)
	msg := "wl reject: " + wantedID
	if reason != "" {
		if len(reason) > 500 {
			reason = reason[:500] + "..."
		}
		msg += " — " + reason
	}
	return c.mutate(wantedID, msg, stmts...)
}

// Close marks an in_review item as completed without a stamp.
func (c *Client) Close(wantedID string) (*MutationResult, error) {
	if result := c.prIdempotent(wantedID, "completed"); result != nil {
		return result, nil
	}
	stmts := []string{commons.CloseWantedDML(wantedID)}
	return c.mutate(wantedID, "wl close: "+wantedID, stmts...)
}

// Delete soft-deletes a wanted item by setting status=withdrawn.
// In PR mode, if the item only exists on a branch (never on main),
// we skip the mutation and just clean up the branch instead.
func (c *Client) Delete(wantedID string) (*MutationResult, error) {
	if c.mode == "pr" {
		// Hold lock for the entire check-then-act to prevent a concurrent
		// Post from creating the item on main between the query and cleanup.
		c.mu.Lock()

		if result := c.prIdempotentLocked(wantedID, "withdrawn"); result != nil {
			c.mu.Unlock()
			return result, nil
		}

		branch := commons.BranchName(c.rigHandle, wantedID)
		mainStatus, _, _ := commons.QueryItemStatus(c.db, wantedID, "main")
		if mainStatus == "" {
			// Item only exists on branch — clean up branch and close any PR.
			c.cleanupBranch(branch)
			c.mu.Unlock()
			return &MutationResult{
				Branch: branch,
				Hint:   "branch-only item — branch deleted",
			}, nil
		}
		c.mu.Unlock()
	}
	stmts := []string{commons.DeleteWantedDML(wantedID)}
	return c.mutate(wantedID, "wl delete: "+wantedID, stmts...)
}

// Post creates a new wanted item.
func (c *Client) Post(input PostInput) (*MutationResult, error) {
	id := commons.GenerateWantedID(input.Title)
	item := &commons.WantedItem{
		ID:          id,
		Title:       input.Title,
		Description: input.Description,
		Project:     input.Project,
		Type:        input.Type,
		Priority:    input.Priority,
		EffortLevel: input.EffortLevel,
		Tags:        input.Tags,
		PostedBy:    c.rigHandle,
	}

	dml, err := commons.InsertWantedDML(item)
	if err != nil {
		return nil, err
	}
	return c.mutate(id, "wl post: "+id, dml)
}

// Update modifies mutable fields on an open wanted item.
func (c *Client) Update(wantedID string, fields *commons.WantedUpdate) (*MutationResult, error) {
	dml, err := commons.UpdateWantedDML(wantedID, fields)
	if err != nil {
		return nil, err
	}
	return c.mutate(wantedID, "wl update: "+wantedID, dml)
}
