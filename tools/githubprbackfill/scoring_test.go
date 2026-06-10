package main

import "testing"

func TestScorePR_CapsLowSignalAndReverted(t *testing.T) {
	pr := PRRecord{
		Repo:   "gastownhall/gascity",
		Number: 548,
		Title:  "fix generated output",
		Signals: Signals{
			IdentitySource:           "exact",
			NoEffectiveAuthoredFiles: true,
			LaterReverted:            true,
			EffectiveChangedFiles:    100,
			EffectiveAdditions:       9000,
		},
	}

	got := scorePR(pr)
	if got.Quality != 3 || got.Reliability != 3 {
		t.Fatalf("quality/reliability = %d/%d, want 3/3", got.Quality, got.Reliability)
	}
	if got.Severity != "leaf" {
		t.Fatalf("severity = %q, want leaf", got.Severity)
	}
	if got.Confidence != 0.46 {
		t.Fatalf("confidence = %.2f, want 0.46", got.Confidence)
	}
}

func TestScorePR_BranchFeatureKeepsFour(t *testing.T) {
	pr := PRRecord{
		Repo:   "gastownhall/wasteland",
		Number: 12,
		Title:  "feat: add profile view",
		Signals: Signals{
			IdentitySource:        "override",
			EffectiveChangedFiles: 7,
			EffectiveAdditions:    250,
			EffectiveDeletions:    90,
			FeatureLike:           true,
			RuntimeOrAPILike:      true,
		},
	}

	got := scorePR(pr)
	if got.Quality != 4 || got.Reliability != 4 || got.Creativity != 4 {
		t.Fatalf("score = %+v, want quality/reliability/creativity 4", got)
	}
	if got.Severity != "branch" {
		t.Fatalf("severity = %q, want branch", got.Severity)
	}
	if got.Confidence != 0.53 {
		t.Fatalf("confidence = %.2f, want 0.53", got.Confidence)
	}
}

func TestScorePR_MaintainerAndBlockingSignalsFloorAtThree(t *testing.T) {
	pr := PRRecord{
		Repo:   "gastownhall/beads",
		Number: 99,
		Title:  "refactor storage",
		Signals: Signals{
			IdentitySource:          "provisional",
			EffectiveChangedFiles:   4,
			EffectiveAdditions:      100,
			StrongBlockingReview:    true,
			MaintainerCommits:       2,
			PostReviewAuthorChanges: 3,
		},
	}

	got := scorePR(pr)
	if got.Quality != 3 || got.Reliability != 3 {
		t.Fatalf("quality/reliability = %d/%d, want floor 3/3", got.Quality, got.Reliability)
	}
	if got.Confidence != 0.49 {
		t.Fatalf("confidence = %.2f, want 0.49", got.Confidence)
	}
}
