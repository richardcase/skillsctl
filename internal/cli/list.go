package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			receipts := h.DB.List()

			if asJSON {
				blob, err := json.MarshalIndent(receipts, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(blob))
				return nil
			}

			if len(receipts) == 0 {
				cmd.Println("No skills installed.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tCHANNEL\tVERSION\tAGENTS")
			for _, r := range receipts {
				var agents []string
				for _, l := range r.Links {
					agents = append(agents, l.Target)
				}
				version := shortSha(r.Resolved)
				if r.Pinned {
					version += " (pinned)"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, r.Channel, version, strings.Join(agents, ","))
			}
			return w.Flush()
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the receipts as JSON")
	return cmd
}

func shortSha(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func targetNames(ts []target.Target) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}
