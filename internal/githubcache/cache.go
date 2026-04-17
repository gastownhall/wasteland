// Package githubcache persists a local map of wasteland rig handles to
// their verified GitHub usernames. Entries are populated from GitHub PR
// authorship observed in stamp evidence URLs, either automatically by
// the SDK on stamp approval or on-demand via `wl resolve-github`. The
// cache is local-per-rig and lives at
// $XDG_DATA_HOME/wasteland/github-handles.json.
package githubcache

import (
	"context"
)

// Entry is a single cache record. An absent key means the handle has
// never been resolved; an entry with an empty GitHub field means
// resolution was attempted but no parseable GitHub PR URL was found
// (tried-and-failed). Consumers should treat both states identically
// for the purpose of rendering GitHub links — they differ only in
// whether `wl resolve-github --all` will re-attempt the handle.
type Entry struct {
	// GitHub is the resolved GitHub username, or "" for tried-and-failed.
	GitHub string `json:"github"`
	// SourcePR is the PR URL used to resolve this entry, if any.
	SourcePR string `json:"source_pr,omitempty"`
	// ResolvedAt is an RFC 3339 timestamp of when the entry was recorded.
	ResolvedAt string `json:"resolved_at,omitempty"`
}

// Cache is the operator-facing interface for reading and writing the
// persistent handle map.
type Cache interface {
	// Get returns the entry for handle and whether it was present.
	Get(handle string) (Entry, bool)
	// Put writes entry under handle, overwriting any prior value.
	// Implementations must write atomically (temp-file-then-rename).
	Put(handle string, entry Entry) error
	// All returns a snapshot of every entry in the cache.
	All() map[string]Entry
}

// Resolver extracts a GitHub username from a stamp's evidence URL by
// calling the GitHub REST API.
type Resolver interface {
	// ResolvePRAuthor fetches the .user.login field for the given PR
	// URL. Returns an error if the URL is not a GitHub PR URL, if
	// GITHUB_TOKEN is unset, or if the API call fails.
	ResolvePRAuthor(ctx context.Context, prURL string) (author string, err error)
}

// DefaultPath returns the on-disk path where the cache is persisted.
// The caller is responsible for creating any missing parent directory.
func DefaultPath() (string, error) {
	return defaultPath()
}

// Load opens the cache file at DefaultPath, parsing whatever is there.
// A missing or corrupt file logs a warning and returns an empty,
// usable cache. The returned cache is safe for concurrent Get and
// sequential Put (callers must not issue concurrent Puts).
func Load() (Cache, error) {
	return loadDefault()
}

// NewResolver returns a Resolver that calls api.github.com with the
// GITHUB_TOKEN env var. If the token is unset, ResolvePRAuthor
// returns ErrNoToken on every call so callers can log-and-skip.
func NewResolver() Resolver {
	return newDefaultResolver()
}
