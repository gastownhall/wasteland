package pile

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// fakeQuerier is a test double that returns canned rows.
type fakeQuerier struct {
	rows map[string][]map[string]any // sql prefix -> rows
	err  error
}

func (f *fakeQuerier) QueryRows(sql string) ([]map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	for prefix, rows := range f.rows {
		if len(sql) >= len(prefix) && sql[:len(prefix)] == prefix {
			return rows, nil
		}
	}
	return nil, nil
}

func TestQueryProfile_NotFound(t *testing.T) {
	q := &fakeQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {},
	}}
	_, err := QueryProfile(q, "nobody")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !isNotFound(err) {
		t.Errorf("expected ErrProfileNotFound, got: %v", err)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrProfileNotFound)
}

func TestQueryProfile_UpstreamError(t *testing.T) {
	q := &fakeQuerier{err: fmt.Errorf("connection refused")}
	_, err := QueryProfile(q, "test")
	if err == nil {
		t.Fatal("expected error for upstream failure")
	}
	// Should NOT be ErrProfileNotFound
	if isNotFound(err) {
		t.Errorf("upstream error should not be ErrProfileNotFound: %v", err)
	}
}

func TestQueryProfile_Success(t *testing.T) {
	sheetJSON, _ := json.Marshal(map[string]any{
		"identity": map[string]any{
			"display_name": "Linus Torvalds",
			"bio":          "Creator of Linux",
		},
		"value_dimensions": map[string]any{
			"quality":     0.95,
			"reliability": 0.88,
			"creativity":  0.72,
		},
	})

	q := &fakeQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {
			{
				"handle":     "torvalds",
				"source":     "github",
				"sheet_json": string(sheetJSON),
				"confidence": "0.95",
				"created_at": "2024-01-01",
			},
		},
		"SELECT skill_tags": {},
	}}

	profile, err := QueryProfile(q, "torvalds")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.Handle != "torvalds" {
		t.Errorf("handle = %q, want torvalds", profile.Handle)
	}
	if profile.DisplayName != "Linus Torvalds" {
		t.Errorf("display_name = %q, want Linus Torvalds", profile.DisplayName)
	}
	// 0.95 boot block value * 5 = 4.75 on stamp scale
	if profile.Quality < 4.5 {
		t.Errorf("quality = %f, want >= 4.5 (0.95 * 5)", profile.Quality)
	}
	// Empty stamp rows → AssessmentCount should be 0
	if profile.AssessmentCount != 0 {
		t.Errorf("AssessmentCount = %d, want 0 (no stamp rows)", profile.AssessmentCount)
	}
}

func TestQueryProfile_WithStamps(t *testing.T) {
	sheetJSON, _ := json.Marshal(map[string]any{
		"identity":         map[string]any{"display_name": "Test"},
		"value_dimensions": map[string]any{"quality": 0.5},
	})

	q := &fakeQuerier{rows: map[string][]map[string]any{
		"SELECT handle": {
			{
				"handle":     "test",
				"source":     "github",
				"sheet_json": string(sheetJSON),
				"confidence": "0.8",
				"created_at": "2024-01-01",
			},
		},
		"SELECT skill_tags": {
			{"skill_tags": `["go"]`, "valence": `{"quality":4,"reliability":3,"creativity":2}`, "confidence": "0.9", "message": "Strong Go skills"},
			{"skill_tags": `["python"]`, "valence": `{"quality":3,"reliability":4,"creativity":3}`, "confidence": "0.8", "message": "Python experience"},
		},
	}}

	profile, err := QueryProfile(q, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.AssessmentCount != 2 {
		t.Errorf("AssessmentCount = %d, want 2", profile.AssessmentCount)
	}
}

func TestSearchProfiles(t *testing.T) {
	q := &fakeQuerier{rows: map[string][]map[string]any{
		"SELECT handle, display_name": {
			{"handle": "steve1", "display_name": "Steve One"},
			{"handle": "steve2", "display_name": "Steve Two"},
		},
	}}

	results, err := SearchProfiles(q, "steve", 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
}

func TestEscapeLIKE(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"100%", "100\\%"},
		{"a_b", "a\\_b"},
		{"%_", "\\%\\_"},
		{"back\\slash", "back\\\\slash"},
	}
	for _, tc := range tests {
		got := escapeLIKE(tc.input)
		if got != tc.want {
			t.Errorf("escapeLIKE(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseSheetJSON_Malformed(t *testing.T) {
	profile := &Profile{}
	err := parseSheetJSON("not json", profile)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseSheetJSON_Valid(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{
		"identity": map[string]any{
			"display_name": "Test User",
			"location":     "Testville",
		},
		"value_dimensions": map[string]any{
			"quality":     0.5,
			"reliability": 0.6,
			"creativity":  0.7,
		},
	})
	profile := &Profile{}
	err := parseSheetJSON(string(raw), profile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.DisplayName != "Test User" {
		t.Errorf("display_name = %q, want Test User", profile.DisplayName)
	}
	// 0.5 boot block value * 5 = 2.5 on stamp scale
	if profile.Quality != 2.5 {
		t.Errorf("quality = %f, want 2.5 (0.5 * 5)", profile.Quality)
	}
}
