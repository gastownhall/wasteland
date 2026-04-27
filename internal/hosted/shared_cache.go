package hosted

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/ctxutil"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/sdk"
	"go.opentelemetry.io/otel"
)

const (
	cacheTTL              = 1 * time.Minute
	resolveMissTimeout    = 30 * time.Second
	pendingRefreshTimeout = 30 * time.Second
)

var hostedTracer = otel.Tracer("github.com/gastownhall/wasteland/internal/hosted")

type cachedWorkspace struct {
	workspace *sdk.Workspace
	expiresAt time.Time
}

// pendingUpstreamCache is a shared pending-items cache that refreshes in the
// background and can synchronously refresh on cold/stale request reads.
type pendingUpstreamCache struct {
	cached      map[string][]sdk.PendingItem
	refreshedAt time.Time
	provider    *remote.DoltHubProvider
	upOrg       string
	upDB        string
	interval    time.Duration
	inflight    *pendingRefresh
	stop        chan struct{}
	stopOnce    sync.Once
	mu          sync.RWMutex
	wg          sync.WaitGroup
}

type pendingRefresh struct {
	done  chan struct{}
	items map[string][]sdk.PendingItem
	err   error
}

func newPendingUpstreamCache(provider *remote.DoltHubProvider, upOrg, upDB string, interval time.Duration) *pendingUpstreamCache {
	c := &pendingUpstreamCache{
		provider: provider,
		upOrg:    upOrg,
		upDB:     upDB,
		interval: interval,
		stop:     make(chan struct{}),
	}

	c.scheduleRefresh(context.Background())

	return c
}

func (c *pendingUpstreamCache) Stop() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		close(c.stop)
		c.mu.Unlock()
	})
	c.wg.Wait()
}

func (c *pendingUpstreamCache) Get() (map[string][]sdk.PendingItem, error) {
	c.mu.RLock()
	cached := c.cached
	stale := c.provider != nil && cached != nil && time.Since(c.refreshedAt) >= c.interval
	refreshing := c.inflight != nil
	c.mu.RUnlock()
	if stale && !refreshing {
		c.scheduleRefresh(context.Background())
	}
	return cached, nil
}

func (c *pendingUpstreamCache) GetContext(ctx context.Context) (map[string][]sdk.PendingItem, error) {
	c.mu.RLock()
	cached := c.cached
	stale := c.provider != nil && (cached == nil || time.Since(c.refreshedAt) >= c.interval)
	c.mu.RUnlock()
	if !stale {
		return cached, nil
	}
	return c.refreshSync(ctx)
}

func (c *pendingUpstreamCache) refreshSync(ctx context.Context) (map[string][]sdk.PendingItem, error) {
	refresh := c.startRefresh(ctx)
	select {
	case <-refresh.done:
		if refresh.err != nil {
			return nil, refresh.err
		}
		return refresh.items, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *pendingUpstreamCache) scheduleRefresh(ctx context.Context) {
	refresh := c.startRefresh(ctx)
	go func() {
		<-refresh.done
		if refresh.err != nil {
			slog.Warn("pending items refresh failed", "upstream", c.upOrg+"/"+c.upDB, "error", refresh.err)
		}
	}()
}

func (c *pendingUpstreamCache) refreshNow(ctx context.Context) (map[string][]sdk.PendingItem, error) {
	if c.provider == nil {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return c.cached, nil
	}
	states, err := c.provider.WithContext(ctx).ListPendingWantedIDs(c.upOrg, c.upDB)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]sdk.PendingItem, len(states))
	for id, pending := range states {
		items := make([]sdk.PendingItem, len(pending))
		for i, p := range pending {
			items[i] = sdk.PendingItem{
				RigHandle:   p.RigHandle,
				Status:      p.Status,
				ClaimedBy:   p.ClaimedBy,
				Branch:      p.Branch,
				BranchURL:   p.BranchURL,
				PRURL:       p.PRURL,
				ForkOwner:   p.ForkOwner,
				CompletedBy: p.CompletedBy,
				Evidence:    p.Evidence,
			}
		}
		result[id] = items
	}

	c.mu.Lock()
	c.cached = result
	c.refreshedAt = time.Now()
	c.mu.Unlock()
	return result, nil
}

func (c *pendingUpstreamCache) startRefresh(ctx context.Context) *pendingRefresh {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.provider == nil {
		refresh := &pendingRefresh{
			done:  make(chan struct{}),
			items: c.cached,
		}
		close(refresh.done)
		return refresh
	}
	if c.inflight != nil {
		return c.inflight
	}
	select {
	case <-c.stop:
		refresh := &pendingRefresh{
			done:  make(chan struct{}),
			items: c.cached,
		}
		close(refresh.done)
		return refresh
	default:
	}

	refresh := &pendingRefresh{done: make(chan struct{})}
	c.inflight = refresh
	c.wg.Add(1)
	go c.runRefresh(ctx, refresh)
	return refresh
}

func (c *pendingUpstreamCache) runRefresh(ctx context.Context, refresh *pendingRefresh) {
	defer c.wg.Done()

	sharedCtx, cancel := ctxutil.Detached(ctx, pendingRefreshTimeout)
	defer cancel()

	items, err := c.refreshNow(sharedCtx)

	c.mu.Lock()
	if c.inflight == refresh {
		c.inflight = nil
	}
	c.mu.Unlock()

	refresh.items = items
	refresh.err = err
	close(refresh.done)
}

func activeBootstrapUpstream(headerUpstream, remembered string, wastelands []WastelandConfig) (string, string) {
	findMode := func(upstream string) string {
		for _, wl := range wastelands {
			if wl.Upstream == upstream {
				return wl.Mode
			}
		}
		return ""
	}
	hasUpstream := func(upstream string) bool {
		return findMode(upstream) != ""
	}

	switch {
	case headerUpstream != "" && hasUpstream(headerUpstream):
		return headerUpstream, findMode(headerUpstream)
	case remembered != "" && hasUpstream(remembered):
		return remembered, findMode(remembered)
	case len(wastelands) == 1:
		return wastelands[0].Upstream, wastelands[0].Mode
	case len(wastelands) > 0:
		return wastelands[0].Upstream, wastelands[0].Mode
	default:
		return "", ""
	}
}
