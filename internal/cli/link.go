package cli

import (
	"fmt"

	"github.com/richardcase/skillsctl/internal/source"
	"github.com/spf13/cobra"
)

// newLinkCmd is install restricted to a local path. The two spellings exist
// because "install" describes what the user wants and "link" describes what
// actually happens: nothing is fetched, nothing is copied, and the skill stays
// where they are editing it.
func newLinkCmd() *cobra.Command {
	var o installOpts

	cmd := &cobra.Command{
		Use:   "link <path>",
		Short: "Link a skill you are working on, where it already is",
		Long: "Register a skill from a directory on this machine, linked in place.\n\n" +
			"Nothing is copied into the store, so edits to the directory are live in every\n" +
			"agent immediately — which is what makes this the way to develop a skill.\n" +
			"Removing it takes away the symlinks and nothing else.\n\n" +
			"`skillsctl install ./path` does exactly the same thing.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Parse(args[0])
			if err != nil {
				return err
			}
			if src.Channel != source.ChannelLocal {
				return fmt.Errorf("link takes a path to a directory on this machine, and %q is a %s source: install it with `skillsctl install %s`",
					args[0], src.Channel, args[0])
			}
			return runInstall(cmd, args[0], o)
		},
	}

	cmd.Flags().StringSliceVarP(&o.agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	cmd.Flags().StringArrayVar(&o.skills, "skill", nil, "skill to link, by name or path; repeat for several")
	cmd.Flags().BoolVar(&o.all, "all", false, "link every skill found in the directory")
	cmd.Flags().StringVar(&o.as, "as", "", "link under this name instead of the one in SKILL.md")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}
