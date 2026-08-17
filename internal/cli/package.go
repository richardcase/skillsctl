package cli

import (
	"bytes"
	"fmt"

	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/pack"
	"github.com/spf13/cobra"
)

type packageOpts struct {
	dryRun bool
}

func newPackageCmd() *cobra.Command {
	var o packageOpts

	cmd := &cobra.Command{
		Use:   "package <source-dir> <oci-ref>",
		Short: "Package a directory of skills into an OCI artifact and push it",
		Long: "Package walks <source-dir>, packages every skill it finds (each SKILL.md\n" +
			"and everything beside and below it) into one OCI artifact, and pushes it to\n" +
			"<oci-ref> (registry/repository:tag). It excludes any .git directory and\n" +
			"anything a .gitignore in the tree marks ignored. Authentication reuses\n" +
			"Docker's own config and credential helpers.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPackage(cmd, args[0], args[1], o)
		},
	}

	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "show what would be packaged without pushing")
	return cmd
}

func runPackage(cmd *cobra.Command, dir, ref string, o packageOpts) error {
	found, err := discover.Walk(dir)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return fmt.Errorf("%s: %w", dir, discover.ErrNoSkill)
	}

	for _, s := range found {
		cmd.Printf("package %s\n", s.Rel)
	}

	if o.dryRun {
		cmd.Printf("would push %s\n", ref)
		return nil
	}

	var buf bytes.Buffer
	if err := pack.Tar(&buf, dir); err != nil {
		return fmt.Errorf("build skills layer: %w", err)
	}

	if err := newOCI().Push(cmd.Context(), ref, &buf); err != nil {
		return err
	}
	cmd.Printf("pushed %s\n", ref)
	return nil
}
