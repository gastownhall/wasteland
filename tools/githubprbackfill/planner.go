package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

type planOptions struct {
	CommonsDir            string
	Repos                 []string
	IdentityOverridesPath string
	ScoreOverridesPath    string
	ExcludedLoginsPath    string
	GitHubCachePath       string
	OutPath               string
	FetchOnly             bool
	AllowDirty            bool
	LimitPerRepo          int
	GitHubMergedAfter     *time.Time
	GitHubPageSize        int
	GitHubRequestDelay    time.Duration
	GitHubParallelism     int
}

func runPlanWithOptions(ctx context.Context, opts planOptions) error {
	if len(opts.Repos) == 0 {
		return fmt.Errorf("at least one --repo is required")
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return fmt.Errorf("GITHUB_TOKEN is required")
	}
	cache, err := loadGitHubCache(opts.GitHubCachePath)
	if err != nil {
		return err
	}
	client := newGitHubClient(token, cache, opts.GitHubCachePath, opts.GitHubPageSize, opts.GitHubRequestDelay)
	client.parallelism = opts.GitHubParallelism
	client.mergedAfter = opts.GitHubMergedAfter
	var fetched []fetchedPR
	for _, repo := range opts.Repos {
		fmt.Fprintf(os.Stderr, "fetching %s\n", repo)
		prs, err := client.fetchRepo(ctx, repo, opts.LimitPerRepo)
		if err != nil {
			return err
		}
		fetched = append(fetched, prs...)
		fmt.Fprintf(os.Stderr, "fetched %d eligible merged main PRs for %s\n", len(prs), repo)
	}
	if err := cache.save(opts.GitHubCachePath); err != nil {
		return err
	}
	if opts.FetchOnly {
		return nil
	}
	if opts.OutPath == "" {
		return fmt.Errorf("--out is required unless --fetch-only is set")
	}
	commons, err := loadCommonsState(opts.CommonsDir, opts.AllowDirty)
	if err != nil {
		return err
	}
	identityOverrides, err := loadIdentityOverrides(opts.IdentityOverridesPath)
	if err != nil {
		return err
	}
	scoreOverrides, err := loadScoreOverrides(opts.ScoreOverridesPath)
	if err != nil {
		return err
	}
	excludedLogins, err := loadExcludedLogins(opts.ExcludedLoginsPath)
	if err != nil {
		return err
	}
	cacheHash, err := cache.hash()
	if err != nil {
		return err
	}
	manifest := buildManifest(opts, commons, identityOverrides, scoreOverrides, excludedLogins, fetched, cacheHash)
	if err := buildRows(&manifest); err != nil {
		return err
	}
	hash, err := manifestHash(manifest)
	if err != nil {
		return err
	}
	manifest.Hash = hash
	if err := buildRows(&manifest); err != nil {
		return err
	}
	return writeManifest(opts.OutPath, manifest)
}

func buildManifest(opts planOptions, commons commonsState, identityOverrides map[string]identityOverride, scoreOverrides map[string]scoreOverride, excludedLogins map[string]string, fetched []fetchedPR, cacheHash string) Manifest {
	now := time.Now().UTC().Format(time.RFC3339)
	m := Manifest{
		Version:        backfillVersion,
		GeneratedAt:    now,
		FormulaVersion: formulaVersion,
		Inputs: ManifestInputs{
			Repos:                 append([]string(nil), opts.Repos...),
			IdentityOverridesPath: opts.IdentityOverridesPath,
			ScoreOverridesPath:    opts.ScoreOverridesPath,
			ExcludedLoginsPath:    opts.ExcludedLoginsPath,
			ExcludedLogins:        excludedLoginList(excludedLogins),
			GitHubParallelism:     opts.GitHubParallelism,
			GitHubCacheHash:       cacheHash,
			GitHubFetchedAt:       now,
		},
		SourceState: SourceState{
			CommonsDir:    opts.CommonsDir,
			CommonsCommit: commons.Commit,
		},
	}
	if opts.GitHubMergedAfter != nil {
		m.Inputs.GitHubMergedAfter = opts.GitHubMergedAfter.UTC().Format(time.RFC3339)
	}
	reverted := detectRevertedPRs(fetched)
	mapped := make(map[string]IdentityMapping)
	for _, fp := range fetched {
		pr := prRecordFromFetched(fp)
		if reason, ok := excludedLoginReason(fp.Pull.User, excludedLogins); ok {
			pr.Reason = reason
			pr.Decision = "skip"
			m.PRs = append(m.PRs, pr)
			continue
		}
		if pr.Reason == "" {
			pr.Reason = eligibilityReason(fp, commons)
		}
		if pr.Reason != "" {
			pr.Decision = "skip"
			m.PRs = append(m.PRs, pr)
			continue
		}
		login := fp.Pull.User.Login
		handle, source, reason := resolveIdentity(login, commons, identityOverrides)
		pr.Subject = handle
		pr.Signals.IdentitySource = source
		pr.Decision = "stamp"
		if reverted[fp.Repo][fp.Pull.Number] {
			pr.Signals.LaterReverted = true
		}
		pr.Score = scorePR(pr)
		if ov, ok := scoreOverrides[fmt.Sprintf("%s#%d", fp.Repo, fp.Pull.Number)]; ok {
			pr.Score = Score{
				Quality:     ov.Quality,
				Reliability: ov.Reliability,
				Creativity:  ov.Creativity,
				Severity:    ov.Severity,
				Confidence:  ov.Confidence,
				Source:      "override",
				Rationale:   []string{ov.Reason},
			}
		}
		m.PRs = append(m.PRs, pr)
		mapped[strings.ToLower(login)] = IdentityMapping{
			GitHubLogin: login,
			Handle:      handle,
			Source:      source,
			Reason:      reason,
		}
	}
	for _, v := range mapped {
		m.IdentityMappings = append(m.IdentityMappings, v)
	}
	canonicalizeManifest(&m)
	return m
}

func excludedLoginReason(user *ghUser, excluded map[string]string) (string, bool) {
	if user == nil {
		return "", false
	}
	reason, ok := excluded[strings.ToLower(user.Login)]
	return reason, ok
}

func prRecordFromFetched(fp fetchedPR) PRRecord {
	pr := fp.Pull
	record := PRRecord{
		Repo:      fp.Repo,
		Number:    pr.Number,
		Title:     pr.Title,
		URL:       pr.HTMLURL,
		BaseRef:   pr.Base.Ref,
		CreatedAt: pr.CreatedAt,
		Decision:  "skip",
		Signals:   signalsFromFetched(fp),
	}
	if pr.User != nil {
		record.AuthorLogin = pr.User.Login
	}
	if pr.MergedAt != nil {
		record.MergedAt = *pr.MergedAt
	}
	return record
}

func eligibilityReason(fp fetchedPR, commons commonsState) string {
	if fp.Pull.MergedAt == nil {
		return "not_merged"
	}
	if fp.Pull.Base.Ref != "main" {
		return "non_main_base"
	}
	if fp.Pull.User == nil || fp.Pull.User.Login == "" {
		return "skipped_missing_author"
	}
	if fp.Pull.User.Type != "User" {
		return "skipped_bot_actor"
	}
	if commons.EvidenceURLs[canonicalEvidenceURL(fp.Pull.HTMLURL)] {
		return "already_stamped"
	}
	return ""
}

func resolveIdentity(login string, commons commonsState, overrides map[string]identityOverride) (string, string, string) {
	if handle, ok := commons.RigHandles[strings.ToLower(login)]; ok {
		return handle, "exact", "case-insensitive existing rig match"
	}
	if ov, ok := overrides[login]; ok {
		return ov.Handle, "override", ov.Reason
	}
	return githubHandle(login), "provisional", "created from GitHub login"
}

func signalsFromFetched(fp fetchedPR) Signals {
	signals := Signals{
		RawAdditions:     fp.Pull.Additions,
		RawDeletions:     fp.Pull.Deletions,
		RawChangedFiles:  fp.Pull.ChangedFiles,
		Labels:           labelsFromIssue(fp.Issue),
		SkillTags:        skillTags(fp),
		FeatureLike:      featureLike(fp),
		RuntimeOrAPILike: runtimeOrAPILike(fp),
	}
	var effectiveFiles int
	for _, file := range fp.Files {
		if generatedOrLowSignal(file.Filename) {
			continue
		}
		effectiveFiles++
		signals.EffectiveAdditions += file.Additions
		signals.EffectiveDeletions += file.Deletions
	}
	signals.EffectiveChangedFiles = effectiveFiles
	signals.NoEffectiveAuthoredFiles = effectiveFiles == 0
	signals.GeneratedOnly = len(fp.Files) > 0 && effectiveFiles == 0
	signals.DependencyOnly = dependencyOnly(fp.Files)
	signals.DocsOnly = docsOnly(fp.Files)
	signals.CIOnly = ciOnly(fp.Files)
	signals.MechanicalOnly = mechanicalLike(fp)
	signals.RevertOnly = revertLike(fp.Pull.Title)
	signals.StrongBlockingReview = strongBlockingSignal(fp)
	signals.MaintainerCommits = maintainerCommitCount(fp)
	signals.PostReviewAuthorChanges = postReviewAuthorChanges(fp)
	signals.ReviewFixupCommit = reviewFixupCommit(fp)
	signals.LargeGeneratedDiff = (signals.RawAdditions+signals.RawDeletions) >= 1500 && (signals.EffectiveAdditions+signals.EffectiveDeletions) < 300
	return signals
}

func labelsFromIssue(issue ghIssue) []string {
	var labels []string
	for _, label := range issue.Labels {
		labels = append(labels, label.Name)
	}
	sort.Strings(labels)
	return labels
}

func detectRevertedPRs(fetched []fetchedPR) map[string]map[int]bool {
	out := make(map[string]map[int]bool)
	re := regexp.MustCompile(`(?i)\b(?:reverts?|backs out|undo)\s+(?:https://github\.com/[^/\s]+/[^/\s]+/pull/|[a-z0-9_.-]+/[a-z0-9_.-]+#|#)(\d+)\b`)
	for _, fp := range fetched {
		text := fp.Pull.Title + "\n" + fp.Pull.Body
		for _, commit := range fp.Commits {
			text += "\n" + commit.Commit.Message
		}
		for _, match := range re.FindAllStringSubmatch(text, -1) {
			var n int
			_, _ = fmt.Sscanf(match[1], "%d", &n)
			if n <= 0 || n == fp.Pull.Number {
				continue
			}
			if out[fp.Repo] == nil {
				out[fp.Repo] = make(map[int]bool)
			}
			out[fp.Repo][n] = true
		}
	}
	return out
}

func writeManifest(path string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.MkdirAll(pathDir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest dir: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write manifest temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace manifest: %w", err)
	}
	return nil
}

func pathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func repoName(repo string) string {
	return path.Base(repo)
}
