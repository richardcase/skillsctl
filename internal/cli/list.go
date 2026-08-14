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
				agents := reg.Agents(r)
				version := shortSha(r.Resolved)
				if version == "" {
					// A local skill has no revision. An empty cell reads as a
					// broken table; a dash reads as "there isn't one".
					version = "-"
				}
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

// shortSha abbreviates a commit sha and leaves everything else alone. It is
// deliberately narrow: a plugin's Resolved is a version string, and truncating
// "2026.01.15" to "2026.01" would report a version that was never installed.
func shortSha(resolved string) string {
	if len(resolved) != shaLen || !isHex(resolved) {
		return resolved
	}
	return resolved[:7]
}

// shaLen is the length of the full hex sha git resolves to.
const shaLen = 40

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}
