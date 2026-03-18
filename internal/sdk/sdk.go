// Package sdk provides a high-level client for the Wasteland wanted board.
//
// It extracts mode-aware mutation orchestration (wild-west vs PR branch workflow)
// from the TUI into a reusable layer that can be consumed by any frontend.
package sdk

import (
	"sync"

	"github.com/gastownhall/wasteland/internal/commons"
)

// ClientConfig holds the parameters needed to create a Client.
type ClientConfig struct {
	DB        commons.DB // database backend (required)
	RigHandle string     // current rig handle (required)
	Mode      string     // "wild-west" or "pr"
	Signing   bool       // GPG-signed dolt commits
	HopURI    string     // rig's HOP protocol URI
	NoPush    bool       // skip pushing after mutations

	// Optional callbacks — nil disables the feature.
	CreatePR          func(branch string) (string, error)
	CheckPR           func(branch string) string
	ClosePR           func(branch string) error // close the PR for the given branch
	LoadDiff          func(branch string) (string, error)
	LoadPendingDetail func(wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	SaveConfig        func(mode string, signing bool) error
	ListPendingItems  func() (map[string][]PendingItem, error) // returns wanted IDs with pending upstream PR state
	BranchURL         func(branch string) string               // returns a web URL for the branch
	CloseUpstreamPR   func(prURL string) error                 // close an upstream PR by its web URL
}

// Client provides mode-aware operations against the Wasteland wanted board.
type Client struct {
	db        commons.DB
	rigHandle string
	mode      string
	signing   bool
	hopURI    string
	noPush    bool
	mu        sync.Mutex // serializes mutations (dolt CLI is single-writer)

	// CreatePR submits a PR for the given branch. Nil disables the feature.
	CreatePR func(branch string) (string, error)
	// CheckPR returns an existing PR URL for the branch, or "".
	CheckPR func(branch string) string
	// ClosePR closes the PR associated with the given branch. Nil disables the feature.
	ClosePR func(branch string) error
	// LoadDiff returns a diff for the given branch. Nil disables the feature.
	LoadDiff func(branch string) (string, error)
	// LoadPendingDetail loads detail for a pending upstream item from the source branch's fork.
	// Nil falls back to reading the branch from the client's configured DB.
	LoadPendingDetail func(wantedID string, pending PendingItem) (*commons.WantedItem, *commons.CompletionRecord, *commons.Stamp, error)
	// SaveConfig persists mode and signing settings. Nil disables the feature.
	SaveConfig func(mode string, signing bool) error
	// ListPendingItems returns wanted IDs with pending upstream PR state. Nil disables the feature.
	ListPendingItems func() (map[string][]PendingItem, error)
	// BranchURL returns a web URL for the given branch. Nil disables the feature.
	BranchURL func(branch string) string
	// CloseUpstreamPR closes an upstream PR by its web URL. Nil disables the feature.
	CloseUpstreamPR func(prURL string) error
}

// New creates a Client from the given config.
func New(cfg ClientConfig) *Client {
	return &Client{
		db:                cfg.DB,
		rigHandle:         cfg.RigHandle,
		mode:              cfg.Mode,
		signing:           cfg.Signing,
		hopURI:            cfg.HopURI,
		noPush:            cfg.NoPush,
		CreatePR:          cfg.CreatePR,
		CheckPR:           cfg.CheckPR,
		ClosePR:           cfg.ClosePR,
		LoadDiff:          cfg.LoadDiff,
		LoadPendingDetail: cfg.LoadPendingDetail,
		SaveConfig:        cfg.SaveConfig,
		ListPendingItems:  cfg.ListPendingItems,
		BranchURL:         cfg.BranchURL,
		CloseUpstreamPR:   cfg.CloseUpstreamPR,
	}
}

// Mode returns the current workflow mode ("wild-west" or "pr").
func (c *Client) Mode() string { return c.mode }

// RigHandle returns the current rig handle.
func (c *Client) RigHandle() string { return c.rigHandle }

// WithRigHandle returns a shallow copy of the client with a different rig handle.
// The copy shares the same DB connection and read-only callbacks but uses the
// new handle for browse/detail/dashboard filtering. Intended for staging-only
// impersonation of another user's read-only view.
func (c *Client) WithRigHandle(handle string) *Client {
	return &Client{
		db:                c.db,
		rigHandle:         handle,
		mode:              c.mode,
		signing:           c.signing,
		hopURI:            c.hopURI,
		noPush:            c.noPush,
		CreatePR:          c.CreatePR,
		CheckPR:           c.CheckPR,
		ClosePR:           c.ClosePR,
		LoadDiff:          c.LoadDiff,
		LoadPendingDetail: c.LoadPendingDetail,
		SaveConfig:        c.SaveConfig,
		ListPendingItems:  c.ListPendingItems,
		BranchURL:         c.BranchURL,
		CloseUpstreamPR:   c.CloseUpstreamPR,
	}
}
