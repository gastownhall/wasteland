package hosted

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/remote"
	"github.com/gastownhall/wasteland/internal/sdk"
)

const cacheTTL = 1 * time.Minute

type cachedWorkspace struct {
	workspace *sdk.Workspace
	expiresAt time.Time
}

// WorkspaceResolver resolves per-user sdk.Workspaces from Nango credentials.
type WorkspaceResolver struct {
	nango    *NangoClient
	sessions *SessionStore
	mu       sync.Mutex
	cache    map[string]*cachedWorkspace // connectionID -> cached workspace

	pendingMu    sync.Mutex
	pendingCache map[string]*pendingUpstreamCache // upstream ("org/db") -> shared cache
}

// pendingUpstreamCache is a background-refreshing cache of pending items
// shared across all users on the same upstream.
type pendingUpstreamCache struct {
	mu     sync.RWMutex
	cached map[string][]sdk.PendingItem
	stop   chan struct{}
}

func newPendingUpstreamCache(provider *remote.DoltHubProvider, upOrg, upDB string, interval time.Duration) *pendingUpstreamCache {
	c := &pendingUpstreamCache{stop: make(chan struct{})}

	refresh := func() {
		states, err := provider.ListPendingWantedIDs(upOrg, upDB)
		if err != nil {
			slog.Warn("pending items refresh failed", "upstream", upOrg+"/"+upDB, "error", err)
			return
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
		c.mu.Unlock()
	}

	go refresh()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				refresh()
			case <-c.stop:
				return
			}
		}
	}()

	return c
}

func (c *pendingUpstreamCache) Get() (map[string][]sdk.PendingItem, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached, nil
}

// NewWorkspaceResolver creates a WorkspaceResolver.
func NewWorkspaceResolver(nango *NangoClient, sessions *SessionStore) *WorkspaceResolver {
	return &WorkspaceResolver{
		nango:        nango,
		sessions:     sessions,
		cache:        make(map[string]*cachedWorkspace),
		pendingCache: make(map[string]*pendingUpstreamCache),
	}
}

// Resolve builds or returns a cached sdk.Workspace for the given session.
func (wr *WorkspaceResolver) Resolve(session *UserSession) (*sdk.Workspace, error) {
	// Fast path: return cached workspace if still valid.
	wr.mu.Lock()
	if cached, ok := wr.cache[session.ConnectionID]; ok && time.Now().Before(cached.expiresAt) {
		wr.mu.Unlock()
		return cached.workspace, nil
	}
	wr.mu.Unlock()

	// Fetch metadata and API key from Nango (no lock held during network call).
	apiKey, meta, err := wr.nango.GetConnection(session.ConnectionID)
	if err != nil {
		return nil, fmt.Errorf("resolving credentials: %w", err)
	}
	if meta == nil || len(meta.Wastelands) == 0 {
		return nil, fmt.Errorf("no wasteland config found for connection %s", session.ConnectionID)
	}

	// Re-check cache under lock to avoid duplicate workspace creation (TOCTOU).
	wr.mu.Lock()
	defer wr.mu.Unlock()

	if cached, ok := wr.cache[session.ConnectionID]; ok && time.Now().Before(cached.expiresAt) {
		return cached.workspace, nil
	}

	// Build a new workspace with a client for each wasteland.
	ws := sdk.NewWorkspace(meta.RigHandle)
	for i := range meta.Wastelands {
		wl := &meta.Wastelands[i]
		client, err := wr.buildClient(wl, meta.RigHandle, session.ConnectionID, apiKey, meta)
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

	wr.cache[session.ConnectionID] = &cachedWorkspace{
		workspace: ws,
		expiresAt: time.Now().Add(cacheTTL),
	}
	return ws, nil
}

// InvalidateConnection removes the cached workspace for a connection.
func (wr *WorkspaceResolver) InvalidateConnection(connectionID string) {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	delete(wr.cache, connectionID)
}

// getOrCreatePendingCache returns a shared background-refreshing cache for the
// given upstream. All users on the same upstream share a single cache instance.
func (wr *WorkspaceResolver) getOrCreatePendingCache(provider *remote.DoltHubProvider, upOrg, upDB string) *pendingUpstreamCache {
	key := upOrg + "/" + upDB
	wr.pendingMu.Lock()
	defer wr.pendingMu.Unlock()
	if c, ok := wr.pendingCache[key]; ok {
		return c
	}
	c := newPendingUpstreamCache(provider, upOrg, upDB, 2*time.Minute)
	wr.pendingCache[key] = c
	return c
}

func (wr *WorkspaceResolver) buildClient(wl *WastelandConfig, rigHandle, connectionID, apiKey string, _ *UserMetadata) (*sdk.Client, error) {
	upOrg, upDB, err := federation.ParseUpstream(wl.Upstream)
	if err != nil {
		return nil, fmt.Errorf("parsing upstream %q: %w", wl.Upstream, err)
	}

	mode := wl.Mode
	if mode == "" {
		mode = "pr"
	}

	db := backend.NewRemoteDB(apiKey, upOrg, upDB, wl.ForkOrg, wl.ForkDB, mode)

	provider := remote.NewDoltHubProvider(apiKey)

	branchURL := func(branch string) string {
		return fmt.Sprintf("https://www.dolthub.com/repositories/%s/%s/data/%s",
			wl.ForkOrg, wl.ForkDB, strings.ReplaceAll(branch, "/", "%2F"))
	}

	// Capture the upstream for the SaveConfig closure.
	upstream := wl.Upstream

	client := sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: rigHandle,
		Mode:      mode,
		LoadDiff: func(branch string) (string, error) {
			return db.Diff(branch)
		},
		CreatePR: func(branch string) (string, error) {
			// Build PR title from the wanted item.
			wantedID := extractWantedIDFromBranch(branch)
			prTitle := fmt.Sprintf("[wl] %s", wantedID)
			if item, _, _, qerr := commons.QueryFullDetailAsOf(db, wantedID, branch); qerr == nil && item != nil {
				prTitle = fmt.Sprintf("[wl] %s", item.Title)
			}

			// Build PR description from the branch diff.
			var prBody string
			if diff, derr := db.Diff(branch); derr == nil {
				prBody = diff
			}

			prURL, err := provider.CreatePR(wl.ForkOrg, upOrg, upDB, branch, prTitle, prBody)
			if err != nil && strings.Contains(err.Error(), "already exists") {
				existingURL, existingID := provider.FindPR(upOrg, upDB, wl.ForkOrg, branch)
				if existingID != "" {
					if uerr := provider.UpdatePR(upOrg, upDB, existingID, prTitle, prBody); uerr != nil {
						slog.Warn("failed to update existing PR", "pr_id", existingID, "error", uerr)
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
		ListPendingItems: wr.getOrCreatePendingCache(provider, upOrg, upDB).Get,
		BranchURL:        branchURL,
		Signing:          wl.Signing,
		SaveConfig: func(mode string, signing bool) error {
			// Read-modify-write: fetch current metadata, update just this wasteland, write back.
			_, currentMeta, err := wr.nango.GetConnection(connectionID)
			if err != nil {
				return fmt.Errorf("reading metadata for save: %w", err)
			}
			if currentMeta == nil {
				return fmt.Errorf("no metadata found for connection %s", connectionID)
			}
			entry := currentMeta.FindWasteland(upstream)
			if entry == nil {
				return fmt.Errorf("wasteland %s not found in metadata", upstream)
			}
			entry.Mode = mode
			entry.Signing = signing
			return wr.nango.SetMetadata(connectionID, currentMeta)
		},
		LoadPendingDetail: func(wantedID string, pending sdk.PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error) {
			if pending.ForkOwner == "" || pending.Branch == "" {
				return nil, nil, nil, fmt.Errorf("pending item %q is missing fork owner or branch", wantedID)
			}
			forkDB := backend.NewRemoteDB(apiKey, upOrg, upDB, pending.ForkOwner, upDB, mode)
			return commons.QueryFullDetailAsOf(forkDB, wantedID, pending.Branch)
		},
	})

	return client, nil
}

// extractWantedIDFromBranch parses a branch name like "wl/{rig}/{wantedID}"
// and returns the wanted ID, or the raw branch name as fallback.
func extractWantedIDFromBranch(branch string) string {
	parts := strings.SplitN(branch, "/", 3)
	if len(parts) == 3 && parts[0] == "wl" {
		return parts[2]
	}
	return branch
}

// extractPRID extracts the pull request ID from a DoltHub PR URL like
// "https://www.dolthub.com/repositories/org/db/pulls/123".
func extractPRID(prURL string) string {
	idx := strings.LastIndex(prURL, "/pulls/")
	if idx < 0 {
		return ""
	}
	return prURL[idx+len("/pulls/"):]
}
