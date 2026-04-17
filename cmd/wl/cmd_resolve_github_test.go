package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
)

// fakeCache is an in-memory githubcache.Cache for tests.
type fakeCache struct {
	mu      sync.Mutex
	entries map[string]githubcache.Entry
}

func newFakeCache(seed map[string]githubcache.Entry) *fakeCache {
	c := &fakeCache{entries: map[string]githubcache.Entry{}}
	for k, v := range seed {
		c.entries[k] = v
	}
	return c
}

func (c *fakeCache) Get(handle string) (githubcache.Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[handle]
	return e, ok
}

func (c *fakeCache) Put(handle string, entry githubcache.Entry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[handle] = entry
	return nil
}

func (c *fakeCache) All() map[string]githubcache.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]githubcache.Entry, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}

// fakeResolver returns canned logins or errors keyed by PR URL.
// errOnAny short-circuits every call with the same error, handy for
// simulating a missing token for every subject in --all.
type fakeResolver struct {
	responses map[string]string
	errs      map[string]error
	errOnAny  error
	calls     []string
}

func (r *fakeResolver) ResolvePRAuthor(_ context.Context, prURL string) (string, error) {
	r.calls = append(r.calls, prURL)
	if r.errOnAny != nil {
		return "", r.errOnAny
	}
	if err, ok := r.errs[prURL]; ok {
		return "", err
	}
	if login, ok := r.responses[prURL]; ok {
		return login, nil
	}
	return "", fmt.Errorf("unexpected PR URL %q", prURL)
}

// fakeRowQuerier maps SQL fragments to canned row sets.
type fakeRowQuerier struct {
	handler func(sql string) ([]map[string]any, error)
}

func (f *fakeRowQuerier) QueryRows(sql string) ([]map[string]any, error) {
	return f.handler(sql)
}

// withResolveGitHubOverrides swaps the package-level deps for tests.
func withResolveGitHubOverrides(
	t *testing.T,
	cache githubcache.Cache,
	reader pile.RowQuerier,
	resolver githubcache.Resolver,
	now time.Time,
) {
	t.Helper()
	oldLoad := loadGitHubCache
	oldResolver := newGitHubResolver
	oldReader := newCommonsReaderForCmd
	oldNow := resolveNow
	loadGitHubCache = func() (githubcache.Cache, error) { return cache, nil }
	newGitHubResolver = func() githubcache.Resolver { return resolver }
	newCommonsReaderForCmd = func() pile.RowQuerier { return reader }
	resolveNow = func() time.Time { return now }
	t.Cleanup(func() {
		loadGitHubCache = oldLoad
		newGitHubResolver = oldResolver
		newCommonsReaderForCmd = oldReader
		resolveNow = oldNow
	})
}

func fixedNow() time.Time {
	return time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
}

func TestResolveGitHub_Single_Success(t *testing.T) {
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(sql string) ([]map[string]any, error) {
		if !strings.Contains(sql, "s.subject = 'alice'") {
			t.Fatalf("unexpected sql: %s", sql)
		}
		return []map[string]any{
			{"evidence": "not a url"},
			{"evidence": "https://github.com/gastownhall/gascity/pull/548"},
		}, nil
	}}
	resolver := &fakeResolver{
		responses: map[string]string{
			"https://github.com/gastownhall/gascity/pull/548": "alice-gh",
		},
	}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "alice"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if want := "Resolved alice \u2192 alice-gh (via gastownhall/gascity#548)."; !strings.Contains(stdout.String(), want) {
		t.Fatalf("stdout missing success line: %q", stdout.String())
	}
	got, ok := cache.Get("alice")
	if !ok {
		t.Fatalf("cache missing alice")
	}
	if got.GitHub != "alice-gh" ||
		got.SourcePR != "https://github.com/gastownhall/gascity/pull/548" ||
		got.ResolvedAt != fixedNow().Format(time.RFC3339) {
		t.Fatalf("entry = %+v", got)
	}
}

func TestResolveGitHub_Single_NoPRURL(t *testing.T) {
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(string) ([]map[string]any, error) {
		return []map[string]any{
			{"evidence": "some prose about the work"},
			{"evidence": "https://github.com/foo/bar"}, // repo URL, not PR
		}, nil
	}}
	resolver := &fakeResolver{}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "rome"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No resolvable PR URL for \"rome\"") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	got, ok := cache.Get("rome")
	if !ok {
		t.Fatalf("tried-and-failed entry not written")
	}
	if got.GitHub != "" || got.SourcePR != "" || got.ResolvedAt == "" {
		t.Fatalf("entry = %+v", got)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver should not be called; calls = %v", resolver.calls)
	}
}

func TestResolveGitHub_Single_NoToken(t *testing.T) {
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(string) ([]map[string]any, error) {
		return []map[string]any{
			{"evidence": "https://github.com/gastownhall/gascity/pull/1"},
		}, nil
	}}
	resolver := &fakeResolver{
		errs: map[string]error{
			"https://github.com/gastownhall/gascity/pull/1": githubcache.ErrNoToken,
		},
	}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "alice"}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected non-zero exit")
	}
	if !strings.Contains(stderr.String(), "GITHUB_TOKEN is not set") {
		t.Fatalf("stderr missing helpful message: %q", stderr.String())
	}
	if _, ok := cache.Get("alice"); ok {
		t.Fatalf("cache must not be written on resolver error")
	}
}

func TestResolveGitHub_All_MixedOutcomes(t *testing.T) {
	cache := newFakeCache(map[string]githubcache.Entry{
		"alice": {GitHub: "alice-gh", SourcePR: "https://github.com/x/y/pull/1", ResolvedAt: "2026-04-01T00:00:00Z"},
		"rome":  {GitHub: "", ResolvedAt: "2026-04-01T00:00:00Z"}, // tried-and-failed; should retry
	})
	reader := &fakeRowQuerier{handler: func(sql string) ([]map[string]any, error) {
		switch {
		case strings.Contains(sql, "SELECT DISTINCT subject"):
			return []map[string]any{
				{"subject": "alice"},
				{"subject": "rome"},
				{"subject": "zed"},
			}, nil
		case strings.Contains(sql, "s.subject = 'rome'"):
			// rome still has no PR URL evidence
			return []map[string]any{{"evidence": "describing work"}}, nil
		case strings.Contains(sql, "s.subject = 'zed'"):
			return []map[string]any{
				{"evidence": "https://github.com/foo/bar/pull/9"},
			}, nil
		default:
			t.Fatalf("unexpected sql: %s", sql)
			return nil, nil
		}
	}}
	resolver := &fakeResolver{
		responses: map[string]string{
			"https://github.com/foo/bar/pull/9": "zed-gh",
		},
	}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"Skipped alice",
		"No resolvable PR URL for \"rome\"",
		"Resolved zed \u2192 zed-gh (via foo/bar#9).",
		"Resolved 1, skipped 1 (already cached), tried-and-failed 1, errored 0.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q: %q", want, out)
		}
	}
	// alice untouched
	if got, _ := cache.Get("alice"); got.GitHub != "alice-gh" {
		t.Fatalf("alice should not be re-resolved: %+v", got)
	}
	// zed added
	if got, ok := cache.Get("zed"); !ok || got.GitHub != "zed-gh" {
		t.Fatalf("zed not added: %+v", got)
	}
}

func TestResolveGitHub_All_Refresh(t *testing.T) {
	cache := newFakeCache(map[string]githubcache.Entry{
		"alice": {GitHub: "old-login", ResolvedAt: "2026-04-01T00:00:00Z"},
	})
	reader := &fakeRowQuerier{handler: func(sql string) ([]map[string]any, error) {
		switch {
		case strings.Contains(sql, "SELECT DISTINCT subject"):
			return []map[string]any{{"subject": "alice"}}, nil
		case strings.Contains(sql, "s.subject = 'alice'"):
			return []map[string]any{
				{"evidence": "https://github.com/foo/bar/pull/2"},
			}, nil
		default:
			t.Fatalf("unexpected sql: %s", sql)
			return nil, nil
		}
	}}
	resolver := &fakeResolver{
		responses: map[string]string{
			"https://github.com/foo/bar/pull/2": "alice-new",
		},
	}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "--all", "--refresh"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit = %d stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Resolved alice \u2192 alice-new") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	got, _ := cache.Get("alice")
	if got.GitHub != "alice-new" || got.SourcePR != "https://github.com/foo/bar/pull/2" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestResolveGitHub_ArgValidation(t *testing.T) {
	// --all + positional should error
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(string) ([]map[string]any, error) { return nil, nil }}
	resolver := &fakeResolver{}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "--all", "alice"}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected failure with --all + positional")
	}
	if !strings.Contains(stderr.String(), "--all takes no positional") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"resolve-github"}, &stdout, &stderr); code == 0 {
		t.Fatalf("expected failure with no args")
	}
	if !strings.Contains(stderr.String(), "provide exactly one handle") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// Sanity: if the resolver returns a transient error during --all, the
// batch continues and the summary counts it as errored. Exit code is
// non-zero so cron / CI can distinguish clean batches from limping ones.
func TestResolveGitHub_All_ContinueOnError(t *testing.T) {
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(sql string) ([]map[string]any, error) {
		switch {
		case strings.Contains(sql, "SELECT DISTINCT subject"):
			return []map[string]any{{"subject": "alice"}, {"subject": "bob"}}, nil
		case strings.Contains(sql, "s.subject = 'alice'"):
			return []map[string]any{{"evidence": "https://github.com/o/r/pull/1"}}, nil
		case strings.Contains(sql, "s.subject = 'bob'"):
			return []map[string]any{{"evidence": "https://github.com/o/r/pull/2"}}, nil
		}
		return nil, nil
	}}
	resolver := &fakeResolver{
		responses: map[string]string{"https://github.com/o/r/pull/2": "bob-gh"},
		errs:      map[string]error{"https://github.com/o/r/pull/1": errors.New("boom")},
	}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "--all"}, &stdout, &stderr); code == 0 {
		t.Fatalf("exit code should be non-zero when batch had errors, got 0; stdout=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Resolved 1, skipped 0 (already cached), tried-and-failed 0, errored 1.") {
		t.Fatalf("summary missing: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "alice") {
		t.Fatalf("stderr should mention alice: %q", stderr.String())
	}
}

// --all hits ErrNoToken on the first handle and bails fast rather than
// printing N identical errors.
func TestResolveGitHub_All_NoTokenBailsFast(t *testing.T) {
	cache := newFakeCache(nil)
	reader := &fakeRowQuerier{handler: func(sql string) ([]map[string]any, error) {
		switch {
		case strings.Contains(sql, "SELECT DISTINCT subject"):
			return []map[string]any{{"subject": "alice"}, {"subject": "bob"}, {"subject": "charlie"}}, nil
		case strings.Contains(sql, "s.subject ="):
			return []map[string]any{{"evidence": "https://github.com/o/r/pull/1"}}, nil
		}
		return nil, nil
	}}
	resolver := &fakeResolver{errOnAny: githubcache.ErrNoToken}
	withResolveGitHubOverrides(t, cache, reader, resolver, fixedNow())

	var stdout, stderr bytes.Buffer
	if code := run([]string{"resolve-github", "--all"}, &stdout, &stderr); code == 0 {
		t.Fatalf("exit should be non-zero on token failure")
	}
	if !strings.Contains(stderr.String(), "GITHUB_TOKEN is not set") {
		t.Fatalf("stderr missing token guidance: %q", stderr.String())
	}
	// Should NOT contain a second "charlie" error — bailed after first.
	if strings.Count(stderr.String(), "GITHUB_TOKEN is not set") != 1 {
		t.Fatalf("token message repeated; should bail after first hit: %q", stderr.String())
	}
}
