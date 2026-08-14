package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

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

			// cmd.Print and friends resolve to stderr unless a writer was
			// set, so everything list produces is written to stdout by hand.
			// The listing is the command's product: `list --json > skills.json`
			// has to capture it.
			out := cmd.OutOrStdout()

			if asJSON {
				blob, err := json.MarshalIndent(receipts, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, string(blob))
				return err
			}

			if len(receipts) == 0 {
				_, err := fmt.Fprintln(out, "No skills installed.")
				return err
			}

			reg := e.channels()

			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tCHANNEL\tVERSION\tAGENTS")
			for _, r := range receipts {
				agents := reg.Agents(r, e.cfg)
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
