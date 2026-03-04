package commons

import (
	"fmt"
	"strings"
	"testing"
)

func TestQueryScoreboard_BasicRanking(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY s.subject":   "subject,stamp_count,weighted_score,unique_towns,avg_quality,avg_reliability\nalice,4,15,3,4.2,3.8\nbob,2,6,2,3.5,4.0\n",
		"stamp_id IS NOT NULL": "completed_by,completions\nalice,3\nbob,1\n",
		"s.skill_tags":         "subject,skill_tags\n",
		"FROM rigs":            "handle,display_name\nalice,Alice Chen\nbob,Bob Smith\n",
	}}
	entries, err := QueryScoreboard(db, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].RigHandle != "alice" {
		t.Errorf("first entry = %q, want alice", entries[0].RigHandle)
	}
	if entries[0].WeightedScore != 15 {
		t.Errorf("alice weighted_score = %d, want 15", entries[0].WeightedScore)
	}
	if entries[0].StampCount != 4 {
		t.Errorf("alice stamp_count = %d, want 4", entries[0].StampCount)
	}
	if entries[0].UniqueTowns != 3 {
		t.Errorf("alice unique_towns = %d, want 3", entries[0].UniqueTowns)
	}
	if entries[0].Completions != 3 {
		t.Errorf("alice completions = %d, want 3", entries[0].Completions)
	}
	if entries[0].DisplayName != "Alice Chen" {
		t.Errorf("alice display_name = %q, want Alice Chen", entries[0].DisplayName)
	}
	if entries[0].TrustTier != "settler" {
		t.Errorf("alice trust_tier = %q, want settler", entries[0].TrustTier)
	}
	if entries[1].TrustTier != "scavenger" {
		t.Errorf("bob trust_tier = %q, want scavenger", entries[1].TrustTier)
	}
}

func TestQueryScoreboard_Empty(t *testing.T) {
	t.Parallel()
	db := &fakeDB{results: map[string]string{
		"GROUP BY s.subject": "subject,stamp_count,weighted_score,unique_towns,avg_quality,avg_reliability\n",
	}}
	entries, err := QueryScoreboard(db, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Errorf("got %v, want nil", entries)
	}
}

func TestQueryScoreboard_WeightedScoring(t *testing.T) {
	t.Parallel()
	// Charlie has fewer stamps but higher severity → higher weighted score → ranks first.
	db := &fakeDB{results: map[string]string{
		"GROUP BY s.subject":   "subject,stamp_count,weighted_score,unique_towns,avg_quality,avg_reliability\ncharlie,2,10,1,5.0,5.0\ndave,5,5,3,3.0,3.0\n",
		"stamp_id IS NOT NULL": "completed_by,completions\n",
		"s.skill_tags":         "subject,skill_tags\n",
		"FROM rigs":            "handle,display_name\n",
	}}
	entries, err := QueryScoreboard(db, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].RigHandle != "charlie" {
		t.Errorf("first entry = %q, want charlie (higher weighted_score)", entries[0].RigHandle)
	}
	if entries[0].TrustTier != "settler" {
		t.Errorf("charlie trust_tier = %q, want settler", entries[0].TrustTier)
	}
}

func TestQueryScoreboard_QueryError(t *testing.T) {
	t.Parallel()
	db := &fakeDB{err: fmt.Errorf("fake error")}
	_, err := QueryScoreboard(db, 10)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "fake error") {
		t.Errorf("error = %q, want to contain 'fake error'", err.Error())
	}
}

func TestDeriveTrustTier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		score int
		want  string
	}{
		{0, "drifter"},
		{2, "drifter"},
		{3, "scavenger"},
		{9, "scavenger"},
		{10, "settler"},
		{24, "settler"},
		{25, "warrior"},
		{49, "warrior"},
		{50, "imperator"},
		{100, "imperator"},
	}
	for _, tt := range tests {
		got := DeriveTrustTier(tt.score)
		if got != tt.want {
			t.Errorf("DeriveTrustTier(%d) = %q, want %q", tt.score, got, tt.want)
		}
	}
}
