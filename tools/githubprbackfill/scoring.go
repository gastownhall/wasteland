package main

import (
	"fmt"
	"strings"
)

func scorePR(pr PRRecord) Score {
	s := pr.Signals
	score := 4
	var rationale []string

	if s.LaterReverted || s.NoEffectiveAuthoredFiles || s.GeneratedOnly || s.DependencyOnly || s.MechanicalOnly {
		score = min(score, 3)
		rationale = append(rationale, "low-signal cap")
	}
	if s.StrongBlockingReview {
		score--
		rationale = append(rationale, "strong blocking review")
	}
	if s.MaintainerCommits > 0 {
		score--
		rationale = append(rationale, "maintainer commits")
	}
	if s.PostReviewAuthorChanges > 0 && s.StrongBlockingReview {
		score--
		rationale = append(rationale, "post-review author changes after strong signal")
	}
	score = clamp(score, 3, 4)

	creativity := 3
	switch {
	case s.DependencyOnly || s.GeneratedOnly || s.MechanicalOnly || s.DocsOnly || s.CIOnly || s.RevertOnly || s.NoEffectiveAuthoredFiles:
		creativity = 2
	case s.FeatureLike || s.RuntimeOrAPILike:
		creativity = 4
	}

	severity := "leaf"
	churn := s.EffectiveAdditions + s.EffectiveDeletions
	title := strings.ToLower(pr.Title)
	labelText := strings.ToLower(strings.Join(s.Labels, " "))
	switch {
	case s.EffectiveChangedFiles >= 20 || churn >= 1500:
		severity = "root"
	case s.EffectiveChangedFiles >= 5 || churn >= 300 || containsAny(title+" "+labelText, "feat", "feature", "perf", "performance", "refactor"):
		severity = "branch"
	}
	if s.NoEffectiveAuthoredFiles {
		severity = "leaf"
	}

	confidence := 0.51
	switch s.IdentitySource {
	case "exact":
		confidence = 0.55
	case "override":
		confidence = 0.53
	case "provisional":
		confidence = 0.51
	}
	if s.MaintainerCommits > 0 || s.StrongBlockingReview {
		confidence = minFloat(confidence, 0.49)
	}
	if s.LaterReverted || s.SubjectConflict || s.QuestionableAttribution {
		confidence = minFloat(confidence, 0.46)
	}

	if len(rationale) == 0 {
		rationale = append(rationale, "merged main PR")
	}
	return Score{
		Quality:     score,
		Reliability: score,
		Creativity:  creativity,
		Severity:    severity,
		Confidence:  confidence,
		Source:      "formula",
		Rationale:   rationale,
	}
}

func validateScore(score Score) error {
	if score.Quality < 1 || score.Quality > 5 {
		return fmt.Errorf("quality %d outside 1..5", score.Quality)
	}
	if score.Reliability < 1 || score.Reliability > 5 {
		return fmt.Errorf("reliability %d outside 1..5", score.Reliability)
	}
	if score.Creativity < 1 || score.Creativity > 5 {
		return fmt.Errorf("creativity %d outside 1..5", score.Creativity)
	}
	switch score.Severity {
	case "leaf", "branch", "root":
	default:
		return fmt.Errorf("invalid severity %q", score.Severity)
	}
	if score.Confidence < 0 || score.Confidence > 1 {
		return fmt.Errorf("confidence %.2f outside 0..1", score.Confidence)
	}
	return nil
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
