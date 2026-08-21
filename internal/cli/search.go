package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/registry"
	"github.com/spf13/cobra"
)

func newSearchCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Find skills in the registry by name, description or tag",
		Long: "Search skillsctl's curated registry for skills matching query, printing a\n" +
			"source for each match that can be passed straight to `skillsctl install`.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearch(cmd, args[0], asJSON)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the matches as JSON")
	return cmd
}

func runSearch(cmd *cobra.Command, query string, asJSON bool) error {
	e, err := newEnv()
	if err != nil {
		return err
	}

	entries, err := e.registry().Fetch(cmd.Context())
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}
	matches := matchEntries(entries, query)

	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(matches, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	if len(matches) == 0 {
		_, err := fmt.Fprintf(out, "No skills found matching %q.\n", query)
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tSOURCE\tDESCRIPTION")
	for _, e := range matches {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", e.Name, e.Source, e.Description)
	}
	return w.Flush()
}

// matchEntries returns the entries whose name, description or any tag
// contains query, case-insensitively, preserving the registry's order.
func matchEntries(entries []registry.Entry, query string) []registry.Entry {
	q := strings.ToLower(query)
	var out []registry.Entry
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Name), q) ||
			strings.Contains(strings.ToLower(e.Description), q) ||
			matchesAnyTag(e.Tags, q) {
			out = append(out, e)
		}
	}
	return out
}

func matchesAnyTag(tags []string, q string) bool {
	for _, t := range tags {
		if strings.Contains(strings.ToLower(t), q) {
			return true
		}
	}
	return false
}
