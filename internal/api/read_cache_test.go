package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadCache_GetMiss(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	if got := c.Get("missing"); got != nil {
		t.Fatalf("expected nil, got %q", got)
	}
}

func TestReadCache_GetOrFetch_CachesResult(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	calls := 0
	fetch := func() ([]byte, error) {
		calls++
		return []byte("hello"), nil
	}

	// First call should invoke fetch.
	got, err := c.GetOrFetch("k", fetch)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Second call should return cached.
	got, err = c.GetOrFetch("k", fetch)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("expected %q, got %q", "hello", got)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call after cache hit, got %d", calls)
	}
}

func TestReadCache_TTLExpiry(t *testing.T) {
	c := NewReadCache(10*time.Millisecond, 10)
	_, _ = c.GetOrFetch("k", func() ([]byte, error) {
		return []byte("v1"), nil
	})

	time.Sleep(20 * time.Millisecond)

	// Should be expired.
	if got := c.Get("k"); got != nil {
		t.Fatalf("expected nil after expiry, got %q", got)
	}

	// Should re-fetch.
	got, _ := c.GetOrFetch("k", func() ([]byte, error) {
		return []byte("v2"), nil
	})
	if string(got) != "v2" {
		t.Fatalf("expected %q, got %q", "v2", got)
	}
}

func TestReadCache_ConcurrentCoalescing(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	var fetchCount atomic.Int32

	fetch := func() ([]byte, error) { //nolint:unparam // test helper always succeeds
		fetchCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate slow fetch
		return []byte("result"), nil
	}

	var wg sync.WaitGroup
	const n = 20
	results := make([][]byte, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.GetOrFetch("same-key", fetch)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d got error: %v", i, errs[i])
		}
		if string(results[i]) != "result" {
			t.Fatalf("goroutine %d got %q", i, results[i])
		}
	}

	// Only one fetch should have executed.
	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("expected 1 fetch, got %d", got)
	}
}

func TestReadCache_Invalidate(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	_, _ = c.GetOrFetch("a", func() ([]byte, error) { return []byte("1"), nil })
	_, _ = c.GetOrFetch("b", func() ([]byte, error) { return []byte("2"), nil })

	c.Invalidate()

	if got := c.Get("a"); got != nil {
		t.Fatalf("expected nil after invalidate, got %q", got)
	}
	if got := c.Get("b"); got != nil {
		t.Fatalf("expected nil after invalidate, got %q", got)
	}
}

func TestReadCache_InvalidateKey(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	_, _ = c.GetOrFetch("a", func() ([]byte, error) { return []byte("1"), nil })
	_, _ = c.GetOrFetch("b", func() ([]byte, error) { return []byte("2"), nil })

	c.InvalidateKey("a")

	if got := c.Get("a"); got != nil {
		t.Fatalf("expected nil for invalidated key, got %q", got)
	}
	if got := c.Get("b"); got == nil {
		t.Fatal("expected non-nil for non-invalidated key")
	}
}

func TestReadCache_Eviction(t *testing.T) {
	c := NewReadCache(time.Minute, 3)

	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("k%d", i)
		_, _ = c.GetOrFetch(key, func() ([]byte, error) {
			return []byte(key), nil
		})
		// Small sleep so storedAt differs for ordering.
		time.Sleep(2 * time.Millisecond)
	}

	// Should have evicted the oldest entries (k0, k1) keeping k2, k3, k4.
	c.mu.Lock()
	count := len(c.entries)
	c.mu.Unlock()
	if count != 3 {
		t.Fatalf("expected 3 entries after eviction, got %d", count)
	}

	// Oldest should be gone.
	if got := c.Get("k0"); got != nil {
		t.Fatalf("expected k0 evicted, got %q", got)
	}
	if got := c.Get("k1"); got != nil {
		t.Fatalf("expected k1 evicted, got %q", got)
	}
	// Newest should remain.
	if got := c.Get("k4"); got == nil {
		t.Fatal("expected k4 to remain")
	}
}

func TestReadCache_FetchError_NotCached(t *testing.T) {
	c := NewReadCache(time.Minute, 10)
	fetchErr := fmt.Errorf("db down")

	_, err := c.GetOrFetch("k", func() ([]byte, error) {
		return nil, fetchErr
	})
	if !errors.Is(err, fetchErr) {
		t.Fatalf("expected fetchErr, got %v", err)
	}

	// Error should not be cached.
	if got := c.Get("k"); got != nil {
		t.Fatalf("expected nil after fetch error, got %q", got)
	}
}
