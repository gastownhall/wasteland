// Package pile provides a read-only DoltHub client for hop/the-pile.
//
// This is intentionally separate from the SDK/backend stack, which is built
// around joined wastelands with fork semantics. The pile is a global read-only
// database of seeded developer profiles.
package pile

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/gastownhall/wasteland/internal/backend"
)

// Client is a read-only DoltHub API client for hop/the-pile.
type Client struct {
	org    string
	db     string
	branch string
	token  string
	client *http.Client
}

// New creates a Client targeting the given org/db on DoltHub.
// Token is optional for public databases.
func New(token, org, db string) *Client {
	return &Client{
		org:    org,
		db:     db,
		branch: "main",
		token:  token,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewDefault creates a Client for hop/the-pile with no auth token.
func NewDefault() *Client {
	return New("", "hop", "the-pile")
}

// NewCommonsReader creates a read-only Client for hop/wl-commons, the public
// anonymous upstream used as a fallback data source when a handle has no
// boot_block in the-pile.
func NewCommonsReader() *Client {
	return New("", "hop", "wl-commons")
}

// queryRaw runs a SQL query and returns the raw DoltHub JSON response body.
func (p *Client) queryRaw(sql string) ([]byte, error) {
	apiURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
		backend.DoltHubAPIBase, p.org, p.db,
		url.PathEscape(p.branch), url.QueryEscape(sql))

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if p.token != "" {
		req.Header.Set("Authorization", "token "+p.token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pile query failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoltHub API returned %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	return body, nil
}

// QueryRows runs a SQL query and returns parsed JSON rows.
func (p *Client) QueryRows(sql string) ([]map[string]any, error) {
	body, err := p.queryRaw(sql)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Status  string            `json:"query_execution_status"`
		Message string            `json:"query_execution_message"`
		Rows    []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}
	if resp.Status == "Error" {
		return nil, fmt.Errorf("query error: %s", resp.Message)
	}

	rows := make([]map[string]any, 0, len(resp.Rows))
	for _, raw := range resp.Rows {
		var row map[string]any
		if err := json.Unmarshal(raw, &row); err != nil {
			continue
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
