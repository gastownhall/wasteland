package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/julianknutsen/wasteland/internal/sdk"
	"github.com/julianknutsen/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newPostCmd(stdout, stderr io.Writer) *cobra.Command {
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
		Use:   "post",
		Short: "Post a new wanted item to the commons",
		Long: `Post a new wanted item to the Wasteland commons (shared wanted board).

Creates a wanted item with a unique w-<hash> ID and inserts it into the
fork clone of the commons database. In wild-west mode the commit is
auto-pushed to upstream (canonical) and origin (fork).

Use --no-push to skip pushing (offline work).

Examples:
  wl post --title "Fix auth bug" --project gastown --type bug
  wl post --title "Add federation sync" --type feature --priority 1 --effort large
  wl post --title "Update docs" --tags "docs,federation" --effort small
  wl post --title "Offline item" --no-push`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPost(cmd, stdout, stderr, title, description, project, itemType, priority, effort, tags, noPush)
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "Title of the wanted item (required)")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Detailed description")
	cmd.Flags().StringVar(&project, "project", "", "Project name (e.g., gastown, beads)")
	typeHelp := "Item type: feature, bug, design, rfc, docs"
	if inferGateEnabled() {
		typeHelp += ", inference"
	}
	cmd.Flags().StringVar(&itemType, "type", "", typeHelp)
	cmd.Flags().IntVar(&priority, "priority", 2, "Priority: 0=critical, 1=high, 2=medium, 3=low, 4=backlog")
	cmd.Flags().StringVar(&effort, "effort", "medium", "Effort level: trivial, small, medium, large, epic")
	cmd.Flags().StringVar(&tags, "tags", "", "Comma-separated tags (e.g., 'go,auth,federation')")
	cmd.Flags().BoolVar(&noPush, "no-push", false, "Skip pushing to remotes (offline work)")

	_ = cmd.MarkFlagRequired("title")
	_ = cmd.RegisterFlagCompletionFunc("project", completeProjectNames)
	_ = cmd.RegisterFlagCompletionFunc("type", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		types := []string{"feature", "bug", "design", "rfc", "docs"}
		if inferGateEnabled() {
			types = append(types, "inference")
		}
		return types, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("effort", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"trivial", "small", "medium", "large", "epic"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

func runPost(cmd *cobra.Command, stdout, _ io.Writer, title, description, project, itemType string, priority int, effort, tags string, noPush bool) error {
	var tagList []string
	if tags != "" {
		for _, t := range strings.Split(tags, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				tagList = append(tagList, t)
			}
		}
	}

	if err := validatePostInputs(itemType, effort, priority); err != nil {
		return err
	}

	wlCfg, err := resolveWasteland(cmd)
	if err != nil {
		return hintWrap(err)
	}

	client, err := newSDKClient(wlCfg, noPush)
	if err != nil {
		return err
	}

	result, err := client.Post(sdk.PostInput{
		Title:       title,
		Description: description,
		Project:     project,
		Type:        itemType,
		Priority:    priority,
		EffortLevel: effort,
		Tags:        tagList,
	})
	if err != nil {
		return err
	}

	itemID := ""
	if result.Detail != nil && result.Detail.Item != nil {
		itemID = result.Detail.Item.ID
	}

	fmt.Fprintf(stdout, "%s Posted wanted item: %s\n", style.Bold.Render("✓"), style.Bold.Render(itemID))
	fmt.Fprintf(stdout, "  Title:    %s\n", title)
	if project != "" {
		fmt.Fprintf(stdout, "  Project:  %s\n", project)
	}
	if itemType != "" {
		fmt.Fprintf(stdout, "  Type:     %s\n", itemType)
	}
	fmt.Fprintf(stdout, "  Priority: %d\n", priority)
	fmt.Fprintf(stdout, "  Effort:   %s\n", effort)
	if len(tagList) > 0 {
		fmt.Fprintf(stdout, "  Tags:     %s\n", strings.Join(tagList, ", "))
	}
	fmt.Fprintf(stdout, "  Posted by: %s\n", wlCfg.RigHandle)
	if result.Branch != "" {
		fmt.Fprintf(stdout, "  Branch:   %s\n", result.Branch)
	}
	if result.Detail != nil && result.Detail.PRURL != "" {
		fmt.Fprintf(stdout, "  PR: %s\n", result.Detail.PRURL)
	}
	if result.Hint != "" {
		fmt.Fprintf(stdout, "\n  %s\n", style.Dim.Render(result.Hint))
	}

	printNextHint(stdout, "Next: others can claim this. Browse: wl browse")

	return nil
}

// validatePostInputs validates the type, effort, and priority fields.
func validatePostInputs(itemType, effort string, priority int) error {
	validTypes := map[string]bool{
		"feature": true, "bug": true, "design": true, "rfc": true, "docs": true, "inference": true,
	}
	if itemType != "" && !validTypes[itemType] {
		return fmt.Errorf("invalid type %q: must be one of feature, bug, design, rfc, docs, inference", itemType)
	}

	validEfforts := map[string]bool{
		"trivial": true, "small": true, "medium": true, "large": true, "epic": true,
	}
	if !validEfforts[effort] {
		return fmt.Errorf("invalid effort %q: must be one of trivial, small, medium, large, epic", effort)
	}

	if priority < 0 || priority > 4 {
		return fmt.Errorf("invalid priority %d: must be 0-4", priority)
	}

	return nil
}
