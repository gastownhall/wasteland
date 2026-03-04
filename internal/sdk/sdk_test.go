package sdk

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/julianknutsen/wasteland/internal/commons"
)

// --- fakeDB: in-memory commons.DB for SDK tests ---

type fakeItem struct {
	ID          string
	Title       string
	Description string
	Project     string
	Type        string
	Priority    int
	PostedBy    string
	ClaimedBy   string
	Status      string
	EffortLevel string
	CreatedAt   string
	UpdatedAt   string
}

type fakeCompletion struct {
	ID          string
	WantedID    string
	CompletedBy string
	Evidence    string
	StampID     string
	ValidatedBy string
}

type fakeStamp struct {
	ID          string
	Author      string
	Subject     string
	Valence     string
	Severity    string
	ContextID   string
	ContextType string
	SkillTags   string
	Message     string
}

type fakeDB struct {
	mu          sync.Mutex
	items       map[string]*fakeItem
	completions map[string]*fakeCompletion // keyed by wanted_id
	stamps      map[string]*fakeStamp
	branches    map[string]bool                 // active branches
	branchItems map[string]map[string]*fakeItem // branch -> id -> item (branch-specific state)

	pushCalls       int
	pushBranchCalls []string
	pushMainCalls   int
	syncCalls       int
	execCalls       []execCall
}

type execCall struct {
	Branch    string
	CommitMsg string
	Stmts     []string
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		items:       make(map[string]*fakeItem),
		completions: make(map[string]*fakeCompletion),
		stamps:      make(map[string]*fakeStamp),
		branches:    make(map[string]bool),
		branchItems: make(map[string]map[string]*fakeItem),
	}
}

func (f *fakeDB) seedItem(item fakeItem) {
	f.items[item.ID] = &item
}

// csvQuote wraps a value in double-quotes if it contains commas, quotes, or newlines.
func csvQuote(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// Query returns CSV-formatted data matching the SQL request.
func (f *fakeDB) Query(sql, ref string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Determine which item(s) to return based on the SQL and ref.
	switch {
	case strings.Contains(sql, "FROM wanted") && strings.Contains(sql, "WHERE id"):
		return f.queryWantedByID(sql, ref)
	case strings.Contains(sql, "FROM wanted"):
		return f.queryWantedBrowse(sql, ref)
	case strings.Contains(sql, "FROM completions"):
		return f.queryCompletion(sql, ref)
	case strings.Contains(sql, "FROM stamps"):
		return f.queryStamp(sql, ref)
	default:
		return "id\n", nil
	}
}

func (f *fakeDB) queryWantedByID(sql, ref string) (string, error) { //nolint:unparam // error return needed for interface consistency
	id := extractWhereID(sql)
	item := f.resolveItem(id, ref)
	if item == nil {
		// Return header only (no rows).
		if strings.Contains(sql, "description") {
			return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\n", nil
		}
		if strings.Contains(sql, "claimed_by") && !strings.Contains(sql, "description") && !strings.Contains(sql, "title") {
			return "status,claimed_by\n", nil
		}
		return "status\n", nil
	}

	if strings.Contains(sql, "SELECT status FROM") {
		return fmt.Sprintf("status\n%s\n", item.Status), nil
	}
	if strings.Contains(sql, "SELECT status,") || (strings.Contains(sql, "SELECT status") && !strings.Contains(sql, "title")) {
		return fmt.Sprintf("status,claimed_by\n%s,%s\n", item.Status, item.ClaimedBy), nil
	}
	return f.itemDetailCSV(item), nil
}

func (f *fakeDB) queryWantedBrowse(sql, ref string) (string, error) { //nolint:unparam // error return needed for interface consistency
	items := f.resolveItems(ref)
	var rows []string
	long := strings.Contains(sql, "description")
	header := "id,title,project,type,priority,posted_by,claimed_by,status,effort_level"
	if long {
		header = "id,title,description,project,type,priority,posted_by,claimed_by,status,effort_level"
	}

	for _, item := range items {
		if !f.matchesFilter(item, sql) {
			continue
		}
		if long {
			rows = append(rows, fmt.Sprintf("%s,%s,%s,%s,%s,%d,%s,%s,%s,%s",
				item.ID, csvQuote(item.Title), csvQuote(item.Description), item.Project, item.Type, item.Priority,
				item.PostedBy, item.ClaimedBy, item.Status, item.EffortLevel))
		} else {
			rows = append(rows, fmt.Sprintf("%s,%s,%s,%s,%d,%s,%s,%s,%s",
				item.ID, csvQuote(item.Title), item.Project, item.Type, item.Priority,
				item.PostedBy, item.ClaimedBy, item.Status, item.EffortLevel))
		}
	}
	if len(rows) == 0 {
		return header + "\n", nil
	}
	return header + "\n" + strings.Join(rows, "\n") + "\n", nil
}

func (f *fakeDB) matchesFilter(item *fakeItem, sql string) bool {
	if s := extractEqValue(sql, "status"); s != "" && item.Status != s {
		return false
	}
	if s := extractEqValue(sql, "claimed_by"); s != "" && item.ClaimedBy != s {
		return false
	}
	if s := extractEqValue(sql, "posted_by"); s != "" && item.PostedBy != s {
		return false
	}
	return true
}

func (f *fakeDB) queryCompletion(sql, _ string) (string, error) { //nolint:unparam // error return needed for Query dispatch
	wid := extractEqValue(sql, "wanted_id")
	c, ok := f.completions[wid]
	if !ok {
		return "id,wanted_id,completed_by,evidence,stamp_id,validated_by\n", nil
	}
	return fmt.Sprintf("id,wanted_id,completed_by,evidence,stamp_id,validated_by\n%s,%s,%s,%s,%s,%s\n",
		c.ID, c.WantedID, c.CompletedBy, csvQuote(c.Evidence), c.StampID, c.ValidatedBy), nil
}

func (f *fakeDB) queryStamp(sql, _ string) (string, error) { //nolint:unparam // error return needed for Query dispatch
	sid := extractWhereID(sql)
	s, ok := f.stamps[sid]
	if !ok {
		return "id,author,subject,valence,severity,context_id,context_type,skill_tags,message\n", nil
	}
	return fmt.Sprintf("id,author,subject,valence,severity,context_id,context_type,skill_tags,message\n%s,%s,%s,%s,%s,%s,%s,%s,%s\n",
		s.ID, s.Author, s.Subject, csvQuote(s.Valence), s.Severity, s.ContextID, s.ContextType, csvQuote(s.SkillTags), csvQuote(s.Message)), nil
}

// Exec applies DML and tracks calls. Interprets basic mutations.
func (f *fakeDB) Exec(branch, commitMsg string, _ bool, stmts ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.execCalls = append(f.execCalls, execCall{Branch: branch, CommitMsg: commitMsg, Stmts: stmts})

	if branch != "" {
		f.branches[branch] = true
		if _, ok := f.branchItems[branch]; !ok {
			// Clone main items to branch.
			f.branchItems[branch] = make(map[string]*fakeItem)
			for id, item := range f.items {
				cp := *item
				f.branchItems[branch][id] = &cp
			}
		}
	}

	anyChanged := false
	for _, stmt := range stmts {
		if f.applyDML(stmt, branch) {
			anyChanged = true
		}
	}
	if !anyChanged {
		return fmt.Errorf("nothing to commit")
	}
	return nil
}

func (f *fakeDB) applyDML(stmt, branch string) bool {
	target := f.items
	if branch != "" {
		target = f.branchItems[branch]
	}

	lower := strings.ToLower(stmt)
	switch {
	case strings.HasPrefix(lower, "update wanted set"):
		return f.applyUpdateWanted(stmt, target)
	case strings.HasPrefix(lower, "update completions set"):
		return f.applyUpdateCompletions(stmt)
	case strings.HasPrefix(lower, "insert") && strings.Contains(lower, "into wanted"):
		return f.applyInsertWanted(stmt, target)
	case strings.HasPrefix(lower, "insert") && strings.Contains(lower, "into completions"):
		return f.applyInsertCompletion(stmt, target)
	case strings.HasPrefix(lower, "insert") && strings.Contains(lower, "into stamps"):
		return f.applyInsertStamp(stmt)
	case strings.HasPrefix(lower, "delete from completions"):
		wid := extractEqValue(stmt, "wanted_id")
		if _, ok := f.completions[wid]; ok {
			delete(f.completions, wid)
			return true
		}
		return false
	case strings.HasPrefix(lower, "delete from wanted"):
		id := extractEqValue(stmt, "id")
		if _, ok := target[id]; ok {
			delete(target, id)
			return true
		}
		return false
	}
	return false
}

// applyUpdateWanted handles UPDATE wanted SET ... WHERE ... with WHERE validation.
func (f *fakeDB) applyUpdateWanted(stmt string, target map[string]*fakeItem) bool {
	where := extractWhereClause(stmt)
	id := extractEqValue(where, "id")
	item, ok := target[id]
	if !ok {
		return false
	}
	if !matchesWhere(item, where) {
		return false
	}

	// Extract just the SET clause (between "set" and "where") to avoid
	// matching status values in the WHERE condition.
	lower := strings.ToLower(stmt)
	setClause := lower
	if wi := strings.Index(lower, " where "); wi > 0 {
		setClause = lower[:wi]
	}

	changed := false
	switch {
	case strings.Contains(setClause, "status='claimed'"):
		item.Status = "claimed"
		if cb := extractSetValue(stmt, "claimed_by"); cb != "" {
			item.ClaimedBy = cb
		}
		changed = true
	case strings.Contains(setClause, "status='open'"):
		item.Status = "open"
		item.ClaimedBy = ""
		changed = true
	case strings.Contains(setClause, "status='in_review'"):
		item.Status = "in_review"
		changed = true
	case strings.Contains(setClause, "status='completed'"):
		item.Status = "completed"
		changed = true
	case strings.Contains(setClause, "status='withdrawn'"):
		item.Status = "withdrawn"
		changed = true
	}

	// Handle non-status field updates (title, description, etc. from UpdateWantedDML).
	if t := extractSetValue(stmt, "title"); t != "" {
		item.Title = t
		changed = true
	}
	if d := extractSetValue(stmt, "description"); d != "" {
		item.Description = d
		changed = true
	}
	if p := extractSetValue(stmt, "project"); p != "" {
		item.Project = p
		changed = true
	}
	if e := extractSetValue(stmt, "effort_level"); e != "" {
		item.EffortLevel = e
		changed = true
	}
	return changed
}

// applyUpdateCompletions handles UPDATE completions SET ... WHERE id='...'.
func (f *fakeDB) applyUpdateCompletions(stmt string) bool {
	where := extractWhereClause(stmt)
	cid := extractEqValue(where, "id")
	for _, c := range f.completions {
		if c.ID == cid {
			if vb := extractSetValue(stmt, "validated_by"); vb != "" {
				c.ValidatedBy = vb
			}
			if sid := extractSetValue(stmt, "stamp_id"); sid != "" {
				c.StampID = sid
			}
			return true
		}
	}
	return false
}

// applyInsertWanted handles INSERT INTO wanted (...) VALUES (...).
func (f *fakeDB) applyInsertWanted(stmt string, target map[string]*fakeItem) bool {
	values := extractInsertValues(stmt)
	if len(values) < 12 {
		return false
	}
	id := values[0]
	if _, exists := target[id]; exists {
		return false
	}
	priority := 2
	_, _ = fmt.Sscanf(values[5], "%d", &priority)
	target[id] = &fakeItem{
		ID:          id,
		Title:       values[1],
		Description: values[2],
		Project:     values[3],
		Type:        values[4],
		Priority:    priority,
		PostedBy:    values[7],
		Status:      values[8],
		EffortLevel: values[9],
		CreatedAt:   values[10],
		UpdatedAt:   values[11],
	}
	return true
}

// applyInsertCompletion handles INSERT IGNORE INTO completions (...) SELECT ... FROM wanted WHERE ...
// Validates the WHERE subquery conditions (item must be in_review, claimed_by must match).
func (f *fakeDB) applyInsertCompletion(stmt string, target map[string]*fakeItem) bool {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "select ")
	if idx < 0 {
		return false
	}
	fromIdx := strings.Index(lower[idx:], " from ")
	if fromIdx < 0 {
		return false
	}
	selectClause := stmt[idx+7 : idx+fromIdx]
	parts := strings.SplitN(selectClause, ",", 6)
	if len(parts) < 4 {
		return false
	}
	cid := strings.Trim(strings.TrimSpace(parts[0]), "'")
	wid := strings.Trim(strings.TrimSpace(parts[1]), "'")
	completedBy := strings.Trim(strings.TrimSpace(parts[2]), "'")
	evidence := strings.Trim(strings.TrimSpace(parts[3]), "'")

	// Check WHERE conditions from the SELECT subquery.
	whereIdx := strings.Index(lower[idx:], " where ")
	if whereIdx >= 0 {
		where := stmt[idx+whereIdx+7:]
		item := target[wid]
		if item == nil {
			return false
		}
		if reqStatus := extractEqValue(where, "status"); reqStatus != "" {
			if item.Status != reqStatus {
				return false
			}
		}
		if reqClaimed := extractEqValue(where, "claimed_by"); reqClaimed != "" {
			if item.ClaimedBy != reqClaimed {
				return false
			}
		}
	}

	// NOT EXISTS: skip if completion already exists for this wanted ID.
	if _, exists := f.completions[wid]; exists {
		return false
	}

	f.completions[wid] = &fakeCompletion{
		ID:          cid,
		WantedID:    wid,
		CompletedBy: completedBy,
		Evidence:    evidence,
	}
	return true
}

// applyInsertStamp handles INSERT INTO stamps (...) VALUES (...).
func (f *fakeDB) applyInsertStamp(stmt string) bool {
	values := extractInsertValues(stmt)
	if len(values) < 10 {
		return false
	}
	sid := values[0]
	if _, exists := f.stamps[sid]; exists {
		return false
	}
	f.stamps[sid] = &fakeStamp{
		ID:          values[0],
		Author:      values[1],
		Subject:     values[2],
		Valence:     values[3],
		Severity:    values[5],
		ContextID:   values[6],
		ContextType: values[7],
		SkillTags:   values[8],
		Message:     values[9],
	}
	return true
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

func (f *fakeDB) PushBranch(_ string, _ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushBranchCalls = append(f.pushBranchCalls, "pushed")
	return nil
}

func (f *fakeDB) PushMain(_ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushMainCalls++
	return nil
}

func (f *fakeDB) Sync() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls++
	return nil
}

func (f *fakeDB) MergeBranch(branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Apply branch items to main.
	if bi, ok := f.branchItems[branch]; ok {
		for id, item := range bi {
			cp := *item
			f.items[id] = &cp
		}
	}
	return nil
}

func (f *fakeDB) DeleteRemoteBranch(_ string) error { return nil }

func (f *fakeDB) PushWithSync(_ io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pushCalls++
	return nil
}

func (f *fakeDB) CanWildWest() error { return nil }

// resolveItem returns the item from branch or main.
// Non-existent branches return nil (matching DoltHub 404 behavior).
func (f *fakeDB) resolveItem(id, ref string) *fakeItem {
	if ref != "" && ref != "main" {
		if !f.branches[ref] {
			return nil // branch doesn't exist
		}
		if bi, ok := f.branchItems[ref]; ok {
			if item, ok := bi[id]; ok {
				return item
			}
		}
		// Branch exists but hasn't modified this item — inherit from main.
	}
	return f.items[id]
}

// resolveItems returns all items from the appropriate ref.
func (f *fakeDB) resolveItems(ref string) map[string]*fakeItem {
	if ref != "" && ref != "main" {
		if bi, ok := f.branchItems[ref]; ok {
			return bi
		}
	}
	return f.items
}

func (f *fakeDB) itemDetailCSV(item *fakeItem) string {
	header := "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at"
	row := fmt.Sprintf("%s,%s,%s,%s,%s,%d,,%s,%s,%s,%s,%s,%s",
		item.ID, csvQuote(item.Title), csvQuote(item.Description), item.Project, item.Type,
		item.Priority, item.PostedBy, item.ClaimedBy, item.Status,
		item.EffortLevel, item.CreatedAt, item.UpdatedAt)
	return header + "\n" + row + "\n"
}

// --- helpers for parsing SQL strings in tests ---

func extractWhereID(sql string) string {
	return extractEqValue(sql, "id")
}

func extractEqValue(sql, field string) string {
	// Find field='...' or field = '...'
	patterns := []string{field + "='", field + " = '", field + "= '", field + " ='"}
	for _, pat := range patterns {
		idx := strings.Index(sql, pat)
		if idx >= 0 {
			rest := sql[idx+len(pat):]
			end := strings.Index(rest, "'")
			if end >= 0 {
				return rest[:end]
			}
		}
	}
	return ""
}

func extractSetValue(sql, field string) string {
	// Find field='...' in SET clause.
	return extractEqValue(sql, field)
}

// extractWhereClause returns the portion of a SQL statement after " WHERE ".
func extractWhereClause(stmt string) string {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, " where ")
	if idx < 0 {
		return ""
	}
	return stmt[idx+7:]
}

// matchesWhere checks whether a fakeItem satisfies the WHERE conditions in a clause.
// Supports id, status, and claimed_by conditions.
func matchesWhere(item *fakeItem, where string) bool {
	if id := extractEqValue(where, "id"); id != "" {
		if item.ID != id {
			return false
		}
	}
	if status := extractEqValue(where, "status"); status != "" {
		if item.Status != status {
			return false
		}
	}
	if claimedBy := extractEqValue(where, "claimed_by"); claimedBy != "" {
		if item.ClaimedBy != claimedBy {
			return false
		}
	}
	return true
}

// extractInsertValues extracts values from a VALUES (...) clause,
// stripping single quotes and converting NULL to empty string.
func extractInsertValues(stmt string) []string {
	lower := strings.ToLower(stmt)
	idx := strings.Index(lower, "values (")
	if idx < 0 {
		idx = strings.Index(lower, "values(")
		if idx < 0 {
			return nil
		}
		idx += 7
	} else {
		idx += 8
	}

	rest := stmt[idx:]
	depth := 1
	end := -1
	inQuote := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
		if ch == '\'' && !inQuote {
			inQuote = true
			continue
		}
		if ch == '\'' && inQuote {
			if i+1 < len(rest) && rest[i+1] == '\'' {
				i++
				continue
			}
			inQuote = false
			continue
		}
		if inQuote {
			continue
		}
		if ch == '(' {
			depth++
		}
		if ch == ')' {
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
	}
	if end < 0 {
		return nil
	}
	return splitValues(rest[:end])
}

// splitValues splits a comma-separated SQL values list, respecting single-quote boundaries.
func splitValues(s string) []string {
	var values []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '\'' && !inQuote {
			inQuote = true
			continue
		}
		if ch == '\'' && inQuote {
			if i+1 < len(s) && s[i+1] == '\'' {
				current.WriteByte('\'')
				i++
				continue
			}
			inQuote = false
			continue
		}
		if ch == ',' && !inQuote {
			values = append(values, strings.TrimSpace(current.String()))
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	values = append(values, strings.TrimSpace(current.String()))
	for i, v := range values {
		if strings.EqualFold(v, "NULL") {
			values[i] = ""
		}
	}
	return values
}

// compile-time check
var _ commons.DB = (*fakeDB)(nil)

// --- Tests ---

func TestBrowse_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})
	db.seedItem(fakeItem{ID: "w-2", Title: "Add feature", Status: "claimed", Priority: 2, ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	result, err := c.Browse(commons.BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result.Items))
	}
}

func TestBrowse_WithStatusFilter(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, EffortLevel: "medium"})
	db.seedItem(fakeItem{ID: "w-2", Title: "Add feature", Status: "claimed", Priority: 2, ClaimedBy: "bob", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	result, err := c.Browse(commons.BrowseFilter{Status: "open"})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result.Items))
	}
	if result.Items[0].ID != "w-1" {
		t.Errorf("expected w-1, got %s", result.Items[0].ID)
	}
}

func TestBrowse_Long(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{
		ID: "w-1", Title: "infer: what is 1+1", Status: "open", Priority: 2,
		Type: "inference", Description: `{"prompt":"what is 1+1","model":"llama3.2:1b","seed":42}`,
		EffortLevel: "medium",
	})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	// Without Long: description should be empty.
	result, err := c.Browse(commons.BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if result.Items[0].Description != "" {
		t.Errorf("expected empty description without Long, got %q", result.Items[0].Description)
	}

	// With Long: description should be populated.
	result, err = c.Browse(commons.BrowseFilter{Long: true})
	if err != nil {
		t.Fatalf("Browse Long: %v", err)
	}
	if result.Items[0].Description == "" {
		t.Error("expected description with Long, got empty")
	}
	if !strings.Contains(result.Items[0].Description, "llama3.2:1b") {
		t.Errorf("description should contain model, got %q", result.Items[0].Description)
	}
}

func TestBrowse_PendingClaimedBy_Single(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})
	db.seedItem(fakeItem{ID: "w-2", Title: "Add feature", Status: "claimed", ClaimedBy: "bob", Priority: 2, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItems: func() (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {{RigHandle: "charlie", Status: "claimed", ClaimedBy: "charlie"}},
			}, nil
		},
	})

	result, err := c.Browse(commons.BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	for _, item := range result.Items {
		switch item.ID {
		case "w-1":
			if item.ClaimedBy != "charlie (pending)" {
				t.Errorf("w-1: expected ClaimedBy='charlie (pending)', got %q", item.ClaimedBy)
			}
			// Status should be promoted from "open" to "claimed" to match the pending claim.
			if item.Status != "claimed" {
				t.Errorf("w-1: expected Status='claimed', got %q", item.Status)
			}
		case "w-2":
			// Already claimed on main — should not be overwritten.
			if item.ClaimedBy != "bob" {
				t.Errorf("w-2: expected ClaimedBy='bob', got %q", item.ClaimedBy)
			}
		}
	}
}

func TestBrowse_PendingFurthestState(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItems: func() (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {
					{RigHandle: "charlie", Status: "claimed", ClaimedBy: "charlie"},
					{RigHandle: "dave", Status: "in_review", ClaimedBy: "dave"},
				},
			}, nil
		},
	})

	result, err := c.Browse(commons.BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	for _, item := range result.Items {
		if item.ID == "w-1" {
			// Furthest state is "in_review" from dave.
			if item.Status != "in_review" {
				t.Errorf("w-1: expected Status='in_review' (furthest), got %q", item.Status)
			}
			if item.ClaimedBy != "dave (pending)" {
				t.Errorf("w-1: expected ClaimedBy='dave (pending)', got %q", item.ClaimedBy)
			}
			// Badge count should be 2.
			if result.PendingIDs["w-1"] != 2 {
				t.Errorf("expected PendingIDs[w-1]=2, got %d", result.PendingIDs["w-1"])
			}
			return
		}
	}
	t.Error("w-1 not found in results")
}

func TestBrowse_PendingClaimedBy_PreservesExisting(t *testing.T) {
	db := newFakeDB()
	// In PR mode, branch overlays set ClaimedBy before upstream merge.
	// Verify upstream doesn't overwrite branch-overlay ClaimedBy.
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})
	db.branches["wl/bob/w-1"] = true
	db.branchItems["wl/bob/w-1"] = map[string]*fakeItem{
		"w-1": {ID: "w-1", Title: "Fix bug", Status: "claimed", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"},
	}

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "bob",
		Mode:      "pr",
		ListPendingItems: func() (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {{RigHandle: "charlie", Status: "claimed", ClaimedBy: "charlie"}},
			}, nil
		},
	})

	result, err := c.Browse(commons.BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}

	for _, item := range result.Items {
		if item.ID == "w-1" {
			// Branch overlay set ClaimedBy="bob"; upstream with same rank should NOT overwrite.
			if item.ClaimedBy != "bob" {
				t.Errorf("w-1: expected ClaimedBy='bob' (from branch overlay), got %q", item.ClaimedBy)
			}
			return
		}
	}
	t.Error("w-1 not found in results")
}

func TestDetail_UpstreamPRs(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "bob",
		Mode:      "wild-west",
		ListPendingItems: func() (map[string][]PendingItem, error) {
			return map[string][]PendingItem{
				"w-1": {
					{RigHandle: "charlie", Status: "claimed", Branch: "wl/charlie/w-1", PRURL: "https://example.com/pr/1"},
				},
			}, nil
		},
	})

	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(result.UpstreamPRs) != 1 {
		t.Fatalf("expected 1 upstream PR, got %d", len(result.UpstreamPRs))
	}
	if result.UpstreamPRs[0].RigHandle != "charlie" {
		t.Errorf("expected rig_handle=charlie, got %q", result.UpstreamPRs[0].RigHandle)
	}
	if result.UpstreamPRs[0].PRURL != "https://example.com/pr/1" {
		t.Errorf("expected PRURL, got %q", result.UpstreamPRs[0].PRURL)
	}
}

func TestDetail_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "wild-west"})

	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.Item == nil {
		t.Fatal("expected item, got nil")
	}
	if result.Item.ID != "w-1" {
		t.Errorf("expected w-1, got %s", result.Item.ID)
	}
	if result.Branch != "" {
		t.Errorf("expected no branch in wild-west, got %q", result.Branch)
	}
}

func TestDetail_PRMode(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if result.Item == nil {
		t.Fatal("expected item, got nil")
	}
	if result.Item.Status != "open" {
		t.Errorf("expected open, got %s", result.Item.Status)
	}
}

func TestDashboard(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "My task", Status: "claimed", ClaimedBy: "alice", PostedBy: "bob", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	data, err := c.Dashboard()
	if err != nil {
		t.Fatalf("Dashboard: %v", err)
	}
	if len(data.Claimed) != 1 {
		t.Errorf("expected 1 claimed item, got %d", len(data.Claimed))
	}
}

func TestClaim_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "wild-west"})

	result, err := c.Claim("w-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if result.Detail == nil {
		t.Fatal("expected detail in result")
	}
	if result.Detail.Item.Status != "claimed" {
		t.Errorf("expected claimed, got %s", result.Detail.Item.Status)
	}
	if db.pushCalls != 1 {
		t.Errorf("expected 1 push, got %d", db.pushCalls)
	}
}

func TestClaim_PRMode(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	result, err := c.Claim("w-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if result.Detail.Item.Status != "claimed" {
		t.Errorf("expected claimed, got %s", result.Detail.Item.Status)
	}
	if result.Detail.Branch == "" {
		t.Error("expected branch in PR mode")
	}
	if len(db.pushBranchCalls) != 1 {
		t.Errorf("expected 1 branch push, got %d", len(db.pushBranchCalls))
	}
}

func TestUnclaim_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "claimed", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "wild-west"})

	result, err := c.Unclaim("w-1")
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	if result.Detail.Item.Status != "open" {
		t.Errorf("expected open, got %s", result.Detail.Item.Status)
	}
}

func TestReject_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})
	db.completions["w-1"] = &fakeCompletion{ID: "c-1", WantedID: "w-1", CompletedBy: "bob"}

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	result, err := c.Reject("w-1", "needs more work")
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if result.Detail.Item.Status != "claimed" {
		t.Errorf("expected claimed, got %s", result.Detail.Item.Status)
	}
}

func TestClose_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	result, err := c.Close("w-1")
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if result.Detail.Item.Status != "completed" {
		t.Errorf("expected completed, got %s", result.Detail.Item.Status)
	}
}

func TestDelete_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})

	result, err := c.Delete("w-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if result.Detail.Item.Status != "withdrawn" {
		t.Errorf("expected withdrawn, got %s", result.Detail.Item.Status)
	}
}

func TestPRAutoCleanup(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "claimed", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	// Unclaim reverts to open, which matches main status if we set main to open.
	// First, claim on PR branch, then unclaim — but the item starts as "claimed" on main.
	// Unclaim makes it "open". Since main was "claimed", they differ, so no auto-cleanup.
	result, err := c.Unclaim("w-1")
	if err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	// Main was "claimed", branch is now "open" — different, so branch stays.
	if result.Detail.Branch == "" {
		t.Error("expected branch to remain (statuses differ)")
	}
}

func TestApplyBranch(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", PostedBy: "alice", EffortLevel: "medium"})
	db.branches["wl/bob/w-1"] = true
	db.branchItems["wl/bob/w-1"] = map[string]*fakeItem{
		"w-1": {ID: "w-1", Title: "Fix bug", Status: "claimed", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"},
	}

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	if err := c.ApplyBranch("wl/bob/w-1"); err != nil {
		t.Fatalf("ApplyBranch: %v", err)
	}
	// Branch should be deleted.
	if db.branches["wl/bob/w-1"] {
		t.Error("expected branch to be deleted")
	}
	// Main should have merged state.
	if db.items["w-1"].Status != "claimed" {
		t.Errorf("expected claimed on main, got %s", db.items["w-1"].Status)
	}
	if db.pushMainCalls != 1 {
		t.Errorf("expected 1 push main, got %d", db.pushMainCalls)
	}
}

func TestDiscardBranch(t *testing.T) {
	db := newFakeDB()
	db.branches["wl/bob/w-1"] = true

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	if err := c.DiscardBranch("wl/bob/w-1"); err != nil {
		t.Fatalf("DiscardBranch: %v", err)
	}
	if db.branches["wl/bob/w-1"] {
		t.Error("expected branch to be deleted")
	}
}

func TestSync(t *testing.T) {
	db := newFakeDB()
	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "wild-west"})

	if err := c.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if db.syncCalls != 1 {
		t.Errorf("expected 1 sync, got %d", db.syncCalls)
	}
}

func TestSaveSettings(t *testing.T) {
	var savedMode string
	var savedSigning bool

	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "bob",
		Mode:      "wild-west",
		SaveConfig: func(mode string, signing bool) error {
			savedMode = mode
			savedSigning = signing
			return nil
		},
	})

	if err := c.SaveSettings("pr", true); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}
	if savedMode != "pr" {
		t.Errorf("expected pr, got %s", savedMode)
	}
	if !savedSigning {
		t.Error("expected signing=true")
	}
	if c.Mode() != "pr" {
		t.Errorf("expected client mode updated to pr, got %s", c.Mode())
	}
}

func TestSaveSettings_NilCallback(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "bob", Mode: "wild-west"})

	if err := c.SaveSettings("pr", true); err == nil {
		t.Error("expected error when SaveConfig is nil")
	}
}

func TestSubmitPR(t *testing.T) {
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "bob",
		Mode:      "pr",
		CreatePR: func(_ string) (string, error) {
			return "https://example.com/pr/1", nil
		},
	})

	url, err := c.SubmitPR("wl/bob/w-1")
	if err != nil {
		t.Fatalf("SubmitPR: %v", err)
	}
	if url != "https://example.com/pr/1" {
		t.Errorf("expected PR URL, got %s", url)
	}
}

func TestBranchDiff(t *testing.T) {
	c := New(ClientConfig{
		DB:        newFakeDB(),
		RigHandle: "bob",
		Mode:      "pr",
		LoadDiff: func(_ string) (string, error) {
			return "diff content", nil
		},
	})

	diff, err := c.BranchDiff("wl/bob/w-1")
	if err != nil {
		t.Fatalf("BranchDiff: %v", err)
	}
	if diff != "diff content" {
		t.Errorf("expected diff content, got %s", diff)
	}
}

func TestBranchActions_PRMode_NoPR(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	// Claim creates a branch with a delta (open → claimed).
	result, err := c.Claim("w-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	d := result.Detail
	if d.Branch == "" {
		t.Fatal("expected branch")
	}
	if d.Delta == "" {
		t.Fatal("expected delta")
	}
	// PR mode + delta + no PR → submit_pr, discard
	if len(d.BranchActions) != 2 {
		t.Fatalf("expected 2 branch actions, got %v", d.BranchActions)
	}
	if d.BranchActions[0] != "submit_pr" {
		t.Errorf("expected submit_pr, got %s", d.BranchActions[0])
	}
	if d.BranchActions[1] != "discard" {
		t.Errorf("expected discard, got %s", d.BranchActions[1])
	}
}

func TestBranchActions_PRMode_WithPR(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "bob",
		Mode:      "pr",
		CheckPR: func(_ string) string {
			return "https://example.com/pr/1"
		},
	})

	// Claim creates a branch with a delta.
	result, err := c.Claim("w-1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	d := result.Detail
	// PR mode + delta + existing PR → discard only
	if len(d.BranchActions) != 1 {
		t.Fatalf("expected 1 branch action, got %v", d.BranchActions)
	}
	if d.BranchActions[0] != "discard" {
		t.Errorf("expected discard, got %s", d.BranchActions[0])
	}
}

func TestBranchActions_WildWest(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "wild-west"})

	// Wild-west Detail doesn't produce branches, so no branch actions.
	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(result.BranchActions) != 0 {
		t.Errorf("expected no branch actions in wild-west, got %v", result.BranchActions)
	}
}

func TestBranchActions_NoBranch(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", Priority: 1, PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "bob", Mode: "pr"})

	// No branch exists, so no branch actions.
	result, err := c.Detail("w-1")
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(result.BranchActions) != 0 {
		t.Errorf("expected no branch actions, got %v", result.BranchActions)
	}
}

func TestDelete_PR_BranchOnly_CleansUpBranch(t *testing.T) {
	db := newFakeDB()
	// Item only exists on branch, NOT on main.
	db.branches["wl/alice/w-1"] = true
	db.branchItems["wl/alice/w-1"] = map[string]*fakeItem{
		"w-1": {ID: "w-1", Title: "New thing", Status: "open", PostedBy: "alice", EffortLevel: "medium"},
	}

	createPRCalled := false
	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "pr",
		CreatePR: func(_ string) (string, error) {
			createPRCalled = true
			return "https://example.com/pr/1", nil
		},
	})

	result, err := c.Delete("w-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Branch should be cleaned up.
	if db.branches["wl/alice/w-1"] {
		t.Error("expected branch to be deleted")
	}
	// Should NOT have created a PR.
	if createPRCalled {
		t.Error("should NOT create a PR for branch-only delete")
	}
	// cleanupBranch should NOT exec data deletion before deleting the branch —
	// that creates divergent commits which are counterproductive for remote backends.
	if len(db.execCalls) != 0 {
		t.Errorf("expected 0 exec calls (branch delete handles cleanup), got %d", len(db.execCalls))
	}
	// Hint should indicate branch cleanup.
	if result.Hint == "" {
		t.Error("expected a hint about branch cleanup")
	}
}

func TestDelete_PR_ExistsOnMain_CommitsWithdrawal(t *testing.T) {
	db := newFakeDB()
	// Item exists on main — delete should proceed normally (commit withdrawn).
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "open", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "pr"})

	result, err := c.Delete("w-1")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if result.Detail == nil || result.Detail.Item == nil {
		t.Fatal("expected detail with item")
	}
	if result.Detail.Item.Status != "withdrawn" {
		t.Errorf("expected withdrawn, got %s", result.Detail.Item.Status)
	}
}

func TestMode(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "bob", Mode: "pr"})
	if c.Mode() != "pr" {
		t.Errorf("expected pr, got %s", c.Mode())
	}
}

func TestRigHandle(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "bob"})
	if c.RigHandle() != "bob" {
		t.Errorf("expected bob, got %s", c.RigHandle())
	}
}
