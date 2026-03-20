package commons

import (
	"fmt"
	"testing"
)

func TestParseSchemaVersion(t *testing.T) {
	tests := []struct {
		input   string
		want    SchemaVersion
		wantErr bool
	}{
		{"1.2", SchemaVersion{1, 2, "1.2"}, false},
		{"2.0", SchemaVersion{2, 0, "2.0"}, false},
		{"0.1", SchemaVersion{0, 1, "0.1"}, false},
		{"10.20", SchemaVersion{10, 20, "10.20"}, false},
		{"", SchemaVersion{}, true},
		{"abc", SchemaVersion{}, true},
		{"1", SchemaVersion{}, true},
		{"1.2.3", SchemaVersion{1, 2, "1.2.3"}, true}, // we only want MAJOR.MINOR
		{"a.b", SchemaVersion{}, true},
		{"1.b", SchemaVersion{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSchemaVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseSchemaVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if got.Major != tt.want.Major || got.Minor != tt.want.Minor {
				t.Errorf("ParseSchemaVersion(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSchemaVersion_123(t *testing.T) {
	// "1.2.3" should fail since SplitN with n=2 produces ["1", "2.3"]
	// and "2.3" is not a valid integer for MINOR.
	_, err := ParseSchemaVersion("1.2.3")
	if err == nil {
		t.Error("expected error for 1.2.3 but got nil")
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name     string
		local    SchemaVersion
		upstream SchemaVersion
		want     VersionDelta
	}{
		{"same", sv(1, 2), sv(1, 2), VersionSame},
		{"minor bump", sv(1, 1), sv(1, 2), VersionMinor},
		{"major bump", sv(1, 2), sv(2, 0), VersionMajor},
		{"local ahead minor", sv(1, 3), sv(1, 2), VersionAhead},
		{"local ahead major", sv(2, 0), sv(1, 5), VersionAhead},
		{"major bump with minor", sv(1, 5), sv(2, 3), VersionMajor},
		{"zero to one", sv(0, 1), sv(1, 0), VersionMajor},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareVersions(tt.local, tt.upstream)
			if got != tt.want {
				t.Errorf("CompareVersions(%v, %v) = %d, want %d", tt.local, tt.upstream, got, tt.want)
			}
		})
	}
}

func TestSchemaVersionString(t *testing.T) {
	v := SchemaVersion{Major: 1, Minor: 2, Raw: "1.2"}
	if s := v.String(); s != "1.2" {
		t.Errorf("String() = %q, want %q", s, "1.2")
	}
}

func TestSchemaVersionIsZero(t *testing.T) {
	zero := SchemaVersion{}
	if !zero.IsZero() {
		t.Error("expected zero value to be IsZero()")
	}
	nonZero := SchemaVersion{Major: 1, Minor: 0, Raw: "1.0"}
	if nonZero.IsZero() {
		t.Error("expected non-zero value to not be IsZero()")
	}
}

// sv is a test helper to create a SchemaVersion.
func sv(major, minor int) SchemaVersion {
	return SchemaVersion{Major: major, Minor: minor, Raw: fmt.Sprintf("%d.%d", major, minor)}
}
