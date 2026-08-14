package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/store"
	"github.com/spf13/cobra"
)

func newGCCmd() *cobra.Command {
	var (
		dryRun bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Reclaim disk no installed skill references",
		Long: "Delete revision directories and bare mirrors that no receipt references.\n\n" +
			"Removing a skill unlinks it and forgets its receipt; the copy on disk stays\n" +
			"until gc collects it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			e, err := newEnv()
			if err != nil {
				return err
			}

			// The state lock is held across the scan and the deletion. An
			// install extracts its revision before committing the receipt
			// that makes it live, and this is what stops a collection from
			// landing in between.
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			found, err := e.store.Collect(liveRoots(h.DB))
			if err != nil {
				return err
			}

			freed, delErr := found, error(nil)
			if !dryRun {
				freed, delErr = e.store.Delete(found)
			}

			// Report before returning any deletion error, so a partial
			// collection still says what it managed to free.
			if err := reportGC(cmd, freed, dryRun, asJSON); err != nil {
				return err
			}
			if delErr == nil {
				return nil
			}
			if freed.IsEmpty() {
				return delErr
			}
			// Some of what was found is gone and some is not. Freeing part of
			// it is not the same as freeing none, and a script clearing space
			// has to be able to tell them apart.
			return partialf("freed %s, but the rest could not be removed: %v", humanBytes(freed.Bytes()), delErr)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be reclaimed without deleting it")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the report as JSON")
	return cmd
}

// liveRoots reduces the receipt set to the store's root set.
func liveRoots(db *state.DB) store.Live {
	receipts := db.List()
	live := store.Live{
		RevPaths: make([]string, 0, len(receipts)),
		Slugs:    make([]string, 0, len(receipts)),
	}
	for _, r := range receipts {
		live.RevPaths = append(live.RevPaths, r.RevPath)
		live.Slugs = append(live.Slugs, r.Slug)
	}
	return live
}

// reportGC writes the whole report to stdout. cmd.Print and friends resolve to
// stderr unless a writer was set, so redirecting `skillsctl gc` would otherwise
// split the listing from its summary across two streams.
func reportGC(cmd *cobra.Command, rep store.Report, dryRun, asJSON bool) error {
	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	if rep.MirrorsSkipped {
		_, _ = fmt.Fprintln(out, "a receipt records no repository, so no bare mirror could be proven unused: only revisions were considered")
	}

	if rep.IsEmpty() {
		_, err := fmt.Fprintln(out, "Nothing to reclaim.")
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, item := range rep.All() {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", item.Rel, humanBytes(item.Bytes))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	verb := "reclaimed"
	if dryRun {
		verb = "would reclaim"
	}
	_, err := fmt.Fprintf(out, "%s %s, %s\n", verb, gcSummary(rep), humanBytes(rep.Bytes()))
	return err
}

// gcSummary counts a report as "2 revisions and 1 mirror".
func gcSummary(rep store.Report) string {
	var parts []string
	if n := len(rep.Revisions); n > 0 {
		parts = append(parts, plural(n, "revision"))
	}
	if n := len(rep.Mirrors); n > 0 {
		parts = append(parts, plural(n, "mirror"))
	}
	return strings.Join(parts, " and ")
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for rest := n / unit; rest >= unit; rest /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
