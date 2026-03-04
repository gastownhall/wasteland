package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// dolthubGraphQLURL is the DoltHub GraphQL API endpoint. Var so tests can override.
var dolthubGraphQLURL = "https://www.dolthub.com/graphql"

// dolthubAPIBase is the DoltHub REST API base URL. Var so tests can override.
var dolthubAPIBase = "https://www.dolthub.com/api/v1alpha1"

const dolthubRemoteBase = "https://doltremoteapi.dolthub.com"

// dolthubRepoBase is the DoltHub web base URL. Var so tests can override.
var dolthubRepoBase = "https://www.dolthub.com/repositories"

// DoltHubProvider implements Provider for DoltHub-hosted databases.
type DoltHubProvider struct {
	token      string
	httpClient *http.Client // optional; if set, used instead of creating new clients
}

// NewDoltHubProvider creates a DoltHubProvider with the given API token.
func NewDoltHubProvider(token string) *DoltHubProvider {
	return &DoltHubProvider{token: token}
}

// NewDoltHubProviderWithClient creates a DoltHubProvider using a pre-configured
// HTTP client whose transport handles auth (e.g. Nango proxy).
func NewDoltHubProviderWithClient(client *http.Client) *DoltHubProvider {
	return &DoltHubProvider{httpClient: client}
}

// getClient returns the injected HTTP client if set, otherwise creates a new
// one with the given timeout.
func (d *DoltHubProvider) getClient(timeout time.Duration) *http.Client {
	if d.httpClient != nil {
		return d.httpClient
	}
	return &http.Client{Timeout: timeout}
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

	req, err := http.NewRequest("POST", dolthubAPIBase+"/fork", bytes.NewReader(reqBody))
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
	backoff := 500 * time.Millisecond
	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		time.Sleep(backoff)

		url := fmt.Sprintf("%s/fork?operationName=%s", dolthubAPIBase, operationName)
		req, err := http.NewRequest("GET", url, nil)
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
			if backoff < 8*time.Second {
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

		if backoff < 8*time.Second {
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

	req, err := http.NewRequest("POST", dolthubGraphQLURL, bytes.NewReader(payload))
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
	req, err := http.NewRequest("GET", url, nil)
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

	req, err := http.NewRequest("POST", url, bytes.NewReader(reqBody))
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
		return fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, prResp.ID), nil
	}

	// Fallback: return the pulls page URL.
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

// FindPR searches for an open PR on DoltHub matching the given from-branch and fork org.
// Returns the PR URL and ID, or empty strings if none found.
//
// The list endpoint doesn't include branch details, so we fetch each open PR's
// detail to match on from_branch and from_branch_owner.
func (d *DoltHubProvider) FindPR(upstreamOrg, db, forkOrg, fromBranch string) (prURL, prID string) {
	pulls, err := d.listPulls(upstreamOrg, db)
	if err != nil {
		return "", ""
	}

	// Check each open PR's detail for matching branch info.
	for _, pr := range pulls {
		if !strings.EqualFold(pr.State, "open") {
			continue
		}
		detailURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubAPIBase, upstreamOrg, db, pr.PullID)
		detail, err := d.dolthubGet(detailURL)
		if err != nil {
			continue
		}
		var prDetail struct {
			FromBranch      string `json:"from_branch"`
			FromBranchOwner string `json:"from_branch_owner"`
		}
		if err := json.Unmarshal(detail, &prDetail); err != nil {
			continue
		}
		if prDetail.FromBranch == fromBranch && prDetail.FromBranchOwner == forkOrg {
			prURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pr.PullID)
			return prURL, pr.PullID
		}
	}
	return "", ""
}

// dolthubGet performs a GET request to the DoltHub API. Adds auth if a token is set.
func (d *DoltHubProvider) dolthubGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
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

	req, err := http.NewRequest("PATCH", patchURL, bytes.NewReader(reqBody))
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

	req, err := http.NewRequest("PATCH", patchURL, bytes.NewReader(reqBody))
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
	return nil
}

// PendingWantedState represents the state of a wanted item from a pending upstream PR's fork branch.
type PendingWantedState struct {
	RigHandle string
	Status    string
	ClaimedBy string
	Branch    string // e.g. "wl/alice/w-001"
	BranchURL string // web URL for the fork branch
	PRURL     string // web URL for the upstream PR
}

// ListPendingWantedIDs returns wanted IDs that have open upstream PRs, with
// the real state from each PR's fork branch. It lists open PRs, fetches each
// PR's detail for the from_branch, then queries the fork branch in parallel
// for the actual item status.
func (d *DoltHubProvider) ListPendingWantedIDs(upstreamOrg, db string) (map[string][]PendingWantedState, error) {
	pulls, err := d.listPulls(upstreamOrg, db)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	// Collect PR details in parallel.
	type prInfo struct {
		pullID          string
		fromBranch      string
		fromBranchOwner string
		rigHandle       string
		wantedID        string
	}

	// Filter open PRs first.
	var openPulls []pullSummary
	for _, pr := range pulls {
		if strings.EqualFold(pr.State, "open") {
			openPulls = append(openPulls, pr)
		}
	}

	// Fetch PR details in parallel.
	type detailResult struct {
		info prInfo
		ok   bool
	}
	detailCh := make(chan detailResult, len(openPulls))
	var detailWG sync.WaitGroup

	for _, pr := range openPulls {
		detailWG.Add(1)
		go func(pullID string) {
			defer detailWG.Done()
			detailURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubAPIBase, upstreamOrg, db, pullID)
			detail, err := d.dolthubGet(detailURL)
			if err != nil {
				return
			}
			var prDetail struct {
				FromBranch      string `json:"from_branch"`
				FromBranchOwner string `json:"from_branch_owner"`
			}
			if err := json.Unmarshal(detail, &prDetail); err != nil {
				return
			}
			if !strings.HasPrefix(prDetail.FromBranch, "wl/") {
				return
			}
			rest := strings.TrimPrefix(prDetail.FromBranch, "wl/")
			slashIdx := strings.Index(rest, "/")
			if slashIdx < 0 {
				return
			}
			rigHandle := rest[:slashIdx]
			wantedID := rest[slashIdx+1:]
			if wantedID == "" {
				return
			}
			detailCh <- detailResult{
				info: prInfo{
					pullID:          pullID,
					fromBranch:      prDetail.FromBranch,
					fromBranchOwner: prDetail.FromBranchOwner,
					rigHandle:       rigHandle,
					wantedID:        wantedID,
				},
				ok: true,
			}
		}(pr.PullID)
	}

	detailWG.Wait()
	close(detailCh)

	var prs []prInfo
	for r := range detailCh {
		if r.ok {
			prs = append(prs, r.info)
		}
	}

	// Query fork branches in parallel for real item state.
	type indexedResult struct {
		idx   int
		state PendingWantedState
	}
	results := make(chan indexedResult, len(prs))
	var wg sync.WaitGroup

	for i, pr := range prs {
		wg.Add(1)
		go func(idx int, pr prInfo) {
			defer wg.Done()

			prURL := fmt.Sprintf("%s/%s/%s/pulls/%s", dolthubRepoBase, upstreamOrg, db, pr.pullID)
			branchURL := fmt.Sprintf("%s/%s/%s/data/%s",
				dolthubRepoBase, pr.fromBranchOwner, db, url.PathEscape(pr.fromBranch))

			state := PendingWantedState{
				RigHandle: pr.rigHandle,
				Branch:    pr.fromBranch,
				BranchURL: branchURL,
				PRURL:     prURL,
			}

			// Query the fork branch for the item's actual status.
			owner := pr.fromBranchOwner
			if owner == "" {
				owner = upstreamOrg
			}
			q := fmt.Sprintf("SELECT status, claimed_by FROM wanted WHERE id='%s'", pr.wantedID)
			queryURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
				dolthubAPIBase, owner, db, url.PathEscape(pr.fromBranch), url.QueryEscape(q))

			body, err := d.dolthubGet(queryURL)
			if err == nil {
				var qr queryResponse
				if json.Unmarshal(body, &qr) == nil && len(qr.Rows) > 0 {
					if v, ok := qr.Rows[0]["status"]; ok {
						state.Status = v
					}
					if v, ok := qr.Rows[0]["claimed_by"]; ok {
						state.ClaimedBy = v
					}
				}
			}
			// If fork query fails, still include with empty status (rig handle is known).

			results <- indexedResult{idx: idx, state: state}
		}(i, pr)
	}

	wg.Wait()
	close(results)

	ids := make(map[string][]PendingWantedState)
	for r := range results {
		wantedID := prs[r.idx].wantedID
		ids[wantedID] = append(ids[wantedID], r.state)
	}
	return ids, nil
}

// queryResponse represents the DoltHub SQL API JSON response.
type queryResponse struct {
	Rows []map[string]string `json:"rows"`
}

// Type returns "dolthub".
func (d *DoltHubProvider) Type() string { return "dolthub" }
