//go:build integration

package integration

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// cloneDir is set once in TestMain; all tests query this shared clone.
var cloneDir string

func TestMain(m *testing.M) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		fmt.Fprintf(os.Stderr, "dolt not found in PATH — skipping integration tests\n")
		os.Exit(0)
	}

	tmp, err := os.MkdirTemp("", "wl-integration-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating temp dir: %v\n", err)
		os.Exit(1)
	}

	cloneDir = filepath.Join(tmp, "wl-commons")

	cmd := exec.Command(doltPath, "clone", "hop/wl-commons", cloneDir)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "skipping: cannot clone hop/wl-commons (network unavailable?): %v\n", err)
		os.RemoveAll(tmp)
		os.Exit(0) // skip gracefully, don't fail
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// doltQuery runs a SQL query against the cloned database and returns CSV output.
func doltQuery(query string) (string, error) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(doltPath, "sql", "-q", query, "-r", "csv")
	cmd.Dir = cloneDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("query failed: %s", string(exitErr.Stderr))
		}
		return "", err
	}
	return string(out), nil
}

// parseCSVRows parses CSV output, returning header + data rows.
func parseCSVRows(raw string) ([][]string, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(raw)))
	return r.ReadAll()
}

func TestCommonsClone(t *testing.T) {
	dotDolt := filepath.Join(cloneDir, ".dolt")
	info, err := os.Stat(dotDolt)
	if err != nil {
		t.Fatalf(".dolt directory not found in clone: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf(".dolt exists but is not a directory")
	}
}

func TestCommonsSchema_Tables(t *testing.T) {
	out, err := doltQuery("SHOW TABLES")
	if err != nil {
		t.Fatalf("SHOW TABLES: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	tables := make(map[string]bool)
	for _, row := range rows[1:] { // skip header
		if len(row) > 0 {
			tables[row[0]] = true
		}
	}

	expected := []string{"_meta", "rigs", "wanted", "completions", "stamps", "badges", "chain_meta"}
	for _, name := range expected {
		if !tables[name] {
			t.Errorf("missing table %q; got tables: %v", name, tables)
		}
	}
}

func TestCommonsSchema_WantedColumns(t *testing.T) {
	out, err := doltQuery("DESCRIBE wanted")
	if err != nil {
		t.Fatalf("DESCRIBE wanted: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	columns := make(map[string]bool)
	for _, row := range rows[1:] {
		if len(row) > 0 {
			columns[row[0]] = true
		}
	}

	expected := []string{
		"id", "title", "description", "project", "type", "priority",
		"tags", "posted_by", "claimed_by", "status", "effort_level",
		"evidence_url", "sandbox_required", "sandbox_scope", "sandbox_min_tier",
		"created_at", "updated_at",
	}
	for _, col := range expected {
		if !columns[col] {
			t.Errorf("wanted table missing column %q", col)
		}
	}
}

func TestCommonsSchema_RigsColumns(t *testing.T) {
	out, err := doltQuery("DESCRIBE rigs")
	if err != nil {
		t.Fatalf("DESCRIBE rigs: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	columns := make(map[string]bool)
	for _, row := range rows[1:] {
		if len(row) > 0 {
			columns[row[0]] = true
		}
	}

	expected := []string{"handle", "display_name", "dolthub_org"}
	for _, col := range expected {
		if !columns[col] {
			t.Errorf("rigs table missing column %q", col)
		}
	}
}

func TestCommonsData_WantedNotEmpty(t *testing.T) {
	out, err := doltQuery("SELECT COUNT(*) AS cnt FROM wanted")
	if err != nil {
		t.Fatalf("counting wanted rows: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("no rows returned from COUNT query")
	}

	count, err := strconv.Atoi(rows[1][0])
	if err != nil {
		t.Fatalf("parsing count %q: %v", rows[1][0], err)
	}
	if count == 0 {
		t.Error("wanted table is empty — expected at least 1 row")
	}
}

func TestCommonsData_ValidStatuses(t *testing.T) {
	out, err := doltQuery("SELECT DISTINCT status FROM wanted")
	if err != nil {
		t.Fatalf("querying statuses: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	valid := map[string]bool{
		"open": true, "claimed": true, "in_review": true,
		"completed": true, "withdrawn": true, "validated": true,
	}
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		s := row[0]
		if !valid[s] {
			t.Errorf("invalid status %q — expected one of %v", s, valid)
		}
	}
}

func TestCommonsData_ValidPriorities(t *testing.T) {
	out, err := doltQuery("SELECT DISTINCT priority FROM wanted WHERE priority IS NOT NULL")
	if err != nil {
		t.Fatalf("querying priorities: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		p, err := strconv.Atoi(row[0])
		if err != nil {
			t.Errorf("non-integer priority %q: %v", row[0], err)
			continue
		}
		if p < 0 || p > 4 {
			t.Errorf("priority %d out of range 0–4", p)
		}
	}
}

func TestCommonsData_ValidTypes(t *testing.T) {
	out, err := doltQuery("SELECT DISTINCT type FROM wanted WHERE type IS NOT NULL AND type != ''")
	if err != nil {
		t.Fatalf("querying types: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}

	valid := map[string]bool{
		"feature": true, "bug": true, "design": true,
		"rfc": true, "docs": true, "research": true, "community": true,
	}
	for _, row := range rows[1:] {
		if len(row) == 0 {
			continue
		}
		typ := row[0]
		if !valid[typ] {
			t.Errorf("invalid type %q — expected one of %v", typ, valid)
		}
	}
}

func TestCommonsData_OpenItemsExist(t *testing.T) {
	out, err := doltQuery("SELECT COUNT(*) AS cnt FROM wanted WHERE status = 'open'")
	if err != nil {
		t.Fatalf("counting open items: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("no rows returned from COUNT query")
	}

	count, err := strconv.Atoi(rows[1][0])
	if err != nil {
		t.Fatalf("parsing count %q: %v", rows[1][0], err)
	}
	if count == 0 {
		t.Error("no open wanted items — expected at least 1")
	}
}

func TestCommonsData_MetaVersion(t *testing.T) {
	out, err := doltQuery("SELECT `value` FROM _meta WHERE `key` = 'schema_version'")
	if err != nil {
		t.Fatalf("querying _meta schema_version: %v", err)
	}

	rows, err := parseCSVRows(out)
	if err != nil {
		t.Fatalf("parsing CSV: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("no schema_version row in _meta")
	}

	version := rows[1][0]
	if version != "1.2" {
		t.Errorf("schema_version = %q, want %q", version, "1.2")
	}
}
