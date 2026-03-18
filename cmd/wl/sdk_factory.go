package main

import (
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/sdk"
)

// newSDKClient creates an SDK client from a federation config with all mutation
// callbacks wired up. Package-level variable to allow test overrides.
var newSDKClient = func(cfg *federation.Config, noPush bool) (*sdk.Client, error) {
	db, err := openDBFromConfig(cfg)
	if err != nil {
		return nil, err
	}
	return sdk.New(sdk.ClientConfig{
		DB:        db,
		RigHandle: cfg.RigHandle,
		Mode:      cfg.ResolveMode(),
		Signing:   cfg.Signing,
		HopURI:    cfg.HopURI,
		NoPush:    noPush,
		CreatePR: func(branch string) (string, error) {
			if cfg.ResolveBackend() != federation.BackendLocal {
				return createPRForBranchRemote(cfg, db, branch)
			}
			return createPRForBranch(cfg, branch)
		},
		CheckPR: func(branch string) string {
			return checkPRForBranch(cfg, branch)
		},
		ClosePR: func(branch string) error {
			return closePRForBranch(cfg, branch)
		},
		LoadPendingDetail: pendingDetailLoaderCallback(cfg),
		ListPendingItems:  listPendingItemsFromPRs(cfg),
		BranchURL:         branchURLCallback(cfg),
		CloseUpstreamPR:   closeUpstreamPRCallback(cfg),
	}), nil
}
