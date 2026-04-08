// Package herald watches the wanted board and notifies on changes.
//
// Herald polls the wanted board via a Lister, diffs against persisted state,
// and dispatches Change events through a pluggable Notifier interface.
package herald

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Item is a snapshot of a wanted board entry for diffing purposes.
type Item struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// ChangeKind describes what changed between polls.
type ChangeKind string

// Change kind constants.
const (
	Added         ChangeKind = "added"
	Removed       ChangeKind = "removed"
	StatusChanged ChangeKind = "status_changed"
)

// Change describes a single wanted-board mutation detected by a poll.
type Change struct {
	Kind      ChangeKind `json:"kind"`
	Item      Item       `json:"item"`
	OldStatus string     `json:"old_status,omitempty"` // only for StatusChanged
}

// Lister fetches the current wanted board snapshot.
type Lister interface {
	ListItems() ([]Item, error)
}

// Notifier receives change notifications.
type Notifier interface {
	Notify(changes []Change) error
}

// StateStore persists the last-seen snapshot to disk as JSON.
type StateStore struct {
	path string
}

// NewStateStore creates a StateStore that reads/writes state at the given path.
func NewStateStore(path string) *StateStore {
	return &StateStore{path: path}
}

// DefaultStatePath returns the default state file path under XDG data dir.
func DefaultStatePath(dataDir string) string {
	return filepath.Join(dataDir, "herald-state.json")
}

// state is the on-disk format.
type state struct {
	Items     []Item    `json:"items"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Load reads persisted state. Returns nil items (not error) if file missing.
func (s *StateStore) Load() ([]Item, error) {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading herald state: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing herald state: %w", err)
	}
	return st.Items, nil
}

// Save persists the current snapshot.
func (s *StateStore) Save(items []Item) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("creating herald state dir: %w", err)
	}
	st := state{Items: items, UpdatedAt: time.Now().UTC()}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling herald state: %w", err)
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Diff computes changes between old and new snapshots.
func Diff(old, cur []Item) []Change {
	oldMap := make(map[string]Item, len(old))
	for _, item := range old {
		oldMap[item.ID] = item
	}
	curMap := make(map[string]Item, len(cur))
	for _, item := range cur {
		curMap[item.ID] = item
	}

	var changes []Change

	// Detect added and status-changed items.
	for _, item := range cur {
		prev, existed := oldMap[item.ID]
		if !existed {
			changes = append(changes, Change{Kind: Added, Item: item})
		} else if prev.Status != item.Status {
			changes = append(changes, Change{
				Kind:      StatusChanged,
				Item:      item,
				OldStatus: prev.Status,
			})
		}
	}

	// Detect removed items.
	for _, item := range old {
		if _, exists := curMap[item.ID]; !exists {
			changes = append(changes, Change{Kind: Removed, Item: item})
		}
	}

	return changes
}

// Poll performs a single poll cycle: list → diff → notify → save.
func Poll(lister Lister, notifier Notifier, store *StateStore) error {
	current, err := lister.ListItems()
	if err != nil {
		return fmt.Errorf("listing wanted board: %w", err)
	}

	previous, err := store.Load()
	if err != nil {
		return fmt.Errorf("loading state: %w", err)
	}

	changes := Diff(previous, current)

	if len(changes) > 0 {
		if err := notifier.Notify(changes); err != nil {
			return fmt.Errorf("notifying: %w", err)
		}
	}

	if err := store.Save(current); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	return nil
}

// LogNotifier writes changes to an io.Writer (e.g. os.Stdout).
type LogNotifier struct {
	Printf func(format string, args ...any)
}

// Notify prints each change.
func (n *LogNotifier) Notify(changes []Change) error {
	for _, c := range changes {
		switch c.Kind {
		case Added:
			n.Printf("[+] %s: %s (%s)\n", c.Item.ID, c.Item.Title, c.Item.Status)
		case Removed:
			n.Printf("[-] %s: %s\n", c.Item.ID, c.Item.Title)
		case StatusChanged:
			n.Printf("[~] %s: %s (%s → %s)\n", c.Item.ID, c.Item.Title, c.OldStatus, c.Item.Status)
		}
	}
	return nil
}
