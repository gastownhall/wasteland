package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/gastownhall/wasteland/internal/pile"
	"github.com/gastownhall/wasteland/internal/style"
	"github.com/spf13/cobra"
)

func newProfileCmd(stdout, stderr io.Writer) *cobra.Command {
	var search string

	cmd := &cobra.Command{
		Use:   "profile [handle]",
		Short: "Look up a developer profile from the-pile",
		Long: `Look up a developer's character sheet from hop/the-pile.

Shows identity, skills (languages, domains, capabilities), notable projects,
and value dimensions assembled from boot blocks and GitHub assessments.

EXAMPLES:
  wl profile torvalds            # Show Torvalds' profile
  wl profile --search steve      # Search for handles matching "steve"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if search != "" {
				return runProfileSearch(cmd, stdout, stderr, search)
			}
			if len(args) == 0 {
				return fmt.Errorf("provide a handle or use --search")
			}
			return runProfile(cmd, stdout, stderr, args[0])
		},
	}

	cmd.Flags().StringVar(&search, "search", "", "Search for profiles matching this term")
	return cmd
}

func runProfile(_ *cobra.Command, stdout, _ io.Writer, handle string) error {
	client := pile.NewDefault()

	sp := style.StartSpinner(stdout, "Fetching profile...")
	profile, err := pile.QueryProfile(client, handle)
	sp.Stop()
	if err != nil {
		return err
	}
	if profile == nil {
		return fmt.Errorf("profile not found for %q", handle)
	}

	// Header
	name := profile.DisplayName
	if name == "" {
		name = profile.Handle
	}
	fmt.Fprintf(stdout, "\n%s  @%s\n", style.Bold.Render(name), profile.Handle)
	if profile.Bio != "" {
		fmt.Fprintf(stdout, "%s\n", profile.Bio)
	}
	if profile.Location != "" {
		fmt.Fprintf(stdout, "Location: %s\n", profile.Location)
	}
	fmt.Fprintf(stdout, "Source: %s  Confidence: %.1f%%\n", profile.Source, profile.Confidence*100)
	if profile.Followers > 0 || profile.AccountAge > 0 {
		fmt.Fprintf(stdout, "Followers: %d  Account age: %.1f years\n", profile.Followers, profile.AccountAge)
	}
	fmt.Fprintln(stdout)

	// Value dimensions
	fmt.Fprintln(stdout, style.Bold.Render("Value Dimensions"))
	printBar(stdout, "Quality    ", profile.Quality)
	printBar(stdout, "Reliability", profile.Reliability)
	printBar(stdout, "Creativity ", profile.Creativity)
	fmt.Fprintln(stdout)

	// Languages
	if len(profile.Languages) > 0 {
		fmt.Fprintf(stdout, "%s (%d)\n", style.Bold.Render("Languages"), len(profile.Languages))
		tbl := style.NewTable(
			style.Column{Name: "LANGUAGE", Width: 16},
			style.Column{Name: "Q", Width: 3, Align: style.AlignRight},
			style.Column{Name: "R", Width: 3, Align: style.AlignRight},
			style.Column{Name: "C", Width: 3, Align: style.AlignRight},
			style.Column{Name: "EVIDENCE", Width: 50},
		)
		for _, l := range profile.Languages {
			tbl.AddRow(l.Name,
				fmt.Sprintf("%d", l.Quality),
				fmt.Sprintf("%d", l.Reliability),
				fmt.Sprintf("%d", l.Creativity),
				truncateField(l.Message, 50))
		}
		fmt.Fprint(stdout, tbl.Render())
		fmt.Fprintln(stdout)
	}

	// Domains
	if len(profile.Domains) > 0 {
		fmt.Fprintf(stdout, "%s (%d)\n", style.Bold.Render("Domains"), len(profile.Domains))
		tbl := style.NewTable(
			style.Column{Name: "DOMAIN", Width: 24},
			style.Column{Name: "Q", Width: 3, Align: style.AlignRight},
			style.Column{Name: "R", Width: 3, Align: style.AlignRight},
			style.Column{Name: "C", Width: 3, Align: style.AlignRight},
			style.Column{Name: "EVIDENCE", Width: 42},
		)
		for _, d := range profile.Domains {
			tbl.AddRow(d.Name,
				fmt.Sprintf("%d", d.Quality),
				fmt.Sprintf("%d", d.Reliability),
				fmt.Sprintf("%d", d.Creativity),
				truncateField(d.Message, 42))
		}
		fmt.Fprint(stdout, tbl.Render())
		fmt.Fprintln(stdout)
	}

	// Capabilities
	if len(profile.Capabilities) > 0 {
		fmt.Fprintf(stdout, "%s (%d)\n", style.Bold.Render("Capabilities"), len(profile.Capabilities))
		tbl := style.NewTable(
			style.Column{Name: "CAPABILITY", Width: 24},
			style.Column{Name: "Q", Width: 3, Align: style.AlignRight},
			style.Column{Name: "R", Width: 3, Align: style.AlignRight},
			style.Column{Name: "C", Width: 3, Align: style.AlignRight},
			style.Column{Name: "EVIDENCE", Width: 42},
		)
		for _, c := range profile.Capabilities {
			tbl.AddRow(c.Name,
				fmt.Sprintf("%d", c.Quality),
				fmt.Sprintf("%d", c.Reliability),
				fmt.Sprintf("%d", c.Creativity),
				truncateField(c.Message, 42))
		}
		fmt.Fprint(stdout, tbl.Render())
		fmt.Fprintln(stdout)
	}

	// Notable projects
	if len(profile.NotableProjects) > 0 {
		fmt.Fprintf(stdout, "%s (%d)\n", style.Bold.Render("Notable Projects"), len(profile.NotableProjects))
		tbl := style.NewTable(
			style.Column{Name: "PROJECT", Width: 20},
			style.Column{Name: "STARS", Width: 8, Align: style.AlignRight},
			style.Column{Name: "TIER", Width: 6},
			style.Column{Name: "ROLE", Width: 12},
			style.Column{Name: "LANGUAGES", Width: 30},
		)
		for _, np := range profile.NotableProjects {
			tbl.AddRow(np.Name,
				fmt.Sprintf("%d", np.Stars),
				np.ImpactTier,
				np.Role,
				strings.Join(np.Languages, ", "))
		}
		fmt.Fprint(stdout, tbl.Render())
		fmt.Fprintln(stdout)
	}

	// Stats footer
	fmt.Fprintf(stdout, "Assessments: %d  Total stars: %d  Repos: %d\n",
		profile.AssessmentCount, profile.TotalStars, profile.TotalRepos)

	return nil
}

func runProfileSearch(_ *cobra.Command, stdout, _ io.Writer, query string) error {
	client := pile.NewDefault()

	sp := style.StartSpinner(stdout, "Searching profiles...")
	results, err := pile.SearchProfiles(client, query, 20)
	sp.Stop()
	if err != nil {
		return err
	}

	if len(results) == 0 {
		fmt.Fprintln(stdout, "No profiles found.")
		return nil
	}

	tbl := style.NewTable(
		style.Column{Name: "HANDLE", Width: 24},
		style.Column{Name: "NAME", Width: 40},
	)
	for _, r := range results {
		tbl.AddRow(r.Handle, r.DisplayName)
	}
	fmt.Fprintf(stdout, "Found %d profiles:\n\n", len(results))
	fmt.Fprint(stdout, tbl.Render())

	return nil
}

func printBar(w io.Writer, label string, value float64) {
	value = clamp01(value / 5) // value is on 0-5 stamp scale; normalize to 0-1
	pct := int(value * 100)
	barLen := int(value * 20)
	bar := strings.Repeat("\u2588", barLen) + strings.Repeat("\u2591", 20-barLen)
	fmt.Fprintf(w, "  %s  %s  %d%%\n", label, bar, pct)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func truncateField(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
