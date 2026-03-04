package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/julianknutsen/wasteland/internal/commons"
	"github.com/julianknutsen/wasteland/internal/sdk"
)

// --- fakeDB for API tests ---

type fakeItem struct {
	id, title, description, project, typ, postedBy, claimedBy, status, effortLevel string
	priority                                                                       int
}

type fakeDB struct {
	mu              sync.Mutex
	items           map[string]*fakeItem
	completions     map[string]string // wanted_id -> completion_id
	branches        map[string]bool
	branchItems     map[string]map[string]*fakeItem
	leaderboardCSV  string            // CSV response for leaderboard aggregation query
	leaderSkillsCSV string            // CSV response for leaderboard skills query
	results         map[string]string // generic: sql substring -> CSV output
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		items:       make(map[string]*fakeItem),
		completions: make(map[string]string),
		branches:    make(map[string]bool),
		branchItems: make(map[string]map[string]*fakeItem),
	}
}

func (f *fakeDB) Query(sql, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Generic results map takes priority (used by scoreboard tests).
	for key, val := range f.results {
		if strings.Contains(sql, key) {
			return val, nil
		}
	}

	switch {
	case strings.Contains(sql, "FROM wanted") && strings.Contains(sql, "WHERE id"):
		return f.queryByID(sql, ref)
	case strings.Contains(sql, "FROM wanted"):
		return f.queryBrowse(sql, ref)
	case strings.Contains(sql, "FROM completions") && strings.Contains(sql, "GROUP BY"):
		if f.leaderboardCSV != "" {
			return f.leaderboardCSV, nil
		}
		return "completed_by,completions,avg_quality,avg_reliability\n", nil
	case strings.Contains(sql, "FROM completions") && strings.Contains(sql, "skill_tags"):
		if f.leaderSkillsCSV != "" {
			return f.leaderSkillsCSV, nil
		}
		return "completed_by,skill_tags\n", nil
	case strings.Contains(sql, "FROM completions"):
		return f.queryCompletion(sql)
	case strings.Contains(sql, "FROM stamps"):
		return "id,author,subject,valence,severity,context_id,context_type,skill_tags,message\n", nil
	default:
		return "id\n", nil
	}
}

func (f *fakeDB) queryByID(sql, ref string) (string, error) { //nolint:unparam // error return needed by caller
	id := extractVal(sql, "id='")
	item := f.resolve(id, ref)
	if item == nil {
		if strings.Contains(sql, "description") {
			return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\n", nil
		}
		if strings.Contains(sql, "SELECT status FROM") {
			return "status\n", nil
		}
		return "status,claimed_by\n", nil
	}
	if strings.Contains(sql, "SELECT status FROM") {
		return fmt.Sprintf("status\n%s\n", item.status), nil
	}
	if strings.Contains(sql, "SELECT status,") || (strings.Contains(sql, "SELECT status") && !strings.Contains(sql, "title")) {
		return fmt.Sprintf("status,claimed_by\n%s,%s\n", item.status, item.claimedBy), nil
	}
	return f.detailCSV(item), nil
}

func (f *fakeDB) queryBrowse(sql, ref string) (string, error) { //nolint:unparam // error return needed by caller
	hdr := "id,title,project,type,priority,posted_by,claimed_by,status,effort_level"
	items := f.resolveAll(ref)
	var rows []string
	for _, it := range items {
		if s := extractVal(sql, "status = '"); s != "" && it.status != s {
			continue
		}
		if s := extractVal(sql, "claimed_by = '"); s != "" && it.claimedBy != s {
			continue
		}
		if s := extractVal(sql, "posted_by = '"); s != "" && it.postedBy != s {
			continue
		}
		rows = append(rows, fmt.Sprintf("%s,%s,%s,%s,%d,%s,%s,%s,%s",
			it.id, it.title, it.project, it.typ, it.priority, it.postedBy, it.claimedBy, it.status, it.effortLevel))
	}
	if len(rows) == 0 {
		return hdr + "\n", nil
	}
	return hdr + "\n" + strings.Join(rows, "\n") + "\n", nil
}

func (f *fakeDB) queryCompletion(sql string) (string, error) { //nolint:unparam // error return needed by caller
	wid := extractVal(sql, "wanted_id='")
	cid, ok := f.completions[wid]
	if !ok {
		return "id,wanted_id,completed_by,evidence,stamp_id,validated_by\n", nil
	}
	return fmt.Sprintf("id,wanted_id,completed_by,evidence,stamp_id,validated_by\n%s,%s,bob,http://example.com,,\n", cid, wid), nil
}

func (f *fakeDB) detailCSV(it *fakeItem) string {
	hdr := "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at"
	row := fmt.Sprintf("%s,%s,%s,%s,%s,%d,,%s,%s,%s,%s,,",
		it.id, it.title, it.description, it.project, it.typ, it.priority, it.postedBy, it.claimedBy, it.status, it.effortLevel)
	return hdr + "\n" + row + "\n"
}

func (f *fakeDB) Exec(branch, _ string, _ bool, stmts ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if branch != "" {
		f.branches[branch] = true
		if _, ok := f.branchItems[branch]; !ok {
			f.branchItems[branch] = make(map[string]*fakeItem)
			for id, it := range f.items {
				cp := *it
				f.branchItems[branch][id] = &cp
			}
		}
	}

	for _, stmt := range stmts {
		f.applyDML(stmt, branch)
	}
	return nil
}

func (f *fakeDB) applyDML(stmt, branch string) {
	target := f.items
	if branch != "" {
		target = f.branchItems[branch]
	}

	lower := strings.ToLower(stmt)
	if !strings.HasPrefix(lower, "update wanted set") {
		return
	}
	id := extractVal(stmt, "id='")
	it, ok := target[id]
	if !ok {
		return
	}
	setClause := lower
	if wi := strings.Index(lower, " where "); wi > 0 {
		setClause = lower[:wi]
	}
	switch {
	case strings.Contains(setClause, "status='claimed'"):
		it.status = "claimed"
		if cb := extractVal(stmt, "claimed_by='"); cb != "" {
			it.claimedBy = cb
		}
	case strings.Contains(setClause, "status='open'"):
		it.status = "open"
		it.claimedBy = ""
	case strings.Contains(setClause, "status='in_review'"):
		it.status = "in_review"
	case strings.Contains(setClause, "status='completed'"):
		it.status = "completed"
	case strings.Contains(setClause, "status='withdrawn'"):
		it.status = "withdrawn"
	}
}

func (f *fakeDB) Branches(prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []string
	for b := range f.branches {
		if strings.HasPrefix(b, prefix) {
			result = append(result, b)
		}
	}
	return result, nil
}

func (f *fakeDB) DeleteBranch(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.branches, name)
	delete(f.branchItems, name)
	return nil
}

func (f *fakeDB) PushBranch(_ string, _ io.Writer) error { return nil }
func (f *fakeDB) PushMain(_ io.Writer) error             { return nil }
func (f *fakeDB) Sync() error                            { return nil }
func (f *fakeDB) MergeBranch(_ string) error             { return nil }
func (f *fakeDB) DeleteRemoteBranch(_ string) error      { return nil }
func (f *fakeDB) PushWithSync(_ io.Writer) error         { return nil }
func (f *fakeDB) CanWildWest() error                     { return nil }

func (f *fakeDB) resolve(id, ref string) *fakeItem {
	if ref != "" && ref != "main" {
		if bi, ok := f.branchItems[ref]; ok {
			if it, ok := bi[id]; ok {
				return it
			}
		}
	}
	return f.items[id]
}

func (f *fakeDB) resolveAll(ref string) map[string]*fakeItem {
	if ref != "" && ref != "main" {
		if bi, ok := f.branchItems[ref]; ok {
			return bi
		}
	}
	return f.items
}

func extractVal(s, prefix string) string {
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(prefix):]
	end := strings.Index(rest, "'")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

var _ commons.DB = (*fakeDB)(nil)

// --- Test helpers ---

func newTestServer(db *fakeDB, mode string) *httptest.Server {
	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      mode,
		SaveConfig: func(_ string, _ bool) error {
			return nil
		},
	})
	srv := New(client)
	return httptest.NewServer(srv)
}

func newTestClient(db *fakeDB) *sdk.Client {
	return sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		SaveConfig: func(_ string, _ bool) error {
			return nil
		},
	})
}

func getJSON(t *testing.T, ts *httptest.Server, path string, v any) *http.Response {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return resp
}

func postJSON(t *testing.T, ts *httptest.Server, path, body string, v any) *http.Response {
	t.Helper()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return resp
}

func doRequest(t *testing.T, ts *httptest.Server, method, path, body string, v any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	if v != nil {
		if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return resp
}

// --- Tests ---

func TestBrowse(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", priority: 1, postedBy: "alice", effortLevel: "medium"}
	db.items["w-2"] = &fakeItem{id: "w-2", title: "Add feature", status: "claimed", priority: 2, claimedBy: "bob", postedBy: "alice", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp BrowseResponse
	r := getJSON(t, ts, "/api/wanted", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
}

func TestBrowseWithFilter(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", priority: 1, effortLevel: "medium"}
	db.items["w-2"] = &fakeItem{id: "w-2", title: "Add feature", status: "claimed", priority: 2, claimedBy: "bob", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp BrowseResponse
	r := getJSON(t, ts, "/api/wanted?status=open", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if resp.Items[0].ID != "w-1" {
		t.Errorf("expected w-1, got %s", resp.Items[0].ID)
	}
}

func TestDetail(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", priority: 1, postedBy: "bob", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp DetailResponse
	r := getJSON(t, ts, "/api/wanted/w-1", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Item == nil {
		t.Fatal("expected item, got nil")
	}
	if resp.Item.ID != "w-1" {
		t.Errorf("expected w-1, got %s", resp.Item.ID)
	}
	if len(resp.Actions) == 0 {
		t.Error("expected available actions")
	}
}

func TestDetailNotFound(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp ErrorResponse
	r := getJSON(t, ts, "/api/wanted/w-nonexistent", &resp)
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", r.StatusCode)
	}
	if resp.Error == "" {
		t.Error("expected error message")
	}
}

func TestDashboard(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "My task", status: "claimed", claimedBy: "alice", postedBy: "bob", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp DashboardResponse
	r := getJSON(t, ts, "/api/dashboard", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if len(resp.Claimed) != 1 {
		t.Errorf("expected 1 claimed, got %d", len(resp.Claimed))
	}
}

func TestConfig(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "pr")
	defer ts.Close()

	var resp ConfigResponse
	r := getJSON(t, ts, "/api/config", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.RigHandle != "alice" {
		t.Errorf("expected alice, got %s", resp.RigHandle)
	}
	if resp.Mode != "pr" {
		t.Errorf("expected pr, got %s", resp.Mode)
	}
}

func TestClaim(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", priority: 1, postedBy: "bob", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp MutationResponse
	r := postJSON(t, ts, "/api/wanted/w-1/claim", "", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Detail == nil || resp.Detail.Item == nil {
		t.Fatal("expected detail in response")
	}
	if resp.Detail.Item.Status != "claimed" {
		t.Errorf("expected claimed, got %s", resp.Detail.Item.Status)
	}
}

func TestUnclaim(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "claimed", claimedBy: "alice", postedBy: "bob", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp MutationResponse
	r := postJSON(t, ts, "/api/wanted/w-1/unclaim", "", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Detail.Item.Status != "open" {
		t.Errorf("expected open, got %s", resp.Detail.Item.Status)
	}
}

func TestClose(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "in_review", claimedBy: "bob", postedBy: "alice", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp MutationResponse
	r := postJSON(t, ts, "/api/wanted/w-1/close", "", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Detail.Item.Status != "completed" {
		t.Errorf("expected completed, got %s", resp.Detail.Item.Status)
	}
}

func TestDelete(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "open", postedBy: "alice", effortLevel: "medium"}

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp MutationResponse
	r := doRequest(t, ts, "DELETE", "/api/wanted/w-1", "", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Detail.Item.Status != "withdrawn" {
		t.Errorf("expected withdrawn, got %s", resp.Detail.Item.Status)
	}
}

func TestReject(t *testing.T) {
	db := newFakeDB()
	db.items["w-1"] = &fakeItem{id: "w-1", title: "Fix bug", status: "in_review", claimedBy: "bob", postedBy: "alice", effortLevel: "medium"}
	db.completions["w-1"] = "c-1"

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp MutationResponse
	r := postJSON(t, ts, "/api/wanted/w-1/reject", `{"reason":"needs work"}`, &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp.Detail.Item.Status != "claimed" {
		t.Errorf("expected claimed, got %s", resp.Detail.Item.Status)
	}
}

func TestSync(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp map[string]string
	r := postJSON(t, ts, "/api/sync", "", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp["status"] != "synced" {
		t.Errorf("expected synced, got %s", resp["status"])
	}
}

func TestSaveSettings(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp map[string]string
	r := doRequest(t, ts, "PUT", "/api/settings", `{"mode":"pr","signing":true}`, &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if resp["status"] != "saved" {
		t.Errorf("expected saved, got %s", resp["status"])
	}
}

func TestSaveSettingsInvalidMode(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp ErrorResponse
	r := doRequest(t, ts, "PUT", "/api/settings", `{"mode":"invalid"}`, &resp)
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", r.StatusCode)
	}
}

func TestCORSMiddleware(t *testing.T) {
	db := newFakeDB()
	client := sdk.New(sdk.ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	srv := New(client)
	handler := CORSMiddleware(srv)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	req, _ := http.NewRequest("OPTIONS", ts.URL+"/api/wanted", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck // test cleanup

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected 204, got %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS Allow-Origin header")
	}
}

func TestLeaderboard(t *testing.T) {
	db := newFakeDB()
	db.leaderboardCSV = "completed_by,completions,avg_quality,avg_reliability\nalice,5,4.2,3.8\nbob,3,4.0,4.5\n"
	db.leaderSkillsCSV = "completed_by,skill_tags\nalice,\"[\"\"go\"\",\"\"sql\"\"]\"\nbob,\"[\"\"testing\"\"]\"\n"

	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp LeaderboardResponse
	r := getJSON(t, ts, "/api/leaderboard", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].RigHandle != "alice" {
		t.Errorf("first entry = %q, want alice", resp.Entries[0].RigHandle)
	}
	if resp.Entries[0].Completions != 5 {
		t.Errorf("alice completions = %d, want 5", resp.Entries[0].Completions)
	}
	if len(resp.Entries[0].TopSkills) != 2 {
		t.Errorf("alice top_skills count = %d, want 2", len(resp.Entries[0].TopSkills))
	}
	if len(resp.Entries[1].TopSkills) != 1 {
		t.Errorf("bob top_skills count = %d, want 1", len(resp.Entries[1].TopSkills))
	}
}

func TestLeaderboard_Empty(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	var resp LeaderboardResponse
	r := getJSON(t, ts, "/api/leaderboard", &resp)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", r.StatusCode)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(resp.Entries))
	}
}

func TestLeaderboard_InvalidLimit(t *testing.T) {
	db := newFakeDB()
	ts := newTestServer(db, "wild-west")
	defer ts.Close()

	// Negative limit is silently coerced to default (20), matching handleBrowse pattern.
	var resp LeaderboardResponse
	r := getJSON(t, ts, "/api/leaderboard?limit=-5", &resp)
	if r.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for negative limit (coerced to default), got %d", r.StatusCode)
	}
}
