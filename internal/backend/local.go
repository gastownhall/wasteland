package backend

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
)

// LocalDB implements DB using the local dolt CLI.
type LocalDB struct {
	dir    string
	syncFn func(dir string) error // injected sync strategy
}

// NewLocalDB creates a DB backed by a local dolt database directory.
// syncFn determines Sync behavior; use PRSync or WildWestSync.
// If syncFn is nil, defaults to WildWestSync.
func NewLocalDB(dir string, syncFn func(string) error) *LocalDB {
	if syncFn == nil {
		syncFn = WildWestSync
	}
	return &LocalDB{dir: dir, syncFn: syncFn}
}

// Dir returns the local database directory path.
func (l *LocalDB) Dir() string { return l.dir }

// Query runs a read-only SQL SELECT, injecting AS OF for non-empty refs.
func (l *LocalDB) Query(sql, ref string) (string, error) {
	if ref != "" {
		sql = injectAsOf(sql, ref)
	}
	return commons.DoltSQLQuery(l.dir, sql)
}

// Exec runs DML on a branch (or main if branch is ""), then auto-commits.
func (l *LocalDB) Exec(branch, commitMsg string, signed bool, stmts ...string) error {
	if branch != "" {
		if err := commons.CheckoutBranchFrom(l.dir, branch, "main"); err != nil {
			return fmt.Errorf("checkout branch %s: %w", branch, err)
		}
	}

	// Ensure each statement ends with a semicolon before joining.
	for i, s := range stmts {
		s = strings.TrimRight(s, "; \t\n")
		stmts[i] = s + ";"
	}
	script := strings.Join(stmts, "\n") + "\n"
	script += "CALL DOLT_ADD('-A');\n"
	script += commons.CommitSQL(commitMsg, signed)

	err := commons.DoltSQLScript(l.dir, script)

	if branch != "" {
		if checkoutErr := commons.CheckoutMain(l.dir); checkoutErr != nil {
			if err != nil {
				return errors.Join(err, fmt.Errorf("checkout cleanup: %w", checkoutErr))
			}
			return fmt.Errorf("checkout main after branch exec: %w", checkoutErr)
		}
	}

	return err
}

// Branches returns branch names matching the given prefix.
func (l *LocalDB) Branches(prefix string) ([]string, error) {
	return commons.ListBranches(l.dir, prefix)
}

// DeleteBranch removes a local branch.
func (l *LocalDB) DeleteBranch(name string) error {
	return commons.DeleteBranch(l.dir, name)
}

// DeleteRemoteBranch removes a branch on the origin remote.
func (l *LocalDB) DeleteRemoteBranch(branch string) error {
	return commons.DeleteRemoteBranch(l.dir, "origin", branch)
}

// PushBranch force-pushes a branch to origin.
func (l *LocalDB) PushBranch(branch string, stdout io.Writer) error {
	return commons.PushBranch(l.dir, branch, stdout)
}

// PushMain force-pushes local main to origin.
func (l *LocalDB) PushMain(stdout io.Writer) error {
	return commons.PushOriginMain(l.dir, stdout)
}

// PushAllRemotes pushes to both upstream and origin with sync retry.
func (l *LocalDB) PushAllRemotes(stdout io.Writer) error {
	return commons.PushAllRemotes(l.dir, stdout)
}

// CanWildWest returns nil — local databases support wild-west mode.
func (l *LocalDB) CanWildWest() error { return nil }

// Sync pulls latest from upstream using the injected sync strategy.
func (l *LocalDB) Sync() error {
	return l.syncFn(l.dir)
}

// PRSync resets main to upstream and fetches origin branches so PR
// mutations are visible via AS OF.
func PRSync(dir string) error {
	if err := commons.ResetMainToUpstream(dir); err != nil {
		return err
	}
	_ = commons.FetchRemote(dir, "origin")
	_ = commons.TrackOriginBranches(dir, "wl/")
	return nil
}

// WildWestSync pulls latest changes from the upstream remote.
func WildWestSync(dir string) error {
	return commons.PullUpstream(dir)
}

// MergeBranch merges a branch into main.
func (l *LocalDB) MergeBranch(branch string) error {
	return commons.MergeBranch(l.dir, branch)
}

// injectAsOf rewrites a SELECT query to include an AS OF clause.
// It handles "FROM table" → "FROM table AS OF 'ref'" for each table reference.
// NOTE: only handles the first FROM clause. Subqueries are not rewritten.
// Callers needing multi-table AS OF should use explicit AS OF variants.
func injectAsOf(sql, ref string) string {
	escaped := commons.EscapeSQL(ref)
	// Dolt supports AS OF at the query level: SELECT ... FROM t AS OF 'ref' WHERE ...
	// For simplicity, insert AS OF after FROM clause. This handles the common
	// patterns used in commons: single-table SELECTs.
	//
	// The existing code already has AS OF variants (QueryWantedDetailAsOf, etc.)
	// that manually embed AS OF. With the backend interface, callers pass the ref
	// and LocalDB injects it.
	upper := strings.ToUpper(sql)
	fromIdx := strings.Index(upper, " FROM ")
	if fromIdx < 0 {
		return sql
	}

	// Find the table name after FROM (skip any extra whitespace).
	afterFrom := sql[fromIdx+6:]
	trimmed := strings.TrimLeft(afterFrom, " \t\n\r")
	leadingSpace := len(afterFrom) - len(trimmed)
	tableName := extractTableName(trimmed)
	if tableName == "" {
		return sql
	}

	rest := afterFrom[leadingSpace+len(tableName):]
	return sql[:fromIdx+6] + tableName + fmt.Sprintf(" AS OF '%s'", escaped) + rest
}

// extractTableName extracts the table name from the start of a SQL fragment.
func extractTableName(s string) string {
	s = strings.TrimSpace(s)
	var name strings.Builder
	for _, ch := range s {
		if ch == ' ' || ch == ';' || ch == '\n' || ch == '\r' || ch == '\t' {
			break
		}
		name.WriteRune(ch)
	}
	return name.String()
}
