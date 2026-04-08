package herald

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// fakeLister returns a fixed set of items.
type fakeLister struct {
	items []Item
	err   error
}

func (f *fakeLister) ListItems() ([]Item, error) { return f.items, f.err }

// recordingNotifier captures notifications.
type recordingNotifier struct {
	calls [][]Change
}

func (r *recordingNotifier) Notify(changes []Change) error {
	r.calls = append(r.calls, changes)
	return nil
}

func TestDiff_EmptyToItems(t *testing.T) {
	items := []Item{
		{ID: "w-1", Title: "Fix bug", Status: "open"},
		{ID: "w-2", Title: "Add feature", Status: "open"},
	}
	changes := Diff(nil, items)
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(changes))
	}
	for _, c := range changes {
		if c.Kind != Added {
			t.Errorf("expected Added, got %s", c.Kind)
		}
	}
}

func TestDiff_ItemsToEmpty(t *testing.T) {
	old := []Item{{ID: "w-1", Title: "Fix bug", Status: "open"}}
	changes := Diff(old, nil)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != Removed {
		t.Errorf("expected Removed, got %s", changes[0].Kind)
	}
}

func TestDiff_StatusChanged(t *testing.T) {
	old := []Item{{ID: "w-1", Title: "Fix bug", Status: "open"}}
	cur := []Item{{ID: "w-1", Title: "Fix bug", Status: "claimed"}}
	changes := Diff(old, cur)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	c := changes[0]
	if c.Kind != StatusChanged {
		t.Errorf("expected StatusChanged, got %s", c.Kind)
	}
	if c.OldStatus != "open" {
		t.Errorf("expected old status open, got %s", c.OldStatus)
	}
	if c.Item.Status != "claimed" {
		t.Errorf("expected new status claimed, got %s", c.Item.Status)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	items := []Item{{ID: "w-1", Title: "Fix bug", Status: "open"}}
	changes := Diff(items, items)
	if len(changes) != 0 {
		t.Fatalf("expected 0 changes, got %d", len(changes))
	}
}

func TestDiff_MixedChanges(t *testing.T) {
	old := []Item{
		{ID: "w-1", Title: "Fix bug", Status: "open"},
		{ID: "w-2", Title: "Old item", Status: "open"},
	}
	cur := []Item{
		{ID: "w-1", Title: "Fix bug", Status: "claimed"},
		{ID: "w-3", Title: "New item", Status: "open"},
	}
	changes := Diff(old, cur)
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d", len(changes))
	}

	kinds := map[ChangeKind]int{}
	for _, c := range changes {
		kinds[c.Kind]++
	}
	if kinds[StatusChanged] != 1 {
		t.Errorf("expected 1 StatusChanged, got %d", kinds[StatusChanged])
	}
	if kinds[Added] != 1 {
		t.Errorf("expected 1 Added, got %d", kinds[Added])
	}
	if kinds[Removed] != 1 {
		t.Errorf("expected 1 Removed, got %d", kinds[Removed])
	}
}

func TestStateStore_LoadMissing(t *testing.T) {
	store := NewStateStore(filepath.Join(t.TempDir(), "missing.json"))
	items, err := store.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items != nil {
		t.Errorf("expected nil items, got %v", items)
	}
}

func TestStateStore_SaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := NewStateStore(path)

	items := []Item{
		{ID: "w-1", Title: "Fix bug", Status: "open"},
		{ID: "w-2", Title: "Add feature", Status: "claimed"},
	}
	if err := store.Save(items); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 items, got %d", len(loaded))
	}
	if loaded[0].ID != "w-1" || loaded[1].ID != "w-2" {
		t.Errorf("unexpected items: %v", loaded)
	}
}

func TestStateStore_SaveCreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "dir")
	path := filepath.Join(dir, "state.json")
	store := NewStateStore(path)

	if err := store.Save([]Item{{ID: "w-1", Title: "Test", Status: "open"}}); err != nil {
		t.Fatalf("save error: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("state file was not created")
	}
}

func TestPoll_FirstRun(t *testing.T) {
	lister := &fakeLister{items: []Item{
		{ID: "w-1", Title: "Fix bug", Status: "open"},
	}}
	notifier := &recordingNotifier{}
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))

	if err := Poll(lister, notifier, store); err != nil {
		t.Fatalf("poll error: %v", err)
	}

	if len(notifier.calls) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(notifier.calls))
	}
	if len(notifier.calls[0]) != 1 {
		t.Fatalf("expected 1 change, got %d", len(notifier.calls[0]))
	}
	if notifier.calls[0][0].Kind != Added {
		t.Errorf("expected Added, got %s", notifier.calls[0][0].Kind)
	}
}

func TestPoll_NoChanges(t *testing.T) {
	items := []Item{{ID: "w-1", Title: "Fix bug", Status: "open"}}
	lister := &fakeLister{items: items}
	notifier := &recordingNotifier{}
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))

	// First poll seeds state.
	if err := Poll(lister, notifier, store); err != nil {
		t.Fatalf("first poll error: %v", err)
	}

	// Second poll should detect no changes.
	notifier.calls = nil
	if err := Poll(lister, notifier, store); err != nil {
		t.Fatalf("second poll error: %v", err)
	}
	if len(notifier.calls) != 0 {
		t.Errorf("expected 0 notify calls on no-change, got %d", len(notifier.calls))
	}
}

func TestPoll_DetectsNewItem(t *testing.T) {
	store := NewStateStore(filepath.Join(t.TempDir(), "state.json"))

	// Seed with one item.
	lister := &fakeLister{items: []Item{{ID: "w-1", Title: "Fix bug", Status: "open"}}}
	notifier := &recordingNotifier{}
	if err := Poll(lister, notifier, store); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	// Add a second item.
	lister.items = append(lister.items, Item{ID: "w-2", Title: "New task", Status: "open"})
	notifier.calls = nil
	if err := Poll(lister, notifier, store); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if len(notifier.calls) != 1 || len(notifier.calls[0]) != 1 {
		t.Fatalf("expected 1 call with 1 change, got %v", notifier.calls)
	}
	if notifier.calls[0][0].Kind != Added {
		t.Errorf("expected Added, got %s", notifier.calls[0][0].Kind)
	}
}

func TestLogNotifier_Formats(t *testing.T) {
	var messages []string
	n := &LogNotifier{Printf: func(format string, args ...any) {
		messages = append(messages, fmt.Sprintf(format, args...))
	}}

	changes := []Change{
		{Kind: Added, Item: Item{ID: "w-1", Title: "Bug", Status: "open"}},
		{Kind: Removed, Item: Item{ID: "w-2", Title: "Old"}},
		{Kind: StatusChanged, Item: Item{ID: "w-3", Title: "Task", Status: "claimed"}, OldStatus: "open"},
	}
	if err := n.Notify(changes); err != nil {
		t.Fatalf("notify error: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
}
