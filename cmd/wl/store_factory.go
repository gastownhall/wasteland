package main

import (
	"fmt"

	"github.com/gastownhall/wasteland/internal/backend"
	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
)

// openDB creates a commons.DB for the given local database directory.
// Package-level variable to allow test overrides.
var openDB = func(localDir string) commons.DB {
	return backend.NewLocalDB(localDir, nil)
}

// syncFnForMode returns the appropriate sync function for the given mode.
func syncFnForMode(mode string) func(string) error {
	if mode == federation.ModePR {
		return backend.PRSync
	}
	return backend.WildWestSync
}

// openDBFromConfig creates a commons.DB using the resolved backend from config.
// Package-level variable to allow test overrides.
var openDBFromConfig = func(cfg *federation.Config) (commons.DB, error) {
	if cfg.ResolveBackend() == federation.BackendLocal {
		return backend.NewLocalDB(cfg.LocalDir, syncFnForMode(cfg.ResolveMode())), nil
	}
	if cfg.IsGitHub() {
		return nil, fmt.Errorf("GitHub backend requires local dolt\n\n  Install: https://docs.dolthub.com/introduction/installation\n  Then: wl join --github %s --local-db", cfg.Upstream)
	}
	token := commons.DoltHubToken()
	if token == "" {
		return nil, fmt.Errorf("DOLTHUB_TOKEN required for remote mode\n\nGet your token from https://www.dolthub.com/settings/tokens")
	}
	upOrg, upDB, err := federation.ParseUpstream(cfg.Upstream)
	if err != nil {
		return nil, err
	}
	return backend.NewRemoteDB(token, upOrg, upDB, cfg.ForkOrg, cfg.ForkDB), nil
}
