// Package rolecfg parses Gas Town role definition files (TOML).
//
// A role file defines an agent's identity: its name, available tools,
// context sources, and operational constraints. Example:
//
//	name = "polecat"
//
//	tools = ["bash", "read", "write", "grep"]
//
//	context = ["CLAUDE.md", "docs/conventions.md"]
//
//	[constraints]
//	max_file_size = 1048576
//	read_only_paths = [".env", "secrets/"]
package rolecfg

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// Role defines an agent role configuration.
type Role struct {
	// Name is the role identifier (e.g., "polecat", "witness", "refinery").
	Name string `toml:"name"`

	// Tools lists the tools available to this role.
	Tools []string `toml:"tools"`

	// Context lists files or globs that should be loaded as context.
	Context []string `toml:"context"`

	// Constraints defines operational limits for the role.
	Constraints Constraints `toml:"constraints"`
}

// Constraints defines operational limits for a role.
type Constraints struct {
	// MaxFileSize is the maximum file size in bytes the role may read.
	MaxFileSize int64 `toml:"max_file_size"`

	// ReadOnlyPaths lists paths the role must not modify.
	ReadOnlyPaths []string `toml:"read_only_paths"`
}

// ParseFile reads and parses a role definition from a TOML file at path.
func ParseFile(path string) (*Role, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("rolecfg: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

// Parse reads and parses a role definition from r.
func Parse(r io.Reader) (*Role, error) {
	var role Role
	md, err := toml.NewDecoder(r).Decode(&role)
	if err != nil {
		return nil, fmt.Errorf("rolecfg: decode: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("rolecfg: unknown keys: %s", strings.Join(keys, ", "))
	}
	if err := Validate(&role); err != nil {
		return nil, err
	}
	return &role, nil
}

// Validate checks that a Role has all required fields and valid values.
func Validate(r *Role) error {
	var errs []string
	if r.Name == "" {
		errs = append(errs, "name is required")
	}
	if len(r.Tools) == 0 {
		errs = append(errs, "tools must not be empty")
	}
	if r.Constraints.MaxFileSize < 0 {
		errs = append(errs, "constraints.max_file_size must not be negative")
	}
	if len(errs) > 0 {
		return fmt.Errorf("rolecfg: validate: %s", strings.Join(errs, "; "))
	}
	return nil
}

// ErrUnknownKey is returned when the TOML contains unrecognized keys.
var ErrUnknownKey = errors.New("rolecfg: unknown key")
