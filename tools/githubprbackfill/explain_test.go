package main

import (
	"testing"
	"time"
)

func TestParseGitHubPRURL(t *testing.T) {
	repo, number, err := parseGitHubPRURL("https://github.com/gastownhall/gascity/pull/1723")
	if err != nil {
		t.Fatal(err)
	}
	if repo != "gastownhall/gascity" || number != 1723 {
		t.Fatalf("repo/number = %q/%d", repo, number)
	}
}

func TestRawInputsFromFetchedIncludesScoringMetadata(t *testing.T) {
	at := time.Date(2026, 5, 1, 2, 3, 4, 0, time.UTC)
	fp := fetchedPR{
		Repo: "gastownhall/gascity",
		Pull: ghPull{
			Number:  1723,
			Title:   "fix runtime sessions",
			User:    &ghUser{Login: "alice", Type: "User"},
			Base:    ghBaseRef{Ref: "main"},
			HTMLURL: "https://github.com/gastownhall/gascity/pull/1723",
		},
		Issue: ghIssue{Labels: []ghLabel{{Name: "bug"}}},
		Files: []ghFile{{
			Filename:  "internal/runtime/session.go",
			Additions: 10,
			Deletions: 2,
			Changes:   12,
		}},
		Commits: []ghCommit{{Author: &ghUser{Login: "alice", Type: "User"}}},
		Reviews: []ghReview{{
			User:        &ghUser{Login: "bob", Type: "User"},
			State:       "CHANGES_REQUESTED",
			Body:        "must fix this regression",
			SubmittedAt: &at,
		}},
	}
	fp.Commits[0].Commit.Message = "fix runtime session"
	fp.Commits[0].Commit.Author.Date = at

	raw := rawInputsFromFetched(fp, true)
	if raw.Author.Login != "alice" || raw.Labels[0] != "bug" || len(raw.Files) != 1 || len(raw.Commits) != 1 {
		t.Fatalf("raw inputs = %+v", raw)
	}
	if len(raw.Reviews) != 1 || raw.Reviews[0].Body == "" || raw.Reviews[0].Signals[0] != "blocking_text_match" {
		t.Fatalf("review metadata = %+v", raw.Reviews)
	}
}

func TestExplainRulesMatchesScoreSignals(t *testing.T) {
	pr := PRRecord{
		Title: "feat: add runtime API",
		Signals: Signals{
			IdentitySource:        "exact",
			EffectiveChangedFiles: 8,
			EffectiveAdditions:    500,
			RuntimeOrAPILike:      true,
			StrongBlockingReview:  true,
		},
	}
	pr.Score = scorePR(pr)
	rules := explainRules(pr)
	if pr.Score.Severity != "branch" || pr.Score.Confidence != 0.49 {
		t.Fatalf("score = %+v", pr.Score)
	}
	if len(rules.QualityReliability) < 3 || len(rules.Confidence) < 3 {
		t.Fatalf("rules = %+v", rules)
	}
}
