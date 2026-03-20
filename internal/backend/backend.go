// Package backend abstracts SQL execution against a dolt database.
//
// All query results use dolt's CSV format (header + rows) so existing
// parsers in commons work unchanged.
package backend

import "io"

// DB abstracts SQL execution against a dolt database.
type DB interface {
	// Query runs a read-only SQL SELECT.
	// ref: "" = working copy / HEAD, "branch-name", "remote/main".
	// Returns CSV output matching dolt sql -r csv format.
	Query(sql, ref string) (string, error)

	// Exec runs DML statements and auto-commits on the given branch.
	// branch: "" = main, "name" = named branch (created from main if needed).
	// Callers pass pure DML only — no DOLT_ADD or DOLT_COMMIT.
	// The implementation handles commit semantics internally.
	Exec(branch, commitMsg string, signed bool, stmts ...string) error

	// Branches returns branch names matching prefix.
	Branches(prefix string) ([]string, error)

	// DeleteBranch removes a branch.
	DeleteBranch(name string) error

	// PushBranch pushes a branch to origin. No-op for remote.
	PushBranch(branch string, stdout io.Writer) error

	// PushMain pushes main to origin. No-op for remote.
	PushMain(stdout io.Writer) error

	// Sync pulls latest from upstream. No-op for remote.
	Sync() error

	// MergeBranch merges a branch into main. No-op for remote.
	MergeBranch(branch string) error

	// DeleteRemoteBranch removes a branch on the origin remote. No-op for remote.
	DeleteRemoteBranch(branch string) error

	// PushAllRemotes pushes to both upstream and origin with sync retry. No-op for remote.
	PushAllRemotes(stdout io.Writer) error

	// CanWildWest returns nil if the backend supports wild-west mode (direct
	// upstream writes). Returns an error with a user-facing message if not.
	CanWildWest() error
}
