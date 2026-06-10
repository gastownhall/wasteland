package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/sdk"
	"github.com/spf13/cobra"
)

type testContextKey string

func withServeGracefulOverrides(
	t *testing.T,
	listenFn func(*http.Server) error,
	notifyFn func(chan<- os.Signal),
	shutdownFn func(*http.Server, context.Context) error,
) {
	t.Helper()
	oldListen := serveHTTPListen
	oldNotify := serveSignalNotify
	oldShutdown := serveShutdown
	if listenFn != nil {
		serveHTTPListen = listenFn
	}
	if notifyFn != nil {
		serveSignalNotify = notifyFn
	}
	if shutdownFn != nil {
		serveShutdown = shutdownFn
	}
	t.Cleanup(func() {
		serveHTTPListen = oldListen
		serveSignalNotify = oldNotify
		serveShutdown = oldShutdown
	})
}

func TestResolvePort_UsesEnvOverride(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Int("port", 8999, "")
	t.Setenv("PORT", "4321")
	if got := resolvePort(cmd); got != 4321 {
		t.Fatalf("resolvePort() = %d", got)
	}
}

func TestResolvePort_InvalidEnvFallsBackToFlag(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Int("port", 8123, "")
	t.Setenv("PORT", "nope")
	if got := resolvePort(cmd); got != 8123 {
		t.Fatalf("resolvePort() = %d", got)
	}
}

func TestHostedPublicUpstream_IsCanonical(t *testing.T) {
	if hostedPublicUpstreamOrg != "hop" {
		t.Fatalf("hostedPublicUpstreamOrg = %q", hostedPublicUpstreamOrg)
	}
	if hostedPublicUpstreamDB != "wl-commons" {
		t.Fatalf("hostedPublicUpstreamDB = %q", hostedPublicUpstreamDB)
	}
	if hostedPublicUpstream != "hop/wl-commons" {
		t.Fatalf("hostedPublicUpstream = %q", hostedPublicUpstream)
	}
}

func TestRunServe_NoSyncSkipsLocalSyncAndPush(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("DOLTHUB_TOKEN", "token")
	saveTestConfig(t, &federation.Config{
		Upstream:  "hop/wl-commons",
		ForkOrg:   "alice",
		ForkDB:    "wl-commons",
		LocalDir:  t.TempDir(),
		RigHandle: "alice",
		Backend:   federation.BackendLocal,
		Mode:      federation.ModePR,
		JoinedAt:  time.Now(),
	})
	installReviewDolt(t)

	var syncCalls atomic.Int32
	var pushCalls atomic.Int32
	withLocalWorkflowDBOverride(t, func(string, string) localWorkflowDB {
		return fakeLocalWorkflowDB{
			pushMainFn: func(io.Writer) error {
				pushCalls.Add(1)
				return errors.New("push should be skipped")
			},
			scriptedDB: scriptedDB{
				syncFunc: func() error {
					syncCalls.Add(1)
					return errors.New("sync should be skipped")
				},
				queryFunc: func(string, string) (string, error) { return "", nil },
			},
		}
	})
	withSelfHostedAPIServerOverride(t, func(client *sdk.Client) selfHostedAPIServer {
		return &fakeSelfHostedServer{client: client}
	})
	withServeListenOverride(t, func(*http.Server) error { return nil })

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--wasteland", "hop/wl-commons", "serve", "--no-sync", "--port", "8125"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(serve --no-sync) = %d, stderr = %q", code, stderr.String())
	}
	if syncCalls.Load() != 0 || pushCalls.Load() != 0 {
		t.Fatalf("sync calls = %d, push calls = %d", syncCalls.Load(), pushCalls.Load())
	}
}

func TestPendingItemsCache_GetAndStop(t *testing.T) {
	cache := &pendingItemsCache{
		cached: map[string][]sdk.PendingItem{
			"w-1": {{RigHandle: "alice"}},
		},
		stop: make(chan struct{}),
	}
	items, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(items["w-1"]) != 1 || items["w-1"][0].RigHandle != "alice" {
		t.Fatalf("items = %+v", items)
	}
	cache.Stop()
	select {
	case <-cache.stop:
	default:
		t.Fatal("stop channel was not closed")
	}
}

func TestPendingItemsCache_GetContext_RefreshesOnMiss(t *testing.T) {
	key := testContextKey("serve-pending-refresh-miss")
	old := listPendingWantedStatesContext
	listPendingWantedStatesContext = func(ctx context.Context, upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "wasteland" || db != "wl-commons" || token != "" {
			return nil, fmt.Errorf("unexpected refresh args: %q %q %q", upstreamOrg, db, token)
		}
		if got := ctx.Value(key); got != "trace-bound" {
			return nil, fmt.Errorf("context value = %v, want trace-bound", got)
		}
		return map[string][]remote.PendingWantedState{
			"w-9": {{RigHandle: "charlie", Status: "claimed"}},
		}, nil
	}
	t.Cleanup(func() {
		listPendingWantedStatesContext = old
	})

	cache := &pendingItemsCache{
		upstreamOrg: "wasteland",
		db:          "wl-commons",
		interval:    time.Minute,
		stop:        make(chan struct{}),
	}
	defer cache.Stop()

	items, err := cache.GetContext(context.WithValue(context.Background(), key, "trace-bound"))
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if len(items["w-9"]) != 1 || items["w-9"][0].RigHandle != "charlie" {
		t.Fatalf("items = %+v", items)
	}
}

func TestPendingItemsCache_GetContext_StaleCachedReturnsCachedAndDedupesRefresh(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	key := testContextKey("serve-pending-stale")
	ctxSeen := make(chan any, 1)
	old := listPendingWantedStatesContext
	listPendingWantedStatesContext = func(ctx context.Context, upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "pending-test" || db != "wl-commons" {
			return old(ctx, upstreamOrg, db, token)
		}
		select {
		case ctxSeen <- ctx.Value(key):
		default:
		}
		calls.Add(1)
		<-release
		return map[string][]remote.PendingWantedState{
			"w-2": {{RigHandle: "fresh"}},
		}, nil
	}
	t.Cleanup(func() {
		listPendingWantedStatesContext = old
	})

	cache := &pendingItemsCache{
		cached: map[string][]sdk.PendingItem{
			"w-2": {{RigHandle: "stale"}},
		},
		refreshedAt: time.Now().Add(-2 * time.Minute),
		upstreamOrg: "pending-test",
		db:          "wl-commons",
		interval:    time.Minute,
		stop:        make(chan struct{}),
	}
	defer cache.Stop()

	items, err := cache.GetContext(context.WithValue(context.Background(), key, "trace-bound"))
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if got := items["w-2"][0].RigHandle; got != "stale" {
		t.Fatalf("cached rig handle = %q, want stale", got)
	}

	for i := 0; i < 50 && calls.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls after first GetContext() = %d, want 1", calls.Load())
	}
	select {
	case got := <-ctxSeen:
		if got != "trace-bound" {
			t.Fatalf("refresh context value = %v, want trace-bound", got)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stale refresh did not observe request context")
	}

	items, err = cache.GetContext(context.Background())
	if err != nil {
		t.Fatalf("second GetContext() error = %v", err)
	}
	if got := items["w-2"][0].RigHandle; got != "stale" {
		t.Fatalf("second cached rig handle = %q, want stale", got)
	}
	time.Sleep(25 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("refresh calls after second GetContext() = %d, want 1", calls.Load())
	}

	close(release)
	for i := 0; i < 50; i++ {
		refreshed, err := cache.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(refreshed["w-2"]) == 1 && refreshed["w-2"][0].RigHandle == "fresh" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cache did not refresh: %+v", cache.cached)
}

func TestPendingItemsCache_GetContext_CanceledFirstCallerDoesNotPoisonSharedRefresh(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	old := listPendingWantedStatesContext
	listPendingWantedStatesContext = func(ctx context.Context, upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "pending-cold" || db != "wl-commons" {
			return old(ctx, upstreamOrg, db, token)
		}
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return map[string][]remote.PendingWantedState{
			"w-3": {{RigHandle: "shared"}},
		}, nil
	}
	t.Cleanup(func() {
		listPendingWantedStatesContext = old
	})

	cache := &pendingItemsCache{
		upstreamOrg: "pending-cold",
		db:          "wl-commons",
		interval:    time.Minute,
		stop:        make(chan struct{}),
	}
	defer cache.Stop()

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	cancelFirst()

	firstErrCh := make(chan error, 1)
	go func() {
		_, err := cache.GetContext(firstCtx)
		firstErrCh <- err
	}()

	secondItemsCh := make(chan map[string][]sdk.PendingItem, 1)
	secondErrCh := make(chan error, 1)
	go func() {
		items, err := cache.GetContext(context.Background())
		if err != nil {
			secondErrCh <- err
			return
		}
		secondItemsCh <- items
	}()

	for i := 0; i < 50 && calls.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}

	close(release)

	if err := <-firstErrCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller error = %v, want context.Canceled", err)
	}
	select {
	case err := <-secondErrCh:
		t.Fatalf("second caller error = %v", err)
	case items := <-secondItemsCh:
		if len(items["w-3"]) != 1 || items["w-3"][0].RigHandle != "shared" {
			t.Fatalf("second caller items = %+v", items)
		}
	case <-time.After(time.Second):
		t.Fatal("second caller did not receive refreshed items")
	}
}

func TestPendingItemsCache_GetContext_CanceledStaleCallerKeepsRefreshingFlagUntilRefreshCompletes(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	old := listPendingWantedStatesContext
	listPendingWantedStatesContext = func(ctx context.Context, upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "pending-stale-cancel" || db != "wl-commons" {
			return old(ctx, upstreamOrg, db, token)
		}
		calls.Add(1)
		<-release
		return map[string][]remote.PendingWantedState{
			"w-4": {{RigHandle: "fresh"}},
		}, nil
	}
	t.Cleanup(func() {
		listPendingWantedStatesContext = old
	})

	cache := &pendingItemsCache{
		cached: map[string][]sdk.PendingItem{
			"w-4": {{RigHandle: "stale"}},
		},
		refreshedAt: time.Now().Add(-2 * time.Minute),
		upstreamOrg: "pending-stale-cancel",
		db:          "wl-commons",
		interval:    time.Minute,
		stop:        make(chan struct{}),
	}
	defer cache.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	items, err := cache.GetContext(ctx)
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if got := items["w-4"][0].RigHandle; got != "stale" {
		t.Fatalf("cached rig handle = %q, want stale", got)
	}
	cancel()

	for i := 0; i < 50 && calls.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}

	time.Sleep(25 * time.Millisecond)
	cache.mu.RLock()
	refreshing := cache.refreshing
	cache.mu.RUnlock()
	if !refreshing {
		t.Fatal("refreshing flag was cleared before the background refresh completed")
	}

	_, err = cache.GetContext(context.Background())
	if err != nil {
		t.Fatalf("second GetContext() error = %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("refresh calls after second GetContext() = %d, want 1", calls.Load())
	}

	close(release)
	for i := 0; i < 50; i++ {
		refreshed, err := cache.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(refreshed["w-4"]) == 1 && refreshed["w-4"][0].RigHandle == "fresh" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("cache did not refresh: %+v", cache.cached)
}

func TestPendingItemsCache_StopWaitsForInFlightRefresh(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	old := listPendingWantedStatesContext
	listPendingWantedStatesContext = func(ctx context.Context, upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "pending-stop" || db != "wl-commons" {
			return old(ctx, upstreamOrg, db, token)
		}
		calls.Add(1)
		<-release
		return map[string][]remote.PendingWantedState{
			"w-5": {{RigHandle: "fresh"}},
		}, nil
	}
	t.Cleanup(func() {
		listPendingWantedStatesContext = old
	})

	cache := &pendingItemsCache{
		cached: map[string][]sdk.PendingItem{
			"w-5": {{RigHandle: "stale"}},
		},
		refreshedAt: time.Now().Add(-2 * time.Minute),
		upstreamOrg: "pending-stop",
		db:          "wl-commons",
		interval:    time.Minute,
		stop:        make(chan struct{}),
	}

	items, err := cache.GetContext(context.Background())
	if err != nil {
		t.Fatalf("GetContext() error = %v", err)
	}
	if got := items["w-5"][0].RigHandle; got != "stale" {
		t.Fatalf("cached rig handle = %q, want stale", got)
	}

	for i := 0; i < 50 && calls.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}

	stopped := make(chan struct{})
	go func() {
		cache.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop() returned before in-flight refresh completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not wait for in-flight refresh")
	}
}

func TestNewDetailRefresh_UsesQueryOverride(t *testing.T) {
	old := queryScoreboardDetailEntries
	queryScoreboardDetailEntries = func(_ commons.DB, limit int) ([]commons.ScoreboardDetailEntry, error) {
		if limit != 100 {
			t.Fatalf("limit = %d", limit)
		}
		return []commons.ScoreboardDetailEntry{{
			ScoreboardEntry: commons.ScoreboardEntry{RigHandle: "alice"},
		}}, nil
	}
	t.Cleanup(func() {
		queryScoreboardDetailEntries = old
	})

	data, err := newDetailRefresh(scriptedDB{})()
	if err != nil {
		t.Fatalf("newDetailRefresh() error = %v", err)
	}
	if out := string(data); !strings.Contains(out, `"rig_handle":"alice"`) || !strings.Contains(out, `"entries"`) {
		t.Fatalf("output = %q", out)
	}
}

func TestNewDumpRefresh_UsesQueryOverride(t *testing.T) {
	old := queryScoreboardDumpData
	queryScoreboardDumpData = func(_ commons.DB) (*commons.ScoreboardDump, error) {
		return &commons.ScoreboardDump{
			Rigs: []commons.RigRow{{Handle: "alice"}},
		}, nil
	}
	t.Cleanup(func() {
		queryScoreboardDumpData = old
	})

	data, err := newDumpRefresh(scriptedDB{})()
	if err != nil {
		t.Fatalf("newDumpRefresh() error = %v", err)
	}
	if out := string(data); !strings.Contains(out, `"handle":"alice"`) || !strings.Contains(out, `"rigs"`) {
		t.Fatalf("output = %q", out)
	}
}

func TestNewPendingItemsCache_PrewarmsAndMapsStates(t *testing.T) {
	withPendingWantedStatesOverride(t, func(upstreamOrg, db, token string) (map[string][]remote.PendingWantedState, error) {
		if upstreamOrg != "hop" || db != "wl-commons" || token != "" {
			t.Fatalf("got %q %q %q", upstreamOrg, db, token)
		}
		return map[string][]remote.PendingWantedState{
			"w-1": {{
				RigHandle:   "alice",
				Status:      "in_review",
				ClaimedBy:   "alice",
				Branch:      "wl/alice/w-1",
				BranchURL:   "https://example/branch",
				PRURL:       "https://example/pr/1",
				ForkOwner:   "alice",
				CompletedBy: "alice",
				Evidence:    "https://example/evidence",
			}},
		}, nil
	})

	cache := newPendingItemsCache("hop", "wl-commons", time.Hour)
	defer cache.Stop()

	var got map[string][]sdk.PendingItem
	for i := 0; i < 50; i++ {
		items, err := cache.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if len(items["w-1"]) == 1 {
			got = items
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got["w-1"]) != 1 || got["w-1"][0].PRURL != "https://example/pr/1" {
		t.Fatalf("items = %+v", got)
	}
}

func TestNewDetailRefresh_PropagatesQueryErrors(t *testing.T) {
	old := queryScoreboardDetailEntries
	queryScoreboardDetailEntries = func(commons.DB, int) ([]commons.ScoreboardDetailEntry, error) {
		return nil, errors.New("boom")
	}
	t.Cleanup(func() {
		queryScoreboardDetailEntries = old
	})

	if _, err := newDetailRefresh(scriptedDB{})(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestPendingItemsCache_GetReturnsCachedMapByReference(t *testing.T) {
	cache := &pendingItemsCache{
		cached: map[string][]sdk.PendingItem{
			"w-2": {{RigHandle: "bob"}},
		},
		stop: make(chan struct{}),
	}
	items, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	items["w-2"][0].RigHandle = "changed"
	again, err := cache.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if again["w-2"][0].RigHandle != "changed" {
		t.Fatalf("cached map was unexpectedly copied: %+v", again)
	}
}

func TestResolvePort_PrefersEnvInNewServeCmd(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newServeCmd(&stdout, &stderr)
	t.Setenv("PORT", "7000")
	if got := resolvePort(cmd); got != 7000 {
		t.Fatalf("resolvePort(newServeCmd) = %d", got)
	}
}

func TestInitSentry_NoDSNAndInvalidDSN(t *testing.T) {
	t.Run("no dsn", func(t *testing.T) {
		t.Setenv("SENTRY_DSN", "")
		initSentry("test")
	})

	t.Run("invalid dsn", func(t *testing.T) {
		t.Setenv("SENTRY_DSN", "not-a-dsn")
		initSentry("test")
	})
}

func TestListenAndServeGraceful_SignalPaths(t *testing.T) {
	t.Run("graceful shutdown", func(t *testing.T) {
		done := make(chan struct{})
		withServeGracefulOverrides(
			t,
			func(*http.Server) error {
				<-done
				return http.ErrServerClosed
			},
			func(c chan<- os.Signal) {
				c <- os.Interrupt
			},
			func(*http.Server, context.Context) error {
				close(done)
				return nil
			},
		)

		if err := listenAndServeGraceful(&http.Server{Addr: "127.0.0.1:0"}); err != nil {
			t.Fatalf("listenAndServeGraceful() error = %v", err)
		}
	})

	t.Run("shutdown failure", func(t *testing.T) {
		done := make(chan struct{})
		withServeGracefulOverrides(
			t,
			func(*http.Server) error {
				<-done
				return http.ErrServerClosed
			},
			func(c chan<- os.Signal) {
				c <- os.Interrupt
			},
			func(*http.Server, context.Context) error {
				close(done)
				return errors.New("shutdown boom")
			},
		)

		err := listenAndServeGraceful(&http.Server{Addr: "127.0.0.1:0"})
		if err == nil || !strings.Contains(err.Error(), "graceful shutdown failed: shutdown boom") {
			t.Fatalf("err = %v", err)
		}
	})
}
