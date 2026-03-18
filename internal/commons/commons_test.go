package commons

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseSimpleCSV_Empty(t *testing.T) {
	t.Parallel()
	got := parseSimpleCSV("")
	if got != nil {
		t.Errorf("parseSimpleCSV(\"\") = %v, want nil", got)
	}
}

func TestParseSimpleCSV_HeaderOnly(t *testing.T) {
	t.Parallel()
	got := parseSimpleCSV("id,title,status\n")
	if got != nil {
		t.Errorf("parseSimpleCSV(header-only) = %v, want nil", got)
	}
}

func TestParseSimpleCSV_SingleRow(t *testing.T) {
	t.Parallel()
	data := "id,title,status\nw-abc,Fix bug,open"
	got := parseSimpleCSV(data)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0]["id"] != "w-abc" {
		t.Errorf("id = %q, want %q", got[0]["id"], "w-abc")
	}
	if got[0]["title"] != "Fix bug" {
		t.Errorf("title = %q, want %q", got[0]["title"], "Fix bug")
	}
	if got[0]["status"] != "open" {
		t.Errorf("status = %q, want %q", got[0]["status"], "open")
	}
}

func TestParseSimpleCSV_MultiRow(t *testing.T) {
	t.Parallel()
	data := "id,title\nw-1,First\nw-2,Second\nw-3,Third"
	got := parseSimpleCSV(data)
	if len(got) != 3 {
		t.Fatalf("got %d rows, want 3", len(got))
	}
	for i, wantID := range []string{"w-1", "w-2", "w-3"} {
		if got[i]["id"] != wantID {
			t.Errorf("row %d id = %q, want %q", i, got[i]["id"], wantID)
		}
	}
}

func TestParseSimpleCSV_MissingFields(t *testing.T) {
	t.Parallel()
	data := "id,title,status\nw-abc,Fix bug"
	got := parseSimpleCSV(data)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if _, ok := got[0]["status"]; ok {
		t.Error("expected missing 'status' field to not be present")
	}
}

func TestParseSimpleCSV_SkipsBlankLines(t *testing.T) {
	t.Parallel()
	data := "id,title\nw-1,First\n\nw-2,Second\n"
	got := parseSimpleCSV(data)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
}

func TestParseSimpleCSV_TrimsWhitespace(t *testing.T) {
	t.Parallel()
	data := " id , title \n w-abc , Fix bug "
	got := parseSimpleCSV(data)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0]["id"] != "w-abc" {
		t.Errorf("id = %q, want %q", got[0]["id"], "w-abc")
	}
	if got[0]["title"] != "Fix bug" {
		t.Errorf("title = %q, want %q", got[0]["title"], "Fix bug")
	}
}

func TestParseSimpleCSV_MultilineQuotedField(t *testing.T) {
	t.Parallel()
	data := "id,description,priority\nw-abc,\"first line\n\nsecond line\",1\n"
	got := parseSimpleCSV(data)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1", len(got))
	}
	if got[0]["id"] != "w-abc" {
		t.Errorf("id = %q, want %q", got[0]["id"], "w-abc")
	}
	if got[0]["description"] != "first line\n\nsecond line" {
		t.Errorf("description = %q, want multiline field preserved", got[0]["description"])
	}
	if got[0]["priority"] != "1" {
		t.Errorf("priority = %q, want %q", got[0]["priority"], "1")
	}
}

func TestEscapeSQL_SingleQuotes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"it's", "it''s"},
		{"", ""},
		{"'; DROP TABLE wanted;--", "''; DROP TABLE wanted;--"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := EscapeSQL(tt.input)
			if got != tt.want {
				t.Errorf("EscapeSQL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEscapeSQL_Backslashes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{`path\to\file`, `path\\to\\file`},
		{`trailing\`, `trailing\\`},
		{`it\'s`, `it\\''s`},
		{`no special`, `no special`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := EscapeSQL(tt.input)
			if got != tt.want {
				t.Errorf("EscapeSQL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestGenerateWantedID_Format(t *testing.T) {
	t.Parallel()
	id := GenerateWantedID("Test Title")
	if !strings.HasPrefix(id, "w-") {
		t.Errorf("GenerateWantedID() = %q, want prefix 'w-'", id)
	}
	// "w-" + 10 hex chars = 12 chars total
	if len(id) != 12 {
		t.Errorf("GenerateWantedID() length = %d, want 12", len(id))
	}
	hexPart := id[2:]
	for _, c := range hexPart {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			t.Errorf("GenerateWantedID() contains non-hex char %q in %q", string(c), id)
		}
	}
}

func TestIsNothingToCommit_MatchingError(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("error: Nothing to commit")
	if !isNothingToCommit(err) {
		t.Error("isNothingToCommit should return true for matching error")
	}
}

func TestIsNothingToCommit_NonMatchingError(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("some other database error")
	if isNothingToCommit(err) {
		t.Error("isNothingToCommit should return false for non-matching error")
	}
}

func TestIsNothingToCommit_Nil(t *testing.T) {
	t.Parallel()
	if isNothingToCommit(nil) {
		t.Error("isNothingToCommit should return false for nil error")
	}
}

func TestIsNothingToCommit_DoltHubToCommitID(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf(`polling write operation "op-123": HTTP 400: cannot return null for non-nullable field sqlwrite.tocommitid`)
	if !isNothingToCommit(err) {
		t.Error("isNothingToCommit should return true for DoltHub tocommitid error")
	}
}

func TestGenerateWantedID_Uniqueness(t *testing.T) {
	t.Parallel()
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := GenerateWantedID("Same Title")
		if seen[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestCommitSQL_Unsigned(t *testing.T) {
	t.Parallel()
	got := CommitSQL("wl post: Fix bug", false)
	want := "CALL DOLT_COMMIT('-m', 'wl post: Fix bug');\n"
	if got != want {
		t.Errorf("CommitSQL(unsigned) = %q, want %q", got, want)
	}
}

func TestCommitSQL_Signed(t *testing.T) {
	t.Parallel()
	got := CommitSQL("wl post: Fix bug", true)
	want := "CALL DOLT_COMMIT('-S', '-m', 'wl post: Fix bug');\n"
	if got != want {
		t.Errorf("CommitSQL(signed) = %q, want %q", got, want)
	}
}

func TestCommitSQL_EscapesQuotes(t *testing.T) {
	t.Parallel()
	got := CommitSQL("wl post: it's a test", true)
	if !strings.Contains(got, "it''s a test") {
		t.Errorf("commitSQL did not escape single quotes: %q", got)
	}
	if !strings.Contains(got, "'-S'") {
		t.Errorf("commitSQL missing -S flag: %q", got)
	}
}

func TestFormatTagsJSON(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{"empty", nil, "NULL"},
		{"single tag", []string{"go"}, `'["go"]'`},
		{"multiple tags", []string{"go", "auth"}, `'["go","auth"]'`},
		{"single quote", []string{"it's"}, `'["it''s"]'`},
		{"double quote", []string{`say "hello"`}, `'["say \"hello\""]'`},
		{"backslash", []string{`path\to`}, `'["path\\to"]'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatTagsJSON(tt.tags)
			if got != tt.want {
				t.Errorf("formatTagsJSON(%v) = %s, want %s", tt.tags, got, tt.want)
			}
		})
	}
}

func TestBuildBrowseQuery_MyItems(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, MyItems: "my-rig"}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "(posted_by = 'my-rig' OR claimed_by = 'my-rig')") {
		t.Errorf("MyItems should produce OR clause, got:\n%s", q)
	}
	// MyItems should suppress separate PostedBy/ClaimedBy.
	if strings.Contains(q, "AND posted_by =") || strings.Contains(q, "AND claimed_by =") {
		t.Error("MyItems should suppress separate posted_by/claimed_by conditions")
	}
}

func TestBuildBrowseQuery_MyItems_OverridesPostedClaimedBy(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, MyItems: "my-rig", PostedBy: "other", ClaimedBy: "other"}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "(posted_by = 'my-rig' OR claimed_by = 'my-rig')") {
		t.Errorf("MyItems should take priority, got:\n%s", q)
	}
	if strings.Contains(q, "posted_by = 'other'") {
		t.Error("PostedBy should be ignored when MyItems is set")
	}
}

func TestBuildBrowseQuery_SortPriority(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, Sort: SortPriority}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "ORDER BY priority ASC, created_at DESC") {
		t.Errorf("SortPriority should order by priority, got:\n%s", q)
	}
}

func TestBuildBrowseQuery_SortNewest(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, Sort: SortNewest}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "ORDER BY created_at DESC") {
		t.Errorf("SortNewest should order by created_at DESC, got:\n%s", q)
	}
	if strings.Contains(q, "priority ASC") {
		t.Error("SortNewest should not include priority ordering")
	}
}

func TestBuildBrowseQuery_SortAlpha(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, Sort: SortAlpha}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "ORDER BY title ASC") {
		t.Errorf("SortAlpha should order by title ASC, got:\n%s", q)
	}
}

func TestBuildBrowseQuery_PriorityFilter(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: 1}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "priority = 1") {
		t.Errorf("Priority=1 should filter by priority, got:\n%s", q)
	}
}

func TestBuildBrowseQuery_Long(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, Long: true}
	q := BuildBrowseQuery(f)
	if !strings.Contains(q, "description") {
		t.Errorf("Long=true should include description column, got:\n%s", q)
	}
}

func TestBuildBrowseQuery_NotLong(t *testing.T) {
	t.Parallel()
	f := BrowseFilter{Priority: -1, Long: false}
	q := BuildBrowseQuery(f)
	if strings.Contains(q, "description") {
		t.Errorf("Long=false should not include description column, got:\n%s", q)
	}
}

func TestValidPriorities(t *testing.T) {
	t.Parallel()
	got := ValidPriorities()
	want := []int{-1, 0, 1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestPriorityLabel(t *testing.T) {
	t.Parallel()
	if got := PriorityLabel(-1); got != "all" {
		t.Errorf("PriorityLabel(-1) = %q, want %q", got, "all")
	}
	if got := PriorityLabel(0); got != "P0" {
		t.Errorf("PriorityLabel(0) = %q, want %q", got, "P0")
	}
	if got := PriorityLabel(3); got != "P3" {
		t.Errorf("PriorityLabel(3) = %q, want %q", got, "P3")
	}
}

func TestSortLabel(t *testing.T) {
	t.Parallel()
	if got := SortLabel(SortPriority); got != "priority" {
		t.Errorf("SortLabel(SortPriority) = %q", got)
	}
	if got := SortLabel(SortNewest); got != "newest" {
		t.Errorf("SortLabel(SortNewest) = %q", got)
	}
	if got := SortLabel(SortAlpha); got != "alpha" {
		t.Errorf("SortLabel(SortAlpha) = %q", got)
	}
}

func TestFormatTagsJSON_RoundTrip(t *testing.T) {
	t.Parallel()
	tags := []string{"it's", "go", `say "hi"`}
	result := formatTagsJSON(tags)
	// Strip SQL quoting: outer single quotes and unescape ''
	inner := result[1 : len(result)-1]
	inner = strings.ReplaceAll(inner, "''", "'")
	parsed := parseTagsJSON(inner)
	if len(parsed) != len(tags) {
		t.Fatalf("round-trip got %d tags, want %d", len(parsed), len(tags))
	}
	for i, want := range tags {
		if parsed[i] != want {
			t.Errorf("tag[%d] = %q, want %q", i, parsed[i], want)
		}
	}
}

// --- AcceptUpstreamDML tests ---

func testAcceptUpstreamDML() []string {
	stamp := &Stamp{
		ID:          "s-test",
		Author:      "alice",
		Subject:     "charlie",
		Quality:     4,
		Reliability: 3,
		Severity:    "medium",
		ContextID:   "c-test",
		ContextType: "completion",
		SkillTags:   []string{"go", "sql"},
		Message:     "great work",
	}
	return AcceptUpstreamDML("w-1", "c-test", "charlie", "https://proof.example.com", "alice", "hop://alice", stamp)
}

func TestAcceptUpstreamDML_StatementCount(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	if len(stmts) != 5 {
		t.Fatalf("expected 5 statements, got %d", len(stmts))
	}
}

func TestAcceptUpstreamDML_DeleteCompletion(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	s := stmts[0]
	if !strings.HasPrefix(s, "DELETE FROM completions") {
		t.Errorf("stmt[0] should be DELETE FROM completions, got %s", s)
	}
	if !strings.Contains(s, "wanted_id='w-1'") {
		t.Errorf("stmt[0] missing wanted_id, got %s", s)
	}
}

func TestAcceptUpstreamDML_InsertCompletion(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	s := stmts[1]
	if !strings.HasPrefix(s, "INSERT IGNORE INTO completions") {
		t.Errorf("stmt[1] should be INSERT IGNORE INTO completions, got %s", s)
	}
	if !strings.Contains(s, "'c-test'") {
		t.Errorf("stmt[1] missing completion ID, got %s", s)
	}
	if !strings.Contains(s, "'charlie'") {
		t.Errorf("stmt[1] missing completed_by, got %s", s)
	}
	if !strings.Contains(s, "https://proof.example.com") {
		t.Errorf("stmt[1] missing evidence, got %s", s)
	}
}

func TestAcceptUpstreamDML_UpdateWanted(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	s := stmts[2]
	if !strings.HasPrefix(s, "UPDATE wanted SET") {
		t.Errorf("stmt[2] should be UPDATE wanted SET, got %s", s)
	}
	if !strings.Contains(s, "status='completed'") {
		t.Errorf("stmt[2] missing status=completed, got %s", s)
	}
	if !strings.Contains(s, "claimed_by='charlie'") {
		t.Errorf("stmt[2] missing claimed_by=charlie, got %s", s)
	}
	// No status precondition in WHERE
	if strings.Contains(s, "AND status=") {
		t.Errorf("stmt[2] should not have status precondition in WHERE, got %s", s)
	}
}

func TestAcceptUpstreamDML_InsertStamp(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	s := stmts[3]
	if !strings.HasPrefix(s, "INSERT INTO stamps") {
		t.Errorf("stmt[3] should be INSERT INTO stamps, got %s", s)
	}
	if !strings.Contains(s, "'s-test'") {
		t.Errorf("stmt[3] missing stamp ID, got %s", s)
	}
	if !strings.Contains(s, "'alice'") {
		t.Errorf("stmt[3] missing author, got %s", s)
	}
}

func TestAcceptUpstreamDML_UpdateCompletion(t *testing.T) {
	t.Parallel()
	stmts := testAcceptUpstreamDML()
	s := stmts[4]
	if !strings.HasPrefix(s, "UPDATE completions SET") {
		t.Errorf("stmt[4] should be UPDATE completions SET, got %s", s)
	}
	if !strings.Contains(s, "validated_by='alice'") {
		t.Errorf("stmt[4] missing validated_by, got %s", s)
	}
	if !strings.Contains(s, "stamp_id='s-test'") {
		t.Errorf("stmt[4] missing stamp_id, got %s", s)
	}
}
