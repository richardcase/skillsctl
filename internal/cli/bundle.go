package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/spf13/cobra"
)

func newBundleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bundle",
		Short: "Write the installed skills as a portable skills.toml",
		Long: "Project the current receipts into the skills.toml manifest format and write it\n" +
			"to stdout, so that `skillsctl bundle > skills.toml` on one machine and\n" +
			"`skillsctl sync skills.toml` on another install the same set.\n\n" +
			"A local skill is left out and named on stderr: its source is a path on this\n" +
			"machine, which means nothing on another.",
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

			// bundle wants the present set itself, not a resolution that can
			// fail: it has receipts on disk and is perfectly able to describe
			// them even when no agent directory exists yet on this machine.
			f, excluded := manifest.FromReceipts(h.DB.List(), e.channels(), e.cfg.Present())

			// cmd.Print and friends resolve to stderr unless a writer was set,
			// so the manifest is written to stdout by hand. It is the command's
			// product: `bundle > skills.toml` has to capture it, and only it.
			if err := manifest.Encode(cmd.OutOrStdout(), f); err != nil {
				return err
			}

			if len(excluded) > 0 {
				cmd.PrintErrf("warning: %s left out of the manifest: %s\n",
					count(len(excluded), "local skill"), strings.Join(excluded, ", "))
			}
			return nil
		},
	}
}
