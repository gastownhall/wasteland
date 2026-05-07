package main

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
)

func rowIDs(repo string, number int) RowIDs {
	name := path.Base(repo)
	return RowIDs{
		Wanted:     fmt.Sprintf("w-gh-%s-%d", name, number),
		Completion: fmt.Sprintf("c-gh-%s-%d", name, number),
		Stamp:      fmt.Sprintf("s-gh-%s-%d", name, number),
	}
}

func buildRows(m *Manifest) error {
	var rows SyntheticRows
	seenRigs := map[string]bool{systemRig: true}
	now := m.GeneratedAt
	if now == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	rows.Rigs = append(rows.Rigs, RigRow{
		Handle:       systemRig,
		DisplayName:  "Gastownhall Backfill",
		GTVersion:    "github-backfill",
		TrustLevel:   0,
		RigType:      "system",
		RegisteredAt: now,
		LastSeen:     now,
	})

	deltas := make(map[string]ScoreboardDelta)
	skipped := make(map[string]int)
	for i := range m.PRs {
		pr := &m.PRs[i]
		if pr.Decision != "stamp" {
			if pr.Reason != "" {
				skipped[pr.Reason]++
			}
			continue
		}
		if pr.Score.Source == "" {
			pr.Score = scorePR(*pr)
		}
		if err := validateScore(pr.Score); err != nil {
			return fmt.Errorf("score %s#%d: %w", pr.Repo, pr.Number, err)
		}
		ids := rowIDs(pr.Repo, pr.Number)
		pr.RowIDs = ids
		repoName := path.Base(pr.Repo)
		created := pr.CreatedAt.UTC().Format(time.RFC3339)
		merged := pr.MergedAt.UTC().Format(time.RFC3339)
		evidence := canonicalPRURL(pr.Repo, pr.Number)
		if pr.URL != "" {
			evidence = pr.URL
		}
		tagInputs := append([]string{pr.Score.Severity}, pr.Signals.SkillTags...)
		tags := uniqueStrings([]string{"github-backfill", repoName, "github-pr"}, tagInputs...)

		rows.Wanted = append(rows.Wanted, WantedRow{
			ID:          ids.Wanted,
			Title:       fmt.Sprintf("[github:%s#%d] %s", repoName, pr.Number, pr.Title),
			Description: fmt.Sprintf("github-pr-backfill formula=%s repo=%s pr=%d author=%s merged_at=%s url=%s", m.FormulaVersion, pr.Repo, pr.Number, pr.AuthorLogin, merged, evidence),
			Project:     repoName,
			Type:        "github_pr",
			Priority:    priorityForSeverity(pr.Score.Severity),
			Tags:        tags,
			PostedBy:    systemRig,
			ClaimedBy:   pr.Subject,
			Status:      "completed",
			EffortLevel: effortForSeverity(pr.Score.Severity),
			EvidenceURL: evidence,
			CreatedAt:   created,
			UpdatedAt:   merged,
		})
		rows.Completions = append(rows.Completions, CompletionRow{
			ID:          ids.Completion,
			WantedID:    ids.Wanted,
			CompletedBy: pr.Subject,
			Evidence:    evidence,
			ValidatedBy: systemRig,
			StampID:     ids.Stamp,
			CompletedAt: created,
			ValidatedAt: merged,
		})
		rows.Stamps = append(rows.Stamps, StampRow{
			ID:      ids.Stamp,
			Author:  systemRig,
			Subject: pr.Subject,
			Valence: map[string]int{
				"quality":     pr.Score.Quality,
				"reliability": pr.Score.Reliability,
				"creativity":  pr.Score.Creativity,
			},
			Confidence:  pr.Score.Confidence,
			Severity:    pr.Score.Severity,
			ContextID:   ids.Completion,
			ContextType: "completion",
			SkillTags:   pr.Signals.SkillTags,
			Message:     fmt.Sprintf("github-pr-backfill formula=%s severity=%s quality=%d reliability=%d creativity=%d confidence=%.2f signals=%s", m.FormulaVersion, pr.Score.Severity, pr.Score.Quality, pr.Score.Reliability, pr.Score.Creativity, pr.Score.Confidence, strings.Join(pr.Score.Rationale, "; ")),
			CreatedAt:   merged,
		})
		if pr.Signals.IdentitySource == "provisional" && !seenRigs[pr.Subject] {
			rows.Rigs = append(rows.Rigs, RigRow{
				Handle:       pr.Subject,
				DisplayName:  "@" + pr.AuthorLogin,
				GTVersion:    "github-backfill",
				TrustLevel:   0,
				RigType:      "human",
				RegisteredAt: created,
				LastSeen:     merged,
			})
			seenRigs[pr.Subject] = true
		}
		delta := deltas[pr.Subject]
		delta.StampCount++
		delta.Completions++
		delta.WeightedScore += severityWeight(pr.Score.Severity)
		delta.SkillTags = uniqueStrings(delta.SkillTags, pr.Signals.SkillTags...)
		deltas[pr.Subject] = delta
	}
	rows.sort()
	m.SyntheticRows = rows
	m.ValidationExpectations = ValidationExpectations{
		ExpectedRigs:        len(rows.Rigs),
		ExpectedWanted:      len(rows.Wanted),
		ExpectedCompletions: len(rows.Completions),
		ExpectedStamps:      len(rows.Stamps),
		SkippedByReason:     skipped,
		ScoreboardDeltas:    deltas,
	}
	return nil
}

func (r *SyntheticRows) sort() {
	sort.Slice(r.Rigs, func(i, j int) bool { return r.Rigs[i].Handle < r.Rigs[j].Handle })
	sort.Slice(r.Wanted, func(i, j int) bool { return r.Wanted[i].ID < r.Wanted[j].ID })
	sort.Slice(r.Completions, func(i, j int) bool { return r.Completions[i].ID < r.Completions[j].ID })
	sort.Slice(r.Stamps, func(i, j int) bool { return r.Stamps[i].ID < r.Stamps[j].ID })
}

func canonicalPRURL(repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", repo, number)
}

func githubHandle(login string) string {
	lower := strings.ToLower(strings.TrimSpace(login))
	if validHandle(lower) {
		return lower
	}
	escaped := strings.ToLower(url.PathEscape(login))
	replacer := strings.NewReplacer("%", "-", ".", "-", "_", "-")
	return strings.Trim(replacer.Replace(escaped), "-")
}

func validHandle(handle string) bool {
	if len(handle) == 0 || len(handle) > 64 {
		return false
	}
	for i, r := range handle {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && (r == '-' || r == '_')
		if !ok {
			return false
		}
	}
	return handle[0] >= 'a' && handle[0] <= 'z' || handle[0] >= '0' && handle[0] <= '9'
}

func priorityForSeverity(severity string) int {
	switch severity {
	case "root":
		return 0
	case "branch":
		return 1
	default:
		return 2
	}
}

func effortForSeverity(severity string) string {
	switch severity {
	case "root":
		return "large"
	case "branch":
		return "medium"
	default:
		return "small"
	}
}

func severityWeight(severity string) int {
	switch severity {
	case "root":
		return 5
	case "branch":
		return 3
	default:
		return 1
	}
}

func uniqueStrings(seed []string, values ...string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range append(seed, values...) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
