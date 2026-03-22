package rolecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_valid(t *testing.T) {
	input := `
name = "polecat"
tools = ["bash", "read", "write"]
context = ["CLAUDE.md"]

[constraints]
max_file_size = 1048576
read_only_paths = [".env"]
`
	r, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Name != "polecat" {
		t.Errorf("name = %q, want %q", r.Name, "polecat")
	}
	if len(r.Tools) != 3 {
		t.Errorf("tools count = %d, want 3", len(r.Tools))
	}
	if len(r.Context) != 1 || r.Context[0] != "CLAUDE.md" {
		t.Errorf("context = %v, want [CLAUDE.md]", r.Context)
	}
	if r.Constraints.MaxFileSize != 1048576 {
		t.Errorf("max_file_size = %d, want 1048576", r.Constraints.MaxFileSize)
	}
	if len(r.Constraints.ReadOnlyPaths) != 1 || r.Constraints.ReadOnlyPaths[0] != ".env" {
		t.Errorf("read_only_paths = %v, want [.env]", r.Constraints.ReadOnlyPaths)
	}
}

func TestParse_minimalValid(t *testing.T) {
	input := `
name = "witness"
tools = ["read"]
`
	r, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Name != "witness" {
		t.Errorf("name = %q, want %q", r.Name, "witness")
	}
}

func TestParse_missingName(t *testing.T) {
	input := `tools = ["bash"]`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error = %q, want it to mention 'name is required'", err)
	}
}

func TestParse_missingTools(t *testing.T) {
	input := `name = "polecat"`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for missing tools")
	}
	if !strings.Contains(err.Error(), "tools must not be empty") {
		t.Errorf("error = %q, want it to mention 'tools must not be empty'", err)
	}
}

func TestParse_unknownKey(t *testing.T) {
	input := `
name = "polecat"
tools = ["bash"]
bogus = true
`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("error = %q, want it to mention 'unknown keys'", err)
	}
}

func TestParse_negativeMaxFileSize(t *testing.T) {
	input := `
name = "polecat"
tools = ["bash"]

[constraints]
max_file_size = -1
`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for negative max_file_size")
	}
	if !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("error = %q, want it to mention 'must not be negative'", err)
	}
}

func TestParse_invalidTOML(t *testing.T) {
	input := `name = [[[invalid`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for invalid TOML")
	}
}

func TestParseFile(t *testing.T) {
	content := `
name = "refinery"
tools = ["bash", "read"]
context = ["docs/merge-protocol.md"]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "role.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := ParseFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Name != "refinery" {
		t.Errorf("name = %q, want %q", r.Name, "refinery")
	}
}

func TestParseFile_notFound(t *testing.T) {
	_, err := ParseFile("/nonexistent/role.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestValidate_multipleErrors(t *testing.T) {
	r := &Role{}
	err := Validate(r)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("error missing 'name is required': %v", err)
	}
	if !strings.Contains(err.Error(), "tools must not be empty") {
		t.Errorf("error missing 'tools must not be empty': %v", err)
	}
}
