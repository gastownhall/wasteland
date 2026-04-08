package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
)

// csvDB returns a fixed CSV response for Query calls.
type csvDB struct {
	noopDB
	csv string
}

func (d *csvDB) Query(string, string) (string, error) { return d.csv, nil }

func TestRunHerald_OneShotFirstRun(t *testing.T) {
	saveWasteland(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	db := &csvDB{csv: "id,title,project,type,priority,posted_by,claimed_by,status,effort_level\nw-1,Fix bug,,task,1,alice,,open,medium\n"}
	oldDBFromConfig := openDBFromConfig
	openDBFromConfig = func(*federation.Config) (commons.DB, error) { return db, nil }
	t.Cleanup(func() { openDBFromConfig = oldDBFromConfig })

	var stdout, stderr bytes.Buffer
	cmd := wastelandCmd()
	err := runHerald(cmd, &stdout, &stderr, false, 0)
	if err != nil {
		t.Fatalf("runHerald() error: %v", err)
	}

	got := stdout.String()
	if !strings.Contains(got, "[+]") {
		t.Errorf("expected [+] for new item, got %q", got)
	}
	if !strings.Contains(got, "w-1") {
		t.Errorf("expected item ID w-1, got %q", got)
	}
}

func TestRunHerald_OneShotNoChanges(t *testing.T) {
	saveWasteland(t)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	db := &csvDB{csv: "id,title,project,type,priority,posted_by,claimed_by,status,effort_level\nw-1,Fix bug,,task,1,alice,,open,medium\n"}
	oldDBFromConfig := openDBFromConfig
	openDBFromConfig = func(*federation.Config) (commons.DB, error) { return db, nil }
	t.Cleanup(func() { openDBFromConfig = oldDBFromConfig })

	var stdout, stderr bytes.Buffer
	cmd := wastelandCmd()

	// First run seeds state.
	if err := runHerald(cmd, &stdout, &stderr, false, 0); err != nil {
		t.Fatalf("first poll: %v", err)
	}

	// Second run should produce no output.
	stdout.Reset()
	if err := runHerald(cmd, &stdout, &stderr, false, 0); err != nil {
		t.Fatalf("second poll: %v", err)
	}
	if stdout.String() != "" {
		t.Errorf("expected no output on second poll, got %q", stdout.String())
	}
}

func TestRunHerald_NotJoined(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	cmd := wastelandCmd()
	err := runHerald(cmd, &stdout, &stderr, false, 0)
	if err == nil {
		t.Fatal("expected error when not joined")
	}
}
