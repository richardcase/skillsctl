package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/richardcase/skillsctl/internal/plan"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

// newSkillTemplate is the SKILL.md a scaffolded skill starts with. Only name
// and description are read by discover.Frontmatter, so those are the only
// fields it needs to satisfy discovery. Both are double-quoted YAML scalars,
// since a description containing ": " — the TODO placeholder's own default —
// would otherwise read as a second mapping key rather than plain text.
const newSkillTemplate = `---
name: %s
description: %s
---

# %s

TODO: write the skill.
`

// yamlQuote renders s as a double-quoted YAML scalar.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func newNewCmd() *cobra.Command {
	var (
		agents      []string
		description string
		dryRun      bool
	)

	cmd := &cobra.Command{
		Use:   "new <name>",
		Short: "Scaffold a skill you are about to write, and link it in place",
		Long: "Create ./<name>/SKILL.md with the frontmatter a skill needs, then link it\n" +
			"into every agent found — the same thing `skillsctl link ./<name>` does to a\n" +
			"directory you had already written by hand.\n\n" +
			"Refuses to overwrite a directory that already exists.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNew(cmd, args[0], description, installOpts{agents: agents, dryRun: dryRun})
		},
	}

	cmd.Flags().StringSliceVarP(&agents, "agent", "a", nil, "agents to link into (default: every agent found)")
	cmd.Flags().StringVar(&description, "description", "", "the skill's description in SKILL.md (default: a TODO placeholder)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing it")
	return cmd
}

func runNew(cmd *cobra.Command, name, description string, o installOpts) error {
	if err := target.ValidateSkillName(name); err != nil {
		return fmt.Errorf("refusing to scaffold: %w", err)
	}
	if _, err := os.Stat(name); err == nil {
		return fmt.Errorf("%s already exists: choose a different name, or run `skillsctl link ./%s` on it directly", name, name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", name, err)
	}

	if description == "" {
		description = "TODO: describe what this skill does"
	}

	body := fmt.Sprintf(newSkillTemplate, yamlQuote(name), yamlQuote(description), name)
	var p plan.Plan
	p.Add(
		plan.Mkdir{Path: name},
		plan.WriteFile{Path: filepath.Join(name, "SKILL.md"), Content: []byte(body)},
	)

	if o.dryRun {
		e, err := newEnv()
		if err != nil {
			return err
		}
		if _, err := e.targets(o.agents); err != nil {
			return err
		}
		p.Add(plan.Note{Text: fmt.Sprintf("run: skillsctl link ./%s%s", name, agentSuffix(o.agents))})
		for _, line := range p.Describe() {
			cmd.Println(line)
		}
		return nil
	}

	ex := &plan.Executor{Out: cmd.OutOrStdout()}
	if err := ex.Apply(cmd.Context(), p); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists: choose a different name, or run `skillsctl link ./%s` on it directly", name, name)
		}
		return err
	}

	// The scaffold now legitimately exists on disk: a failure from here on is
	// runInstall's alone to report. Rollback stays local to the step that
	// failed rather than reaching back to delete an already-successful step —
	// exactly what left dangling links behind before this fix, when a late
	// install/commit failure still triggered a RemoveAll of the scaffold.
	return runInstall(cmd, "./"+name, o)
}

// agentSuffix renders the -a flag a dry run's suggested link command would
// need, or "" when every agent found is the answer install would give anyway.
func agentSuffix(agents []string) string {
	if len(agents) == 0 {
		return ""
	}
	return " -a " + strings.Join(agents, ",")
}
