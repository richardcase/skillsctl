package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/richardcase/skillsctl/internal/adopt"
	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/spf13/cobra"
)

func newAdoptCmd() *cobra.Command {
	var (
		agents []string
		dryRun bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "adopt",
		Short: "Take over skills already in an agent's skills directory",
		Long: "Record the skills already sitting in each agent's skills directory, so list,\n" +
			"outdated, update and remove know about them.\n\n" +
			"A symlink is adopted where it points, and removing it later takes away the\n" +
			"symlink and nothing else. One that leads into a clean git checkout is recorded\n" +
			"with the sha it is at, pinned, so update never re-points a checkout you are\n" +
			"working in without being asked.\n\n" +
			"Nothing is moved, copied or deleted. A real directory in a skills directory has\n" +
			"no symlink to record, so adopt reports it rather than taking it over.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAdopt(cmd, agents, dryRun, asJSON)
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "agents to scan (default: every agent found)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be adopted without recording it")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

func runAdopt(cmd *cobra.Command, agents []string, dryRun, asJSON bool) error {
	ctx := cmd.Context()

	e, err := newEnv()
	if err != nil {
		return err
	}
	targets, err := e.targets(agents)
	if err != nil {
		return err
	}

	// Held until the command exits, for the same reason install holds it: what
	// the scan decided about a name must still be true when the receipt for it
	// is committed.
	h, err := e.openState()
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	rep, err := adopt.Scan(ctx, targets, h.DB, gitx.New(), e.store)
	if err != nil {
		return err
	}

	adoptions, additions := rep.Adoptions(), rep.Additions()
	p, err := adoptPlan(e.store, h.DB, adoptions, additions)
	if err != nil {
		return err
	}

	if !dryRun && !p.IsEmpty() {
		ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
		if err := ex.Apply(ctx, p); err != nil {
			return err
		}
		if err := h.Commit(); err != nil {
			return fmt.Errorf("%w\nnothing on disk was touched, so re-running adopt is safe", err)
		}
	}

	if err := reportAdopt(cmd, rep, adoptions, additions, p, dryRun, asJSON); err != nil {
		return err
	}
	return adoptErr(rep, adoptions, additions)
}

// adoptPlan turns each adoption into the one op it needs, and each addition
// into an amended copy of the receipt it belongs to.
//
// There is no Link op and there never can be: every symlink involved is already
// on disk and already points where its receipt says. That is what makes it
// impossible for adopt to plan something destructive, rather than merely
// unlikely to — an addition is held to the same standard as an adoption.
func adoptPlan(st *store.Store, db *state.DB, adoptions []adopt.Adoption, additions []adopt.Addition) (plan.Plan, error) {
	var p plan.Plan
	now := time.Now().UTC()

	git := channel.NewGit(st, gitx.New())
	local := channel.NewLocal(st)

	for _, a := range adoptions {
		var r state.Receipt
		switch a.Class {
		case adopt.ClassGit:
			r = git.AdoptReceipt(a.Name, a.Repo.Repo, a.Repo.SHA, a.Repo.Subpath, a.Dest, a.Links, now)
		case adopt.ClassLocal:
			r = local.AdoptReceipt(a.Name, a.Dest, a.Links, now)
		default:
			return plan.Plan{}, fmt.Errorf("adopt %s: nothing adopts a %q entry", a.Name, a.Class)
		}
		p.Add(plan.Record{Receipt: r})
	}

	for _, a := range additions {
		existing, ok := db.Receipts[a.Name]
		if !ok {
			return plan.Plan{}, fmt.Errorf("adopt %s: a link was classified against a receipt that is not there", a.Name)
		}

		// Links shares its backing array with the receipt in the DB, and the
		// executor is what decides whether any of this is written.
		updated := *existing
		updated.Links = make([]state.Link, 0, len(existing.Links)+len(a.Links))
		updated.Links = append(append(updated.Links, existing.Links...), a.Links...)
		updated.UpdatedAt = now
		p.Add(plan.Record{Receipt: updated})
	}
	return p, nil
}

// reportAdopt writes the whole report to stdout, since cmd.Print resolves to
// stderr and `adopt --json` has to be capturable.
func reportAdopt(cmd *cobra.Command, rep adopt.Report, adoptions []adopt.Adoption, additions []adopt.Addition, p plan.Plan, dryRun, asJSON bool) error {
	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	// A dry run shows the plan itself rather than a summary of it. Reading the
	// ops is how the spec's safety step is checked: every line is a record, and
	// there is nothing else there to be.
	if dryRun {
		for _, line := range p.Describe() {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	} else {
		if len(adoptions) > 0 {
			w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "NAME\tCHANNEL\tVERSION\tAGENTS\tSOURCE")
			for _, a := range adoptions {
				version, src := "-", a.Dest
				if a.Repo != nil {
					version = shortSha(a.Repo.SHA) + " (pinned)"
					src = a.Repo.Repo.RepoURL
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Class, version, linkAgents(a), src)
			}
			if err := w.Flush(); err != nil {
				return err
			}
		}
		// An addition amends a receipt the table has already described, so it
		// reports as the one thing that changed: where the skill now reaches.
		for _, a := range additions {
			if _, err := fmt.Fprintf(out, "linked %s into %s\n", a.Name, additionAgents(a)); err != nil {
				return err
			}
		}
	}

	for _, e := range rep.Skipped() {
		if _, err := fmt.Fprintf(out, "skipped %s [%s]: %s\n", e.Name, e.Target, e.Reason); err != nil {
			return err
		}
	}

	_, err := fmt.Fprintln(out, adoptSummary(rep, adoptions, additions, dryRun))
	return err
}

func adoptSummary(rep adopt.Report, adoptions []adopt.Adoption, additions []adopt.Addition, dryRun bool) string {
	verb := "adopted"
	if dryRun {
		verb = "would adopt"
	}

	summary := fmt.Sprintf("%s %s", verb, plural(len(adoptions), "skill"))
	if n := len(additions); n > 0 {
		summary += fmt.Sprintf(", linked %s into another agent", plural(n, "skill"))
	}
	if n := rep.Managed(); n > 0 {
		summary += fmt.Sprintf(", %d already managed", n)
	}
	if n := len(rep.Skipped()); n > 0 {
		summary += fmt.Sprintf(", %s skipped", entries(n))
	}
	return summary
}

func linkAgents(a adopt.Adoption) string {
	names := make([]string, 0, len(a.Links))
	for _, l := range a.Links {
		names = append(names, l.Target)
	}
	return strings.Join(names, ",")
}

func additionAgents(a adopt.Addition) string {
	names := make([]string, 0, len(a.Links))
	for _, l := range a.Links {
		names = append(names, l.Target)
	}
	return strings.Join(names, ",")
}

func entries(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

// adoptErr sets the exit code once the report has been printed: the reasons are
// already on screen, so this only has to say how much of the job was done.
func adoptErr(rep adopt.Report, adoptions []adopt.Adoption, additions []adopt.Addition) error {
	skipped := len(rep.Skipped())
	if skipped == 0 {
		return nil
	}
	// A link added to an existing receipt is work done, the same as a receipt
	// written: something on this machine is managed now that was not before.
	if len(adoptions)+len(additions) == 0 {
		return fmt.Errorf("nothing could be adopted: %s skipped, for the reasons above", entries(skipped))
	}
	return partialf("%s skipped", entries(skipped))
}
