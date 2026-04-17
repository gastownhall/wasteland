package pile

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// routedFakeQuerier routes SQL queries to per-query handlers. It returns
// errQuery if a query doesn't match any handler.
type routedFakeQuerier struct {
	handlers []routedHandler
}

type routedHandler struct {
	match string
	rows  []map[string]any
	err   error
}

func (f *routedFakeQuerier) QueryRows(sql string) ([]map[string]any, error) {
	for _, h := range f.handlers {
		if strings.Contains(sql, h.match) {
			return h.rows, h.err
		}
	}
	return nil, fmt.Errorf("routedFakeQuerier: no handler for %q", sql)
}

func TestParseEvidence(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantURL string
		wantLbl string
		wantTxt string
	}{
		{
			"github pr", "https://github.com/gastownhall/gascity/pull/548",
			"https://github.com/gastownhall/gascity/pull/548", "gastownhall/gascity#548", "",
		},
		{
			"github pr http", "http://github.com/foo/bar/pull/1",
			"http://github.com/foo/bar/pull/1", "foo/bar#1", "",
		},
		{
			"github blob", "https://github.com/gastownhall/gascity/blob/main/README.md",
			"https://github.com/gastownhall/gascity/blob/main/README.md", "gastownhall/gascity", "",
		},
		{
			"github bare repo", "https://github.com/foo/bar",
			"https://github.com/foo/bar", "foo/bar", "",
		},
		{
			"non github url kept", "https://example.com/path",
			"https://example.com/path", "", "",
		},
		{
			"non github http kept", "http://example.com/path",
			"http://example.com/path", "", "",
		},
		{
			"bare scheme rejected", "https://",
			"", "", "https://",
		},
		{
			"scheme only with space", "https:// ",
			"", "", "https://",
		},
		{
			"ftp url rejected", "ftp://example.com/path",
			"", "", "ftp://example.com/path",
		},
		{
			"free text", "Wrote a docs PR yesterday",
			"", "", "Wrote a docs PR yesterday",
		},
		{"empty", "", "", "", ""},
		{"whitespace", "   ", "", "", ""},
		{
			"padded url", "  https://github.com/foo/bar/pull/9  ",
			"https://github.com/foo/bar/pull/9", "foo/bar#9", "",
		},
		{
			// Regression: prefix matching used to emit the URL+trailing text
			// as evidence_url, producing a broken link.
			"github pr with trailing text", "https://github.com/foo/bar/pull/1 shipped it",
			"", "", "https://github.com/foo/bar/pull/1 shipped it",
		},
		{
			"github repo with trailing text", "https://github.com/foo/bar hello",
			"", "", "https://github.com/foo/bar hello",
		},
		{
			"non github url with trailing text", "https://example.com/path hello",
			"", "", "https://example.com/path hello",
		},
		{
			"github repo with query", "https://github.com/foo/bar?tab=readme-ov-file",
			"https://github.com/foo/bar?tab=readme-ov-file", "foo/bar", "",
		},
		{
			"github repo with fragment", "https://github.com/foo/bar#readme",
			"https://github.com/foo/bar#readme", "foo/bar", "",
		},
		{
			"github pr with fragment", "https://github.com/foo/bar/pull/1#issuecomment-42",
			"https://github.com/foo/bar/pull/1#issuecomment-42", "foo/bar#1", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotLbl, gotTxt := parseEvidence(tc.in)
			if gotURL != tc.wantURL || gotLbl != tc.wantLbl || gotTxt != tc.wantTxt {
				t.Fatalf("parseEvidence(%q) = (%q, %q, %q), want (%q, %q, %q)",
					tc.in, gotURL, gotLbl, gotTxt, tc.wantURL, tc.wantLbl, tc.wantTxt)
			}
		})
	}
}

func TestQueryProfileResponse_CharacterSheet(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{{
			"handle":     "alice",
			"source":     "github",
			"sheet_json": `{"identity":{"display_name":"Alice","github_login":"alice"},"value_dimensions":{"quality":0.8}}`,
			"confidence": "0.9",
			"created_at": "2026-01-01",
		}}},
		{match: "FROM stamps WHERE subject", rows: []map[string]any{}},
	}}
	// commons should not be consulted when boot_block is present.
	commonsQ := &routedFakeQuerier{}

	// A cacheGet that panics on call proves the character-sheet path never
	// consults the cache.
	noCacheGet := func(string) (string, bool) {
		t.Fatal("cacheGet must not be called for character_sheet responses")
		return "", false
	}
	resp, err := QueryProfileResponse(pileQ, commonsQ, noCacheGet, "alice")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v", err)
	}
	if resp.Kind != KindCharacterSheet {
		t.Fatalf("Kind = %q, want character_sheet", resp.Kind)
	}
	if resp.CharacterSheet == nil || resp.CharacterSheet.Handle != "alice" {
		t.Fatalf("CharacterSheet = %+v", resp.CharacterSheet)
	}

	// Marshal should yield a flat object with kind first and the Profile
	// fields flattened at top level (not nested under "Profile").
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.HasPrefix(string(raw), `{"kind":"character_sheet"`) {
		t.Fatalf("Marshal() = %s, want kind-leading object", string(raw))
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, nested := envelope["Profile"]; nested {
		t.Fatalf("character_sheet body must stay flattened; found nested Profile key: %v", envelope)
	}
	if envelope["handle"] != "alice" {
		t.Fatalf("handle should be at top level, got %v", envelope["handle"])
	}
}

func TestQueryProfileResponse_StampFeed_FromCommons(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", rows: []map[string]any{{
			"id":         "s1",
			"skill_tags": `["go","backend"]`,
			"valence":    `{"quality":4,"reliability":5}`,
			"message":    "Nice work",
			"author":     "julianknutsen",
			"created_at": "2026-04-13 09:33:05",
			"evidence":   "https://github.com/gastownhall/gascity/pull/548",
		}}},
	}}

	resp, err := QueryProfileResponse(pileQ, commonsQ, nil, "rileywhite")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v", err)
	}
	if resp.Kind != KindStampFeed {
		t.Fatalf("Kind = %q, want stamp_feed", resp.Kind)
	}
	feed := resp.StampFeed
	if feed == nil || feed.Handle != "rileywhite" {
		t.Fatalf("StampFeed = %+v", feed)
	}
	// Without a cache, the stamp feed emits no GitHub link — the UI gates
	// the anchor on a non-empty href, so no unverified fallback renders.
	if feed.GithubURL != "" {
		t.Fatalf("GithubURL = %q, want empty string when cacheGet is nil", feed.GithubURL)
	}
	if feed.StampsError != nil {
		t.Fatalf("StampsError = %v, want nil", feed.StampsError)
	}
	if len(feed.Stamps) != 1 {
		t.Fatalf("len(Stamps) = %d, want 1", len(feed.Stamps))
	}
	s := feed.Stamps[0]
	if s.ID != "s1" || s.Validator != "julianknutsen" ||
		s.Quality != 4 || s.Reliability != 5 ||
		s.EvidenceLabel != "gastownhall/gascity#548" {
		t.Fatalf("StampFeedEntry = %+v", s)
	}
	if got := strings.Join(s.SkillTags, ","); got != "go,backend" {
		t.Fatalf("SkillTags = %v", s.SkillTags)
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if envelope["kind"] != "stamp_feed" {
		t.Fatalf("envelope kind = %v", envelope["kind"])
	}
	if _, ok := envelope["stamps_error"]; !ok {
		t.Fatal("stamps_error field should always be present on stamp_feed responses")
	}
	if _, nested := envelope["StampFeed"]; nested {
		t.Fatalf("stamp_feed body must stay flattened; found nested StampFeed key: %v", envelope)
	}
}

func TestQueryProfileResponse_NotFound(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", rows: []map[string]any{}},
	}}

	_, err := QueryProfileResponse(pileQ, commonsQ, nil, "nobody")
	if err == nil {
		t.Fatal("QueryProfileResponse() should return error for truly unknown handle")
	}
	if !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("error = %v, want ErrProfileNotFound", err)
	}
}

func TestQueryProfileResponse_StampsUnavailable(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsErr := fmt.Errorf("dolthub upstream exploded")
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", err: commonsErr},
	}}

	resp, err := QueryProfileResponse(pileQ, commonsQ, nil, "rileywhite")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v, want degraded response", err)
	}
	if resp.Kind != KindStampFeed {
		t.Fatalf("Kind = %q", resp.Kind)
	}
	if resp.StampFeed.StampsError == nil || *resp.StampFeed.StampsError != "stamps_unavailable" {
		t.Fatalf("StampsError = %v", resp.StampFeed.StampsError)
	}
	if len(resp.StampFeed.Stamps) != 0 {
		t.Fatalf("Stamps should be empty, got %d", len(resp.StampFeed.Stamps))
	}
}

func TestQueryProfileResponse_PileError_Propagates(t *testing.T) {
	pileErr := fmt.Errorf("pile upstream exploded")
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", err: pileErr},
	}}
	commonsQ := &routedFakeQuerier{}

	_, err := QueryProfileResponse(pileQ, commonsQ, nil, "alice")
	if err == nil || errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("error = %v, want non-404 upstream error", err)
	}
}

func TestQueryProfileResponse_NilCommons_FallsThroughToNotFound(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}

	_, err := QueryProfileResponse(pileQ, nil, nil, "nobody")
	if err == nil || !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("error = %v, want ErrProfileNotFound when commons is nil", err)
	}
}

func TestQueryProfileResponse_NilCommons_StillServesCharacterSheet(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{{
			"handle":     "alice",
			"source":     "github",
			"sheet_json": `{"identity":{"display_name":"Alice"},"value_dimensions":{"quality":0.5}}`,
			"confidence": "0.9",
			"created_at": "2026-01-01",
		}}},
		{match: "FROM stamps WHERE subject", rows: []map[string]any{}},
	}}

	resp, err := QueryProfileResponse(pileQ, nil, nil, "alice")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v, want success without commons", err)
	}
	if resp.Kind != KindCharacterSheet {
		t.Fatalf("Kind = %q", resp.Kind)
	}
}

// TestQueryProfileResponse_StampFeed_CacheHitPopulatesGithubURL verifies
// that when the handle has a resolved GitHub username in the cache, the
// stamp feed links to github.com/<resolved> rather than the bare handle.
func TestQueryProfileResponse_StampFeed_CacheHitPopulatesGithubURL(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", rows: []map[string]any{{
			"id":         "s1",
			"skill_tags": `["go"]`,
			"valence":    `{"quality":4,"reliability":5}`,
			"message":    "",
			"author":     "validator",
			"created_at": "2026-04-13",
			"evidence":   "https://github.com/foo/bar/pull/1",
		}}},
	}}
	cacheGet := func(h string) (string, bool) {
		if h == "rileywhite" {
			return "mr-gh", true
		}
		return "", false
	}

	resp, err := QueryProfileResponse(pileQ, commonsQ, cacheGet, "rileywhite")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v", err)
	}
	if resp.Kind != KindStampFeed {
		t.Fatalf("Kind = %q, want stamp_feed", resp.Kind)
	}
	if resp.StampFeed.GithubURL != "https://github.com/mr-gh" {
		t.Fatalf("GithubURL = %q, want https://github.com/mr-gh", resp.StampFeed.GithubURL)
	}
}

// TestQueryProfileResponse_StampFeed_CacheMissEmptyGithubURL verifies
// that a cache miss (handle never resolved) yields an empty GithubURL —
// the UI gates its anchor on a non-empty href so no link renders.
func TestQueryProfileResponse_StampFeed_CacheMissEmptyGithubURL(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", rows: []map[string]any{{
			"id":         "s1",
			"skill_tags": `["go"]`,
			"valence":    `{"quality":4,"reliability":5}`,
			"author":     "validator",
			"created_at": "2026-04-13",
			"evidence":   "https://github.com/foo/bar/pull/1",
		}}},
	}}
	cacheGet := func(string) (string, bool) { return "", false }

	resp, err := QueryProfileResponse(pileQ, commonsQ, cacheGet, "rileywhite")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v", err)
	}
	if resp.StampFeed.GithubURL != "" {
		t.Fatalf("GithubURL = %q, want empty on cache miss", resp.StampFeed.GithubURL)
	}
}

// TestQueryProfileResponse_StampFeed_TriedAndFailedEmptyGithubURL verifies
// that a cache entry with an empty GitHub field (tried-and-failed) also
// yields an empty GithubURL, indistinguishable from a straight miss for
// render purposes.
func TestQueryProfileResponse_StampFeed_TriedAndFailedEmptyGithubURL(t *testing.T) {
	pileQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "FROM boot_blocks", rows: []map[string]any{}},
	}}
	commonsQ := &routedFakeQuerier{handlers: []routedHandler{
		{match: "LEFT JOIN completions", rows: []map[string]any{{
			"id":         "s1",
			"skill_tags": `["go"]`,
			"valence":    `{"quality":4,"reliability":5}`,
			"author":     "validator",
			"created_at": "2026-04-13",
			"evidence":   "https://github.com/foo/bar/pull/1",
		}}},
	}}
	// Handle present but GitHub empty — tried resolution, found no PR URL.
	cacheGet := func(string) (string, bool) { return "", true }

	resp, err := QueryProfileResponse(pileQ, commonsQ, cacheGet, "rome")
	if err != nil {
		t.Fatalf("QueryProfileResponse() error = %v", err)
	}
	if resp.StampFeed.GithubURL != "" {
		t.Fatalf("GithubURL = %q, want empty for tried-and-failed entry", resp.StampFeed.GithubURL)
	}
}

func TestParseStampFeedRow_DefaultsAndBadJSON(t *testing.T) {
	// Missing skill_tags and valence → empty slice + zeros, not nil.
	entry := parseStampFeedRow(map[string]any{"id": "s1"})
	if entry.SkillTags == nil {
		t.Fatalf("SkillTags = nil, want []string{}")
	}
	if len(entry.SkillTags) != 0 {
		t.Fatalf("SkillTags = %v, want empty", entry.SkillTags)
	}

	// Malformed skill_tags → tags reset to [], entry still emitted.
	entry = parseStampFeedRow(map[string]any{"id": "s2", "skill_tags": "not json"})
	if entry.SkillTags == nil || len(entry.SkillTags) != 0 {
		t.Fatalf("malformed skill_tags should yield empty slice, got %v", entry.SkillTags)
	}
}

func TestProfileResponseMarshal_RejectsNilBody(t *testing.T) {
	r := &ProfileResponse{Kind: KindCharacterSheet}
	if _, err := json.Marshal(r); err == nil {
		t.Fatal("expected error for nil CharacterSheet body")
	}
	r = &ProfileResponse{Kind: KindStampFeed}
	if _, err := json.Marshal(r); err == nil {
		t.Fatal("expected error for nil StampFeed body")
	}
	r = &ProfileResponse{Kind: "bogus"}
	if _, err := json.Marshal(r); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
