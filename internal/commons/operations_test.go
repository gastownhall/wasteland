package commons

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type execCall struct {
	branch    string
	commitMsg string
	signed    bool
	stmts     []string
}

type operationsTestDB struct {
	queryFunc func(sql, ref string) (string, error)
	execFunc  func(branch, commitMsg string, signed bool, stmts ...string) error
	execCalls []execCall
}

func (db *operationsTestDB) Query(sql, ref string) (string, error) {
	if db.queryFunc != nil {
		return db.queryFunc(sql, ref)
	}
	return "", nil
}

func (db *operationsTestDB) Exec(branch, commitMsg string, signed bool, stmts ...string) error {
	db.execCalls = append(db.execCalls, execCall{
		branch:    branch,
		commitMsg: commitMsg,
		signed:    signed,
		stmts:     append([]string(nil), stmts...),
	})
	if db.execFunc != nil {
		return db.execFunc(branch, commitMsg, signed, stmts...)
	}
	return nil
}

func (db *operationsTestDB) Branches(string) ([]string, error) { return nil, nil }
func (db *operationsTestDB) DeleteBranch(string) error         { return nil }
func (db *operationsTestDB) PushBranch(string, io.Writer) error {
	return nil
}
func (db *operationsTestDB) PushMain(io.Writer) error        { return nil }
func (db *operationsTestDB) Sync() error                     { return nil }
func (db *operationsTestDB) MergeBranch(string) error        { return nil }
func (db *operationsTestDB) DeleteRemoteBranch(string) error { return nil }
func (db *operationsTestDB) PushWithSync(io.Writer) error    { return nil }
func (db *operationsTestDB) CanWildWest() error              { return nil }

func TestGeneratePrefixedID_Format(t *testing.T) {
	t.Parallel()

	got := GeneratePrefixedID("c", "w-1", "alice", "proof")
	if !strings.HasPrefix(got, "c-") {
		t.Fatalf("GeneratePrefixedID() = %q, want c- prefix", got)
	}
	if len(got) != len("c-")+16 {
		t.Fatalf("GeneratePrefixedID() length = %d, want %d", len(got), len("c-")+16)
	}
	for _, ch := range got[2:] {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			t.Fatalf("GeneratePrefixedID() contains non-hex char %q in %q", string(ch), got)
		}
	}
}

func TestInsertWantedDML_UsesDefaultsAndOptionalFields(t *testing.T) {
	t.Parallel()

	dml, err := InsertWantedDML(&WantedItem{
		ID:          "w-1",
		Title:       "Fix it's bug",
		Description: "Detailed description",
		Project:     "gascity",
		Type:        "bug",
		Priority:    1,
		Tags:        []string{"go", "auth"},
		PostedBy:    "alice",
	})
	if err != nil {
		t.Fatalf("InsertWantedDML() error = %v", err)
	}
	for _, want := range []string{
		"'w-1'",
		"'Fix it''s bug'",
		"'Detailed description'",
		"'gascity'",
		"'bug'",
		`'["go","auth"]'`,
		"'alice'",
		"'open'",
		"'medium'",
	} {
		if !strings.Contains(dml, want) {
			t.Fatalf("InsertWantedDML() missing %q in %s", want, dml)
		}
	}
}

func TestInsertWantedDML_ValidatesRequiredFields(t *testing.T) {
	t.Parallel()

	if _, err := InsertWantedDML(&WantedItem{Title: "Missing ID"}); err == nil {
		t.Fatal("expected error for missing ID")
	}
	if _, err := InsertWantedDML(&WantedItem{ID: "w-1"}); err == nil {
		t.Fatal("expected error for missing title")
	}
}

func TestWLCommons_WrapperMethodsDelegateToConfiguredDB(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		queryFunc: func(sql, _ string) (string, error) {
			switch {
			case strings.Contains(sql, "FROM completions"):
				return "id,wanted_id,completed_by,evidence,stamp_id,validated_by\nc-1,w-1,alice,proof,s-1,bob\n", nil
			case strings.Contains(sql, "FROM stamps"):
				return "id,author,subject,valence,severity,context_id,context_type,skill_tags,message\ns-1,bob,alice,\"{\"\"quality\"\":4,\"\"reliability\"\":5}\",medium,w-1,wanted,\"[\"\"go\"\"]\",solid work\n", nil
			case strings.Contains(sql, "description"):
				return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\nw-1,Task desc,Details,gascity,bug,1,\"[\"\"go\"\"]\",alice,bob,claimed,small,2026-03-01,2026-03-02\n", nil
			default:
				return "id,title,status,claimed_by,posted_by\nw-1,Task desc,claimed,bob,alice\n", nil
			}
		},
	}

	store := NewWLCommons(db)
	store.SetSigning(true)
	store.SetHopURI("hop://alice")

	if _, err := store.QueryWanted("w-1"); err != nil {
		t.Fatalf("QueryWanted() error = %v", err)
	}
	if _, err := store.QueryWantedDetail("w-1"); err != nil {
		t.Fatalf("QueryWantedDetail() error = %v", err)
	}
	if _, err := store.QueryCompletion("w-1"); err != nil {
		t.Fatalf("QueryCompletion() error = %v", err)
	}
	if _, err := store.QueryStamp("s-1"); err != nil {
		t.Fatalf("QueryStamp() error = %v", err)
	}

	if err := store.InsertWanted(&WantedItem{ID: "w-1", Title: "Task desc"}); err != nil {
		t.Fatalf("InsertWanted() error = %v", err)
	}
	if err := store.ClaimWanted("w-1", "alice"); err != nil {
		t.Fatalf("ClaimWanted() error = %v", err)
	}
	if err := store.UnclaimWanted("w-1"); err != nil {
		t.Fatalf("UnclaimWanted() error = %v", err)
	}
	if err := store.SubmitCompletion("c-1", "w-1", "alice", "proof"); err != nil {
		t.Fatalf("SubmitCompletion() error = %v", err)
	}
	if err := store.AcceptCompletion("w-1", "c-1", "bob", &Stamp{ID: "s-1", Subject: "alice", Quality: 4, Reliability: 5, Severity: "medium"}); err != nil {
		t.Fatalf("AcceptCompletion() error = %v", err)
	}
	if err := store.UpdateWanted("w-1", &WantedUpdate{Title: "New title"}); err != nil {
		t.Fatalf("UpdateWanted() error = %v", err)
	}
	if err := store.RejectCompletion("w-1", "bob", "not enough evidence"); err != nil {
		t.Fatalf("RejectCompletion() error = %v", err)
	}
	if err := store.CloseWanted("w-1"); err != nil {
		t.Fatalf("CloseWanted() error = %v", err)
	}
	if err := store.DeleteWanted("w-1"); err != nil {
		t.Fatalf("DeleteWanted() error = %v", err)
	}

	if len(db.execCalls) != 9 {
		t.Fatalf("len(execCalls) = %d, want 9", len(db.execCalls))
	}
	for _, call := range db.execCalls {
		if !call.signed {
			t.Fatalf("call %+v should be signed", call)
		}
	}
	if got := db.execCalls[3].commitMsg; got != "wl done: w-1" {
		t.Fatalf("submit commitMsg = %q, want wl done: w-1", got)
	}
	if !strings.Contains(strings.Join(db.execCalls[3].stmts, "\n"), "hop://alice") {
		t.Fatalf("submit completion statements should include hop URI: %+v", db.execCalls[3].stmts)
	}
}

func TestMutationFunctions_MapNothingToCommitToConflict(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		execFunc: func(string, string, bool, ...string) error {
			return errors.New("nothing to commit")
		},
	}
	stamp := &Stamp{ID: "s-1", Subject: "alice", Quality: 4, Reliability: 5, Severity: "medium"}

	tests := []struct {
		name string
		fn   func() error
		want string
	}{
		{name: "claim", fn: func() error { return ClaimWanted(db, "w-1", "alice", false) }, want: `wanted item "w-1" is not open or does not exist`},
		{name: "unclaim", fn: func() error { return UnclaimWanted(db, "w-1", false) }, want: `wanted item "w-1" is not claimed or does not exist`},
		{name: "submit", fn: func() error { return SubmitCompletion(db, "c-1", "w-1", "alice", "proof", "", false) }, want: `wanted item "w-1" is not claimed by "alice" or does not exist`},
		{name: "accept", fn: func() error { return AcceptCompletion(db, "w-1", "c-1", "bob", "", stamp, false) }, want: `wanted item "w-1" is not in_review or does not exist`},
		{name: "update", fn: func() error { return UpdateWanted(db, "w-1", &WantedUpdate{Title: "new"}, false) }, want: `wanted item "w-1" is not open or does not exist`},
		{name: "close", fn: func() error { return CloseWanted(db, "w-1", false) }, want: `wanted item "w-1" is not in_review or does not exist`},
		{name: "delete", fn: func() error { return DeleteWanted(db, "w-1", false) }, want: `wanted item "w-1" is not open or does not exist`},
		{name: "reject", fn: func() error { return RejectCompletion(db, "w-1", "bob", "", false) }, want: `wanted item "w-1" is not in_review or does not exist`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fn()
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("expected ConflictError, got %T: %v", err, err)
			}
			if conflict.Error() != tt.want {
				t.Fatalf("ConflictError = %q, want %q", conflict.Error(), tt.want)
			}
		})
	}
}

func TestMutationFunctions_WrapUnderlyingErrors(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		execFunc: func(string, string, bool, ...string) error {
			return errors.New("db exploded")
		},
	}

	tests := []struct {
		name string
		fn   func() error
		want string
	}{
		{name: "claim", fn: func() error { return ClaimWanted(db, "w-1", "alice", false) }, want: "claim failed: db exploded"},
		{name: "unclaim", fn: func() error { return UnclaimWanted(db, "w-1", false) }, want: "unclaim failed: db exploded"},
		{name: "submit", fn: func() error { return SubmitCompletion(db, "c-1", "w-1", "alice", "proof", "", false) }, want: "completion failed: db exploded"},
		{name: "accept", fn: func() error {
			return AcceptCompletion(db, "w-1", "c-1", "bob", "", &Stamp{ID: "s-1", Subject: "alice"}, false)
		}, want: "accept failed: db exploded"},
		{name: "update", fn: func() error { return UpdateWanted(db, "w-1", &WantedUpdate{Title: "new"}, false) }, want: "update failed: db exploded"},
		{name: "close", fn: func() error { return CloseWanted(db, "w-1", false) }, want: "close failed: db exploded"},
		{name: "delete", fn: func() error { return DeleteWanted(db, "w-1", false) }, want: "delete failed: db exploded"},
		{name: "reject", fn: func() error { return RejectCompletion(db, "w-1", "bob", "", false) }, want: "reject failed: db exploded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.fn()
			if err == nil || err.Error() != tt.want {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestQueryFunctions_ParseRowsAndRespectRefs(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		queryFunc: func(sql, ref string) (string, error) {
			switch {
			case strings.Contains(sql, "FROM completions"):
				if ref != "wl/alice/w-1" {
					t.Fatalf("QueryCompletion ref = %q, want wl/alice/w-1", ref)
				}
				return "id,wanted_id,completed_by,evidence,stamp_id,validated_by\nc-1,w-1,alice,proof,s-1,bob\n", nil
			case strings.Contains(sql, "FROM stamps"):
				if ref != "wl/alice/w-1" {
					t.Fatalf("QueryStamp ref = %q, want wl/alice/w-1", ref)
				}
				return "id,author,subject,valence,severity,context_id,context_type,skill_tags,message\ns-1,bob,alice,\"{\"\"quality\"\":4,\"\"reliability\"\":5}\",medium,c-1,completion,\"[\"\"go\"\",\"\"sql\"\"]\",solid work\n", nil
			case strings.Contains(sql, "description"):
				if ref != "wl/alice/w-1" {
					t.Fatalf("QueryWantedDetail ref = %q, want wl/alice/w-1", ref)
				}
				return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\nw-1,Task,Details,gascity,bug,2,\"[\"\"go\"\"]\",alice,alice,in_review,small,2026-03-01,2026-03-02\n", nil
			default:
				return "id,title,status,claimed_by,posted_by\nw-1,Task,in_review,alice,bob\n", nil
			}
		},
	}

	item, err := QueryWanted(db, "w-1")
	if err != nil {
		t.Fatalf("QueryWanted() error = %v", err)
	}
	if item.Status != "in_review" || item.ClaimedBy != "alice" || item.PostedBy != "bob" {
		t.Fatalf("QueryWanted() = %+v", item)
	}

	detail, err := QueryWantedDetailAsOf(db, "w-1", "wl/alice/w-1")
	if err != nil {
		t.Fatalf("QueryWantedDetailAsOf() error = %v", err)
	}
	if detail.Priority != 2 || len(detail.Tags) != 1 || detail.Tags[0] != "go" {
		t.Fatalf("detail = %+v", detail)
	}

	completion, err := QueryCompletionAsOf(db, "w-1", "wl/alice/w-1")
	if err != nil {
		t.Fatalf("QueryCompletionAsOf() error = %v", err)
	}
	if completion.ValidatedBy != "bob" {
		t.Fatalf("completion = %+v", completion)
	}

	stamp, err := QueryStampAsOf(db, "s-1", "wl/alice/w-1")
	if err != nil {
		t.Fatalf("QueryStampAsOf() error = %v", err)
	}
	if stamp.Quality != 4 || stamp.Reliability != 5 || len(stamp.SkillTags) != 2 {
		t.Fatalf("stamp = %+v", stamp)
	}
}

func TestQueryFunctions_ReportNotFoundWithRefContext(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		queryFunc: func(string, string) (string, error) {
			return "id,title,status,claimed_by,posted_by\n", nil
		},
	}

	if _, err := QueryWantedDetailAsOf(db, "w-1", "wl/alice/w-1"); err == nil || !strings.Contains(err.Error(), "not found on ref wl/alice/w-1") {
		t.Fatalf("QueryWantedDetailAsOf() error = %v", err)
	}
	if _, err := QueryCompletionAsOf(db, "w-1", "wl/alice/w-1"); err == nil || !strings.Contains(err.Error(), "no completion found for wanted item") {
		t.Fatalf("QueryCompletionAsOf() error = %v", err)
	}
	if _, err := QueryStampAsOf(db, "s-1", "wl/alice/w-1"); err == nil || !strings.Contains(err.Error(), "not found on ref wl/alice/w-1") {
		t.Fatalf("QueryStampAsOf() error = %v", err)
	}
}

func TestUpdateWantedDML_RequiresFieldsAndSupportsExplicitEmptyTags(t *testing.T) {
	t.Parallel()

	if _, err := UpdateWantedDML("w-1", &WantedUpdate{Priority: -1}); err == nil {
		t.Fatal("expected error when no fields are set")
	}
	dml, err := UpdateWantedDML("w-1", &WantedUpdate{
		Title:       "Updated",
		Priority:    0,
		EffortLevel: "large",
		Tags:        []string{},
		TagsSet:     true,
	})
	if err != nil {
		t.Fatalf("UpdateWantedDML() error = %v", err)
	}
	for _, want := range []string{"title='Updated'", "priority=0", "effort_level='large'", "tags=NULL", "status='open'"} {
		if !strings.Contains(dml, want) {
			t.Fatalf("UpdateWantedDML() missing %q in %s", want, dml)
		}
	}
}

func TestUpdateWantedDML_OptionalTextFields(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name       string
		fields     WantedUpdate
		want, omit []string
	}{
		{"clear description", WantedUpdate{Priority: -1, DescriptionSet: true}, []string{"description=''"}, []string{"project="}},
		{"clear project", WantedUpdate{Priority: -1, ProjectSet: true}, []string{"project=''"}, []string{"description="}},
		{"clear both", WantedUpdate{Priority: -1, DescriptionSet: true, ProjectSet: true}, []string{"description=''", "project=''"}, nil},
		{"omitted", WantedUpdate{Priority: -1, Title: "New title"}, []string{"title='New title'"}, []string{"description=", "project="}},
		{"nonempty without flags", WantedUpdate{Priority: -1, Description: "New description", Project: "New project"}, []string{"description='New description'", "project='New project'"}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dml, err := UpdateWantedDML("w-1", &tt.fields)
			if err != nil {
				t.Fatalf("UpdateWantedDML() error = %v", err)
			}
			for _, want := range tt.want {
				if !strings.Contains(dml, want) {
					t.Errorf("missing %q in %s", want, dml)
				}
			}
			for _, omit := range tt.omit {
				if strings.Contains(dml, omit) {
					t.Errorf("unexpected %q in %s", omit, dml)
				}
			}
		})
	}
}

func TestAcceptAndCloseUpstreamDML_AdoptForkCompletion(t *testing.T) {
	t.Parallel()

	stamp := &Stamp{
		ID:          "s-1",
		Subject:     "alice",
		Quality:     5,
		Reliability: 4,
		Severity:    "high",
		SkillTags:   []string{"go"},
		Message:     "great fix",
	}
	accept := AcceptUpstreamDML("w-1", "c-1", "alice", "proof", "bob", "hop://bob", stamp)
	closeOnly := CloseUpstreamDML("w-1", "c-1", "alice", "proof", "hop://bob")

	if len(accept) != 5 {
		t.Fatalf("len(AcceptUpstreamDML) = %d, want 5", len(accept))
	}
	if !strings.Contains(strings.Join(accept, "\n"), "UPDATE wanted SET status='completed', claimed_by='alice', evidence_url='proof'") {
		t.Fatalf("AcceptUpstreamDML() missing wanted adoption update: %+v", accept)
	}
	if !strings.Contains(strings.Join(accept, "\n"), "'great fix'") {
		t.Fatalf("AcceptUpstreamDML() missing stamp message: %+v", accept)
	}
	if len(closeOnly) != 3 {
		t.Fatalf("len(CloseUpstreamDML) = %d, want 3", len(closeOnly))
	}
	if strings.Contains(strings.Join(closeOnly, "\n"), "INSERT INTO stamps") {
		t.Fatalf("CloseUpstreamDML() should not create a stamp: %+v", closeOnly)
	}
}

func TestRejectCompletion_TruncatesLongReasonInCommitMessage(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{}
	reason := strings.Repeat("x", 600)
	if err := RejectCompletion(db, "w-1", "bob", reason, true); err != nil {
		t.Fatalf("RejectCompletion() error = %v", err)
	}
	if len(db.execCalls) != 1 {
		t.Fatalf("len(execCalls) = %d, want 1", len(db.execCalls))
	}
	got := db.execCalls[0].commitMsg
	if !strings.HasPrefix(got, "wl reject by bob: w-1") {
		t.Fatalf("commitMsg = %q, want reject prefix", got)
	}
	if !strings.Contains(got, "...") {
		t.Fatalf("commitMsg = %q, want truncated ellipsis", got)
	}
}

func TestQueryFullDetailAsOf_LoadsCompletionAndStampForEffectiveState(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		queryFunc: func(sql, _ string) (string, error) {
			switch {
			case strings.Contains(sql, "LEFT JOIN completions"):
				return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at,completion_id,completion_wanted_id,completed_by,evidence,completion_stamp_id,validated_by,stamp_record_id,stamp_author,stamp_subject,stamp_valence,stamp_severity,stamp_context_id,stamp_context_type,stamp_skill_tags,stamp_message\nw-1,Task,Details,gascity,bug,2,\"[\"\"go\"\"]\",alice,alice,completed,small,2026-03-01,2026-03-02,c-1,w-1,alice,proof,s-1,bob,s-1,bob,alice,\"{\"\"quality\"\":4,\"\"reliability\"\":5}\",medium,c-1,completion,\"[\"\"go\"\"]\",solid work\n", nil
			default:
				return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\nw-1,Task,Details,gascity,bug,2,\"[\"\"go\"\"]\",alice,alice,completed,small,2026-03-01,2026-03-02\n", nil
			}
		},
	}

	item, completion, stamp, err := QueryFullDetailAsOf(db, "w-1", "wl/alice/w-1")
	if err != nil {
		t.Fatalf("QueryFullDetailAsOf() error = %v", err)
	}
	if item == nil || completion == nil || stamp == nil {
		t.Fatalf("full detail = item:%+v completion:%+v stamp:%+v", item, completion, stamp)
	}
	if completion.ID != "c-1" || stamp.ID != "s-1" {
		t.Fatalf("completion/stamp = %+v / %+v", completion, stamp)
	}
}

func TestQueryFullDetailAsOf_IgnoresCompletionAndStampOutsideReviewStates(t *testing.T) {
	t.Parallel()

	db := &operationsTestDB{
		queryFunc: func(sql, _ string) (string, error) {
			if !strings.Contains(sql, "LEFT JOIN completions") {
				t.Fatalf("unexpected query = %q", sql)
			}
			return "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at,completion_id,completion_wanted_id,completed_by,evidence,completion_stamp_id,validated_by,stamp_record_id,stamp_author,stamp_subject,stamp_valence,stamp_severity,stamp_context_id,stamp_context_type,stamp_skill_tags,stamp_message\nw-1,Task,Details,gascity,bug,2,\"[\"\"go\"\"]\",alice,alice,open,small,2026-03-01,2026-03-02,c-1,w-1,alice,proof,s-1,bob,s-1,bob,alice,\"{\"\"quality\"\":4,\"\"reliability\"\":5}\",medium,c-1,completion,\"[\"\"go\"\"]\",solid work\n", nil
		},
	}

	item, completion, stamp, err := QueryFullDetailAsOf(db, "w-1", "wl/alice/w-1")
	if err != nil {
		t.Fatalf("QueryFullDetailAsOf() error = %v", err)
	}
	if item == nil || item.Status != "open" {
		t.Fatalf("item = %+v, want open item", item)
	}
	if completion != nil || stamp != nil {
		t.Fatalf("completion/stamp = %+v / %+v, want nil for open item", completion, stamp)
	}
}

var _ DB = (*operationsTestDB)(nil)
