package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/spf13/cobra"
)

// Overridable package vars so tests can inject fakes without touching
// environment or disk.
var (
	loadGitHubCache        = githubcache.Load
	newGitHubResolver      = githubcache.NewResolver
	newCommonsReaderForCmd = func() pile.RowQuerier { return pile.NewCommonsReader() }
	resolveNow             = func() time.Time { return time.Now().UTC() }
)

// prEvidenceRegex mirrors the canonical PR-URL regex used elsewhere.
var prEvidenceRegex = regexp.MustCompile(`^https?://github\.com/([^/?#\s]+)/([^/?#\s]+)/pull/(\d+)(?:$|[/?#])`)

func newResolveGitHubCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve-github [handle]",
		Short: "Resolve a rig handle to its GitHub username via stamp PR authorship",
		Long: `Populate the local GitHub handle cache by inspecting stamp evidence URLs
in hop/wl-commons and calling the GitHub REST API for PR authorship.

Requires GITHUB_TOKEN (a fine-grained PAT with public_repo read).

Examples:
  wl resolve-github alice            # Resolve one handle (always re-resolves)
  wl resolve-github --all            # Resolve every observed handle, skipping cached
  wl resolve-github --all --refresh  # Force re-resolution of cached entries`,
		Args: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			if all {
				if len(args) != 0 {
					return fmt.Errorf("--all takes no positional arguments")
				}
				return nil
			}
			if len(args) != 1 {
				return fmt.Errorf("provide exactly one handle or use --all")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			all, _ := cmd.Flags().GetBool("all")
			refresh, _ := cmd.Flags().GetBool("refresh")
			if all {
				return runResolveGitHubAll(cmd.Context(), stdout, stderr, refresh)
			}
			return runResolveGitHubOne(cmd.Context(), stdout, stderr, args[0])
		},
	}

	cmd.Flags().Bool("all", false, "Resolve all handles observed in hop/wl-commons stamps")
	cmd.Flags().Bool("refresh", false, "Re-resolve entries even if already cached")
	return cmd
}

// runResolveGitHubOne resolves a single handle and writes its cache entry.
func runResolveGitHubOne(ctx context.Context, stdout, stderr io.Writer, handle string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cache, err := loadGitHubCache()
	if err != nil {
		return fmt.Errorf("resolve-github: loading cache: %w", err)
	}
	reader := newCommonsReaderForCmd()
	resolver := newGitHubResolver()

	outcome, err := resolveHandle(ctx, cache, reader, resolver, handle)
	if err != nil {
		return writeResolverError(stderr, handle, err)
	}
	switch outcome.kind {
	case outcomeResolved:
		fmt.Fprintf(stdout, "Resolved %s \u2192 %s (via %s).\n",
			handle, outcome.login, outcome.label)
	case outcomeTriedAndFailed:
		fmt.Fprintf(stdout, "No resolvable PR URL for %q \u2014 marked tried-and-failed.\n", handle)
	}
	return nil
}

// runResolveGitHubAll iterates every distinct stamp subject and resolves
// those that are absent, tried-and-failed, or being refreshed.
func runResolveGitHubAll(ctx context.Context, stdout, stderr io.Writer, refresh bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cache, err := loadGitHubCache()
	if err != nil {
		return fmt.Errorf("resolve-github: loading cache: %w", err)
	}
	reader := newCommonsReaderForCmd()
	resolver := newGitHubResolver()

	subjects, err := listStampSubjects(reader)
	if err != nil {
		return fmt.Errorf("resolve-github: listing subjects: %w", err)
	}

	var resolved, skipped, triedFailed, errored int
	for _, handle := range subjects {
		existing, ok := cache.Get(handle)
		if ok && existing.GitHub != "" && !refresh {
			fmt.Fprintf(stdout, "Skipped %s (already cached as %s).\n", handle, existing.GitHub)
			skipped++
			continue
		}
		outcome, err := resolveHandle(ctx, cache, reader, resolver, handle)
		if err != nil {
			// A missing token will fail every subsequent call too —
			// bail fast with a single clear message.
			if errors.Is(err, githubcache.ErrNoToken) {
				fmt.Fprintln(stderr, "resolve-github: GITHUB_TOKEN is not set \u2014 set a fine-grained PAT with public_repo read to continue.")
				return errExit
			}
			fmt.Fprintf(stderr, "resolve-github: %s: %v\n", handle, err)
			errored++
			continue
		}
		switch outcome.kind {
		case outcomeResolved:
			fmt.Fprintf(stdout, "Resolved %s \u2192 %s (via %s).\n",
				handle, outcome.login, outcome.label)
			resolved++
		case outcomeTriedAndFailed:
			fmt.Fprintf(stdout, "No resolvable PR URL for %q \u2014 marked tried-and-failed.\n", handle)
			triedFailed++
		}
	}

	fmt.Fprintf(stdout,
		"Resolved %d, skipped %d (already cached), tried-and-failed %d, errored %d.\n",
		resolved, skipped, triedFailed, errored)
	// Non-zero exit when one or more handles errored so cron / CI can
	// distinguish "batch succeeded" from "batch limped through failures".
	if errored > 0 {
		return errExit
	}
	return nil
}

type outcomeKind int

const (
	outcomeResolved outcomeKind = iota
	outcomeTriedAndFailed
)

type resolveOutcome struct {
	kind  outcomeKind
	login string
	label string // e.g. "owner/repo#N"
}

// resolveHandle runs the single-handle resolution logic used by both
// single-handle and --all flows. It writes to the cache on success or
// tried-and-failed outcomes and returns an error only when the caller
// should surface it (resolver failure, commons query failure, cache
// write failure).
func resolveHandle(ctx context.Context, cache githubcache.Cache, reader pile.RowQuerier, resolver githubcache.Resolver, handle string) (resolveOutcome, error) {
	prURL, label, err := findFirstPRURL(reader, handle)
	if err != nil {
		return resolveOutcome{}, err
	}
	now := resolveNow().Format(time.RFC3339)
	if prURL == "" {
		if putErr := cache.Put(handle, githubcache.Entry{ResolvedAt: now}); putErr != nil {
			return resolveOutcome{}, fmt.Errorf("writing cache: %w", putErr)
		}
		return resolveOutcome{kind: outcomeTriedAndFailed}, nil
	}
	login, err := resolver.ResolvePRAuthor(ctx, prURL)
	if err != nil {
		return resolveOutcome{}, err
	}
	entry := githubcache.Entry{
		GitHub:     login,
		SourcePR:   prURL,
		ResolvedAt: now,
	}
	if putErr := cache.Put(handle, entry); putErr != nil {
		return resolveOutcome{}, fmt.Errorf("writing cache: %w", putErr)
	}
	return resolveOutcome{kind: outcomeResolved, login: login, label: label}, nil
}

// findFirstPRURL returns the most recent evidence URL for the handle
// that parses as a GitHub PR URL, along with its owner/repo#N label.
// The SQL LIKE prefilter uses explicit http:// / https:// prefixes so
// a near-miss scheme (e.g. "httpss://") doesn't consume a LIMIT slot
// and hide older valid PRs. The Go regex is still the authoritative
// check for strict URL shape.
func findFirstPRURL(reader pile.RowQuerier, handle string) (string, string, error) {
	sql := fmt.Sprintf(
		`SELECT c.evidence FROM stamps s `+
			`LEFT JOIN completions c ON s.context_id = c.id `+
			`WHERE s.subject = '%s' AND (c.evidence LIKE 'https://github.com/%%/pull/%%' OR c.evidence LIKE 'http://github.com/%%/pull/%%') `+
			`ORDER BY s.created_at DESC, s.id DESC LIMIT 10`,
		commons.EscapeSQL(handle))
	rows, err := reader.QueryRows(sql)
	if err != nil {
		return "", "", fmt.Errorf("querying stamps: %w", err)
	}
	for _, row := range rows {
		raw, _ := row["evidence"].(string)
		if raw == "" {
			continue
		}
		if m := prEvidenceRegex.FindStringSubmatch(raw); m != nil {
			return raw, fmt.Sprintf("%s/%s#%s", m[1], m[2], m[3]), nil
		}
	}
	return "", "", nil
}

// listStampSubjects returns the distinct, sorted set of handles that
// appear as subjects of any stamp in hop/wl-commons.
func listStampSubjects(reader pile.RowQuerier) ([]string, error) {
	rows, err := reader.QueryRows(`SELECT DISTINCT subject FROM stamps`)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		s, _ := row["subject"].(string)
		if s == "" {
			continue
		}
		seen[s] = struct{}{}
	}
	subjects := make([]string, 0, len(seen))
	for s := range seen {
		subjects = append(subjects, s)
	}
	sort.Strings(subjects)
	return subjects, nil
}

// PrimeGitHubCacheAsync fires off a background `resolve-github --all`
// equivalent so a freshly deployed `wl serve` (especially Railway, where
// the cache file starts empty after a deploy onto a fresh persistent
// volume) self-populates without an ops step. Set WL_SKIP_CACHE_PRIME=1
// to disable — handy in local dev to avoid burning GitHub API quota.
//
// Runs in a goroutine. All errors are logged and swallowed. Idempotent:
// subsequent runs skip handles already cached (no --refresh).
func PrimeGitHubCacheAsync() {
	if os.Getenv("WL_SKIP_CACHE_PRIME") == "1" {
		slog.Info("stamp_cache: startup prime skipped (WL_SKIP_CACHE_PRIME=1)")
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("stamp_cache: startup prime panic; swallowing", "panic", r)
			}
		}()
		slog.Info("stamp_cache: startup prime started")
		start := time.Now()
		// runResolveGitHubAll honors cache state (skips resolved,
		// retries tried-and-failed) so repeat runs are cheap. Discard
		// stdout chatter; send stderr to slog-shaped lines via a tiny
		// adapter so operators see errors in the structured log.
		err := runResolveGitHubAll(context.Background(), io.Discard, slogWriter{level: slog.LevelWarn}, false)
		elapsed := time.Since(start).Round(time.Second)
		if err != nil {
			slog.Warn("stamp_cache: startup prime completed with errors",
				"error", err, "elapsed", elapsed)
			return
		}
		slog.Info("stamp_cache: startup prime complete", "elapsed", elapsed)
	}()
}

// slogWriter adapts io.Writer so stderr output from the batch resolver
// lands in the structured log rather than on a detached stream.
type slogWriter struct {
	level slog.Level
}

func (w slogWriter) Write(p []byte) (int, error) {
	msg := string(p)
	// Trim trailing newline that fmt.Fprintln adds so the log line
	// isn't doubled.
	if n := len(msg); n > 0 && msg[n-1] == '\n' {
		msg = msg[:n-1]
	}
	slog.Log(context.Background(), w.level, msg)
	return len(p), nil
}

// writeResolverError prints an operator-friendly stderr message and
// returns errExit so the cobra runner surfaces a non-zero exit without
// a duplicate "wl:" prefix.
func writeResolverError(stderr io.Writer, handle string, err error) error {
	if errors.Is(err, githubcache.ErrNoToken) {
		fmt.Fprintln(stderr, "resolve-github: GITHUB_TOKEN is not set \u2014 set a fine-grained PAT with public_repo read to continue.")
		return errExit
	}
	fmt.Fprintf(stderr, "resolve-github: %s: %v\n", handle, err)
	return errExit
}
