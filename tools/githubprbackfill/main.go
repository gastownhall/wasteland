package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: githubprbackfill <plan|explain-pr|render-sql|validate>")
	}
	switch args[0] {
	case "plan":
		return runPlan(args[1:])
	case "explain-pr":
		return runExplainPR(args[1:])
	case "render-sql":
		return runRenderSQL(args[1:])
	case "validate":
		return runValidate(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	var repos repoFlags
	commonsDir := fs.String("commons-dir", "", "local wl-commons clone")
	identityOverrides := fs.String("identity-overrides", "", "identity override JSON")
	scoreOverrides := fs.String("score-overrides", "", "optional PR score override JSON")
	excludedLogins := fs.String("excluded-logins", "", "optional JSON map or array of GitHub logins to exclude")
	githubCache := fs.String("github-cache", "", "optional resumable GitHub cache")
	fetchOnly := fs.Bool("fetch-only", false, "only populate GitHub cache")
	out := fs.String("out", "", "manifest output path")
	allowDirty := fs.Bool("allow-dirty", false, "allow dirty commons clone")
	limitPerRepo := fs.Int("limit-per-repo", 0, "limit eligible merged main PRs per repo")
	mergedAfter := fs.String("merged-after", "", "only include PRs merged after this UTC timestamp or YYYY-MM-DD date")
	sinceDays := fs.Int("since-days", 0, "only include PRs merged in the last N days")
	githubPageSize := fs.Int("github-page-size", 100, "GitHub page size")
	githubRequestDelay := fs.Duration("github-request-delay", 100*time.Millisecond, "delay between uncached GitHub requests")
	githubParallelism := fs.Int("github-parallelism", 4, "parallel PR detail fetches per repo")
	fs.Var(&repos, "repo", "GitHub repo, e.g. gastownhall/gascity; repeatable")
	if err := fs.Parse(args); err != nil {
		return err
	}
	parsedMergedAfter, err := parseMergedAfter(*mergedAfter, *sinceDays, time.Now().UTC())
	if err != nil {
		return err
	}
	opts := planOptions{
		CommonsDir:            *commonsDir,
		Repos:                 repos,
		IdentityOverridesPath: *identityOverrides,
		ScoreOverridesPath:    *scoreOverrides,
		ExcludedLoginsPath:    *excludedLogins,
		GitHubCachePath:       *githubCache,
		OutPath:               *out,
		FetchOnly:             *fetchOnly,
		AllowDirty:            *allowDirty,
		LimitPerRepo:          *limitPerRepo,
		GitHubMergedAfter:     parsedMergedAfter,
		GitHubPageSize:        *githubPageSize,
		GitHubRequestDelay:    *githubRequestDelay,
		GitHubParallelism:     *githubParallelism,
	}
	return runPlanWithOptions(context.Background(), opts)
}

func parseMergedAfter(value string, sinceDays int, now time.Time) (*time.Time, error) {
	if value != "" && sinceDays > 0 {
		return nil, fmt.Errorf("--merged-after and --since-days are mutually exclusive")
	}
	if sinceDays < 0 {
		return nil, fmt.Errorf("--since-days must be >= 0")
	}
	if sinceDays > 0 {
		t := now.Add(-time.Duration(sinceDays) * 24 * time.Hour).UTC()
		return &t, nil
	}
	if value == "" {
		return nil, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		utc := t.UTC()
		return &utc, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		utc := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return &utc, nil
	}
	return nil, fmt.Errorf("--merged-after must be RFC3339 or YYYY-MM-DD")
}

func runRenderSQL(args []string) error {
	fs := flag.NewFlagSet("render-sql", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "reviewed manifest path")
	expectHash := fs.String("expect-hash", "", "expected canonical manifest hash")
	outDir := fs.String("out-dir", "", "directory for chunked SQL")
	combinedOut := fs.String("combined-out", "", "optional combined SQL file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *expectHash == "" {
		return fmt.Errorf("render-sql requires --manifest and --expect-hash")
	}
	if *outDir == "" && *combinedOut == "" {
		return fmt.Errorf("render-sql requires --out-dir or --combined-out")
	}
	m, err := readManifest(*manifestPath)
	if err != nil {
		return err
	}
	if m.Hash == "" {
		if err := buildRows(&m); err != nil {
			return err
		}
		h, err := manifestHash(m)
		if err != nil {
			return err
		}
		m.Hash = h
		if err := buildRows(&m); err != nil {
			return err
		}
	}
	got, err := manifestHash(m)
	if err != nil {
		return err
	}
	if got != *expectHash {
		return fmt.Errorf("manifest hash mismatch: got %s, want %s", got, *expectHash)
	}
	if err := validateManifest(m); err != nil {
		return err
	}
	files := renderSQLFiles(m)
	return writeSQLFiles(*outDir, *combinedOut, files)
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "reviewed manifest path")
	expectHash := fs.String("expect-hash", "", "expected canonical manifest hash")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" || *expectHash == "" {
		return fmt.Errorf("validate requires --manifest and --expect-hash")
	}
	m, err := readManifest(*manifestPath)
	if err != nil {
		return err
	}
	got, err := manifestHash(m)
	if err != nil {
		return err
	}
	if got != *expectHash {
		return fmt.Errorf("manifest hash mismatch: got %s, want %s", got, *expectHash)
	}
	return validateManifest(m)
}

type repoFlags []string

func (r *repoFlags) String() string {
	return fmt.Sprint([]string(*r))
}

func (r *repoFlags) Set(value string) error {
	if value == "" {
		return fmt.Errorf("repo cannot be empty")
	}
	*r = append(*r, value)
	return nil
}
