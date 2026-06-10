package main

import (
	"strings"
	"testing"
	"time"
)

func TestManifestHashIgnoresHashField(t *testing.T) {
	m := sampleManifest(t)
	if err := buildRows(&m); err != nil {
		t.Fatal(err)
	}
	h1, err := manifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Hash = "sha256:old"
	h2, err := manifestHash(m)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hash changed when only Hash changed: %s != %s", h1, h2)
	}
}

func TestBuildRowsCreatesDeterministicSyntheticRows(t *testing.T) {
	m := sampleManifest(t)
	if err := buildRows(&m); err != nil {
		t.Fatal(err)
	}
	if len(m.SyntheticRows.Wanted) != 1 || len(m.SyntheticRows.Completions) != 1 || len(m.SyntheticRows.Stamps) != 1 {
		t.Fatalf("row counts = wanted %d completions %d stamps %d", len(m.SyntheticRows.Wanted), len(m.SyntheticRows.Completions), len(m.SyntheticRows.Stamps))
	}
	if got := m.SyntheticRows.Wanted[0].ID; got != "w-gh-gascity-548" {
		t.Fatalf("wanted id = %q", got)
	}
	if got := m.SyntheticRows.Completions[0].StampID; got != "s-gh-gascity-548" {
		t.Fatalf("completion stamp id = %q", got)
	}
	if got := m.ValidationExpectations.ScoreboardDeltas["alice"].WeightedScore; got != 3 {
		t.Fatalf("weighted score delta = %d, want 3", got)
	}
}

func TestRenderSQLQuotesUntrustedText(t *testing.T) {
	m := sampleManifest(t)
	m.PRs[0].Title = "fix Bob's parser\nand docs"
	if err := buildRows(&m); err != nil {
		t.Fatal(err)
	}
	files := renderSQLFiles(m)
	var combined strings.Builder
	for _, f := range files {
		combined.WriteString(f.body)
	}
	sql := combined.String()
	if !strings.Contains(sql, "Bob''s parser") {
		t.Fatalf("SQL did not escape single quote:\n%s", sql)
	}
	if !strings.Contains(sql, "INSERT IGNORE INTO stamps") {
		t.Fatalf("SQL missing stamps insert:\n%s", sql)
	}
}

func sampleManifest(t *testing.T) Manifest {
	t.Helper()
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	merged := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	return Manifest{
		Version:        backfillVersion,
		GeneratedAt:    "2026-05-05T00:00:00Z",
		FormulaVersion: formulaVersion,
		Inputs: ManifestInputs{
			Repos:                 []string{"gastownhall/gascity"},
			IdentityOverridesPath: "docs/backfills/github-stamps/identity-overrides.json",
		},
		SourceState: SourceState{
			CommonsDir:    "/tmp/wl-commons",
			CommonsCommit: "abc123",
		},
		PRs: []PRRecord{{
			Repo:        "gastownhall/gascity",
			Number:      548,
			Title:       "feat: add parser",
			URL:         "https://github.com/gastownhall/gascity/pull/548",
			AuthorLogin: "alice",
			Subject:     "alice",
			BaseRef:     "main",
			CreatedAt:   created,
			MergedAt:    merged,
			Decision:    "stamp",
			Signals: Signals{
				IdentitySource:        "exact",
				EffectiveChangedFiles: 6,
				EffectiveAdditions:    200,
				EffectiveDeletions:    120,
				FeatureLike:           true,
				SkillTags:             []string{"go", "gascity", "cli"},
			},
		}},
	}
}
