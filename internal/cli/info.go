package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/richardcase/skillsctl/internal/channel"
	"github.com/richardcase/skillsctl/internal/discover"
	"github.com/richardcase/skillsctl/internal/state"
	"github.com/richardcase/skillsctl/internal/target"
	"github.com/spf13/cobra"
)

// timeFormat is exact to the second and says which zone it is in. Receipts
// store UTC, and a report that dropped the suffix would read as local time.
const timeFormat = "2006-01-02 15:04:05 UTC"

// linkReport is one of a receipt's links, plus what is actually at that path.
// A receipt records that a symlink was created; only the disk can say whether
// it is still there and still points at the same directory.
type linkReport struct {
	Target string `json:"target"`
	Path   string `json:"path"`
	State  string `json:"state"`
	Dest   string `json:"dest"`
	Error  string `json:"error,omitempty"`

	// state is the same fact as State, typed, for the renderer to switch on.
	state target.LinkState
}

// infoReport is one receipt with everything derived from it. Both views render
// from this, so the text and the JSON cannot drift apart.
//
// The receipt is embedded, so every field it records keeps the spelling and the
// place list --json gives it and info --json is a strict superset. Links
// deliberately shadows the embedded Receipt.Links: encoding/json resolves a
// name collision by depth, so the richer entries win and the recorded ones are
// dropped.
type infoReport struct {
	*state.Receipt
	Description string       `json:"description,omitempty"`
	Agents      []string     `json:"agents"`
	Ownership   string       `json:"ownership,omitempty"`
	Links       []linkReport `json:"links"`

	// own is the same fact as Ownership, typed, for the renderer to switch on.
	// nil means the receipt's channel is not registered in this build.
	own *channel.Ownership
	// inStore reports whether the files are where a store-owned receipt says
	// they should be. adopt records a git skill that lives in the user's own
	// working copy, and calling that the store would be a lie the path on the
	// line above contradicts.
	inStore bool
}

func newInfoCmd() *cobra.Command {
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "info <name>",
		Short: "Show one installed skill's receipt in full",
		Long: "Print everything the receipt for a skill records — where it came from, which\n" +
			"revision is installed, where its files are, and every symlink it created —\n" +
			"together with the description from its SKILL.md.\n\n" +
			"Each link is checked against the disk, so a symlink that has been deleted,\n" +
			"broken or re-pointed is named as such. Nothing is fetched and nothing is\n" +
			"repaired: a broken link is a finding, and this command only reports.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			e, err := newEnv()
			if err != nil {
				return err
			}
			h, err := e.openState()
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()

			r, ok := h.DB.Receipts[name]
			if !ok {
				return h.DB.NotInstalled(name)
			}

			rep := describe(e, r)

			// Like list, the report is the command's product rather than a
			// progress message, so it is written to stdout by hand:
			// `info x --json > x.json` has to capture it.
			out := cmd.OutOrStdout()
			if asJSON {
				blob, err := json.MarshalIndent(rep, "", "  ")
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(out, string(blob))
				return err
			}
			return writeInfo(out, rep)
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the receipt and everything derived from it as JSON")
	return cmd
}

// describe gathers everything the report needs. Nothing here fails: a channel
// this build does not know, a SKILL.md that has gone, and a link that cannot be
// read are all facts about the install, and a command that reports facts should
// not refuse to describe one because another is missing.
func describe(e *env, r *state.Receipt) infoReport {
	rep := infoReport{Receipt: r, Agents: e.channels().Agents(r)}

	if ch, err := e.channels().ForReceipt(r); err == nil {
		own := ch.Ownership()
		rep.own = &own
		rep.Ownership = own.String()
	}
	rep.inStore = r.RevPath != "" && e.store.Contains(r.RevPath)

	if r.RevPath != "" {
		if s, err := discover.Root(r.RevPath); err == nil {
			rep.Description = s.Description
		}
	}

	rep.Links = make([]linkReport, 0, len(r.Links))
	for _, l := range r.Links {
		lr := linkReport{Target: l.Target, Path: l.Path}
		st, dest, err := target.Inspect(l.Path, r.RevPath)
		lr.state, lr.State, lr.Dest = st, st.String(), dest
		if err != nil {
			lr.Error = err.Error()
		}
		rep.Links = append(rep.Links, lr)
	}
	return rep
}

func writeInfo(out io.Writer, rep infoReport) error {
	if _, err := fmt.Fprintln(out, rep.Name); err != nil {
		return err
	}
	if rep.Description != "" {
		if _, err := fmt.Fprintln(out, rep.Description); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	field := func(label, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(w, "%s\t%s\n", label, value)
		}
	}

	field("channel", rep.Channel)
	field("source", rep.Source)
	field("subpath", rep.Subpath)
	for _, line := range revisionLines(rep) {
		field(line.label, line.value)
	}
	if rep.RevPath != "" {
		field("files", rep.RevPath)
		field("", owner(rep))
	}
	if !rep.InstalledAt.IsZero() {
		field("installed", rep.InstalledAt.UTC().Format(timeFormat))
	}
	if !rep.UpdatedAt.IsZero() {
		// Shown even when it equals the install time. "Never updated" and
		// "updated back to where it started" are different facts, and only the
		// two timestamps together tell them apart.
		field("updated", rep.UpdatedAt.UTC().Format(timeFormat))
	}

	// A plugin records no links, because the agent that installed it can
	// already see its skills, so its agents are named here instead.
	if len(rep.Links) == 0 && len(rep.Agents) > 0 {
		field("agents", strings.Join(rep.Agents, ","))
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if len(rep.Links) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out, "\nlinks"); err != nil {
		return err
	}
	lw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, l := range rep.Links {
		// The note is appended rather than given a column of its own, so a
		// report where every link is fine has no trailing padding on any line.
		line := fmt.Sprintf("  %s\t%s", l.Target, l.Path)
		if note := linkNote(l); note != "" {
			line += "  " + note
		}
		_, _ = fmt.Fprintln(lw, line)
	}
	return lw.Flush()
}

// labelled is one line of the field block.
type labelled struct{ label, value string }

// revisionLines renders what the skill is tracking and what it is on, which is
// a different question for each of the three ownerships — which is exactly what
// Ownership is for. A receipt whose channel this build does not know falls back
// to printing whatever it recorded.
func revisionLines(rep infoReport) []labelled {
	if rep.own == nil {
		return []labelled{{"ref", rep.Ref}, {"revision", rep.Resolved}}
	}

	switch *rep.own {
	case channel.StoreOwned:
		return []labelled{{"ref", refLine(rep)}, {"revision", revisionValue(rep)}}
	case channel.AgentOwned:
		// The agent chose the version and there is no ref behind it.
		return []labelled{{"version", rep.Resolved}}
	default:
		// The files are the user's own: whatever is in the directory right now
		// is the version, and there is nothing to name.
		return nil
	}
}

// refLine says what the skill follows. A pin is rendered first because pin
// clears Ref, and an empty Ref means the repository's default branch
// everywhere else in the tool — a reading that is wrong twice over for a pinned
// receipt, which tracks nothing and which update will not move.
func refLine(rep infoReport) string {
	if rep.Pinned {
		return "none — a pin tracks no ref"
	}
	return tracked(rep.Ref)
}

func revisionValue(rep infoReport) string {
	if rep.Resolved == "" {
		return ""
	}
	if rep.Pinned {
		return rep.Resolved + " (pinned)"
	}
	return rep.Resolved
}

// owner names who the files belong to, under the path they are at.
func owner(rep infoReport) string {
	if rep.own == nil {
		return ""
	}
	switch *rep.own {
	case channel.StoreOwned:
		if !rep.inStore {
			// What adopt records for a skill it found in a git working copy.
			return "(a working copy of your own, not skillsctl's store)"
		}
		return "(skillsctl's store)"
	case channel.AgentOwned:
		return "(installed by the agent, which owns it)"
	default:
		return "(your own directory, linked in place)"
	}
}

// linkNote is what to say about a link beside its path. A link that is exactly
// what the receipt claims needs no comment; anything else is the reason someone
// ran this command.
func linkNote(l linkReport) string {
	switch {
	case l.Error != "":
		return "(" + l.Error + ")"
	case l.state == target.LinkOK:
		return ""
	case l.state == target.LinkElsewhere:
		// The only state whose destination is news: everywhere else it is
		// either absent or the revision path already printed above.
		return fmt.Sprintf("(elsewhere: %s)", l.Dest)
	default:
		return "(" + l.State + ")"
	}
}
