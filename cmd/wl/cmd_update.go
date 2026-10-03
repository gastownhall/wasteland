package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/wasteland/internal/commons"
	"github.com/spf13/cobra"
)

func newUpdateCmd(stdout, stderr io.Writer) *cobra.Command {
	var (
		title       string
		description string
		project     string
		itemType    string
		priority    int
		effort      string
		tags        string
		noPush      bool
	)

	cmd := &cobra.Command{
		Use:   "update <wanted-id>",
		Short: "Update fields on an open wanted item",
		Long: `Update mutable fields on an open wanted item.

Only items with status 'open' can be updated — once claimed, the contract is locked.

At least one field must be provided. In wild-west mode any joined rig can update.

In wild-west mode the commit is auto-pushed to upstream and origin.
Use --no-push to skip pushing (offline work).

Examples:
  wl update w-abc123 --title "New title"
  wl update w-abc123 --priority 1 --effort large
  wl update w-abc123 --type bug --tags "go,auth"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, stdout, stderr, args[0], title, description, project, itemType, priority, effort, tags, noPush)
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "New title")
	cmd.Flags().StringVarP(&description, "description", "d", "", "New description (empty string clears)")
	cmd.Flags().StringVar(&project, "project", "", "New project (empty string clears)")
	cmd.Flags().StringVar(&itemType, "type", "", "Item type: feature, bug, design, rfc, docs")
	cmd.Flags().IntVar(&priority, "priority", -1, "Priority: 0=critical, 1=high, 2=medium, 3=low, 4=backlog")
	cmd.Flags().StringVar(&effort, "effort", "", "Effort level: trivial, small, medium, large, epic")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags (replaces existing)")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "Skip pushing to remotes (offline work)")
	_ = cmd.RegisterFlagCompletionFunc("project", completeProjectNames)
	cmd.ValidArgsFunction = completeWantedIDs("open")

	return cmd
}

func runUpdate(cmd *cobra.Command, stdout, _ io.Writer, wantedID, title, description, project, itemType string, priority int, effort, tags string, noPush bool) error {
	// Validate before building the update struct.
	if err := validateUpdateInputs(itemType, effort, priority); err != nil {
		return err
	}

	fields := &commons.WantedUpdate{
		Title:          title,
		Description:    description,
		DescriptionSet: cmd.Flags().Changed("description"),
		Project:        project,
		ProjectSet:     cmd.Flags().Changed("project"),
		Type:           itemType,
		Priority:       priority,
		EffortLevel:    effort,
	}

	if tags != "" {
		var tagList []string
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tagList = append(tagList, t)
			}
		}
		fields.Tags = tagList
		fields.TagsSet = true
	}

	if !hasUpdateFields(fields) {
		return fmt.Errorf("at least one field must be provided to update")
	}

	wlCfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	wantedID, err = resolveWantedArg(wlCfg, wantedID)
	if err != nil {
		return err
	}

	client, err := newCommandClient(wlCfg, noPush)
	if err != nil {
		return err
	}

	result, err := client.Update(wantedID, fields)
	if err != nil {
		return err
	}

	renderMutationResult(stdout, "Updated", wantedID, result)
	printNextHint(stdout, "Next: wl browse to see the board")

	return nil
}

// hasUpdateFields returns true if at least one field is set.
func hasUpdateFields(f *commons.WantedUpdate) bool {
	return f.Title != "" || f.Description != "" || f.DescriptionSet || f.Project != "" || f.ProjectSet ||
		f.Type != "" || f.Priority >= 0 || f.EffortLevel != "" || f.TagsSet
}

// validateUpdateInputs validates type, effort, and priority if provided.
func validateUpdateInputs(itemType, effort string, priority int) error {
	validTypes := map[string]bool{
		"feature": true, "bug": true, "design": true, "rfc": true, "docs": true,
	}
	if itemType != "" && !validTypes[itemType] {
		return fmt.Errorf("invalid type %q: must be one of feature, bug, design, rfc, docs", itemType)
	}

	validEfforts := map[string]bool{
		"trivial": true, "small": true, "medium": true, "large": true, "epic": true,
	}
	if effort != "" && !validEfforts[effort] {
		return fmt.Errorf("invalid effort %q: must be one of trivial, small, medium, large, epic", effort)
	}

	if priority != -1 && (priority < 0 || priority > 4) {
		return fmt.Errorf("invalid priority %d: must be 0-4", priority)
	}

	return nil
}
