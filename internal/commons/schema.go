package commons

import (
	"fmt"
	"strconv"
	"strings"
)

// SchemaVersion represents a MAJOR.MINOR version pair from _meta.schema_version.
type SchemaVersion struct {
	Major int
	Minor int
	Raw   string
}

// String returns the canonical "MAJOR.MINOR" representation.
func (v SchemaVersion) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// IsZero returns true if the version was not set (e.g. missing _meta table).
func (v SchemaVersion) IsZero() bool {
	return v.Raw == ""
}

// ParseSchemaVersion parses a version string like "1.2" into a SchemaVersion.
func ParseSchemaVersion(s string) (SchemaVersion, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return SchemaVersion{}, fmt.Errorf("empty schema version")
	}

	parts := strings.SplitN(s, ".", 2)
	if len(parts) != 2 {
		return SchemaVersion{}, fmt.Errorf("invalid schema version %q: expected MAJOR.MINOR", s)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return SchemaVersion{}, fmt.Errorf("invalid schema version %q: bad MAJOR: %w", s, err)
	}

	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return SchemaVersion{}, fmt.Errorf("invalid schema version %q: bad MINOR: %w", s, err)
	}

	return SchemaVersion{Major: major, Minor: minor, Raw: s}, nil
}

// VersionDelta describes the relationship between local and upstream schema versions.
type VersionDelta int

const (
	// VersionSame means versions are identical.
	VersionSame VersionDelta = iota
	// VersionMinor means upstream has a higher MINOR (backwards-compatible).
	VersionMinor
	// VersionMajor means upstream has a higher MAJOR (potentially breaking).
	VersionMajor
	// VersionAhead means local is ahead of upstream (unusual).
	VersionAhead
)

// CompareVersions returns the delta between local and upstream versions.
func CompareVersions(local, upstream SchemaVersion) VersionDelta {
	if local.Major == upstream.Major && local.Minor == upstream.Minor {
		return VersionSame
	}
	if upstream.Major > local.Major {
		return VersionMajor
	}
	if upstream.Major < local.Major {
		return VersionAhead
	}
	// Same major
	if upstream.Minor > local.Minor {
		return VersionMinor
	}
	return VersionAhead
}

// ReadSchemaVersion queries _meta.schema_version from a dolt database at the given ref.
// ref="" queries local HEAD, "upstream/main" queries the upstream ref.
// Returns a zero SchemaVersion if _meta doesn't exist or has no schema_version row.
func ReadSchemaVersion(db DB, ref string) (SchemaVersion, error) {
	query := "SELECT value FROM _meta WHERE `key` = 'schema_version'"
	out, err := db.Query(query, ref)
	if err != nil {
		// Table might not exist in very old databases.
		return SchemaVersion{}, nil
	}

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return SchemaVersion{}, nil
	}

	raw := strings.TrimSpace(lines[1])
	if raw == "" {
		return SchemaVersion{}, nil
	}

	return ParseSchemaVersion(raw)
}
