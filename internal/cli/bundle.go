package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/manifest"
	"github.com/spf13/cobra"
)

func newBundleCmd() *cobra.Command {
	var tags []string

	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Write the installed skills as a portable skills.toml",
		Long: "Project the current receipts into the skills.toml manifest format and write it\n" +
			"to stdout, so that `skillsctl bundle > skills.toml` on one machine and\n" +
			"`skillsctl sync skills.toml` on another install the same set.\n\n" +
			"--tag keeps only receipts carrying at least one of the given tags, for\n" +
			"writing a scoped manifest out of a larger set.\n\n" +
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

			receipts := filterByTags(h.DB.List(), tags)
			f, excluded := manifest.FromReceipts(receipts, e.channels(), e.cfg.Present())

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

	cmd.Flags().StringSliceVar(&tags, "tag", nil, "only bundle skills carrying any of these tags (repeatable)")
	return cmd
}
