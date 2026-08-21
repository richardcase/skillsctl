package cli

import (
	"encoding/json"
	"fmt"

	"github.com/richardcase/skillsctl/internal/lint"
	"github.com/spf13/cobra"
)

func newLintCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "lint <path>",
		Short: "Validate a skill's SKILL.md before publishing it",
		Long: "Check a skill's SKILL.md the way install would read it, but strictly: a\n" +
			"missing or empty name or description is an error here, where install and\n" +
			"discover otherwise tolerate it. A name that would not match its directory is\n" +
			"reported as a warning.\n\n" +
			"path can be a single skill directory, or a directory holding several skills\n" +
			"— every skill found under it is linted. Nothing is changed: this is a check\n" +
			"for the skill's author, not the installer.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			findings, err := lint.Path(args[0])
			if err != nil {
				return err
			}

			if err := reportLint(cmd, findings, asJSON); err != nil {
				return err
			}
			return lintExit(findings)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the findings as JSON")
	return cmd
}

// reportLint writes every finding to stdout, grouped by the skill it is
// about — the same shape doctor uses for its own findings.
func reportLint(cmd *cobra.Command, findings []lint.Finding, asJSON bool) error {
	out := cmd.OutOrStdout()

	if asJSON {
		blob, err := json.MarshalIndent(findings, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(blob))
		return err
	}

	if len(findings) == 0 {
		_, err := fmt.Fprintln(out, "Nothing wrong.")
		return err
	}

	var last string
	for _, f := range findings {
		if f.Skill != last {
			if last != "" {
				if _, err := fmt.Fprintln(out); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(out, f.Skill); err != nil {
				return err
			}
			last = f.Skill
		}
		if _, err := fmt.Fprintf(out, "  %s: %s\n", f.Severity, f.Message); err != nil {
			return err
		}
	}
	return nil
}

// lintExit turns the findings into an exit code. A warning alone still exits
// clean — it is advice, not a reason to fail a publish check — the same way a
// missing cosign is a warning doctor reports without failing.
func lintExit(findings []lint.Finding) error {
	errs := 0
	for _, f := range findings {
		if f.Severity == lint.Error {
			errs++
		}
	}
	if errs == 0 {
		return nil
	}
	return unhealthyf("%s found", count(errs, "problem"))
}
