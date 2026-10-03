package main

import (
	"io"
	"strings"
	"testing"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/gastownhall/wasteland/internal/federation"
	"github.com/gastownhall/wasteland/internal/sdk"
)

// Business logic tests for updating moved to internal/sdk/ (sdk_test.go, lifecycle_test.go).

func TestValidateUpdateInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		itemType string
		effort   string
		priority int
		wantErr  string
	}{
		{"all empty", "", "", -1, ""},
		{"valid type", "bug", "", -1, ""},
		{"invalid type", "bad", "", -1, "invalid type"},
		{"valid effort", "", "small", -1, ""},
		{"invalid effort", "", "huge", -1, "invalid effort"},
		{"valid priority 0", "", "", 0, ""},
		{"valid priority 4", "", "", 4, ""},
		{"invalid priority too high", "", "", 9, "invalid priority"},
		{"invalid priority negative", "", "", -5, "invalid priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateUpdateInputs(tt.itemType, tt.effort, tt.priority)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validateUpdateInputs() unexpected error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("validateUpdateInputs() expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
				}
			}
		})
	}
}

func TestUpdateCommand_ExplicitEmptyTextFields(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		args                 []string
		description, project bool
	}{
		{"description", []string{"--description="}, true, false},
		{"project", []string{"--project="}, false, true},
		{"both", []string{"--description=", "--project="}, true, true},
		{"omitted", []string{"--title=New title"}, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saveHandlerConfig(t)
			withResolveWantedArgOverride(t, func(_ *federation.Config, id string) (string, error) { return id, nil })
			called := false
			withCommandClientOverride(t, func(_ *federation.Config, _ bool) (commandClient, error) {
				return fakeCommandClient{updateFn: func(wantedID string, fields *commons.WantedUpdate) (*sdk.MutationResult, error) {
					called = true
					dml, err := commons.UpdateWantedDML(wantedID, fields)
					if err != nil {
						t.Fatalf("UpdateWantedDML() error = %v", err)
					}
					if strings.Contains(dml, "description=''") != tt.description || strings.Contains(dml, "project=''") != tt.project {
						t.Fatalf("unexpected clear assignments in %s", dml)
					}
					return &sdk.MutationResult{}, nil
				}}, nil
			})
			cmd := newUpdateCmd(io.Discard, io.Discard)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetArgs(append([]string{"w-1"}, tt.args...))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("update command error = %v", err)
			}
			if !called {
				t.Fatal("expected Update to be called")
			}
		})
	}
}

func TestHasUpdateFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fields *commons.WantedUpdate
		want   bool
	}{
		{"empty", &commons.WantedUpdate{Priority: -1}, false},
		{"title set", &commons.WantedUpdate{Title: "new", Priority: -1}, true},
		{"clear description", &commons.WantedUpdate{Priority: -1, DescriptionSet: true}, true},
		{"clear project", &commons.WantedUpdate{Priority: -1, ProjectSet: true}, true},
		{"priority set", &commons.WantedUpdate{Priority: 0}, true},
		{"tags set", &commons.WantedUpdate{Priority: -1, TagsSet: true}, true},
		{"effort set", &commons.WantedUpdate{Priority: -1, EffortLevel: "small"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hasUpdateFields(tt.fields)
			if got != tt.want {
				t.Errorf("hasUpdateFields() = %v, want %v", got, tt.want)
			}
		})
	}
}
