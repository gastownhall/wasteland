package backend

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
)

// DoltHubAPIBase is the DoltHub REST API base URL. Var so tests can override.
var DoltHubAPIBase = "https://www.dolthub.com/api/v1alpha1"

// RemoteDB implements DB using the DoltHub REST API.
// Reads from main go to the upstream (shared) database.
// Branch reads and all writes go to the fork (user's) database.
type RemoteDB struct {
	token      string
	readOwner  string // upstream org
	readDB     string // upstream db name
	writeOwner string // fork org
	writeDB    string // fork db name
	client     *http.Client
}

// NewRemoteDB creates a DB backed by the DoltHub REST API.
func NewRemoteDB(token, readOwner, readDB, writeOwner, writeDB string) *RemoteDB {
	return &RemoteDB{
		token:      token,
		readOwner:  readOwner,
		readDB:     readDB,
		writeOwner: writeOwner,
		writeDB:    writeDB,
		client:     &http.Client{Timeout: 60 * time.Second},
	}
}

// NewRemoteDBWithClient creates a DB backed by the DoltHub REST API using a
// pre-configured HTTP client. The client's transport is responsible for auth
// (e.g. Nango proxy), so no token is stored.
func NewRemoteDBWithClient(client *http.Client, readOwner, readDB, writeOwner, writeDB string) *RemoteDB {
	return &RemoteDB{
		readOwner:  readOwner,
		readDB:     readDB,
		writeOwner: writeOwner,
		writeDB:    writeDB,
		client:     client,
	}
}

// Query runs a read-only SQL SELECT via the DoltHub API.
func (r *RemoteDB) Query(sql, ref string) (string, error) {
	owner := r.readOwner
	db := r.readDB
	branch := "main"

	if ref != "" {
		// Branch refs read from the fork database.
		owner = r.writeOwner
		db = r.writeDB
		branch = ref
	}

	apiURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
		DoltHubAPIBase, owner, db, url.PathEscape(branch), url.QueryEscape(sql))

	body, err := r.doGet(apiURL)
	if err != nil {
		return "", fmt.Errorf("query failed: %w", err)
	}

	return JSONToCSV(body)
}

// Exec runs DML via the DoltHub write API on the given branch.
// The write API accepts only a single statement per call, so multi-statement
// mutations are sent sequentially. After the first write the branch exists,
// so subsequent statements read from the branch (not main) to see prior changes.
func (r *RemoteDB) Exec(branch, _ string, _ bool, stmts ...string) error {
	if branch == "" {
		branch = "main"
	}

	// Determine the from-branch for the first write.
	// If the target branch already exists on the fork, write from that branch
	// to preserve prior mutations (e.g. claim → done). Otherwise write from main.
	fromBranch := "main"
	if branch != "main" && r.branchHasData(branch) {
		fromBranch = branch
	}

	for _, stmt := range stmts {
		if err := r.execOne(fromBranch, branch, stmt); err != nil {
			return err
		}
		// After the first successful write, the branch has data — subsequent
		// statements must read from it to see the prior changes.
		fromBranch = branch
	}
	return nil
}

// execOne sends a single DML statement to the DoltHub write API.
func (r *RemoteDB) execOne(fromBranch, toBranch, stmt string) error {
	apiURL := fmt.Sprintf("%s/%s/%s/write/%s/%s?q=%s",
		DoltHubAPIBase, r.writeOwner, r.writeDB,
		url.PathEscape(fromBranch), url.PathEscape(toBranch),
		url.QueryEscape(stmt))

	body, err := r.doPost(apiURL, nil)
	if err != nil {
		return fmt.Errorf("exec failed: %w", err)
	}

	var writeResp struct {
		OperationName         string `json:"operation_name"`
		QueryExecutionStatus  string `json:"query_execution_status"`
		QueryExecutionMessage string `json:"query_execution_message"`
	}
	if err := json.Unmarshal(body, &writeResp); err != nil {
		return fmt.Errorf("parsing write response: %w", err)
	}

	if writeResp.QueryExecutionStatus == "Error" {
		return fmt.Errorf("write operation failed: %s", writeResp.QueryExecutionMessage)
	}

	if writeResp.OperationName != "" {
		return r.pollOperation(writeResp.OperationName)
	}
	return nil
}

// Branches returns branch names matching the given prefix from the fork.
func (r *RemoteDB) Branches(prefix string) ([]string, error) {
	sql := fmt.Sprintf("SELECT name FROM dolt_branches WHERE name LIKE '%s%%' ORDER BY name",
		commons.EscapeLIKE(prefix))

	// Query branches on the fork database.
	apiURL := fmt.Sprintf("%s/%s/%s/main?q=%s",
		DoltHubAPIBase, r.writeOwner, r.writeDB, url.QueryEscape(sql))

	body, err := r.doGet(apiURL)
	if err != nil {
		return nil, fmt.Errorf("branches query failed: %w", err)
	}

	csv, err := JSONToCSV(body)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if len(lines) < 2 {
		return nil, nil
	}
	var branches []string
	for _, line := range lines[1:] {
		name := strings.TrimSpace(line)
		if name != "" {
			branches = append(branches, name)
		}
	}
	return branches, nil
}

// DeleteBranch deletes a branch on the fork via DOLT_BRANCH('-D', ...).
// Returns an error if branch deletion is not supported by the write API —
// callers should fall back to clearing item data from the branch instead.
func (r *RemoteDB) DeleteBranch(branch string) error {
	if branch == "" || branch == "main" {
		return nil
	}
	escaped := strings.ReplaceAll(branch, "'", "''")
	return r.execOnMain(fmt.Sprintf("CALL DOLT_BRANCH('-D', '%s')", escaped))
}

// PushBranch is a no-op for remote — the write API auto-pushes.
func (r *RemoteDB) PushBranch(_ string, _ io.Writer) error { return nil }

// PushMain is a no-op for remote.
func (r *RemoteDB) PushMain(_ io.Writer) error { return nil }

// PushAllRemotes is a no-op for remote — the write API auto-pushes.
func (r *RemoteDB) PushAllRemotes(_ io.Writer) error { return nil }

// CanWildWest returns an error — the DoltHub REST API cannot push from a fork
// to the upstream, so wild-west mode is not supported.
func (r *RemoteDB) CanWildWest() error {
	return fmt.Errorf("wild-west mode requires direct upstream access; switch to PR mode in settings")
}

// Sync is a no-op for remote — reads always go to the upstream API and are
// always fresh. The DoltHub hosted SQL API does not support remote operations
// (dolt_remotes, DOLT_REMOTE, DOLT_FETCH), so fork-level sync is not possible.
func (r *RemoteDB) Sync() error { return nil }

// MergeBranch merges a branch into the fork's main via the write API.
func (r *RemoteDB) MergeBranch(branch string) error {
	escaped := strings.ReplaceAll(branch, "'", "''")
	return r.execOnMain(fmt.Sprintf("CALL DOLT_MERGE('%s')", escaped))
}

// DeleteRemoteBranch removes a branch on the fork. For remote backend, local
// and remote branches are the same thing (both live on the fork).
func (r *RemoteDB) DeleteRemoteBranch(branch string) error {
	return r.DeleteBranch(branch)
}

// Diff returns a human-readable diff of changes on the given branch
// relative to the fork's main, querying dolt system diff tables via the API.
func (r *RemoteDB) Diff(branch string) (string, error) {
	escaped := strings.ReplaceAll(branch, "'", "''")

	// List changed tables via dolt_diff_stat (2-arg form: from, to).
	tableSQL := fmt.Sprintf(
		"SELECT table_name, rows_added, rows_modified, rows_deleted FROM dolt_diff_stat('main', '%s')", escaped)
	tableCSV, err := r.queryForkBranch(tableSQL, branch)
	if err != nil {
		return "", fmt.Errorf("diff: listing changed tables: %w", err)
	}

	tables := parseDiffTables(tableCSV)
	if len(tables) == 0 {
		return "(no changes)\n", nil
	}

	var buf strings.Builder
	for _, tbl := range tables {
		fmt.Fprintf(&buf, "## %s\n\n", tbl)

		// Query row-level changes via dolt_diff (3-arg form: from, to, table).
		rowSQL := fmt.Sprintf(
			"SELECT * FROM dolt_diff('main', '%s', '%s')",
			escaped, strings.ReplaceAll(tbl, "'", "''"))
		rowCSV, err := r.queryForkBranch(rowSQL, branch)
		if err != nil {
			fmt.Fprintf(&buf, "(error reading diff: %v)\n\n", err)
			continue
		}

		records, err := csv.NewReader(strings.NewReader(rowCSV)).ReadAll()
		if err != nil || len(records) < 2 {
			fmt.Fprintf(&buf, "(no row changes)\n\n")
			continue
		}

		header := records[0]
		buf.WriteString("```\n")
		for _, fields := range records[1:] {
			formatDiffRow(&buf, header, fields)
		}
		buf.WriteString("```\n\n")
	}

	return buf.String(), nil
}

// --- Remote helpers ---

// execOnMain posts a SQL statement to the write API on the fork's main branch
// and polls until the operation completes.
func (r *RemoteDB) execOnMain(sql string) error {
	apiURL := fmt.Sprintf("%s/%s/%s/write/main/main?q=%s",
		DoltHubAPIBase, r.writeOwner, r.writeDB, url.QueryEscape(sql))

	body, err := r.doPost(apiURL, nil)
	if err != nil {
		return fmt.Errorf("execOnMain failed: %w", err)
	}

	var writeResp struct {
		OperationName         string `json:"operation_name"`
		QueryExecutionStatus  string `json:"query_execution_status"`
		QueryExecutionMessage string `json:"query_execution_message"`
	}
	if err := json.Unmarshal(body, &writeResp); err != nil {
		return fmt.Errorf("parsing write response: %w", err)
	}

	if writeResp.QueryExecutionStatus == "Error" {
		return fmt.Errorf("exec error: %s", writeResp.QueryExecutionMessage)
	}

	if writeResp.OperationName != "" {
		return r.pollOperation(writeResp.OperationName)
	}

	return nil
}

// queryForkBranch runs a read-only SELECT against a specific branch on the fork.
func (r *RemoteDB) queryForkBranch(sql, branch string) (string, error) {
	apiURL := fmt.Sprintf("%s/%s/%s/%s?q=%s",
		DoltHubAPIBase, r.writeOwner, r.writeDB, url.PathEscape(branch), url.QueryEscape(sql))

	body, err := r.doGet(apiURL)
	if err != nil {
		return "", fmt.Errorf("queryForkBranch failed: %w", err)
	}

	return JSONToCSV(body)
}

// parseDiffTables extracts table names from a dolt_diff CSV result.
// Uses csv.Reader to correctly handle quoted table names containing commas.
func parseDiffTables(csvData string) []string {
	reader := csv.NewReader(strings.NewReader(csvData))
	records, err := reader.ReadAll()
	if err != nil || len(records) < 2 {
		return nil
	}
	var tables []string
	for _, record := range records[1:] {
		if len(record) > 0 {
			name := strings.TrimSpace(record[0])
			if name != "" {
				tables = append(tables, name)
			}
		}
	}
	return tables
}

// formatDiffRow formats a single diff row into a human-readable block.
// It pairs from_* and to_* columns to show changes.
func formatDiffRow(buf *strings.Builder, header, fields []string) {
	// Find diff_type column.
	diffType := ""
	id := ""
	for i, col := range header {
		if i >= len(fields) {
			break
		}
		if col == "diff_type" {
			diffType = fields[i]
		}
		if col == "to_id" && fields[i] != "" {
			id = fields[i]
		} else if col == "from_id" && id == "" && fields[i] != "" {
			id = fields[i]
		}
	}

	prefix := "~"
	switch diffType {
	case "added":
		prefix = "+"
	case "removed":
		prefix = "-"
	}
	fmt.Fprintf(buf, "%s %s: id=%s\n", prefix, diffType, id)

	// Show changed fields by pairing from_* and to_* columns.
	fromVals := map[string]string{}
	toVals := map[string]string{}
	for i, col := range header {
		if i >= len(fields) {
			break
		}
		if col == "diff_type" || col == "from_commit" || col == "to_commit" ||
			col == "from_commit_date" || col == "to_commit_date" {
			continue
		}
		if strings.HasPrefix(col, "from_") {
			fromVals[strings.TrimPrefix(col, "from_")] = fields[i]
		} else if strings.HasPrefix(col, "to_") {
			toVals[strings.TrimPrefix(col, "to_")] = fields[i]
		}
	}

	for field, fromVal := range fromVals {
		toVal := toVals[field]
		if fromVal != toVal {
			if fromVal == "" {
				fromVal = "(empty)"
			}
			if toVal == "" {
				toVal = "(empty)"
			}
			fmt.Fprintf(buf, "  %s: %s → %s\n", field, fromVal, toVal)
		}
	}
	// Show fields that only exist in to_ (new fields on added rows).
	for field, toVal := range toVals {
		if _, exists := fromVals[field]; !exists && toVal != "" {
			fmt.Fprintf(buf, "  %s: %s\n", field, toVal)
		}
	}
}

// branchHasData checks whether a wl/ branch has item data worth preserving.
// Branches cleared by discard (no wanted row) should start fresh from main.
func (r *RemoteDB) branchHasData(branch string) bool {
	// Extract wanted ID from wl/{rig}/{wantedID} convention.
	parts := strings.SplitN(branch, "/", 3)
	if len(parts) != 3 || parts[0] != "wl" || parts[2] == "" {
		// Not a wl branch — fall back to branch existence check.
		return r.branchExists(branch)
	}
	wantedID := strings.ReplaceAll(parts[2], "'", "''")
	sql := fmt.Sprintf("SELECT COUNT(*) AS cnt FROM wanted WHERE id='%s'", wantedID)
	csv, err := r.queryForkBranch(sql, branch)
	if err != nil {
		// Branch may not exist, or this could be a transient error.
		// Defaulting to false (start from main) is safe — the write API
		// replays from main, which is correct for a new or missing branch.
		slog.Debug("branchHasData check failed, assuming no data", "branch", branch, "error", err)
		return false
	}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	return len(lines) >= 2 && strings.TrimSpace(lines[1]) != "0"
}

// branchExists checks whether a branch exists on the fork database.
func (r *RemoteDB) branchExists(branch string) bool {
	escaped := strings.ReplaceAll(branch, "'", "''")
	sql := fmt.Sprintf("SELECT COUNT(*) AS cnt FROM dolt_branches WHERE name='%s'", escaped)
	csv, err := r.queryForkBranch(sql, "main")
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	return len(lines) >= 2 && strings.TrimSpace(lines[1]) != "0"
}

// --- HTTP helpers ---

func (r *RemoteDB) doGet(apiURL string) ([]byte, error) {
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("authorization", "token "+r.token)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func (r *RemoteDB) doPost(apiURL string, payload []byte) ([]byte, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequest("POST", apiURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if r.token != "" {
		req.Header.Set("authorization", "token "+r.token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// pollOperation polls a DoltHub async write operation until it completes.
func (r *RemoteDB) pollOperation(operationName string) error {
	backoff := 500 * time.Millisecond
	deadline := time.Now().Add(2 * time.Minute)
	var lastErr error
	consecutiveErrors := 0

	for {
		time.Sleep(backoff)
		if time.Now().After(deadline) {
			break
		}

		apiURL := fmt.Sprintf("%s/%s/%s/write?operationName=%s",
			DoltHubAPIBase, r.writeOwner, r.writeDB, url.QueryEscape(operationName))

		body, err := r.doGet(apiURL)
		if err != nil {
			// DoltHub returns HTTP 400 with toCommitId null when the write
			// produced no changes (e.g. ON DUPLICATE KEY UPDATE with same
			// values). Treat this as a no-op success.
			if strings.Contains(strings.ToLower(err.Error()), "sqlwrite.tocommitid") {
				slog.Debug("treating toCommitId-null as no-op success", "operation", operationName)
				return nil
			}
			lastErr = err
			consecutiveErrors++
			// Fail fast: if every poll attempt errors, don't wait the full 2 minutes.
			if consecutiveErrors >= 5 {
				return fmt.Errorf("polling write operation %q: %w", operationName, lastErr)
			}
			if backoff < 8*time.Second {
				backoff *= 2
			}
			continue
		}
		consecutiveErrors = 0

		var pollResp struct {
			Done       bool `json:"done"`
			ResDetails struct {
				QueryExecutionStatus  string `json:"query_execution_status"`
				QueryExecutionMessage string `json:"query_execution_message"`
			} `json:"res_details"`
			// Legacy flat fields (older API responses).
			QueryExecutionStatus  string `json:"query_execution_status"`
			QueryExecutionMessage string `json:"query_execution_message"`
		}
		if err := json.Unmarshal(body, &pollResp); err == nil {
			// Prefer nested res_details (current API), fall back to flat fields (legacy).
			status := pollResp.ResDetails.QueryExecutionStatus
			message := pollResp.ResDetails.QueryExecutionMessage
			if status == "" {
				status = pollResp.QueryExecutionStatus
				message = pollResp.QueryExecutionMessage
			}

			status = strings.ToLower(status)
			if status == "error" {
				return fmt.Errorf("write operation failed: %s", message)
			}
			if status == "success" || status == "successwithwarning" {
				return nil
			}
			if pollResp.Done {
				if status == "" {
					return fmt.Errorf("write operation %q finished with unknown status", operationName)
				}
				return nil
			}
		}

		if backoff < 8*time.Second {
			backoff *= 2
		}
	}

	if lastErr != nil {
		return fmt.Errorf("timed out waiting for write operation %q (last error: %w)", operationName, lastErr)
	}
	return fmt.Errorf("timed out waiting for write operation %q", operationName)
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
