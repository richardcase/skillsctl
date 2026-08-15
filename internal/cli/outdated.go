package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/outdated"
	"github.com/spf13/cobra"
)

func newOutdatedCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "outdated",
		Short: "Report skills whose tracked ref has moved",
		Long: "Compare each installed skill against its remote, reading refs only — nothing is fetched.\n\n" +
			"Pinned skills are listed too, resolved against the repository's default branch, so a pin\n" +
			"never hides the fact that something moved. Exits 3 when an update is available,\n" +
			"and 2 when a remote could not be reached.",
		Args: cobra.NoArgs,
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
			if len(receipts) == 0 && !asJSON {
				cmd.Println("No skills installed.")
				return nil
			}

			entries := outdated.Check(cmd.Context(), gitx.New(), newPlugins(), receipts)

			if asJSON {
				blob, merr := json.MarshalIndent(entries, "", "  ")
				if merr != nil {
					return merr
				}
				cmd.Println(string(blob))
			} else if err := renderOutdated(cmd, entries); err != nil {
				return err
			}

			return outdatedExit(entries)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func renderOutdated(cmd *cobra.Command, entries []outdated.Entry) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tCHANNEL\tREF\tCURRENT\tLATEST\tSTATUS")
	for _, e := range entries {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.Name, e.Channel, dash(e.Ref), dash(shortSha(e.Current)), dash(shortSha(e.Latest)), outdatedStatus(e))
	}
	return w.Flush()
}

// outdatedStatus is the human-readable verdict, including why a row is not
// actionable: an error explains itself, and a pin is a deliberate choice.
func outdatedStatus(e outdated.Entry) string {
	switch {
	case e.Status == outdated.StatusError:
		// git's stderr runs to several lines; a row that spans lines would
		// break the alignment of the whole table.
		return fmt.Sprintf("error: %s", strings.Join(strings.Fields(e.Error), " "))
	case e.Pinned:
		return fmt.Sprintf("%s (pinned)", e.Status)
	default:
		return string(e.Status)
	}
}

// outdatedExit turns the report into an exit code. A skill that could not be
// read leaves the report covering only part of what was asked, which outranks
// the findings: an incomplete report cannot be trusted to say everything is
// current. Pinned skills never set a code on their own — update skips them, so
// nothing actionable follows and a deliberate pin would otherwise mean a
// permanently failing check.
//
// A stale plugin is deliberately not an update: nothing here knows whether a
// newer version exists, only that skillsctl's record of the installed one has
// fallen behind, which `skillsctl update` repairs.
func outdatedExit(entries []outdated.Entry) error {
	var unreadable, updates int
	for _, e := range entries {
		switch {
		case e.Status == outdated.StatusError:
			unreadable++
		case e.Status == outdated.StatusOutdated && !e.Pinned:
			updates++
		}
	}

	switch {
	case unreadable > 0 && updates > 0:
		return partialf("%s could not be checked; %s available",
			count(unreadable, "skill"), count(updates, "update"))
	case unreadable > 0:
		return partialf("%s could not be checked", count(unreadable, "skill"))
	case updates > 0:
		return outdatedf("%s available", count(updates, "update"))
	}
	return nil
}

// count renders "1 skill" or "3 skills".
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
