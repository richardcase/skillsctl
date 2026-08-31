package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/gitx"
	"github.com/richardcase/skillsctl/internal/outdated"
	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/spf13/cobra"
)

func newBrowseCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "browse",
		Short: "Pick installed skills to update or remove",
		Long: "List installed skills with their outdated status, tick the ones to act on,\n" +
			"then choose update or remove for the whole batch.\n\n" +
			"There is no non-interactive form: run `skillsctl update` or\n" +
			"`skillsctl remove <name>` directly when nobody is there to answer the picker.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runBrowse(cmd, dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what the chosen action would change without changing it")
	return cmd
}

func runBrowse(cmd *cobra.Command, dryRun bool) error {
	e, err := newEnv()
	if err != nil {
		return err
	}
	h, err := e.openState(cmd.Context())
	if err != nil {
		return err
	}

	receipts := h.DB.List()
	if len(receipts) == 0 {
		_ = h.Close()
		cmd.Println("No skills installed.")
		return nil
	}

	p := newPicker()
	if !p.Interactive() {
		_ = h.Close()
		return fmt.Errorf("browse has nobody to ask: run `skillsctl update` or `skillsctl remove <name>` directly")
	}

	entries := outdated.Check(cmd.Context(), gitx.New(), newPlugins(), newOCI(), receipts)

	names, err := selectBrowseTargets(p, receipts, entries)
	if err != nil {
		_ = h.Close()
		return err
	}

	action, err := selectBrowseAction(p)
	if err != nil {
		_ = h.Close()
		return err
	}

	// runUpdate and runRemove each open their own state handle, and Open
	// blocks on an exclusive flock held per open file description — a second
	// Open in this process would wait on the first forever, so this one is
	// released before either runs.
	if err := h.Close(); err != nil {
		return err
	}

	if action == browseActionUpdate {
		return runUpdate(cmd, names, false, dryRun)
	}
	return runBrowseRemove(cmd, names, dryRun)
}

const (
	browseActionUpdate = "update"
	browseActionRemove = "remove"
)

// selectBrowseTargets asks which installed skills to act on.
func selectBrowseTargets(p picker, receipts []*state.Receipt, entries []outdated.Entry) ([]string, error) {
	items := make([]prompt.Item, len(receipts))
	for i, r := range receipts {
		items[i] = prompt.Item{Label: browseRow(r, entries[i])}
	}

	chosen, err := p.Select(prompt.Options{
		Header: []string{"installed skills:"},
		Items:  items,
		Single: false,
		Help:   "↑/↓ move · space toggle · a all · enter continue · q cancel",
	})
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(chosen))
	for _, i := range chosen {
		names = append(names, receipts[i].Name)
	}
	return names, nil
}

// browseRow renders one skill's line in the picker: name, channel, version
// and its outdated status, the same facts `list` and `outdated` show
// separately.
func browseRow(r *state.Receipt, e outdated.Entry) string {
	version := shortSha(r.Resolved)
	if version == "" {
		version = "-"
	}
	status := string(e.Status)
	if r.Pinned {
		status += " (pinned)"
	}
	return fmt.Sprintf("%-24s %-8s %-10s %s", r.Name, r.Channel, version, status)
}

// selectBrowseAction asks what to do with the skills just chosen.
func selectBrowseAction(p picker) (string, error) {
	chosen, err := p.Select(prompt.Options{
		Items: []prompt.Item{
			{Label: "Update selected"},
			{Label: "Remove selected"},
		},
		Single: true,
		Help:   "↑/↓ move · enter choose · q cancel",
	})
	if err != nil {
		return "", err
	}
	if chosen[0] == 0 {
		return browseActionUpdate, nil
	}
	return browseActionRemove, nil
}

// runBrowseRemove removes each chosen name in turn through runRemove, the
// same path `skillsctl remove` takes, and reports the batch the way a
// multi-skill install already does: some succeeding and some failing is a
// partial result, not a failure of the whole command.
func runBrowseRemove(cmd *cobra.Command, names []string, dryRun bool) error {
	var failed int
	for _, name := range names {
		if err := runRemove(cmd, name, nil, dryRun); err != nil {
			cmd.Printf("skipped %s: %v\n", name, err)
			failed++
		}
	}

	switch {
	case failed == 0:
		return nil
	case failed < len(names):
		return partialf("%s removed, %s could not be", count(len(names)-failed, "skill"), count(failed, "skill"))
	default:
		return fmt.Errorf("nothing was removed: %s failed, for the reasons above", count(failed, "skill"))
	}
}
