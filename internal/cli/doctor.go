package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/doctor"
	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report where the receipts and the filesystem disagree",
		Long: "Check every receipt against the filesystem, every agent's skills directory against\n" +
			"the receipts, and the store against both.\n\n" +
			"Nothing is changed: each finding names the command that repairs it, so the\n" +
			"decision stays yours. Exits 4 when something is wrong, and 2 when an agent's\n" +
			"skills directory could not be read — an incomplete report cannot say all is well.\n\n" +
			"Every configured agent is scanned. There is no way to narrow it, because a health\n" +
			"check that skipped an agent would report a clean bill of health for a broken one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}

			// The state lock is held across the whole scan, so a concurrent
			// install cannot extract a revision between the receipt pass and
			// the store pass and be reported as an orphan.
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			ts, err := e.targets(nil)
			if err != nil {
				return err
			}

			rep, err := doctor.Scan(ts, h.DB, e.store, e.liveRoots(h.DB))
			if err != nil {
				return err
			}

			if err := reportDoctor(cmd, rep, asJSON); err != nil {
				return err
			}
			return doctorExit(rep)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

// reportDoctor writes the whole report to stdout. cmd.Print and friends resolve
// to stderr unless a writer was set, and the report is the command's product:
// `skillsctl doctor --json > health.json` has to capture it.
func reportDoctor(cmd *cobra.Command, rep doctor.Report, asJSON bool) error {
	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	for _, u := range rep.Unscanned {
		if _, err := fmt.Fprintf(out, "%s could not be scanned: %s\n", u.Path, u.Error); err != nil {
			return err
		}
	}

	for _, w := range rep.Warnings {
		if _, err := fmt.Fprintln(out, w); err != nil {
			return err
		}
	}

	if rep.IsEmpty() {
		_, err := fmt.Fprintln(out, "Nothing wrong.")
		return err
	}

	// One tabwriter per group, because the columns differ by what the finding
	// is about: a skill in an agent, or a directory in the store.
	for i, g := range rep.Groups() {
		if i > 0 || len(rep.Unscanned) > 0 || len(rep.Warnings) > 0 {
			_, _ = fmt.Fprintln(out)
		}
		_, _ = fmt.Fprintln(out, g.Title)

		w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		for _, f := range g.Findings {
			_, _ = fmt.Fprintf(w, "  %s\n", doctorRow(f))
		}
		if err := w.Flush(); err != nil {
			return err
		}
		// One command per skill, so a group covering two skills never prints a
		// repair that names the wrong one.
		for _, fix := range g.Remedies {
			if _, err := fmt.Fprintf(out, "  fix: %s\n", fix); err != nil {
				return err
			}
		}
	}
	// The count is the verdict rather than the report, so it goes out as the
	// note that carries the exit code, the way outdated does it. Printing it
	// here as well would say the same thing twice.
	return nil
}

// doctorRow picks the columns for one finding. They differ by what the finding
// is about — a skill in an agent, a skill in the store, or the store itself —
// and a shared column set would leave a dash in every row of some group.
func doctorRow(f doctor.Finding) string {
	switch {
	case f.Kind == doctor.KindOrphanRevision:
		return fmt.Sprintf("%s\t%s", f.Path, humanBytes(f.Bytes))
	case f.Target == "":
		return fmt.Sprintf("%s\t%s", f.Name, f.Detail)
	default:
		return fmt.Sprintf("%s\t%s\t%s", f.Name, f.Target, f.Detail)
	}
}

// doctorSummary counts the findings, and the skills they are spread over when
// any of them is about a skill at all.
func doctorSummary(rep doctor.Report) string {
	problems := count(len(rep.Findings), "problem")
	if n := rep.Skills(); n > 0 {
		return fmt.Sprintf("%s in %s", problems, count(n, "skill"))
	}
	return problems
}

// doctorExit turns the report into an exit code. An agent that could not be
// scanned outranks the findings for the reason outdated has the same rule: a
// report covering only part of what was asked cannot be trusted to say the rest
// is well.
func doctorExit(rep doctor.Report) error {
	switch {
	case len(rep.Unscanned) > 0 && !rep.IsEmpty():
		return partialf("%s could not be scanned; %s found",
			count(len(rep.Unscanned), "agent"), count(len(rep.Findings), "problem"))
	case len(rep.Unscanned) > 0:
		return partialf("%s could not be scanned", count(len(rep.Unscanned), "agent"))
	case !rep.IsEmpty():
		return unhealthyf("%s", doctorSummary(rep))
	}
	return nil
}
