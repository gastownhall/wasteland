package commons

import (
	"fmt"
	"strings"
	"testing"
)

func TestQueryScoreboardDump_Basic(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"FROM rigs ORDER":        "handle,display_name,dolthub_org,trust_level,registered_at,last_seen,rig_type,parent_rig\nalice,Alice Chen,hop,0,2024-01-15,2024-06-01,human,\n",
		"FROM stamps ORDER":      "id,author,subject,valence,confidence,severity,context_id,context_type,skill_tags,message,created_at\ns-1,bob,alice,{},1,root,c-1,completion,,great,2024-06-01\n",
		"FROM completions ORDER": "id,wanted_id,completed_by,evidence,validated_by,stamp_id,completed_at,validated_at\nc-1,w-1,alice,http://example.com,bob,s-1,2024-06-01,2024-06-02\n",
		"FROM wanted ORDER":      "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\nw-1,Fix bug,desc,proj,task,1,,bob,,open,medium,2024-05-01,2024-05-01\n",
		"FROM badges ORDER":      "id,rig_handle,badge_type,awarded_at,evidence\nb-1,alice,pioneer,2024-03-01,first completion\n",
	}}

	dump, err := QueryScoreboardDump(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dump.Rigs) != 1 {
		t.Fatalf("rigs count = %d, want 1", len(dump.Rigs))
	}
	if dump.Rigs[0].Handle != "alice" {
		t.Errorf("rig handle = %q, want alice", dump.Rigs[0].Handle)
	}
	if dump.Rigs[0].DisplayName != "Alice Chen" {
		t.Errorf("rig display_name = %q, want Alice Chen", dump.Rigs[0].DisplayName)
	}
	// alice has 1 root stamp (weight=5) → newcomer tier
	if dump.Rigs[0].TrustTier != "newcomer" {
		t.Errorf("rig trust_tier = %q, want newcomer", dump.Rigs[0].TrustTier)
	}

	if len(dump.Stamps) != 1 {
		t.Fatalf("stamps count = %d, want 1", len(dump.Stamps))
	}
	if dump.Stamps[0].Author != "bob" {
		t.Errorf("stamp author = %q, want bob", dump.Stamps[0].Author)
	}

	if len(dump.Completions) != 1 {
		t.Fatalf("completions count = %d, want 1", len(dump.Completions))
	}
	if dump.Completions[0].CompletedBy != "alice" {
		t.Errorf("completion completed_by = %q, want alice", dump.Completions[0].CompletedBy)
	}

	if len(dump.Wanted) != 1 {
		t.Fatalf("wanted count = %d, want 1", len(dump.Wanted))
	}
	if dump.Wanted[0].Title != "Fix bug" {
		t.Errorf("wanted title = %q, want Fix bug", dump.Wanted[0].Title)
	}

	if len(dump.Badges) != 1 {
		t.Fatalf("badges count = %d, want 1", len(dump.Badges))
	}
	if dump.Badges[0].BadgeType != "pioneer" {
		t.Errorf("badge type = %q, want pioneer", dump.Badges[0].BadgeType)
	}
}

func TestQueryScoreboardDump_Empty(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"FROM rigs ORDER":        "handle,display_name,dolthub_org,trust_level,registered_at,last_seen,rig_type,parent_rig\n",
		"FROM stamps ORDER":      "id,author,subject,valence,confidence,severity,context_id,context_type,skill_tags,message,created_at\n",
		"FROM completions ORDER": "id,wanted_id,completed_by,evidence,validated_by,stamp_id,completed_at,validated_at\n",
		"FROM wanted ORDER":      "id,title,description,project,type,priority,tags,posted_by,claimed_by,status,effort_level,created_at,updated_at\n",
		"FROM badges ORDER":      "id,rig_handle,badge_type,awarded_at,evidence\n",
	}}

	dump, err := QueryScoreboardDump(db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(dump.Rigs) != 0 {
		t.Errorf("rigs = %d, want 0", len(dump.Rigs))
	}
	if len(dump.Stamps) != 0 {
		t.Errorf("stamps = %d, want 0", len(dump.Stamps))
	}
	if len(dump.Completions) != 0 {
		t.Errorf("completions = %d, want 0", len(dump.Completions))
	}
	if len(dump.Wanted) != 0 {
		t.Errorf("wanted = %d, want 0", len(dump.Wanted))
	}
	if len(dump.Badges) != 0 {
		t.Errorf("badges = %d, want 0", len(dump.Badges))
	}
}

func TestQueryScoreboardDump_QueryError(t *testing.T) {
	t.Parallel()
	db := &fakeDB{err: fmt.Errorf("dump error")}
	_, err := QueryScoreboardDump(db)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "dump error") {
		t.Errorf("error = %q, want to contain 'dump error'", err.Error())
	}
}
