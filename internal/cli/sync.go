package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/source"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	var dryRun bool
	var ref string

	cmd := &cobra.Command{
		Use:   "sync <file-or-source>",
		Short: "Install the skills a skills.toml names",
		Long: "Read a manifest and add what this machine is missing: skills it does not have,\n" +
			"and links into the agents an entry names.\n\n" +
			"<file-or-source> is a local path if one exists there, and otherwise a git\n" +
			"source (owner/repo, a git URL, or scp-form) whose repository root holds\n" +
			"skills.toml — the same shapes install already accepts for a skill, minus\n" +
			"plugin and OCI sources, which name no file to read. --ref chooses that\n" +
			"repository's branch, tag or sha and is ignored for a local file.\n\n" +
			"sync only ever adds. It never re-points a ref, never moves a pin and never\n" +
			"removes a skill, so running it twice changes nothing the second time. A\n" +
			"difference between the manifest and an install is reported rather than\n" +
			"resolved, and so is a skill installed here that the manifest never mentions.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}

			var f manifest.File
			if _, statErr := os.Stat(args[0]); statErr == nil {
				blob, rerr := os.ReadFile(args[0])
				if rerr != nil {
					return fmt.Errorf("read %s: %w", args[0], rerr)
				}
				f, err = manifest.Decode(blob)
				if err != nil {
					return fmt.Errorf("%s: %w", args[0], err)
				}
			} else {
				f, err = manifest.FetchRemote(cmd.Context(), args[0], ref, gitx.New(), e.store)
				if err != nil {
					return err
				}
			}

			// The state lock is taken before anything is written to the store
			// and held until this command exits, so a concurrent gc can never
			// collect a revision this sync is about to link.
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			// Built once and reused for settleSynced below, so a git repository
			// resolved while planning is not resolved a second time settling.
			reg := e.channels()
			rep, p := manifest.Plan(cmd.Context(), reg, f, h.DB, e.cfg)

			if dryRun {
				for _, line := range p.Describe() {
					cmd.Println(line)
				}
				reportSync(cmd, rep, true)
				return syncExit(rep)
			}

			var serr error
			if !p.IsEmpty() {
				ex := &plan.Executor{DB: h.DB, Out: cmd.OutOrStdout(), Run: newRunner()}
				if err := ex.Apply(cmd.Context(), p); err != nil {
					// A mixed plan can fail asymmetrically: if one entry is a
					// plugin (a plan.Exec that already ran `claude plugin
					// install`) and a later entry's link fails, rollback undoes
					// the symlinks but cannot undo the Exec, and h.Commit below
					// never runs — so the plugin ends up installed with no
					// receipt. This self-heals rather than needing handling
					// here: the next sync's Plugin.Prepare finds it already
					// installed, adopts it, and records it without running the
					// command again.
					return err
				}

				// A channel whose agent chooses the version can only be asked
				// once it has run, so the receipts are completed before they are
				// committed rather than after.
				rep, serr = settleSynced(cmd.Context(), ex, reg, h.DB, rep)

				if err := h.Commit(); err != nil {
					return fmt.Errorf("%w\nthe skills were linked but the receipts were not saved; re-run this command to repair", err)
				}
			}

			reportSync(cmd, rep, false)
			if serr != nil {
				cmd.Printf("warning: %v\n", serr)
			}
			if err := syncExit(rep); err != nil {
				return err
			}
			if serr != nil {
				return partialf("the sync ran, but a version could not be read back")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	cmd.Flags().StringVar(&ref, "ref", "", "branch, tag or sha the profile repository tracks (default: its HEAD); ignored for a local file")
	return cmd
}

// settleSynced completes the receipts sync just wrote, for the channels that
// cannot know a version until their agent has run, and folds the result back
// into the report so it says what was actually recorded rather than what was
// planned.
//
// It groups by channel for the reason update does: a channel is asked once, and
// answers for everything it owns. reg is the same registry manifest.Plan ran
// against, passed in rather than rebuilt, so that a git repository resolved
// while planning is not resolved a second time settling.
func settleSynced(ctx context.Context, ex *plan.Executor, reg channel.Registry, db *state.DB, rep manifest.Report) (manifest.Report, error) {
	grouped := map[string][]state.Receipt{}
	var order []string

	for _, v := range rep.Verdicts {
		if v.Status != manifest.StatusInstalled {
			continue
		}
		r, ok := db.Receipts[v.Name]
		if !ok {
			continue
		}
		if _, seen := grouped[r.Channel]; !seen {
			order = append(order, r.Channel)
		}
		grouped[r.Channel] = append(grouped[r.Channel], *r)
	}

	var settled []state.Receipt
	var firstErr error
	for _, name := range order {
		ch, err := reg.For(source.Channel(name))
		if err != nil {
			continue
		}
		got, err := settle(ctx, ex, ch, grouped[name])
		if err != nil && firstErr == nil {
			firstErr = err
		}
		settled = append(settled, got...)
	}

	// Fold the settled versions back into the verdicts, so the report says what
	// was recorded rather than what was planned. A channel whose agent chooses
	// the version cannot answer until it has run.
	byName := make(map[string]state.Receipt, len(settled))
	for _, r := range settled {
		byName[r.Name] = r
	}
	for i, v := range rep.Verdicts {
		if v.Status != manifest.StatusInstalled {
			continue
		}
		if r, ok := byName[v.Name]; ok {
			rep.Verdicts[i].Version = r.Resolved
		}
	}
	return rep, firstErr
}

// reportSync writes one line per entry that is not simply already satisfied,
// then the skills the manifest never mentioned. A run where everything was
// already in place still says so, rather than printing nothing and leaving the
// user wondering whether it ran.
func reportSync(cmd *cobra.Command, rep manifest.Report, dryRun bool) {
	var interesting int
	for _, v := range rep.Verdicts {
		if line := syncLine(v, dryRun); line != "" {
			interesting++
			cmd.Println(line)
		}
		// A channel's own skips are reported beside the entry rather than
		// folded into it: one link a name collision cost is not the entry
		// failing, and the reason names the skill that took the name.
		for _, s := range v.Skipped {
			interesting++
			cmd.Println(s)
		}
	}
	if interesting == 0 {
		cmd.Println("Everything the manifest names is already installed.")
	}

	// An extra is reported after the entries and never changes the exit code:
	// the manifest is not a statement about what must not be installed.
	for _, r := range rep.Extra {
		cmd.Printf("not in the manifest: %s (installed from %s)\n", r.Name, r.Source)
	}
}

// syncLine renders one verdict, or "" for an entry with nothing to say.
func syncLine(v manifest.Verdict, dryRun bool) string {
	where := strings.Join(v.Agents, ", ")

	switch v.Status {
	case manifest.StatusInstalled:
		verb := "installed"
		if dryRun {
			verb = "would install"
		}
		// A local skill has no revision at all, ever. A plugin's is unknown at
		// this point only in the dry-run branch; by the time this line renders
		// for a real run, settleSynced has already read it back. Either way, "@"
		// with nothing after it reads as something missing rather than
		// something absent.
		if v.Version == "" {
			return fmt.Sprintf("%s %s into %s", verb, v.Name, where)
		}
		return fmt.Sprintf("%s %s @ %s into %s", verb, v.Name, shortSha(v.Version), where)

	case manifest.StatusLinked:
		verb := "linked"
		if dryRun {
			verb = "would link"
		}
		return fmt.Sprintf("%s %s into %s", verb, v.Name, where)

	case manifest.StatusDiffers:
		// The remedy is named inline because there is no verb that retargets a
		// ref: pin and unpin move a pin, and nothing moves a ref on its own.
		return fmt.Sprintf("%s differs: %s; remove it and run sync again, or bring the manifest in line",
			v.Name, v.Detail)

	case manifest.StatusError:
		return fmt.Sprintf("skipped %s: %s", v.Name, v.Detail)

	default:
		return ""
	}
}

// syncExit turns the report into an exit code: 0 when every entry is
// satisfied, including a run that did nothing; 2 when some entries applied,
// or merely differ, alongside others that did not; 1 only when nothing
// applied and something actually failed.
//
// A difference is a partial result on its own, even as the only verdict with
// nothing applied: the skill under that name is installed, just not as the
// manifest describes it, which is work done, not work refused. Only
// StatusError — a source that could not be resolved, a skill that could not
// be fetched — counts against the run's having accomplished something, so
// exit 1 needs both no application and an actual failure.
func syncExit(rep manifest.Report) error {
	var applied, failed, skipped int
	for _, v := range rep.Verdicts {
		switch v.Status {
		case manifest.StatusInstalled, manifest.StatusLinked:
			applied++
		case manifest.StatusDiffers:
			skipped++
		case manifest.StatusError:
			failed++
			skipped++
		}
	}

	switch {
	case skipped == 0:
		return nil
	case applied == 0 && failed > 0:
		return fmt.Errorf("nothing was applied: %d of %d entries could not be, for the reasons above",
			skipped, len(rep.Verdicts))
	default:
		return partialf("%d of %d entries applied, for the reasons above", applied, len(rep.Verdicts))
	}
}
