package api

import (
	"sort"
	"sync"
	"time"
)

// ReadCache is a keyed read-through cache with TTL and thundering-herd
// protection. It serves pre-serialized JSON bytes so that multiple HTTP
// readers can share a single []byte without re-marshaling.
type ReadCache struct {
	mu         sync.Mutex
	entries    map[string]*cacheEntry
	inflight   map[string]*call
	maxAge     time.Duration
	maxEntries int
}

type cacheEntry struct {
	data     []byte
	storedAt time.Time
}

// call represents an in-flight fetch. Multiple concurrent callers for the
// same key share a single call via WaitGroup.
type call struct {
	wg  sync.WaitGroup
	val []byte
	err error
}

// NewReadCache creates a cache that keeps entries for maxAge and evicts the
// oldest when maxEntries is exceeded.
func NewReadCache(maxAge time.Duration, maxEntries int) *ReadCache {
	return &ReadCache{
		entries:    make(map[string]*cacheEntry),
		inflight:   make(map[string]*call),
		maxAge:     maxAge,
		maxEntries: maxEntries,
	}
}

// Get returns cached bytes if fresh, nil if miss or stale.
func (c *ReadCache) Get(key string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil
	}
	if time.Since(e.storedAt) > c.maxAge {
		delete(c.entries, key)
		return nil
	}
	return e.data
}

// GetOrFetch returns cached bytes for key, or calls fn to fetch them.
// Concurrent callers for the same key are coalesced: only the first caller
// runs fn while the rest block on its result (singleflight pattern).
func (c *ReadCache) GetOrFetch(key string, fn func() ([]byte, error)) ([]byte, error) {
	// Fast path: cache hit.
	if data := c.Get(key); data != nil {
		return data, nil
	}

	c.mu.Lock()
	// Double-check after acquiring lock.
	if e, ok := c.entries[key]; ok && time.Since(e.storedAt) <= c.maxAge {
		c.mu.Unlock()
		return e.data, nil
	}

	// Join an in-flight fetch if one exists.
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		cl.wg.Wait()
		return cl.val, cl.err
	}

	// First caller: create a new in-flight entry.
	cl := &call{}
	cl.wg.Add(1)
	c.inflight[key] = cl
	c.mu.Unlock()

	// Execute the fetch outside the lock.
	cl.val, cl.err = fn()
	if cl.err == nil {
		c.mu.Lock()
		c.entries[key] = &cacheEntry{data: cl.val, storedAt: time.Now()}
		c.evictLocked()
		c.mu.Unlock()
	}

	// Wake all waiters and remove the in-flight entry.
	cl.wg.Done()
	c.mu.Lock()
	delete(c.inflight, key)
	c.mu.Unlock()

	return cl.val, cl.err
}

// Invalidate clears all cached entries.
func (c *ReadCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
}

// InvalidateKey removes a single cached entry.
func (c *ReadCache) InvalidateKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, key)
}

// evictLocked removes the oldest entries when the cache exceeds maxEntries.
// Must be called with c.mu held.
func (c *ReadCache) evictLocked() {
	if len(c.entries) <= c.maxEntries {
		return
	}

	type kv struct {
		key      string
		storedAt time.Time
	}
	items := make([]kv, 0, len(c.entries))
	for k, e := range c.entries {
		items = append(items, kv{key: k, storedAt: e.storedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].storedAt.Before(items[j].storedAt)
	})

	excess := len(c.entries) - c.maxEntries
	for i := 0; i < excess; i++ {
		delete(c.entries, items[i].key)
	}
}
