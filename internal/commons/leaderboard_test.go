package commons

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// fakeDB implements DB for leaderboard tests.
type fakeDB struct {
	queries []string
	results map[string]string // sql substring -> CSV output
	err     error
}

func (f *fakeDB) Query(sql, _ string) (string, error) {
	f.queries = append(f.queries, sql)
	if f.err != nil {
		return "", f.err
	}
	for key, val := range f.results {
		if strings.Contains(sql, key) {
			return val, nil
		}
	}
	return "", nil
}

func (f *fakeDB) Exec(_, _ string, _ bool, _ ...string) error { return nil }
func (f *fakeDB) Branches(_ string) ([]string, error)         { return nil, nil }
func (f *fakeDB) DeleteBranch(_ string) error                 { return nil }
func (f *fakeDB) PushBranch(_ string, _ io.Writer) error      { return nil }
func (f *fakeDB) PushMain(_ io.Writer) error                  { return nil }
func (f *fakeDB) Sync() error                                 { return nil }
func (f *fakeDB) MergeBranch(_ string) error                  { return nil }
func (f *fakeDB) DeleteRemoteBranch(_ string) error           { return nil }
func (f *fakeDB) PushAllRemotes(_ io.Writer) error            { return nil }
func (f *fakeDB) CanWildWest() error                          { return nil }

func TestQueryLeaderboard_BasicRanking(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,5,4.2,3.8,3.0\nbob,3,4.0,4.5,2.5\n",
		"IN (":     "completed_by,skill_tags\n",
	}}
	entries, err := QueryLeaderboard(db, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].RigHandle != "alice" {
		t.Errorf("first entry = %q, want alice", entries[0].RigHandle)
	}
	if entries[0].Completions != 5 {
		t.Errorf("alice completions = %d, want 5", entries[0].Completions)
	}
	if entries[1].Completions != 3 {
		t.Errorf("bob completions = %d, want 3", entries[1].Completions)
	}
}

func TestQueryLeaderboard_Empty(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\n",
	}}
	entries, err := QueryLeaderboard(db, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("got %v, want nil", entries)
	}
}

func TestQueryLeaderboard_DefaultLimit(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\n",
	}}
	_, _ = QueryLeaderboard(db, 0)
	if len(db.queries) == 0 {
		t.Fatal("no queries executed")
	}
	if !strings.Contains(db.queries[0], "LIMIT 20") {
		t.Errorf("expected LIMIT 20 for zero limit, got: %s", db.queries[0])
	}
}

func TestQueryLeaderboard_CapsLimit(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\n",
	}}
	_, _ = QueryLeaderboard(db, 99999)
	if len(db.queries) == 0 {
		t.Fatal("no queries executed")
	}
	if !strings.Contains(db.queries[0], fmt.Sprintf("LIMIT %d", maxLeaderboardLimit)) {
		t.Errorf("expected LIMIT %d for excessive limit, got: %s", maxLeaderboardLimit, db.queries[0])
	}
}

func TestQueryLeaderboard_QueryError(t *testing.T) {
	t.Parallel()
	db := &fakeDB{err: fmt.Errorf("db down")}
	_, err := QueryLeaderboard(db, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "db down") {
		t.Errorf("error = %q, want to contain 'db down'", err.Error())
	}
}

func TestQueryLeaderboard_ParseError(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,not-a-number,4.0,3.0,2.0\n",
	}}
	_, err := QueryLeaderboard(db, 10)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parsing completions") {
		t.Errorf("error = %q, want to mention 'parsing completions'", err.Error())
	}
}

func TestQueryLeaderboard_WithSkills(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,3,4.0,3.5,3.0\n",
		"IN (":     "completed_by,skill_tags\nalice,\"[\"\"go\"\",\"\"sql\"\"]\"\nalice,\"[\"\"go\"\",\"\"testing\"\"]\"\n",
	}}
	entries, err := QueryLeaderboard(db, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	// "go" appears twice, "sql" and "testing" once each.
	if len(entries[0].TopSkills) == 0 {
		t.Fatal("expected skills, got none")
	}
	if entries[0].TopSkills[0] != "go" {
		t.Errorf("top skill = %q, want 'go'", entries[0].TopSkills[0])
	}
}

func TestQueryLeaderboard_BulkSkillQuery(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,3,4.0,3.5,3.0\nbob,2,3.0,3.0,2.5\n",
		"IN (":     "completed_by,skill_tags\n",
	}}
	_, err := QueryLeaderboard(db, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be exactly 2 queries: one for leaderboard, one for skills (bulk).
	if len(db.queries) != 2 {
		t.Errorf("expected 2 queries (bulk), got %d", len(db.queries))
	}
}

func TestQueryLeaderboard_MalformedSkillTags(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,3,4.0,3.5,3.0\n",
		"IN (":     "completed_by,skill_tags\nalice,not-valid-json\n",
	}}
	// Malformed skill_tags should be silently skipped, not cause an error.
	entries, err := QueryLeaderboard(db, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if len(entries[0].TopSkills) != 0 {
		t.Errorf("expected no skills for malformed tags, got %v", entries[0].TopSkills)
	}
}

func TestQueryLeaderboard_SkillsUseSameJoinPath(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY": "completed_by,completions,avg_quality,avg_reliability,avg_creativity\nalice,3,4.0,3.5,3.0\n",
		"IN (":     "completed_by,skill_tags\n",
	}}
	_, _ = QueryLeaderboard(db, 10)
	// The skills query should use stamp_id join (same as main), not context_id.
	if len(db.queries) < 2 {
		t.Fatal("expected at least 2 queries")
	}
	skillQuery := db.queries[1]
	if !strings.Contains(skillQuery, "c.stamp_id = s.id") {
		t.Errorf("skills query should use stamp_id join, got: %s", skillQuery)
	}
	if strings.Contains(skillQuery, "context_id") {
		t.Errorf("skills query should NOT use context_id join, got: %s", skillQuery)
	}
	if !strings.Contains(skillQuery, "ORDER BY c.completed_by") {
		t.Errorf("skills query should ORDER BY c.completed_by, got: %s", skillQuery)
	}
}

func TestTopNKeys_DeterministicTieBreaking(t *testing.T) {
	t.Parallel()
	freq := map[string]int{
		"go":      3,
		"python":  3,
		"rust":    3,
		"java":    1,
		"testing": 2,
	}
	// Run multiple times to verify determinism.
	var first []string
	for i := 0; i < 20; i++ {
		result := topNKeys(freq, 3)
		if first == nil {
			first = result
		}
		if len(result) != 3 {
			t.Fatalf("got %d keys, want 3", len(result))
		}
		for j := range first {
			if result[j] != first[j] {
				t.Fatalf("nondeterministic: run %d got %v, run 0 got %v", i, result, first)
			}
		}
	}
	// Tied keys (go, python, rust) should be sorted alphabetically.
	if first[0] != "go" || first[1] != "python" || first[2] != "rust" {
		t.Errorf("expected [go python rust], got %v", first)
	}
}

func TestTopNKeys_Empty(t *testing.T) {
	t.Parallel()
	result := topNKeys(map[string]int{}, 5)
	if result != nil {
		t.Errorf("got %v, want nil", result)
	}
}

func TestTopNKeys_FewerThanN(t *testing.T) {
	t.Parallel()
	freq := map[string]int{"a": 1, "b": 2}
	result := topNKeys(freq, 5)
	if len(result) != 2 {
		t.Fatalf("got %d keys, want 2", len(result))
	}
	if result[0] != "b" {
		t.Errorf("first = %q, want 'b'", result[0])
	}
}
