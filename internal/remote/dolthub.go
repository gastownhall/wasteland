package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/observability"
)

// WantedIDPattern matches wanted IDs like w-com-001, w-gt-001, w-wl-004.
var WantedIDPattern = regexp.MustCompile(`\bw-[a-z]+-\d+\b`)

// dolthubGraphQLURL is the DoltHub GraphQL API endpoint. Var so tests can override.
var dolthubGraphQLURL = "https://www.dolthub.com/graphql"

// dolthubAPIBase is the DoltHub REST API base URL. Var so tests can override.
var dolthubAPIBase = "https://www.dolthub.com/api/v1alpha1"

var (
	forkPollInitialBackoff = 500 * time.Millisecond
	forkPollMaxBackoff     = 8 * time.Second
	forkPollTimeout        = 2 * time.Minute
)

const dolthubRemoteBase = "https://doltremoteapi.dolthub.com"

// dolthubRepoBase is the DoltHub web base URL. Var so tests can override.
var dolthubRepoBase = "https://www.dolthub.com/repositories"

// validStatus is the set of lifecycle statuses we recognize. Fork branches
// with statuses outside this set are ignored (non-standard protocol usage).
var validStatus = map[string]bool{
	"open":      true,
	"claimed":   true,
	"in_review": true,
	"completed": true,
}

// DoltHubProvider implements Provider for DoltHub-hosted databases.
type DoltHubProvider struct {
	token      string
	httpClient *http.Client // optional; if set, used instead of creating new clients
	ctx        context.Context
	cache      *doltHubProviderCache
}

// NewDoltHubProvider creates a DoltHubProvider with the given API token.
func NewDoltHubProvider(token string) *DoltHubProvider {
	return &DoltHubProvider{token: token, cache: newDoltHubProviderCache()}
}

// NewDoltHubProviderWithClient creates a DoltHubProvider using a pre-configured
// HTTP client whose transport handles auth (e.g. Nango proxy).
func NewDoltHubProviderWithClient(client *http.Client) *DoltHubProvider {
	return &DoltHubProvider{httpClient: observability.WrapClient(client), cache: newDoltHubProviderCache()}
}

// WithContext returns a shallow copy that binds outbound HTTP calls to ctx.
func (d *DoltHubProvider) WithContext(ctx context.Context) *DoltHubProvider {
	clone := *d
	clone.ctx = ctx
	return &clone
}

// getClient returns the injected HTTP client if set, otherwise creates a new
// one with the given timeout.
func (d *DoltHubProvider) getClient(timeout time.Duration) *http.Client {
	if d.httpClient != nil {
		return d.httpClient
	}
	return observability.WrapClient(&http.Client{Timeout: timeout})
}

func (d *DoltHubProvider) requestContext() context.Context {
	if d.ctx != nil {
		return d.ctx
	}
	return context.Background()
}

// PR lookups are short-lived optimizations for bursty UI reads; keep the TTL
// tight so externally closed/opened PRs become visible quickly.
var prCacheTTL = 2 * time.Second

type prInfo struct {
	pullID          string
	fromBranch      string
	fromBranchOwner string
	author          string
}

type cachedPRLookup struct {
	url       string
	id        string
	expiresAt time.Time
}

type cachedPRIndex struct {
	prs       []prInfo
	expiresAt time.Time
}

type doltHubProviderCache struct {
	mu         sync.RWMutex
	prByBranch map[string]cachedPRLookup
	prIndexes  map[string]cachedPRIndex
}

func newDoltHubProviderCache() *doltHubProviderCache {
	return &doltHubProviderCache{
		prByBranch: make(map[string]cachedPRLookup),
		prIndexes:  make(map[string]cachedPRIndex),
	}
}

func prBranchCacheKey(upstreamOrg, db, forkOrg, fromBranch string) string {
	return upstreamOrg + "/" + db + ":" + forkOrg + ":" + fromBranch
}

func prIndexCacheKey(upstreamOrg, db string) string {
	return upstreamOrg + "/" + db
}

func (d *DoltHubProvider) cachedPRLookup(upstreamOrg, db, forkOrg, fromBranch string) (cachedPRLookup, bool) {
	if d.cache == nil {
		return cachedPRLookup{}, false
	}
	key := prBranchCacheKey(upstreamOrg, db, forkOrg, fromBranch)
	now := time.Now()
	d.cache.mu.RLock()
	entry, ok := d.cache.prByBranch[key]
	d.cache.mu.RUnlock()
	if !ok || now.After(entry.expiresAt) {
		return cachedPRLookup{}, false
	}
	return entry, true
}

func (d *DoltHubProvider) rememberPRLookup(upstreamOrg, db, forkOrg, fromBranch, pullID string) {
	if d.cache == nil || forkOrg == "" || fromBranch == "" || pullID == "" {
		return
	}
	key := prBranchCacheKey(upstreamOrg, db, forkOrg, fromBranch)
	entry := cachedPRLookup{
		url:       fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pullID),
		id:        pullID,
		expiresAt: time.Now().Add(prCacheTTL),
	}
	d.cache.mu.Lock()
	d.cache.prByBranch[key] = entry
	d.cache.mu.Unlock()
}

func (d *DoltHubProvider) cachedPRIndex(upstreamOrg, db string) ([]prInfo, bool) {
	if d.cache == nil {
		return nil, false
	}
	key := prIndexCacheKey(upstreamOrg, db)
	now := time.Now()
	d.cache.mu.RLock()
	entry, ok := d.cache.prIndexes[key]
	d.cache.mu.RUnlock()
	if !ok || now.After(entry.expiresAt) {
		return nil, false
	}
	prs := make([]prInfo, len(entry.prs))
	copy(prs, entry.prs)
	return prs, true
}

func (d *DoltHubProvider) rememberPRIndex(upstreamOrg, db string, prs []prInfo) {
	if d.cache == nil {
		return
	}
	key := prIndexCacheKey(upstreamOrg, db)
	clone := make([]prInfo, len(prs))
	copy(clone, prs)
	expiresAt := time.Now().Add(prCacheTTL)
	branchCounts := make(map[string]int, len(prs))
	for _, pr := range prs {
		if pr.fromBranchOwner == "" || pr.fromBranch == "" || pr.pullID == "" {
			continue
		}
		branchCounts[prBranchCacheKey(upstreamOrg, db, pr.fromBranchOwner, pr.fromBranch)]++
	}

	d.cache.mu.Lock()
	d.cache.prIndexes[key] = cachedPRIndex{prs: clone, expiresAt: expiresAt}
	for _, pr := range prs {
		if pr.fromBranchOwner == "" || pr.fromBranch == "" || pr.pullID == "" {
			continue
		}
		branchKey := prBranchCacheKey(upstreamOrg, db, pr.fromBranchOwner, pr.fromBranch)
		if branchCounts[branchKey] != 1 {
			delete(d.cache.prByBranch, branchKey)
			continue
		}
		d.cache.prByBranch[branchKey] = cachedPRLookup{
			url:       fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pr.pullID),
			id:        pr.pullID,
			expiresAt: expiresAt,
		}
	}
	d.cache.mu.Unlock()
}

// ForkRequiredError is returned when the user needs to manually fork on DoltHub.
type ForkRequiredError struct {
	UpstreamOrg string
	UpstreamDB  string
	ForkOrg     string
}

func (e *ForkRequiredError) Error() string {
	return fmt.Sprintf("fork %s/%s not found under %s on DoltHub", e.UpstreamOrg, e.UpstreamDB, e.ForkOrg)
}

// ForkURL returns the DoltHub URL where the user can fork the database.
func (e *ForkRequiredError) ForkURL() string {
	return fmt.Sprintf("%s/%s/%s", dolthubRepoBase, e.UpstreamOrg, e.UpstreamDB)
}

// DatabaseURL returns the DoltHub remote API URL for the given org/db.
func (d *DoltHubProvider) DatabaseURL(org, db string) string {
	return fmt.Sprintf("%s/%s/%s", dolthubRemoteBase, org, db)
}

// Fork creates a fork of fromOrg/fromDB under toOrg on DoltHub.
//
// If DOLTHUB_SESSION_TOKEN is set (browser session cookie), uses the GraphQL
// createFork mutation which preserves DoltHub fork metadata (parent link, PR
// support). Otherwise attempts the REST API fork endpoint using the standard
// DOLTHUB_TOKEN. If the REST API fails due to auth/permission errors, falls
// back to checking if the fork already exists and returns a ForkRequiredError
// if not.
func (d *DoltHubProvider) Fork(fromOrg, fromDB, toOrg string) error {
	if d.databaseExists(toOrg, fromDB) {
		return nil
	}

	sessionToken := os.Getenv("DOLTHUB_SESSION_TOKEN")
	if sessionToken != "" {
		return d.forkGraphQL(fromOrg, fromDB, toOrg, sessionToken)
	}
	return d.forkREST(fromOrg, fromDB, toOrg)
}

// forkREST uses the DoltHub REST API to create a fork. It POSTs to the fork
// endpoint and polls until the operation completes. Falls back to an
// exists-check with ForkRequiredError if the API returns an auth error.
func (d *DoltHubProvider) forkREST(fromOrg, fromDB, toOrg string) error {
	reqBody, err := json.Marshal(map[string]string{
		"ownerName":          toOrg,
		"parentOwnerName":    fromOrg,
		"parentDatabaseName": fromDB,
	})
	if err != nil {
		return fmt.Errorf("marshaling REST fork request: %w", err)
	}

	req, err := http.NewRequestWithContext(d.requestContext(), "POST", dolthubAPIBase+"/fork", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating REST fork request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "token "+d.token)

	resp, err := d.getClient(60 * time.Second).Do(req)
	if err != nil {
		return d.forkRESTFallback(fromOrg, fromDB, toOrg,
			fmt.Errorf("REST fork request failed: %w", err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading REST fork response: %w", err)
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return d.forkRESTFallback(fromOrg, fromDB, toOrg,
			fmt.Errorf("REST fork auth error (HTTP %d): %s", resp.StatusCode, string(body)))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Check for "already exists" in error responses.
		if strings.Contains(strings.ToLower(string(body)), "already exists") {
			return nil
		}
		return d.forkRESTFallback(fromOrg, fromDB, toOrg,
			fmt.Errorf("REST fork error (HTTP %d): %s", resp.StatusCode, string(body)))
	}

	var forkResp struct {
		Status        string `json:"status"`
		OperationName string `json:"operation_name"`
	}
	if err := json.Unmarshal(body, &forkResp); err != nil {
		return fmt.Errorf("parsing REST fork response: %w", err)
	}

	// If the response already has a success status with no operation to poll, we're done.
	if forkResp.OperationName == "" {
		return nil
	}

	// Poll until the fork operation completes.
	return d.pollForkOperation(forkResp.OperationName, toOrg, fromDB)
}

// pollForkOperation polls the fork endpoint until the operation completes.
// Falls back to checking if the database exists when the poll times out,
// since the fork may complete with a response format we don't recognize.
func (d *DoltHubProvider) pollForkOperation(operationName, forkOrg, forkDB string) error {
	client := d.getClient(30 * time.Second)
	backoff := forkPollInitialBackoff
	deadline := time.Now().Add(forkPollTimeout)

	for time.Now().Before(deadline) {
		select {
		case <-time.After(backoff):
		case <-d.requestContext().Done():
			return d.requestContext().Err()
		}

		url := fmt.Sprintf("%s/fork?operationName=%s", dolthubAPIBase, operationName)
		req, err := http.NewRequestWithContext(d.requestContext(), "GET", url, nil)
		if err != nil {
			return fmt.Errorf("creating fork poll request: %w", err)
		}
		req.Header.Set("authorization", "token "+d.token)

		resp, err := client.Do(req)
		if err != nil {
			if backoff < 8*time.Second {
				backoff *= 2
			}
			continue
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			if backoff < forkPollMaxBackoff {
				backoff *= 2
			}
			continue
		}

		// Some DoltHub environments return an auth/policy error for the poll
		// endpoint even though the fork itself succeeds. If the forked database
		// already exists, treat the operation as complete rather than waiting for
		// the full timeout.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if d.databaseExists(forkOrg, forkDB) {
				return nil
			}
			if backoff < forkPollMaxBackoff {
				backoff *= 2
			}
			continue
		}

		var pollResp struct {
			Status       string `json:"status"`
			OwnerName    string `json:"owner_name"`
			DatabaseName string `json:"database_name"`
		}
		if err := json.Unmarshal(body, &pollResp); err == nil {
			// Check for completion via owner_name + database_name fields.
			if pollResp.OwnerName != "" && pollResp.DatabaseName != "" {
				return nil
			}
			// Check for a terminal status field (e.g., "Success", "Done").
			s := strings.ToLower(pollResp.Status)
			if s == "success" || s == "done" || s == "completed" {
				return nil
			}
		}

		if backoff < forkPollMaxBackoff {
			backoff *= 2
		}
	}

	// The poll timed out, but the fork may have completed anyway.
	// Check directly whether the database exists before reporting failure.
	if d.databaseExists(forkOrg, forkDB) {
		return nil
	}

	return fmt.Errorf("timed out waiting for fork operation %q to complete", operationName)
}

// forkRESTFallback falls back to the exists-check when the REST API fork fails.
// The originalErr parameter provides context about why the REST fork failed.
func (d *DoltHubProvider) forkRESTFallback(fromOrg, fromDB, toOrg string, _ error) error {
	if d.databaseExists(toOrg, fromDB) {
		return nil
	}
	return &ForkRequiredError{
		UpstreamOrg: fromOrg,
		UpstreamDB:  fromDB,
		ForkOrg:     toOrg,
	}
}

// forkGraphQL uses the DoltHub GraphQL createFork mutation with a browser
// session cookie. This preserves fork metadata on DoltHub.
func (d *DoltHubProvider) forkGraphQL(fromOrg, fromDB, toOrg, sessionToken string) error {
	query := `mutation CreateFork($ownerName: String!, $parentOwnerName: String!, $parentRepoName: String!) {
  createFork(ownerName: $ownerName, parentOwnerName: $parentOwnerName, parentRepoName: $parentRepoName) {
    forkOperationName
  }
}`
	reqBody := graphqlRequest{
		Query: query,
		Variables: map[string]any{
			"ownerName":       toOrg,
			"parentOwnerName": fromOrg,
			"parentRepoName":  fromDB,
		},
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling fork request: %w", err)
	}

	req, err := http.NewRequestWithContext(d.requestContext(), "POST", dolthubGraphQLURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating fork request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", "dolthubToken="+sessionToken)

	resp, err := d.getClient(60 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("DoltHub GraphQL fork request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading DoltHub GraphQL response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("DoltHub GraphQL error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var gqlResp graphqlResponse
	if err := json.Unmarshal(body, &gqlResp); err != nil {
		return fmt.Errorf("parsing DoltHub GraphQL response: %w", err)
	}

	if len(gqlResp.Errors) > 0 {
		msg := gqlResp.Errors[0].Message
		if strings.Contains(strings.ToLower(msg), "already exists") ||
			strings.Contains(strings.ToLower(msg), "already been forked") {
			return nil
		}
		return fmt.Errorf("DoltHub GraphQL fork error: %s", msg)
	}

	return nil
}

// databaseExists checks if a database exists on DoltHub by querying the
// REST API. Returns true if the API returns HTTP 200 for the main branch.
func (d *DoltHubProvider) databaseExists(org, db string) bool {
	url := fmt.Sprintf("%s/%s/%s/main", dolthubAPIBase, org, db)
	req, err := http.NewRequestWithContext(d.requestContext(), "GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("authorization", "token "+d.token)

	resp, err := d.getClient(10 * time.Second).Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == 200
}

// graphqlRequest is the JSON body sent to the GraphQL endpoint.
type graphqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// graphqlResponse is the top-level JSON response from GraphQL.
type graphqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// CreatePR opens a pull request on DoltHub from forkOrg/db (fromBranch) to upstreamOrg/db (main).
func (d *DoltHubProvider) CreatePR(forkOrg, upstreamOrg, db, fromBranch, title, body string) (string, error) {
	url := fmt.Sprintf("%s/%s/%s/pulls", dolthubAPIBase, upstreamOrg, db)
	reqBody, err := json.Marshal(map[string]string{
		"title":               title,
		"description":         body,
		"fromBranchOwnerName": forkOrg,
		"fromBranchRepoName":  db,
		"fromBranchName":      fromBranch,
		"toBranchOwnerName":   upstreamOrg,
		"toBranchRepoName":    db,
		"toBranchName":        "main",
	})
	if err != nil {
		return "", fmt.Errorf("marshaling PR request: %w", err)
	}

	req, err := http.NewRequestWithContext(d.requestContext(), "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("creating PR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "token "+d.token)

	resp, err := d.getClient(30 * time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("DoltHub create PR request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading DoltHub PR response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("DoltHub create PR error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	// Extract PR URL from response. The API returns the PR status with an ID.
	var prResp struct {
		Status string `json:"status"`
		ID     string `json:"_id"`
	}
	if err := json.Unmarshal(respBody, &prResp); err == nil && prResp.ID != "" {
		d.invalidatePRRepoCaches(upstreamOrg, db)
		d.rememberPRLookup(upstreamOrg, db, forkOrg, fromBranch, prResp.ID)
		return fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, prResp.ID), nil
	}

	// Fallback: return the pulls page URL.
	d.invalidatePRRepoCaches(upstreamOrg, db)
	return fmt.Sprintf("%s/%s/%s/pulls", dolthubRepoBase, upstreamOrg, db), nil
}

// pullSummary is a minimal representation of a DoltHub pull request from the list endpoint.
type pullSummary struct {
	PullID string `json:"pull_id"`
	State  string `json:"state"`
}

// listPulls returns all pull requests for a repo, paginating through all pages.
func (d *DoltHubProvider) listPulls(upstreamOrg, db string) ([]pullSummary, error) {
	var all []pullSummary
	listURL := fmt.Sprintf("%s/%s/%s/pulls", dolthubAPIBase, upstreamOrg, db)
	for {
		body, err := d.dolthubGet(listURL)
		if err != nil {
			return nil, err
		}
		var page struct {
			Pulls         []pullSummary `json:"pulls"`
			NextPageToken string        `json:"next_page_token"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Pulls...)
		if page.NextPageToken == "" {
			break
		}
		listURL = fmt.Sprintf("%s/%s/%s/pulls?pageToken=%s",
			dolthubAPIBase, upstreamOrg, db, url.QueryEscape(page.NextPageToken))
	}
	return all, nil
}

func (d *DoltHubProvider) listOpenPRDetails(upstreamOrg, db string, allowCached, failOnDetailError bool) ([]prInfo, error) {
	if allowCached {
		if prs, ok := d.cachedPRIndex(upstreamOrg, db); ok {
			return prs, nil
		}
	}
	pulls, err := d.listPulls(upstreamOrg, db)
	if err != nil {
		return nil, err
	}

	var openPulls []pullSummary
	for _, pr := range pulls {
		if strings.EqualFold(pr.State, "open") {
			openPulls = append(openPulls, pr)
		}
	}
	if len(openPulls) == 0 {
		d.rememberPRIndex(upstreamOrg, db, nil)
		return nil, nil
	}

	const maxConcurrency = 10
	type detailResult struct {
		info prInfo
		err  error
	}
	detailCh := make(chan detailResult, len(openPulls))
	var detailWG sync.WaitGroup
	sem := make(chan struct{}, maxConcurrency)

	for _, pr := range openPulls {
		detailWG.Add(1)
		go func(pullID string) {
			defer detailWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			detailURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubAPIBase, upstreamOrg, db, pullID)
			detail, err := d.dolthubGet(detailURL)
			if err != nil {
				detailCh <- detailResult{err: fmt.Errorf("reading PR %s detail: %w", pullID, err)}
				return
			}
			var prDetail struct {
				FromBranch      string `json:"from_branch"`
				FromBranchOwner string `json:"from_branch_owner"`
				Author          string `json:"author"`
			}
			if err := json.Unmarshal(detail, &prDetail); err != nil {
				detailCh <- detailResult{err: fmt.Errorf("decoding PR %s detail: %w", pullID, err)}
				return
			}
			detailCh <- detailResult{
				info: prInfo{
					pullID:          pullID,
					fromBranch:      prDetail.FromBranch,
					fromBranchOwner: prDetail.FromBranchOwner,
					author:          prDetail.Author,
				},
			}
		}(pr.PullID)
	}

	detailWG.Wait()
	close(detailCh)

	detailByPullID := make(map[string]prInfo, len(openPulls))
	for r := range detailCh {
		if r.err != nil {
			if failOnDetailError {
				return nil, r.err
			}
			continue
		}
		detailByPullID[r.info.pullID] = r.info
	}
	prs := make([]prInfo, 0, len(detailByPullID))
	for _, pull := range openPulls {
		if info, ok := detailByPullID[pull.PullID]; ok {
			prs = append(prs, info)
		}
	}
	d.rememberPRIndex(upstreamOrg, db, prs)
	return prs, nil
}

// FindPR searches for an open PR on DoltHub matching the given from-branch and fork org.
// Returns the PR URL and ID, or empty strings if none found.
//
// The list endpoint doesn't include branch details, so we fetch each open PR's
// detail to match on from_branch and from_branch_owner.
func (d *DoltHubProvider) FindPR(upstreamOrg, db, forkOrg, fromBranch string) (prURL, prID string) {
	if cached, ok := d.cachedPRLookup(upstreamOrg, db, forkOrg, fromBranch); ok {
		return cached.url, cached.id
	}

	prs, err := d.listOpenPRDetails(upstreamOrg, db, true, false)
	if err != nil {
		return "", ""
	}

	for _, pr := range prs {
		if pr.fromBranch == fromBranch && pr.fromBranchOwner == forkOrg {
			d.rememberPRLookup(upstreamOrg, db, forkOrg, fromBranch, pr.pullID)
			prURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pr.pullID)
			return prURL, pr.pullID
		}
	}
	return "", ""
}

// dolthubGet performs a GET request to the DoltHub API. Adds auth if a token is set.
func (d *DoltHubProvider) dolthubGet(rawURL string) ([]byte, error) {
	const maxRetries = 2
	var lastErr error
	ctx := d.requestContext()
	for attempt := range maxRetries {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		body, err := d.dolthubGetOnce(rawURL)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

func (d *DoltHubProvider) dolthubGetOnce(rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(d.requestContext(), "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	if d.token != "" {
		req.Header.Set("authorization", "token "+d.token)
	}

	resp, err := d.getClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

func isNoSuchRepositoryError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no such repository")
}

// UpdatePR updates the title and description of an existing DoltHub pull request.
func (d *DoltHubProvider) UpdatePR(upstreamOrg, db, prID, title, description string) error {
	patchURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubAPIBase, upstreamOrg, db, prID)
	reqBody, err := json.Marshal(map[string]string{
		"title":       title,
		"description": description,
	})
	if err != nil {
		return fmt.Errorf("marshaling PR update: %w", err)
	}

	req, err := http.NewRequestWithContext(d.requestContext(), "PATCH", patchURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating PR update request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "token "+d.token)

	resp, err := d.getClient(30 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("DoltHub update PR request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DoltHub update PR error (HTTP %d): %s", resp.StatusCode, string(body))
	}
	d.invalidatePRRepoCaches(upstreamOrg, db)
	return nil
}

// ClosePR closes a DoltHub pull request by setting its state to "closed".
func (d *DoltHubProvider) ClosePR(upstreamOrg, db, prID string) error {
	patchURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubAPIBase, upstreamOrg, db, prID)
	reqBody, err := json.Marshal(map[string]string{
		"state": "closed",
	})
	if err != nil {
		return fmt.Errorf("marshaling PR close: %w", err)
	}

	req, err := http.NewRequestWithContext(d.requestContext(), "PATCH", patchURL, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("creating PR close request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("authorization", "token "+d.token)

	resp, err := d.getClient(30 * time.Second).Do(req)
	if err != nil {
		return fmt.Errorf("DoltHub close PR request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DoltHub close PR error (HTTP %d): %s", resp.StatusCode, string(body))
	}
	d.invalidatePRRepoCaches(upstreamOrg, db)
	return nil
}

// PendingWantedState represents the state of a wanted item from a pending upstream PR's fork branch.
type PendingWantedState struct {
	RigHandle   string
	Status      string
	ClaimedBy   string
	Branch      string // e.g. "wl/alice/w-001"
	BranchURL   string // web URL for the fork branch
	PRURL       string // web URL for the upstream PR
	ForkOwner   string // DoltHub org that owns the fork
	CompletedBy string // from fork branch completions table
	Evidence    string // from fork branch completions table
}

func (d *DoltHubProvider) invalidatePRRepoCaches(upstreamOrg, db string) {
	if d.cache == nil {
		return
	}
	prefix := prIndexCacheKey(upstreamOrg, db) + ":"
	d.cache.mu.Lock()
	delete(d.cache.prIndexes, prIndexCacheKey(upstreamOrg, db))
	for key := range d.cache.prByBranch {
		if strings.HasPrefix(key, prefix) {
			delete(d.cache.prByBranch, key)
		}
	}
	d.cache.mu.Unlock()
}

type pendingBranchSource struct {
	owner  string
	branch string
}

type pendingBranchRow struct {
	wantedID  string
	status    string
	claimedBy string
	isAdded   bool
}

type pendingBranchRows struct {
	source pendingBranchSource
	rows   []pendingBranchRow
	err    error
}

// ListPendingWantedIDs returns wanted IDs that have open upstream PRs, detected
// via data-diff: for each distinct open source branch it queries the fork branch
// and compares against upstream main to find rows that actually differ. This
// catches PRs from any branch naming convention (wl/, main, custom).
func (d *DoltHubProvider) ListPendingWantedIDs(upstreamOrg, db string) (map[string][]PendingWantedState, error) {
	prs, err := d.listOpenPRDetails(upstreamOrg, db, false, true)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	// For PRs from "main" (commits directly on the fork's main), dolt_diff
	// between main and main is a no-op. Fall back to snapshot comparison
	// against upstream main for those PRs.
	type wantedItem struct {
		status    string
		claimedBy string
	}
	snapshotQuery := "SELECT id, status, COALESCE(claimed_by, '') as claimed_by FROM wanted"
	var upstreamItems map[string]wantedItem
	needsUpstream := false
	for _, pr := range prs {
		if pr.fromBranch == "main" {
			needsUpstream = true
			break
		}
	}
	if needsUpstream {
		upstreamItems = make(map[string]wantedItem)
		upstreamURL := fmt.Sprintf("%s/%s/%s/main?q=%s",
			dolthubAPIBase, upstreamOrg, db, url.QueryEscape(snapshotQuery))
		body, err := d.dolthubGet(upstreamURL)
		if err != nil {
			return nil, fmt.Errorf("reading upstream snapshot: %w", err)
		}
		var qr queryResponse
		if err := json.Unmarshal(body, &qr); err != nil {
			return nil, fmt.Errorf("decoding upstream snapshot: %w", err)
		}
		for _, row := range qr.Rows {
			upstreamItems[row["id"]] = wantedItem{
				status:    row["status"],
				claimedBy: row["claimed_by"],
			}
		}
	}

	// Query each PR's fork branch in parallel using dolt_diff to find rows
	// the branch actually changed. The old approach compared the fork's full
	// wanted snapshot against current upstream main, which produced false
	// positives: any fork branched before a main commit would show every
	// subsequently-changed row as "pending" even if the fork never touched it.
	branchPRs := make(map[pendingBranchSource][]prInfo, len(prs))
	for _, pr := range prs {
		owner := pr.fromBranchOwner
		if owner == "" {
			owner = upstreamOrg
		}
		branchPRs[pendingBranchSource{owner: owner, branch: pr.fromBranch}] = append(branchPRs[pendingBranchSource{owner: owner, branch: pr.fromBranch}], pr)
	}

	diffCh := make(chan pendingBranchRows, len(branchPRs))
	var wg sync.WaitGroup
	const maxConcurrency = 10
	sem2 := make(chan struct{}, maxConcurrency)

	// dolt_diff query: returns only rows where status or claimed_by actually
	// changed on the branch. Without the WHERE filter, updated_at drift from
	// fork main syncs would produce false positives for every row.
	diffQuery := "SELECT COALESCE(to_id, from_id) as id, COALESCE(to_status, '') as status, COALESCE(to_claimed_by, '') as claimed_by, diff_type FROM dolt_diff('main', '%s', 'wanted') WHERE diff_type <> 'modified' OR COALESCE(to_status,'') <> COALESCE(from_status,'') OR COALESCE(to_claimed_by,'') <> COALESCE(from_claimed_by,'')"

	for source := range branchPRs {
		wg.Add(1)
		go func(source pendingBranchSource) {
			defer wg.Done()
			sem2 <- struct{}{}
			defer func() { <-sem2 }()
			pullsForSource := branchPRs[source]
			pullIDLabel := source.branch
			if len(pullsForSource) > 0 && pullsForSource[0].pullID != "" {
				pullIDLabel = pullsForSource[0].pullID
			}

			// For PRs from "main", dolt_diff('main','main') is a no-op.
			// Fall back to snapshot comparison against upstream main.
			if source.branch == "main" {
				forkURL := fmt.Sprintf("%s/%s/%s/main?q=%s",
					dolthubAPIBase, source.owner, db, url.QueryEscape(snapshotQuery))
				body, err := d.dolthubGet(forkURL)
				if err != nil {
					if isNoSuchRepositoryError(err) {
						diffCh <- pendingBranchRows{source: source}
						return
					}
					diffCh <- pendingBranchRows{source: source, err: fmt.Errorf("reading fork snapshot for PR %s: %w", pullIDLabel, err)}
					return
				}
				var qr queryResponse
				if err := json.Unmarshal(body, &qr); err != nil {
					diffCh <- pendingBranchRows{source: source, err: fmt.Errorf("decoding fork snapshot for PR %s: %w", pullIDLabel, err)}
					return
				}
				rows := make([]pendingBranchRow, 0, len(qr.Rows))
				for _, row := range qr.Rows {
					id := row["id"]
					forkStatus := row["status"]
					forkClaimedBy := row["claimed_by"]
					upstream, exists := upstreamItems[id]
					if !exists || upstream.status != forkStatus || upstream.claimedBy != forkClaimedBy {
						rows = append(rows, pendingBranchRow{
							wantedID:  id,
							status:    forkStatus,
							claimedBy: forkClaimedBy,
							isAdded:   !exists,
						})
					}
				}
				diffCh <- pendingBranchRows{source: source, rows: rows}
				return
			}

			escaped := strings.ReplaceAll(source.branch, "'", "''")
			q := fmt.Sprintf(diffQuery, escaped)
			forkURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
				dolthubAPIBase, source.owner, db, url.PathEscape(source.branch), url.QueryEscape(q))

			body, err := d.dolthubGet(forkURL)
			if err != nil {
				if isNoSuchRepositoryError(err) {
					diffCh <- pendingBranchRows{source: source}
					return
				}
				diffCh <- pendingBranchRows{source: source, err: fmt.Errorf("reading fork diff for PR %s: %w", pullIDLabel, err)}
				return
			}
			var qr queryResponse
			if err := json.Unmarshal(body, &qr); err != nil {
				diffCh <- pendingBranchRows{source: source, err: fmt.Errorf("decoding fork diff for PR %s: %w", pullIDLabel, err)}
				return
			}

			rows := make([]pendingBranchRow, 0, len(qr.Rows))
			for _, row := range qr.Rows {
				id := row["id"]
				if id == "" {
					continue
				}
				// Skip deleted rows — they have no to_* state to display.
				if row["diff_type"] == "removed" {
					continue
				}
				forkStatus := row["status"]
				forkClaimedBy := row["claimed_by"]

				rows = append(rows, pendingBranchRow{
					wantedID:  id,
					status:    forkStatus,
					claimedBy: forkClaimedBy,
					isAdded:   row["diff_type"] == "added",
				})
			}
			diffCh <- pendingBranchRows{source: source, rows: rows}
		}(source)
	}

	wg.Wait()
	close(diffCh)

	branchResults := make([]pendingBranchRows, 0, len(branchPRs))
	compareIDs := make(map[string]struct{})
	for result := range diffCh {
		if result.err != nil {
			return nil, result.err
		}
		branchResults = append(branchResults, result)
		if result.source.branch == "main" {
			continue
		}
		for _, row := range result.rows {
			if row.wantedID != "" {
				compareIDs[row.wantedID] = struct{}{}
			}
		}
	}

	if len(compareIDs) > 0 && upstreamItems == nil {
		wantedIDs := make([]string, 0, len(compareIDs))
		for wantedID := range compareIDs {
			wantedIDs = append(wantedIDs, wantedID)
		}
		slices.Sort(wantedIDs)
		quotedIDs := make([]string, 0, len(wantedIDs))
		for _, wantedID := range wantedIDs {
			quotedIDs = append(quotedIDs, fmt.Sprintf("'%s'", strings.ReplaceAll(wantedID, "'", "''")))
		}
		upstreamItems = make(map[string]wantedItem, len(wantedIDs))
		upstreamCompareQuery := fmt.Sprintf(
			"SELECT id, status, COALESCE(claimed_by, '') as claimed_by FROM wanted WHERE id IN (%s)",
			strings.Join(quotedIDs, ","),
		)
		upstreamURL := fmt.Sprintf("%s/%s/%s/main?q=%s",
			dolthubAPIBase, upstreamOrg, db, url.QueryEscape(upstreamCompareQuery))
		body, err := d.dolthubGet(upstreamURL)
		if err == nil {
			var qr queryResponse
			if json.Unmarshal(body, &qr) == nil {
				for _, row := range qr.Rows {
					upstreamItems[row["id"]] = wantedItem{
						status:    row["status"],
						claimedBy: row["claimed_by"],
					}
				}
			}
		}
	}

	ids := make(map[string][]PendingWantedState)
	for _, result := range branchResults {
		for _, row := range result.rows {
			if result.source.branch != "main" {
				if upstream, exists := upstreamItems[row.wantedID]; exists &&
					upstream.status == row.status && upstream.claimedBy == row.claimedBy {
					continue
				}
			}
			for _, pr := range branchPRs[result.source] {
				branchURL := fmt.Sprintf("%s/%s/%s/data/%s",
					dolthubRepoBase, result.source.owner, db, url.PathEscape(pr.fromBranch))
				rigHandle := row.claimedBy
				if rigHandle == "" {
					rigHandle = pr.author
				}
				state := PendingWantedState{
					RigHandle: rigHandle,
					Status:    row.status,
					ClaimedBy: row.claimedBy,
					Branch:    pr.fromBranch,
					BranchURL: branchURL,
					PRURL:     fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pr.pullID),
					ForkOwner: result.source.owner,
				}
				// Reject fork statuses not in our lifecycle table. Forks we
				// don't control may use non-standard values — skip those.
				if !validStatus[state.Status] {
					continue
				}
				// Skip stale fork state that doesn't represent intentional action.
				// A diff appears when a branch predates an upstream update — the
				// branch carries forward old state the fork owner never touched.
				//
				// Filter rules:
				// 1. status "open" = untouched item (stale copy), unless the row
				//    was newly added on the branch
				// 2. claimed_by set to someone other than the PR author =
				//    inherited claim from a previous upstream state
				if state.Status == "open" && !row.isAdded {
					continue
				}
				if state.Status == "claimed" && state.ClaimedBy != "" && state.ClaimedBy != pr.author {
					continue
				}
				ids[row.wantedID] = append(ids[row.wantedID], state)
			}
		}
	}

	// For entries past the claiming stage, query the fork branch's completions
	// table to surface evidence from competing submissions.
	type completionRef struct {
		wantedID string
		index    int
	}
	completionTargets := make(map[pendingBranchSource]map[string][]completionRef)
	for wantedID, states := range ids {
		for i := range states {
			if states[i].Status == "open" || states[i].Status == "claimed" {
				continue
			}
			key := pendingBranchSource{owner: states[i].ForkOwner, branch: states[i].Branch}
			if key.owner == "" || key.branch == "" {
				continue
			}
			if completionTargets[key] == nil {
				completionTargets[key] = make(map[string][]completionRef)
			}
			completionTargets[key][wantedID] = append(completionTargets[key][wantedID], completionRef{wantedID: wantedID, index: i})
		}
	}
	for key, wantedRefs := range completionTargets {
		wantedIDs := make([]string, 0, len(wantedRefs))
		for wantedID := range wantedRefs {
			wantedIDs = append(wantedIDs, wantedID)
		}
		slices.Sort(wantedIDs)
		quotedIDs := make([]string, 0, len(wantedIDs))
		for _, wantedID := range wantedIDs {
			quotedIDs = append(quotedIDs, fmt.Sprintf("'%s'", strings.ReplaceAll(wantedID, "'", "''")))
		}
		q := fmt.Sprintf(
			"SELECT wanted_id, completed_by, COALESCE(evidence,'') as evidence FROM completions WHERE wanted_id IN (%s)",
			strings.Join(quotedIDs, ","),
		)
		cURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
			dolthubAPIBase, key.owner, db, url.PathEscape(key.branch), url.QueryEscape(q))
		body, err := d.dolthubGet(cURL)
		if err != nil {
			continue
		}
		var qr queryResponse
		if json.Unmarshal(body, &qr) != nil || len(qr.Rows) == 0 {
			continue
		}
		completionValues := make(map[string]struct {
			completedBy string
			evidence    string
		}, len(qr.Rows))
		for _, row := range qr.Rows {
			rowWantedID := row["wanted_id"]
			if rowWantedID == "" && len(wantedIDs) == 1 {
				rowWantedID = wantedIDs[0]
			}
			if rowWantedID == "" {
				continue
			}
			completionValues[rowWantedID] = struct {
				completedBy string
				evidence    string
			}{
				completedBy: row["completed_by"],
				evidence:    row["evidence"],
			}
		}
		for wantedID, refs := range wantedRefs {
			value, ok := completionValues[wantedID]
			if !ok {
				continue
			}
			for _, ref := range refs {
				ids[ref.wantedID][ref.index].CompletedBy = value.completedBy
				ids[ref.wantedID][ref.index].Evidence = value.evidence
			}
		}
	}

	return ids, nil
}

// queryResponse represents the DoltHub SQL API JSON response.
type queryResponse struct {
	Rows []map[string]string `json:"rows"`
}

// Type returns "dolthub".
func (d *DoltHubProvider) Type() string { return "dolthub" }
