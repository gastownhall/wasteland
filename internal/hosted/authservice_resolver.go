package hosted

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/ctxutil"
	"github.com/gastownhall/wasteland/internal/dolthubauth"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/sdk"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/sync/singleflight"
)

// AuthServiceWorkspaceResolver resolves hosted workspaces through the
// standalone DoltHub auth service.
type AuthServiceWorkspaceResolver struct {
	auth     *dolthubauth.Client
	sessions *SessionStore

	mu    sync.Mutex
	cache map[string]*cachedWorkspace
	group singleflight.Group

	pendingMu    sync.Mutex
	pendingCache map[string]*pendingUpstreamCache
}

// NewAuthServiceWorkspaceResolver constructs a resolver backed by the
// standalone DoltHub auth service.
func NewAuthServiceWorkspaceResolver(auth *dolthubauth.Client, sessions *SessionStore) *AuthServiceWorkspaceResolver {
	return &AuthServiceWorkspaceResolver{
		auth:         auth,
		sessions:     sessions,
		cache:        make(map[string]*cachedWorkspace),
		pendingCache: make(map[string]*pendingUpstreamCache),
	}
}

// Stop terminates any background pending-item caches owned by the resolver.
func (wr *AuthServiceWorkspaceResolver) Stop() {
	wr.pendingMu.Lock()
	caches := make([]*pendingUpstreamCache, 0, len(wr.pendingCache))
	for _, cache := range wr.pendingCache {
		caches = append(caches, cache)
	}
	wr.pendingMu.Unlock()
	for _, cache := range caches {
		cache.Stop()
	}
}

// ResetCaches clears cached workspaces and pending-item caches.
func (wr *AuthServiceWorkspaceResolver) ResetCaches() {
	wr.mu.Lock()
	wr.cache = make(map[string]*cachedWorkspace)
	wr.mu.Unlock()

	wr.pendingMu.Lock()
	caches := make([]*pendingUpstreamCache, 0, len(wr.pendingCache))
	for _, cache := range wr.pendingCache {
		caches = append(caches, cache)
	}
	wr.pendingCache = make(map[string]*pendingUpstreamCache)
	wr.pendingMu.Unlock()

	for _, cache := range caches {
		cache.Stop()
	}
}

// Resolve loads the workspace for the provided session.
func (wr *AuthServiceWorkspaceResolver) Resolve(session *UserSession) (*sdk.Workspace, error) {
	return wr.ResolveContext(context.Background(), session)
}

// ResolveContext loads the workspace for the provided session using the
// supplied context.
func (wr *AuthServiceWorkspaceResolver) ResolveContext(ctx context.Context, session *UserSession) (*sdk.Workspace, error) {
	ctx, span := hostedTracer.Start(ctx, "hosted.auth_service_workspace.resolve")
	defer span.End()

	if cached, ok := wr.cachedWorkspace(session.ConnectionID); ok {
		span.SetAttributes(attribute.Bool("cache.hit", true))
		return cached, nil
	}
	span.SetAttributes(attribute.Bool("cache.hit", false))

	resultCh := wr.group.DoChan(session.ConnectionID, func() (any, error) {
		resolveCtx, cancel := ctxutil.Detached(ctx, resolveMissTimeout)
		defer cancel()
		return wr.resolveMiss(resolveCtx, session)
	})
	return wr.waitOnResolveResult(ctx, span, resultCh)
}

// WarmSession primes the resolver cache for the session's current connection.
func (wr *AuthServiceWorkspaceResolver) WarmSession(session *UserSession, conn *dolthubauth.ConnectionResponse) {
	if session == nil || session.ConnectionID == "" || conn == nil || len(conn.Wastelands) == 0 {
		return
	}
	go func() {
		ctx, cancel := ctxutil.Detached(context.Background(), resolveMissTimeout)
		defer cancel()
		if _, ok := wr.cachedWorkspace(session.ConnectionID); ok {
			return
		}
		resultCh := wr.group.DoChan(session.ConnectionID, func() (any, error) {
			return wr.resolveFromConnection(ctx, session, conn)
		})
		select {
		case <-resultCh:
		case <-ctx.Done():
		}
	}()
}

func (wr *AuthServiceWorkspaceResolver) waitOnResolveResult(ctx context.Context, span trace.Span, resultCh <-chan singleflight.Result) (*sdk.Workspace, error) {
	_, waitSpan := hostedTracer.Start(ctx, "hosted.auth_service_workspace.wait_for_resolve")
	defer waitSpan.End()

	select {
	case result := <-resultCh:
		waitSpan.SetAttributes(attribute.Bool("singleflight.shared", result.Shared))
		span.SetAttributes(attribute.Bool("singleflight.shared", result.Shared))
		if result.Err != nil {
			waitSpan.RecordError(result.Err)
			span.RecordError(result.Err)
			return nil, result.Err
		}
		workspace, _ := result.Val.(*sdk.Workspace)
		return workspace, nil
	case <-ctx.Done():
		waitSpan.RecordError(ctx.Err())
		span.RecordError(ctx.Err())
		return nil, ctx.Err()
	}
}

func (wr *AuthServiceWorkspaceResolver) resolveMiss(ctx context.Context, session *UserSession) (*sdk.Workspace, error) {
	if session.SubjectID == "" {
		return nil, fmt.Errorf("session missing subject ID")
	}
	conn, err := wr.auth.GetConnection(ctx, session.SubjectID, session.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("resolving connection: %w", err)
	}
	return wr.resolveFromConnection(ctx, session, conn)
}

func (wr *AuthServiceWorkspaceResolver) resolveFromConnection(_ context.Context, session *UserSession, conn *dolthubauth.ConnectionResponse) (*sdk.Workspace, error) {
	if conn == nil || len(conn.Wastelands) == 0 {
		return nil, fmt.Errorf("no wasteland config found for connection %s", session.ConnectionID)
	}

	ws := sdk.NewWorkspace(conn.RigHandle)
	proxyClient := wr.auth.NewProxyHTTPClient(session.SubjectID, session.ConnectionID)
	for i := range conn.Wastelands {
		wl := conn.Wastelands[i]
		client, err := wr.buildClient(session, conn, proxyClient, wl)
		if err != nil {
			return nil, fmt.Errorf("building client for %s: %w", wl.Upstream, err)
		}
		ws.Add(sdk.UpstreamInfo{
			Upstream: wl.Upstream,
			ForkOrg:  wl.ForkOrg,
			ForkDB:   wl.ForkDB,
			Mode:     wl.Mode,
		}, client)
	}

	wr.cacheWorkspace(session.ConnectionID, ws)
	return ws, nil
}

// InvalidateConnection evicts any cached workspace for the given connection.
func (wr *AuthServiceWorkspaceResolver) InvalidateConnection(connectionID string) {
	wr.mu.Lock()
	delete(wr.cache, connectionID)
	wr.mu.Unlock()

	prefix := connectionID + ":"
	wr.pendingMu.Lock()
	caches := make([]*pendingUpstreamCache, 0, len(wr.pendingCache))
	for key, cache := range wr.pendingCache {
		if strings.HasPrefix(key, prefix) {
			caches = append(caches, cache)
			delete(wr.pendingCache, key)
		}
	}
	wr.pendingMu.Unlock()
	for _, cache := range caches {
		cache.Stop()
	}
}

func (wr *AuthServiceWorkspaceResolver) cachedWorkspace(connectionID string) (*sdk.Workspace, bool) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	cached, ok := wr.cache[connectionID]
	if !ok || time.Now().After(cached.expiresAt) {
		if ok {
			delete(wr.cache, connectionID)
		}
		return nil, false
	}
	return cached.workspace, true
}

func (wr *AuthServiceWorkspaceResolver) cacheWorkspace(connectionID string, ws *sdk.Workspace) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	wr.cache[connectionID] = &cachedWorkspace{
		workspace: ws,
		expiresAt: time.Now().Add(cacheTTL),
	}
}

func (wr *AuthServiceWorkspaceResolver) getOrCreatePendingCache(
	connectionID string,
	provider *remote.DoltHubProvider,
	upOrg,
	upDB string,
) *pendingUpstreamCache {
	key := connectionID + ":" + upOrg + "/" + upDB
	wr.pendingMu.Lock()
	defer wr.pendingMu.Unlock()
	if c, ok := wr.pendingCache[key]; ok {
		return c
	}
	c := newPendingUpstreamCache(provider, upOrg, upDB, 2*time.Minute)
	wr.pendingCache[key] = c
	return c
}

func (wr *AuthServiceWorkspaceResolver) buildClient(
	session *UserSession,
	conn *dolthubauth.ConnectionResponse,
	proxyClient *http.Client,
	wl dolthubauth.WastelandConfig,
) (*sdk.Client, error) {
	upOrg, upDB, err := federation.ParseUpstream(wl.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream %q: %w", wl.Upstream, err)
	}

	mode := wl.Mode
	if mode == "" {
		mode = "pr"
	}

	db := backend.NewRemoteDBWithClient(proxyClient, upOrg, upDB, wl.ForkOrg, wl.ForkDB, mode)
	provider := remote.NewDoltHubProviderWithClient(proxyClient)
	pendingCache := wr.getOrCreatePendingCache(session.ConnectionID, provider, upOrg, upDB)

	branchURL := func(branch string) string {
		return fmt.Sprintf("https://www.dolthub.com/repositories/%s/%s/data/%s",
			wl.ForkOrg, wl.ForkDB, strings.ReplaceAll(branch, "/", "%2F"))
	}

	upstream := wl.Upstream

	client := sdk.New(sdk.ClientConfig{
		DB:                     db,
		RigHandle:              conn.RigHandle,
		Upstream:               wl.Upstream,
		Mode:                   mode,
		BestEffortPendingReads: true,
		LoadDiff: func(branch string) (string, error) {
			return db.Diff(branch)
		},
		CreatePR: func(branch string) (string, error) {
			wantedID := extractWantedIDFromBranch(branch)
			prTitle := fmt.Sprintf("[wl] %s", wantedID)
			if item, _, _, qerr := commons.QueryFullDetailAsOf(db, wantedID, branch); qerr == nil && item != nil {
				prTitle = fmt.Sprintf("[wl] %s", item.Title)
			}

			var prBody string
			if diff, derr := db.Diff(branch); derr == nil {
				prBody = diff
			}

			prURL, err := provider.CreatePR(wl.ForkOrg, upOrg, upDB, branch, prTitle, prBody)
			if err != nil && strings.Contains(err.Error(), "already exists") {
				existingURL, existingID := provider.FindPR(upOrg, upDB, wl.ForkOrg, branch)
				if existingID != "" {
					if uerr := provider.UpdatePR(upOrg, upDB, existingID, prTitle, prBody); uerr != nil {
						return "", uerr
					}
					return existingURL, nil
				}
			}
			return prURL, err
		},
		CheckPR: func(branch string) string {
			url, _ := provider.FindPR(upOrg, upDB, wl.ForkOrg, branch)
			return url
		},
		CheckPRContext: func(ctx context.Context, branch string) string {
			url, _ := provider.WithContext(ctx).FindPR(upOrg, upDB, wl.ForkOrg, branch)
			return url
		},
		ClosePR: func(branch string) error {
			_, prID := provider.FindPR(upOrg, upDB, wl.ForkOrg, branch)
			if prID == "" {
				return nil
			}
			return provider.ClosePR(upOrg, upDB, prID)
		},
		CloseUpstreamPR: func(prURL string) error {
			prID := extractPRID(prURL)
			if prID == "" {
				return fmt.Errorf("cannot extract PR ID from URL: %s", prURL)
			}
			return provider.ClosePR(upOrg, upDB, prID)
		},
		ListPendingItems: pendingCache.Get,
		ListPendingItemsContext: func(ctx context.Context) (map[string][]sdk.PendingItem, error) {
			return pendingCache.GetContext(ctx)
		},
		BranchURL: branchURL,
		Signing:   wl.Signing,
		SaveConfig: func(mode string, signing bool) error {
			current, err := wr.auth.GetConnection(context.Background(), session.SubjectID, session.ConnectionID)
			if err != nil {
				return err
			}
			_, err = wr.auth.PatchWastelandSettings(
				context.Background(),
				session.SubjectID,
				session.ConnectionID,
				upstream,
				current.RecordVersion,
				mode,
				signing,
			)
			if err == nil {
				wr.InvalidateConnection(session.ConnectionID)
			}
			return err
		},
		LoadPendingItem: func(wantedID string, pending sdk.PendingItem) (*commons.WantedItem, error) {
			if pending.ForkOwner == "" || pending.Branch == "" {
				return nil, fmt.Errorf("pending item %q is missing fork owner or branch", wantedID)
			}
			forkDB := backend.NewRemoteDBWithClient(proxyClient, upOrg, upDB, pending.ForkOwner, upDB, mode)
			return commons.QueryWantedDetailAsOf(forkDB, wantedID, pending.Branch)
		},
		LoadPendingItemContext: func(ctx context.Context, wantedID string, pending sdk.PendingItem) (*commons.WantedItem, error) {
			if pending.ForkOwner == "" || pending.Branch == "" {
				return nil, fmt.Errorf("pending item %q is missing fork owner or branch", wantedID)
			}
			forkDB := backend.NewRemoteDBWithClient(proxyClient, upOrg, upDB, pending.ForkOwner, upDB, mode)
			return commons.QueryWantedDetailAsOf(forkDB.WithContext(ctx), wantedID, pending.Branch)
		},
		LoadPendingDetail: func(wantedID string, pending sdk.PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			if pending.ForkOwner == "" || pending.Branch == "" {
				return nil, nil, nil, fmt.Errorf("pending item %q is missing fork owner or branch", wantedID)
			}
			forkDB := backend.NewRemoteDBWithClient(proxyClient, upOrg, upDB, pending.ForkOwner, upDB, mode)
			return commons.QueryFullDetailAsOf(forkDB, wantedID, pending.Branch)
		},
		LoadPendingDetailContext: func(ctx context.Context, wantedID string, pending sdk.PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			if pending.ForkOwner == "" || pending.Branch == "" {
				return nil, nil, nil, fmt.Errorf("pending item %q is missing fork owner or branch", wantedID)
			}
			forkDB := backend.NewRemoteDBWithClient(proxyClient, upOrg, upDB, pending.ForkOwner, upDB, mode)
			return commons.QueryFullDetailAsOf(forkDB.WithContext(ctx), wantedID, pending.Branch)
		},
	})

	return client, nil
}
