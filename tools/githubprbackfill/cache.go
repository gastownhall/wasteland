// githubprbackfill generates deterministic Wasteland review-stamp backfills
// from merged GitHub pull request metadata.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type githubCache struct {
	Version   string                `json:"version"`
	UpdatedAt string                `json:"updated_at"`
	Entries   map[string]cacheEntry `json:"entries"`
	mu        sync.Mutex
	dirty     int
}

type cacheEntry struct {
	Complete  bool            `json:"complete"`
	FetchedAt string          `json:"fetched_at"`
	Data      json.RawMessage `json:"data"`
}

func loadGitHubCache(path string) (*githubCache, error) {
	if path == "" {
		return &githubCache{Version: backfillVersion, Entries: make(map[string]cacheEntry)}, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &githubCache{Version: backfillVersion, Entries: make(map[string]cacheEntry)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read GitHub cache: %w", err)
	}
	var cache githubCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, fmt.Errorf("parse GitHub cache: %w", err)
	}
	if cache.Entries == nil {
		cache.Entries = make(map[string]cacheEntry)
	}
	return &cache, nil
}

func (c *githubCache) save(path string) error {
	if path == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Version = backfillVersion
	c.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal GitHub cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create GitHub cache dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write GitHub cache temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace GitHub cache: %w", err)
	}
	c.dirty = 0
	return nil
}

func (c *githubCache) hash() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("marshal GitHub cache for hash: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c *githubCache) get(key string) (cacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.Entries[key]
	return entry, ok
}

func (c *githubCache) set(key string, entry cacheEntry) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Entries[key] = entry
	c.dirty++
	return c.dirty
}
