// Package sdk provides a high-level client for the Wasteland wanted board.
//
// It extracts mode-aware mutation orchestration (wild-west vs PR branch workflow)
// from the TUI into a reusable layer that can be consumed by any frontend.
package sdk

import (
	"context"
	"log/slog"
	"sync"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/githubcache"
	"github.com/gastownhall/wasteland/internal/pile"
)

// ClientConfig holds the parameters needed to create a Client.
type ClientConfig struct {
	DB        commons.DB // database backend (required)
	RigHandle string     // current rig handle (required)
	Upstream  string     // canonical upstream identifier when known
	Mode      string     // "wild-west" or "pr"
	Signing   bool       // GPG-signed dolt commits
	HopURI    string     // rig's HOP protocol URI
	NoPush    bool       // skip pushing after mutations
	// BestEffortPendingReads allows browse/detail to omit pending-overlay data
	// when upstream pending callbacks fail. Callers can override this per-request
	// with WithStrictPendingReads when shared caches or contracts require full
	// pending state.
	BestEffortPendingReads bool

	// Optional callbacks — nil disables the feature.
	CreatePR                 func(branch string) (string, error)
	CheckPR                  func(branch string) string
	CheckPRContext           func(ctx context.Context, branch string) string
	ClosePR                  func(branch string) error // close the PR for the given branch
	LoadDiff                 func(branch string) (string, error)
	LoadPendingItem          func(wantedID string, pending PendingItem) (*commons.WantedItem, error)
	LoadPendingItemContext   func(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, error)
	LoadPendingDetail        func(wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	LoadPendingDetailContext func(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	SaveConfig               func(mode string, signing bool) error
	ListPendingItems         func() (map[string][]PendingItem, error) // returns wanted IDs with pending upstream PR state
	ListPendingItemsContext  func(ctx context.Context) (map[string][]PendingItem, error)
	BranchURL                func(branch string) string // returns a web URL for the branch
	CloseUpstreamPR          func(prURL string) error   // close an upstream PR by its web URL
}

// Client provides mode-aware operations against the Wasteland wanted board.
type Client struct {
	db                     commons.DB
	rigHandle              string
	upstream               string
	mode                   string
	signing                bool
	hopURI                 string
	noPush                 bool
	bestEffortPendingReads bool
	mu                     sync.Mutex // serializes mutations (dolt CLI is single-writer)

	// CreatePR submits a PR for the given branch. Nil disables the feature.
	CreatePR func(branch string) (string, error)
	// CheckPR returns an existing PR URL for the branch, or "".
	CheckPR func(branch string) string
	// CheckPRContext returns an existing PR URL for the branch using request-scoped context, or "".
	CheckPRContext func(ctx context.Context, branch string) string
	// ClosePR closes the PR associated with the given branch. Nil disables the feature.
	ClosePR func(branch string) error
	// LoadDiff returns a diff for the given branch. Nil disables the feature.
	LoadDiff func(branch string) (string, error)
	// LoadPendingItem loads only the wanted row for a pending upstream item from the source branch's fork.
	// Nil falls back to reading the branch from the client's configured DB.
	LoadPendingItem func(wantedID string, pending PendingItem) (*commons.WantedItem, error)
	// LoadPendingItemContext loads a pending upstream item with request-scoped context.
	// Nil falls back to LoadPendingItem and then the client's configured DB.
	LoadPendingItemContext func(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, error)
	// LoadPendingDetail loads detail for a pending upstream item from the source branch's fork.
	// Nil falls back to reading the branch from the client's configured DB.
	LoadPendingDetail func(wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	// LoadPendingDetailContext loads pending upstream detail with request-scoped context.
	// Nil falls back to LoadPendingDetail and then the client's configured DB.
	LoadPendingDetailContext func(ctx context.Context, wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	// SaveConfig persists mode and signing settings. Nil disables the feature.
	SaveConfig func(mode string, signing bool) error
	// ListPendingItems returns wanted IDs with pending upstream PR state. Nil disables the feature.
	ListPendingItems func() (map[string][]PendingItem, error)
	// ListPendingItemsContext returns wanted IDs with pending upstream PR state using request-scoped context.
	ListPendingItemsContext func(ctx context.Context) (map[string][]PendingItem, error)
	// BranchURL returns a web URL for the given branch. Nil disables the feature.
	BranchURL func(branch string) string
	// CloseUpstreamPR closes an upstream PR by its web URL. Nil disables the feature.
	CloseUpstreamPR func(prURL string) error

	// ghCache, ghResolver, and ghCommons back the post-Accept GitHub-handle
	// cache hook. A nil ghCache disables the hook entirely. These are set
	// from New (best-effort) and overridable via SetGitHubCache in tests.
	ghCache    githubcache.Cache
	ghResolver githubcache.Resolver
	ghCommons  pile.RowQuerier
}

// New creates a Client from the given config.
func New(cfg ClientConfig) *Client {
	c := &Client{
		db:                       cfg.DB,
		rigHandle:                cfg.RigHandle,
		upstream:                 cfg.Upstream,
		mode:                     cfg.Mode,
		signing:                  cfg.Signing,
		hopURI:                   cfg.HopURI,
		noPush:                   cfg.NoPush,
		bestEffortPendingReads:   cfg.BestEffortPendingReads,
		CreatePR:                 cfg.CreatePR,
		CheckPR:                  cfg.CheckPR,
		CheckPRContext:           cfg.CheckPRContext,
		ClosePR:                  cfg.ClosePR,
		LoadDiff:                 cfg.LoadDiff,
		LoadPendingItem:          cfg.LoadPendingItem,
		LoadPendingItemContext:   cfg.LoadPendingItemContext,
		LoadPendingDetail:        cfg.LoadPendingDetail,
		LoadPendingDetailContext: cfg.LoadPendingDetailContext,
		SaveConfig:               cfg.SaveConfig,
		ListPendingItems:         cfg.ListPendingItems,
		ListPendingItemsContext:  cfg.ListPendingItemsContext,
		BranchURL:                cfg.BranchURL,
		CloseUpstreamPR:          cfg.CloseUpstreamPR,
	}
	// Best-effort init for the post-Accept GitHub-handle cache hook. A
	// failure here leaves ghCache nil so the hook becomes a no-op;
	// mutations never fail because the handle cache is unavailable.
	if cache, err := githubcache.Load(); err != nil {
		slog.Warn("stamp_cache: disabled; could not load github-handles cache", "error", err)
	} else {
		c.ghCache = cache
		c.ghResolver = githubcache.NewResolver()
		c.ghCommons = pile.NewCommonsReader()
	}
	return c
}

type strictPendingReadsKey struct{}

// WithStrictPendingReads disables best-effort pending overlay fallbacks for a
// single browse/detail request. API handlers use this for shared cached reads so
// they fail closed instead of storing partial pending state.
func WithStrictPendingReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, strictPendingReadsKey{}, true)
}

func strictPendingReads(ctx context.Context) bool {
	enabled, _ := ctx.Value(strictPendingReadsKey{}).(bool)
	return enabled
}

// Mode returns the current workflow mode ("wild-west" or "pr").
func (c *Client) Mode() string { return c.mode }

// RigHandle returns the current rig handle.
func (c *Client) RigHandle() string { return c.rigHandle }

// Upstream returns the canonical upstream identifier when known.
func (c *Client) Upstream() string { return c.upstream }

// WithRigHandle returns a shallow copy of the client with a different rig handle.
// The copy shares the same DB connection and read-only callbacks but uses the
// new handle for browse/detail/dashboard filtering. Intended for staging-only
// impersonation of another user's read-only view.
func (c *Client) WithRigHandle(handle string) *Client {
	return &Client{
		db:                       c.db,
		rigHandle:                handle,
		upstream:                 c.upstream,
		mode:                     c.mode,
		signing:                  c.signing,
		hopURI:                   c.hopURI,
		noPush:                   c.noPush,
		bestEffortPendingReads:   c.bestEffortPendingReads,
		CreatePR:                 c.CreatePR,
		CheckPR:                  c.CheckPR,
		CheckPRContext:           c.CheckPRContext,
		ClosePR:                  c.ClosePR,
		LoadDiff:                 c.LoadDiff,
		LoadPendingItem:          c.LoadPendingItem,
		LoadPendingItemContext:   c.LoadPendingItemContext,
		LoadPendingDetail:        c.LoadPendingDetail,
		LoadPendingDetailContext: c.LoadPendingDetailContext,
		SaveConfig:               c.SaveConfig,
		ListPendingItems:         c.ListPendingItems,
		ListPendingItemsContext:  c.ListPendingItemsContext,
		BranchURL:                c.BranchURL,
		CloseUpstreamPR:          c.CloseUpstreamPR,
		ghCache:                  c.ghCache,
		ghResolver:               c.ghResolver,
		ghCommons:                c.ghCommons,
	}
}
