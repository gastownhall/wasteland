package sdk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
)

// githubCacheHookTimeout bounds the synchronous GitHub API call that runs
// after Accept and AcceptUpstream succeed. Kept short so stamp approval
// latency stays dominated by the DML, not the resolver.
const githubCacheHookTimeout = 5 * time.Second

// githubPREvidenceRegex matches a GitHub PR URL and captures owner/repo/N.
// Kept local so the SDK does not depend on cmd/wl or internal/pile parsing
// helpers.
var githubPREvidenceRegex = regexp.MustCompile(`^https?://github\.com/([^/?#\s]+)/([^/?#\s]+)/pull/(\d+)(?:$|[/?#])`)

// nowUTC is overridable so tests can pin ResolvedAt timestamps.
var nowUTC = func() time.Time { return time.Now().UTC() }

// SetGitHubCache injects the post-Accept GitHub-handle cache dependencies.
// Passing a nil cache disables the hook. Intended for tests; production
// callers rely on New to wire the defaults.
func (c *Client) SetGitHubCache(cache githubcache.Cache, resolver githubcache.Resolver, commonsReader pile.RowQuerier) {
	c.ghCache = cache
	c.ghResolver = resolver
	c.ghCommons = commonsReader
}

// populateGitHubCache is the post-Accept hook. It runs after the SDK mutex
// has been released so the blocking GitHub call cannot delay another
// mutation. All failures — including panics — are logged and swallowed;
// the hook must never surface an error or panic to the Accept caller,
// who has already received a committed MutationResult.
func (c *Client) populateGitHubCache(subject string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("stamp_cache: hook panicked; swallowing",
				"subject", subject, "panic", r)
		}
	}()
	if c.ghCache == nil || c.ghResolver == nil || c.ghCommons == nil {
		return
	}
	if subject == "" {
		return
	}
	if existing, ok := c.ghCache.Get(subject); ok && existing.GitHub != "" {
		slog.Debug("stamp_cache: skip; already resolved",
			"subject", subject, "github", existing.GitHub)
		return
	}

	prURL, err := findSubjectPRURL(c.ghCommons, subject)
	if err != nil {
		slog.Warn("stamp_cache: commons query failed; not caching",
			"subject", subject, "error", err)
		return
	}
	now := nowUTC().Format(time.RFC3339)
	if prURL == "" {
		entry := githubcache.Entry{ResolvedAt: now}
		if putErr := c.ghCache.Put(subject, entry); putErr != nil {
			slog.Warn("stamp_cache: cache write failed",
				"subject", subject, "error", putErr)
			return
		}
		slog.Info("stamp_cache: tried-and-failed", "subject", subject)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), githubCacheHookTimeout)
	defer cancel()
	login, err := c.ghResolver.ResolvePRAuthor(ctx, prURL)
	if err != nil {
		// Transient errors (missing token, HTTP 5xx, timeout, network)
		// must leave the cache untouched so the next Accept gets another
		// shot. "Not a GitHub PR URL" cannot happen here because we
		// only pass URLs that matched githubPREvidenceRegex.
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, githubcache.ErrNoToken) {
			slog.Warn("stamp_cache: resolver unavailable; skipping",
				"subject", subject, "pr_url", prURL, "error", err)
			return
		}
		slog.Warn("stamp_cache: resolver failed; skipping",
			"subject", subject, "pr_url", prURL, "error", err)
		return
	}
	entry := githubcache.Entry{GitHub: login, SourcePR: prURL, ResolvedAt: now}
	if putErr := c.ghCache.Put(subject, entry); putErr != nil {
		slog.Warn("stamp_cache: cache write failed",
			"subject", subject, "error", putErr)
		return
	}
	slog.Info("stamp_cache: resolved",
		"subject", subject, "github", login, "source_pr", prURL)
}

// findSubjectPRURL returns the most recent GitHub-PR-shaped evidence
// URL for the subject's stamps in hop/wl-commons, or "" if none match.
// The SQL LIKE prefilter uses explicit http:// / https:// prefixes so
// a near-miss scheme (e.g. "httpss://") doesn't consume a LIMIT slot
// and hide older valid PRs. The Go regex is still the authoritative
// check for strict URL shape.
func findSubjectPRURL(reader pile.RowQuerier, subject string) (string, error) {
	sql := fmt.Sprintf(
		`SELECT c.evidence FROM stamps s `+
			`LEFT JOIN completions c ON s.context_id = c.id `+
			`WHERE s.subject = '%s' AND (c.evidence LIKE 'https://github.com/%%/pull/%%' OR c.evidence LIKE 'http://github.com/%%/pull/%%') `+
			`ORDER BY s.created_at DESC, s.id DESC LIMIT 10`,
		commons.EscapeSQL(subject))
	rows, err := reader.QueryRows(sql)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		raw, _ := row["evidence"].(string)
		if raw == "" {
			continue
		}
		if githubPREvidenceRegex.MatchString(raw) {
			return raw, nil
		}
	}
	return "", nil
}
