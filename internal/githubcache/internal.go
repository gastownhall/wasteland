package githubcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/xdg"
)

// ErrNoToken is returned by Resolver when GITHUB_TOKEN is not set.
var ErrNoToken = errors.New("GITHUB_TOKEN not set")

// githubPRRegex matches a GitHub pull request URL and captures
// (owner, repo, number). Kept local to avoid importing internal/pile.
var githubPRRegex = regexp.MustCompile(`^https?://github\.com/([^/?#\s]+)/([^/?#\s]+)/pull/(\d+)(?:$|[/?#])`)

// defaultPath returns the on-disk path for the persistent cache.
func defaultPath() (string, error) {
	return filepath.Join(xdg.DataHome(), "wasteland", "github-handles.json"), nil
}

// fileCache is the default Cache implementation backed by a JSON file.
// It holds no in-memory state: every Get and All reads the file fresh
// so writes from any process (SDK hook, `wl resolve-github`, another
// `wl serve`) are visible to all readers without a restart. Put holds
// a mutex to serialize read-modify-write cycles within a single
// process; cross-process races may still lose one write but the next
// accept or batch repopulates.
type fileCache struct {
	path string
	mu   sync.Mutex
}

// loadDefault prepares a Cache handle at defaultPath(). It performs a
// single probing read so a malformed file is logged once at startup;
// the returned Cache always reads fresh on subsequent Get/All calls.
func loadDefault() (Cache, error) {
	path, err := defaultPath()
	if err != nil {
		return nil, err
	}
	return loadFromPath(path), nil
}

// loadFromPath returns a fileCache handle. Missing files are fine
// (Get returns no entries). Unreadable or malformed files are logged
// once here; subsequent reads will log again if the condition persists.
// Does not overwrite the file on load — only Put rewrites it.
func loadFromPath(path string) *fileCache {
	fc := &fileCache{path: path}
	if _, err := fc.readAll(); err != nil {
		slog.Warn("github-handles cache unusable at load; starting empty",
			"path", path, "error", err)
	}
	return fc
}

// readAll parses the file at f.path. Missing file → empty map + nil
// error. Malformed file → empty map + error (so loadFromPath can log
// once; routine Get/All callers ignore the error).
func (f *fileCache) readAll() (map[string]Entry, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Entry{}, nil
		}
		return map[string]Entry{}, fmt.Errorf("reading cache: %w", err)
	}
	if len(data) == 0 {
		return map[string]Entry{}, nil
	}
	var parsed map[string]Entry
	if err := json.Unmarshal(data, &parsed); err != nil {
		return map[string]Entry{}, fmt.Errorf("parsing cache: %w", err)
	}
	if parsed == nil {
		parsed = map[string]Entry{}
	}
	return parsed, nil
}

// Get returns the entry for handle and whether it was present. Reads
// the file fresh so callers see writes from any process.
func (f *fileCache) Get(handle string) (Entry, bool) {
	entries, _ := f.readAll()
	e, ok := entries[handle]
	return e, ok
}

// All returns a fresh snapshot of the cache from disk.
func (f *fileCache) All() map[string]Entry {
	entries, _ := f.readAll()
	return entries
}

// Put writes entry under handle and atomically persists via
// temp-file-then-rename. Holds a mutex for the duration of the
// read-modify-write to avoid two goroutines in the same process
// clobbering each other's writes.
//
// If the existing file is unparseable, Put returns an error rather
// than overwriting it — silently wiping the file on a parse error
// turns a recoverable local-data problem into data loss. Operators
// must fix or remove the file manually to proceed.
func (f *fileCache) Put(handle string, entry Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	entries, err := f.readAll()
	if err != nil {
		return fmt.Errorf("refusing to overwrite corrupt cache at %s: %w", f.path, err)
	}
	entries[handle] = entry
	return f.persist(entries)
}

// persist serializes entries to disk via temp-file-then-rename.
func (f *fileCache) persist(entries map[string]Entry) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating cache directory: %w", err)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling cache: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, ".github-handles-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, f.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("renaming cache: %w", err)
	}
	return nil
}

// defaultGitHubAPIBase is the base URL for GitHub's REST API. Tests
// override it via the baseURL field on httpResolver.
const defaultGitHubAPIBase = "https://api.github.com"

// httpResolver calls GitHub's REST API to resolve PR authors.
type httpResolver struct {
	client  *http.Client
	baseURL string
}

// newDefaultResolver returns an httpResolver with a 30s-timeout client.
func newDefaultResolver() Resolver {
	return &httpResolver{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: defaultGitHubAPIBase,
	}
}

// ResolvePRAuthor fetches the GitHub user.login for the given PR URL.
func (r *httpResolver) ResolvePRAuthor(ctx context.Context, prURL string) (string, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return "", ErrNoToken
	}
	m := githubPRRegex.FindStringSubmatch(prURL)
	if m == nil {
		return "", fmt.Errorf("not a GitHub PR URL: %s", prURL)
	}
	owner, repo, number := m[1], m[2], m[3]

	apiURL := fmt.Sprintf("%s/repos/%s/%s/pulls/%s",
		r.baseURL,
		url.PathEscape(owner),
		url.PathEscape(repo),
		number,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "wasteland-github-cache/1")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling GitHub API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Cap the body read. The response for a PR is typically ~10KB JSON;
	// 1 MiB is a safe ceiling against a misbehaving proxy.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("reading GitHub API response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return "", fmt.Errorf("github API %d: %s", resp.StatusCode, snippet)
	}
	var parsed struct {
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("parsing GitHub API response: %w", err)
	}
	if parsed.User.Login == "" {
		return "", fmt.Errorf("response missing user.login")
	}
	return parsed.User.Login, nil
}
