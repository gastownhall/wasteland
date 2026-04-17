package githubcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileCachePutGetRoundTrip(t *testing.T) {
	fc := loadFromPath(filepath.Join(t.TempDir(), "cache.json"))

	want := Entry{
		GitHub:     "rileywhite",
		SourcePR:   "https://github.com/gastownhall/gascity/pull/548",
		ResolvedAt: "2026-04-17T12:00:00Z",
	}
	if err := fc.Put("rileywhite", want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := fc.Get("rileywhite")
	if !ok {
		t.Fatal("Get: want present")
	}
	if got != want {
		t.Fatalf("Get: got %+v, want %+v", got, want)
	}
}

func TestFileCacheGetMissing(t *testing.T) {
	fc := loadFromPath(filepath.Join(t.TempDir(), "cache.json"))

	got, ok := fc.Get("nobody")
	if ok {
		t.Fatal("Get: want not present")
	}
	if got != (Entry{}) {
		t.Fatalf("Get: got %+v, want zero Entry", got)
	}
}

func TestFileCachePutTriedAndFailed(t *testing.T) {
	fc := loadFromPath(filepath.Join(t.TempDir(), "cache.json"))

	want := Entry{GitHub: "", ResolvedAt: "2026-04-17T12:00:00Z"}
	if err := fc.Put("rome", want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	got, ok := fc.Get("rome")
	if !ok {
		t.Fatal("Get: want present")
	}
	if got.GitHub != "" {
		t.Fatalf("Get: want empty GitHub, got %q", got.GitHub)
	}
	if got != want {
		t.Fatalf("Get: got %+v, want %+v", got, want)
	}
}

func TestFileCacheAllIsIndependentSnapshot(t *testing.T) {
	fc := loadFromPath(filepath.Join(t.TempDir(), "cache.json"))
	if err := fc.Put("alice", Entry{GitHub: "alice"}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	snap := fc.All()
	if len(snap) != 1 {
		t.Fatalf("All: got %d entries, want 1", len(snap))
	}

	snap["alice"] = Entry{GitHub: "mutated"}
	snap["bob"] = Entry{GitHub: "bob"}

	got, ok := fc.Get("alice")
	if !ok || got.GitHub != "alice" {
		t.Fatalf("Get alice: got %+v, want GitHub=alice", got)
	}
	if _, ok := fc.Get("bob"); ok {
		t.Fatal("bob should not be present — snapshot mutation leaked back")
	}
}

func TestFileCacheLoadCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	fc := loadFromPath(path)
	if fc == nil {
		t.Fatal("loadFromPath returned nil")
	}
	if got := len(fc.All()); got != 0 {
		t.Fatalf("All: got %d entries, want 0", got)
	}

	// Corrupt file should not have been overwritten by load.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "{ not json" {
		t.Fatalf("file contents changed on load: %q", string(data))
	}
}

func TestFileCachePutRefusesToOverwriteCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fc := loadFromPath(path)
	err := fc.Put("alice", Entry{GitHub: "alice"})
	if err == nil {
		t.Fatal("Put on corrupt cache should return error, not silently wipe")
	}
	// Corrupt file must not have been overwritten.
	data, _ := os.ReadFile(path)
	if string(data) != "{ not json" {
		t.Fatalf("Put overwrote corrupt file: %q", string(data))
	}
}

func TestFileCacheLoadMissingFile(t *testing.T) {
	fc := loadFromPath(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if got := len(fc.All()); got != 0 {
		t.Fatalf("All: got %d entries, want 0", got)
	}
}

func TestFileCachePersistsToDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "cache.json")
	fc := loadFromPath(path)

	entry := Entry{
		GitHub:     "alice",
		SourcePR:   "https://github.com/o/r/pull/1",
		ResolvedAt: "2026-04-17T12:00:00Z",
	}
	if err := fc.Put("alice", entry); err != nil {
		t.Fatalf("Put: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var parsed map[string]Entry
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Unmarshal persisted file: %v (content: %q)", err, string(data))
	}
	if got := parsed["alice"]; got != entry {
		t.Fatalf("persisted entry: got %+v, want %+v", got, entry)
	}

	// Reload from disk and confirm the entry is visible.
	reloaded := loadFromPath(path)
	got, ok := reloaded.Get("alice")
	if !ok {
		t.Fatal("reloaded cache missing alice")
	}
	if got != entry {
		t.Fatalf("reloaded: got %+v, want %+v", got, entry)
	}
}

func TestDefaultPathUsesXDGDataHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	path, err := defaultPath()
	if err != nil {
		t.Fatalf("defaultPath: %v", err)
	}
	want := filepath.Join(tmp, "wasteland", "github-handles.json")
	if path != want {
		t.Fatalf("defaultPath: got %q, want %q", path, want)
	}
}
