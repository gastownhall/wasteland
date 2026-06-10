package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func generatedOrLowSignal(name string) bool {
	n := strings.ToLower(name)
	if strings.HasPrefix(n, "vendor/") || strings.Contains(n, "/vendor/") ||
		strings.HasPrefix(n, "node_modules/") || strings.Contains(n, "/node_modules/") ||
		strings.HasPrefix(n, "dist/") || strings.Contains(n, "/dist/") ||
		strings.HasPrefix(n, "build/") || strings.Contains(n, "/build/") ||
		strings.HasPrefix(n, "coverage/") || strings.Contains(n, "/coverage/") {
		return true
	}
	base := filepath.Base(n)
	if base == "go.sum" || base == "package-lock.json" || base == "pnpm-lock.yaml" ||
		base == "bun.lock" || strings.HasPrefix(base, "bun.lock") || base == "yarn.lock" {
		return true
	}
	if strings.HasSuffix(n, ".min.js") || strings.HasSuffix(n, "_gen.go") ||
		strings.HasSuffix(n, "client_gen.go") || strings.Contains(n, "genclient/") ||
		strings.HasSuffix(n, "openapi.json") || strings.HasSuffix(n, "openapi.txt") {
		return true
	}
	return false
}

func dependencyOnly(files []ghFile) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		base := strings.ToLower(filepath.Base(f.Filename))
		if base != "go.sum" && base != "go.mod" && base != "package-lock.json" &&
			base != "package.json" && base != "pnpm-lock.yaml" && base != "yarn.lock" &&
			base != "bun.lock" && !strings.HasPrefix(base, "bun.lock") {
			return false
		}
	}
	return true
}

func docsOnly(files []ghFile) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		n := strings.ToLower(f.Filename)
		if !strings.HasPrefix(n, "docs/") && !strings.HasSuffix(n, ".md") && !strings.HasSuffix(n, ".txt") {
			return false
		}
	}
	return true
}

func ciOnly(files []ghFile) bool {
	if len(files) == 0 {
		return false
	}
	for _, f := range files {
		n := strings.ToLower(f.Filename)
		if !strings.HasPrefix(n, ".github/") && !strings.Contains(n, "/workflows/") && !strings.Contains(n, "ci") {
			return false
		}
	}
	return true
}

func mechanicalLike(fp fetchedPR) bool {
	title := strings.ToLower(fp.Pull.Title)
	return strings.Contains(title, "format") || strings.Contains(title, "lint") ||
		strings.Contains(title, "rename") || strings.Contains(title, "mechanical")
}

func revertLike(title string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), "revert")
}

func featureLike(fp fetchedPR) bool {
	text := strings.ToLower(fp.Pull.Title + " " + strings.Join(labelsFromIssue(fp.Issue), " "))
	return containsAny(text, "feat", "feature", "perf", "performance", "refactor")
}

func runtimeOrAPILike(fp fetchedPR) bool {
	for _, f := range fp.Files {
		n := strings.ToLower(f.Filename)
		if strings.Contains(n, "api") || strings.Contains(n, "runtime") ||
			strings.Contains(n, "server") || strings.Contains(n, "cmd/") ||
			strings.Contains(n, "internal/") || strings.Contains(n, "web/src") {
			return true
		}
	}
	return false
}

func strongBlockingSignal(fp fetchedPR) bool {
	blocking := regexp.MustCompile(`(?i)\b(blocker|blocking|must fix|unsafe|incorrect|broken|regression|data loss|security|changes requested)\b`)
	for _, review := range fp.Reviews {
		if review.User != nil && review.User.Type == "User" {
			if strings.EqualFold(review.State, "CHANGES_REQUESTED") || blocking.MatchString(review.Body) {
				return true
			}
		}
	}
	for _, comment := range append(fp.IssueComments, fp.ReviewComments...) {
		if comment.User != nil && comment.User.Type == "User" && blocking.MatchString(comment.Body) {
			return true
		}
	}
	return false
}

func maintainerCommitCount(fp fetchedPR) int {
	author := ""
	if fp.Pull.User != nil {
		author = strings.ToLower(fp.Pull.User.Login)
	}
	var count int
	for _, commit := range fp.Commits {
		login, ok := commitHumanLogin(commit)
		if !ok || strings.EqualFold(login, author) {
			continue
		}
		count++
	}
	return count
}

func postReviewAuthorChanges(fp fetchedPR) int {
	author := ""
	if fp.Pull.User != nil {
		author = strings.ToLower(fp.Pull.User.Login)
	}
	reviewAt, ok := earliestNonAuthorHumanReview(fp, author)
	if !ok {
		return 0
	}
	var count int
	for _, commit := range fp.Commits {
		login, ok := commitHumanLogin(commit)
		if !ok || !strings.EqualFold(login, author) {
			continue
		}
		if commit.Commit.Author.Date.After(reviewAt) {
			count++
		}
	}
	return count
}

func earliestNonAuthorHumanReview(fp fetchedPR, author string) (time.Time, bool) {
	var earliest time.Time
	set := false
	consider := func(login string, typ string, at time.Time) {
		if typ != "User" || strings.EqualFold(login, author) || at.IsZero() {
			return
		}
		if !set || at.Before(earliest) {
			earliest = at
			set = true
		}
	}
	for _, review := range fp.Reviews {
		if review.User != nil && review.SubmittedAt != nil {
			consider(review.User.Login, review.User.Type, *review.SubmittedAt)
		}
	}
	for _, comment := range append(fp.IssueComments, fp.ReviewComments...) {
		if comment.User != nil {
			consider(comment.User.Login, comment.User.Type, comment.CreatedAt)
		}
	}
	return earliest, set
}

func reviewFixupCommit(fp fetchedPR) bool {
	re := regexp.MustCompile(`(?i)(address review|review findings|review blockers|fix review|follow-up review)`)
	for _, commit := range fp.Commits {
		if re.MatchString(commit.Commit.Message) {
			return true
		}
	}
	return false
}

func commitHumanLogin(commit ghCommit) (string, bool) {
	if commit.Author != nil && commit.Author.Login != "" && commit.Author.Type == "User" {
		return strings.ToLower(commit.Author.Login), true
	}
	if commit.Committer != nil && commit.Committer.Login != "" && commit.Committer.Type == "User" {
		return strings.ToLower(commit.Committer.Login), true
	}
	return "", false
}

func skillTags(fp fetchedPR) []string {
	var tags []string
	language := dominantLanguage(fp.Files)
	if language != "" {
		tags = append(tags, language)
	}
	tags = append(tags, repoName(fp.Repo))
	tags = append(tags, capabilityTags(fp)...)
	return uniqueStrings(nil, tags...)
}

func dominantLanguage(files []ghFile) string {
	counts := make(map[string]int)
	for _, f := range files {
		if generatedOrLowSignal(f.Filename) {
			continue
		}
		lang := languageForPath(f.Filename)
		if lang == "" {
			continue
		}
		counts[lang] += f.Changes
	}
	var best string
	var bestCount int
	for lang, count := range counts {
		if count > bestCount || count == bestCount && lang < best {
			best = lang
			bestCount = count
		}
	}
	return best
}

func languageForPath(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".css":
		return "css"
	case ".html":
		return "html"
	case ".md":
		return "docs"
	}
	if strings.EqualFold(filepath.Base(name), "makefile") {
		return "makefile"
	}
	return ""
}

func capabilityTags(fp fetchedPR) []string {
	var tags []string
	text := strings.ToLower(fp.Pull.Title + " " + strings.Join(labelsFromIssue(fp.Issue), " "))
	for _, f := range fp.Files {
		text += " " + strings.ToLower(f.Filename)
	}
	for tag, patterns := range map[string][]string{
		"api":         {"api", "openapi"},
		"cli":         {"cmd/", "cli"},
		"tui":         {"tui"},
		"web":         {"web/", "react", "css"},
		"docs":        {"docs/", ".md", "readme"},
		"testing":     {"test", "_test.go", ".spec."},
		"ci":          {".github", "workflow", "ci"},
		"devops":      {"docker", "deploy", "k8s", "terraform"},
		"storage":     {"store", "storage", "database", "sql"},
		"dolt":        {"dolt"},
		"sessions":    {"session"},
		"runtime":     {"runtime"},
		"config":      {"config"},
		"security":    {"security", "auth", "token"},
		"performance": {"perf", "performance"},
	} {
		for _, pattern := range patterns {
			if strings.Contains(text, pattern) {
				tags = append(tags, tag)
				break
			}
		}
	}
	return tags
}
