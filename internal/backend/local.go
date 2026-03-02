package backend

import (
	"fmt"
	"io"
	"strings"

	"github.com/julianknutsen/wasteland/internal/commons"
)

// LocalDB implements DB using the local dolt CLI.
type LocalDB struct {
	dir  string
	mode string // "pr" or "wild-west"
}

// NewLocalDB creates a DB backed by a local dolt database directory.
// mode determines Sync behavior: "pr" resets to upstream, otherwise pulls.
func NewLocalDB(dir, mode string) *LocalDB {
	return &LocalDB{dir: dir, mode: mode}
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
		_ = commons.CheckoutMain(l.dir)
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

// PushWithSync pushes to both upstream and origin with sync retry.
func (l *LocalDB) PushWithSync(stdout io.Writer) error {
	return commons.PushWithSync(l.dir, stdout)
}

// CanWildWest returns nil — local databases support wild-west mode.
func (l *LocalDB) CanWildWest() error { return nil }

// Sync pulls latest from upstream. In PR mode, resets main to upstream
// and fetches origin branches so PR mutations are visible via AS OF.
func (l *LocalDB) Sync() error {
	if l.mode == "pr" {
		if err := commons.ResetMainToUpstream(l.dir); err != nil {
			return err
		}
		_ = commons.FetchRemote(l.dir, "origin")
		_ = commons.TrackOriginBranches(l.dir, "wl/")
		return nil
	}
	return commons.PullUpstream(l.dir)
}

// MergeBranch merges a branch into main.
func (l *LocalDB) MergeBranch(branch string) error {
	return commons.MergeBranch(l.dir, branch)
}

// injectAsOf rewrites a SELECT query to include an AS OF clause.
// It handles "FROM table" → "FROM table AS OF 'ref'" for each table reference.
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

	// Find the table name after FROM.
	afterFrom := sql[fromIdx+6:]
	// Table name ends at space, WHERE, ORDER, LIMIT, GROUP, HAVING, JOIN, or semicolon.
	tableName := extractTableName(afterFrom)
	if tableName == "" {
		return sql
	}

	rest := afterFrom[len(tableName):]
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
