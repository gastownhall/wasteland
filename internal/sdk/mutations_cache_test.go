package sdk

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gastownhall/wasteland/internal/githubcache"
)

// --- test doubles for the post-Accept GitHub-handle cache hook ---

type fakeCache struct {
	mu      sync.Mutex
	entries map[string]githubcache.Entry
	putErr  error
	gets    int
	puts    int
}

func newFakeCache() *fakeCache {
	return &fakeCache{entries: map[string]githubcache.Entry{}}
}

func (f *fakeCache) Get(handle string) (githubcache.Entry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	e, ok := f.entries[handle]
	return e, ok
}

func (f *fakeCache) Put(handle string, entry githubcache.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.entries[handle] = entry
	return nil
}

func (f *fakeCache) All() map[string]githubcache.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]githubcache.Entry, len(f.entries))
	for k, v := range f.entries {
		out[k] = v
	}
	return out
}

type fakeResolver struct {
	mu     sync.Mutex
	calls  int
	login  string
	err    error
	lastPR string
}

func (r *fakeResolver) ResolvePRAuthor(_ context.Context, prURL string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastPR = prURL
	if r.err != nil {
		return "", r.err
	}
	return r.login, nil
}

// fakeCommonsReader implements pile.RowQuerier by matching canned responses
// against the subject found in the WHERE clause. The lookup is by substring
// because the SDK interpolates the handle into the SQL text.
type fakeCommonsReader struct {
	mu        sync.Mutex
	calls     int
	queryErr  error
	bySubject map[string][]map[string]any // subject → rows
}

func newFakeCommonsReader() *fakeCommonsReader {
	return &fakeCommonsReader{bySubject: map[string][]map[string]any{}}
}

func (r *fakeCommonsReader) QueryRows(sql string) ([]map[string]any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.queryErr != nil {
		return nil, r.queryErr
	}
	for subject, rows := range r.bySubject {
		// SDK's query interpolates 'subject' literally; look for it.
		if containsLiteral(sql, "'"+subject+"'") {
			return rows, nil
		}
	}
	return nil, nil
}

func containsLiteral(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	// avoid pulling strings just for Index — simple, test-only impl
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

// setGitHubCacheTestDefaults wires fakes onto a client for a given subject
// with one PR-shaped evidence row.
func setGitHubCacheTestDefaults(c *Client, subject, prURL, login string) (*fakeCache, *fakeResolver, *fakeCommonsReader) {
	cache := newFakeCache()
	resolver := &fakeResolver{login: login}
	reader := newFakeCommonsReader()
	reader.bySubject[subject] = []map[string]any{
		{"evidence": prURL},
	}
	c.SetGitHubCache(cache, resolver, reader)
	return cache, resolver, reader
}

// --- tests ---

func TestPopulateGitHubCache_NilCacheNoOp(_ *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	c.SetGitHubCache(nil, nil, nil)
	// Should not panic or error — no assertion needed beyond that.
	c.populateGitHubCache("bob")
}

func TestAccept_TriggersPopulateGitHubCache(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})
	db.completions["w-1"] = &fakeCompletion{ID: "c-1", WantedID: "w-1", CompletedBy: "bob", Evidence: "proof"}

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	cache, resolver, reader := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")

	if _, err := c.Accept("w-1", AcceptInput{Quality: 4, Reliability: 4}); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if reader.calls != 1 {
		t.Fatalf("commons reader calls = %d, want 1", reader.calls)
	}
	entry, ok := cache.entries["bob"]
	if !ok {
		t.Fatal("expected cache entry for bob, got none")
	}
	if entry.GitHub != "bob-gh" {
		t.Errorf("GitHub = %q, want bob-gh", entry.GitHub)
	}
	if entry.SourcePR != "https://github.com/foo/bar/pull/42" {
		t.Errorf("SourcePR = %q, want the pr_url", entry.SourcePR)
	}
	if entry.ResolvedAt == "" {
		t.Error("expected ResolvedAt to be populated")
	}
}

func TestAccept_SkipsHookWhenAlreadyResolved(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})
	db.completions["w-1"] = &fakeCompletion{ID: "c-1", WantedID: "w-1", CompletedBy: "bob", Evidence: "proof"}

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	cache, resolver, reader := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")
	cache.entries["bob"] = githubcache.Entry{GitHub: "already-known"}

	if _, err := c.Accept("w-1", AcceptInput{Quality: 4, Reliability: 4}); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0 (cache hit should short-circuit)", resolver.calls)
	}
	if reader.calls != 0 {
		t.Errorf("reader calls = %d, want 0 (cache hit should short-circuit)", reader.calls)
	}
	if cache.entries["bob"].GitHub != "already-known" {
		t.Error("cache entry should not have been overwritten")
	}
}

func TestAccept_RetriesTriedAndFailed(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})
	db.completions["w-1"] = &fakeCompletion{ID: "c-1", WantedID: "w-1", CompletedBy: "bob", Evidence: "proof"}

	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	cache, resolver, _ := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")
	cache.entries["bob"] = githubcache.Entry{GitHub: "", ResolvedAt: "2020-01-01T00:00:00Z"} // tried-and-failed

	if _, err := c.Accept("w-1", AcceptInput{Quality: 4, Reliability: 4}); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1 (tried-and-failed should retry)", resolver.calls)
	}
	if cache.entries["bob"].GitHub != "bob-gh" {
		t.Errorf("expected retry to upgrade entry to bob-gh, got %q", cache.entries["bob"].GitHub)
	}
}

func TestAccept_DMLError_SkipsHook(t *testing.T) {
	db := newFakeDB()
	// No item seeded → QueryCompletion returns nil → Accept fails before DML.
	c := New(ClientConfig{DB: db, RigHandle: "alice", Mode: "wild-west"})
	_, resolver, reader := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")

	_, err := c.Accept("w-missing", AcceptInput{Quality: 4, Reliability: 4})
	if err == nil {
		t.Fatal("expected error for missing completion, got nil")
	}

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0 when Accept fails", resolver.calls)
	}
	if reader.calls != 0 {
		t.Errorf("reader calls = %d, want 0 when Accept fails", reader.calls)
	}
}

func TestAcceptUpstream_TriggersPopulateGitHubCache(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		ListPendingItems: pendingItems(map[string][]PendingItem{
			"w-1": {{RigHandle: "charlie", Status: "in_review", CompletedBy: "charlie", Evidence: "https://github.com/foo/bar/pull/99"}},
		}),
	})
	cache, resolver, _ := setGitHubCacheTestDefaults(c, "charlie",
		"https://github.com/foo/bar/pull/99", "charlie-gh")

	if _, err := c.AcceptUpstream("w-1", "charlie", AcceptInput{Quality: 4, Reliability: 3}); err != nil {
		t.Fatalf("AcceptUpstream: %v", err)
	}

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	entry, ok := cache.entries["charlie"]
	if !ok {
		t.Fatal("expected cache entry for charlie")
	}
	if entry.GitHub != "charlie-gh" {
		t.Errorf("GitHub = %q, want charlie-gh", entry.GitHub)
	}
}

func TestAcceptUpstream_DMLError_SkipsHook(t *testing.T) {
	db := newFakeDB()
	db.seedItem(fakeItem{ID: "w-1", Title: "Fix bug", Status: "in_review", ClaimedBy: "bob", PostedBy: "alice", EffortLevel: "medium"})

	c := New(ClientConfig{
		DB:        db,
		RigHandle: "alice",
		Mode:      "wild-west",
		// No pending items for w-1 → AcceptUpstream fails before DML.
		ListPendingItems: pendingItems(map[string][]PendingItem{}),
	})
	_, resolver, _ := setGitHubCacheTestDefaults(c, "charlie",
		"https://github.com/foo/bar/pull/99", "charlie-gh")

	_, err := c.AcceptUpstream("w-1", "charlie", AcceptInput{})
	if err == nil {
		t.Fatal("expected error for missing submission, got nil")
	}
	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0 when AcceptUpstream fails", resolver.calls)
	}
}

func TestPopulateGitHubCache_TransientResolverError_DoesNotWriteCache(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	cache, resolver, _ := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")
	resolver.err = githubcache.ErrNoToken

	c.populateGitHubCache("bob")

	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if _, ok := cache.entries["bob"]; ok {
		t.Error("cache should not contain an entry after a transient resolver error")
	}
}

// A non-transient resolver error (e.g., GitHub 404) should behave the
// same as a transient error: log and skip. Do NOT mark the handle
// tried-and-failed — the evidence URL was parseable, the PR may come
// back later, and the next accept should retry.
func TestPopulateGitHubCache_Non404ResolverError_DoesNotWriteTriedAndFailed(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	cache, resolver, _ := setGitHubCacheTestDefaults(c, "bob",
		"https://github.com/foo/bar/pull/42", "bob-gh")
	resolver.err = fmt.Errorf("github API 404: not found")

	c.populateGitHubCache("bob")

	if _, ok := cache.entries["bob"]; ok {
		t.Error("non-transient resolver error should not write any cache entry (not even tried-and-failed)")
	}
}

// The hook must never surface a panic to the Accept caller — the DML
// has already committed. A resolver that panics should be caught and
// logged.
type panickingResolver struct{}

func (panickingResolver) ResolvePRAuthor(_ context.Context, _ string) (string, error) {
	panic("synthetic panic")
}

func TestPopulateGitHubCache_ResolverPanic_DoesNotPropagate(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	cache := newFakeCache()
	reader := newFakeCommonsReader()
	reader.bySubject["bob"] = []map[string]any{{"evidence": "https://github.com/foo/bar/pull/42"}}
	c.SetGitHubCache(cache, panickingResolver{}, reader)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic leaked through populateGitHubCache: %v", r)
		}
	}()
	c.populateGitHubCache("bob")
	// Reaching here without a panic means the recover in
	// populateGitHubCache caught it.
}

func TestPopulateGitHubCache_NoPRURL_WritesTriedAndFailed(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	cache := newFakeCache()
	resolver := &fakeResolver{}
	reader := newFakeCommonsReader()
	// Evidence exists but isn't a PR URL.
	reader.bySubject["bob"] = []map[string]any{
		{"evidence": "just some text, not a URL"},
		{"evidence": "https://example.com/not-a-pr"},
	}
	c.SetGitHubCache(cache, resolver, reader)

	c.populateGitHubCache("bob")

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0 when no PR URL was found", resolver.calls)
	}
	entry, ok := cache.entries["bob"]
	if !ok {
		t.Fatal("expected tried-and-failed entry for bob")
	}
	if entry.GitHub != "" {
		t.Errorf("GitHub = %q, want empty (tried-and-failed)", entry.GitHub)
	}
	if entry.ResolvedAt == "" {
		t.Error("expected ResolvedAt to be set on tried-and-failed entry")
	}
}

func TestPopulateGitHubCache_CommonsQueryError_DoesNotWriteCache(t *testing.T) {
	c := New(ClientConfig{DB: newFakeDB(), RigHandle: "alice", Mode: "wild-west"})
	cache := newFakeCache()
	resolver := &fakeResolver{login: "unused"}
	reader := newFakeCommonsReader()
	reader.queryErr = errors.New("dolthub timeout")
	c.SetGitHubCache(cache, resolver, reader)

	c.populateGitHubCache("bob")

	if resolver.calls != 0 {
		t.Errorf("resolver calls = %d, want 0 when commons query fails", resolver.calls)
	}
	if len(cache.entries) != 0 {
		t.Errorf("cache should remain empty after commons query failure; got %v", cache.entries)
	}
}
