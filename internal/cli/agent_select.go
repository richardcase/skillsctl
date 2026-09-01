package cli

import (
	"strings"

	"github.com/richardcase/skillsctl/internal/prompt"
	"github.com/richardcase/skillsctl/internal/target"
)

// preTicked are the agents a bare invocation ticks by default, when they are
// already present — the same two skillsctl has always shipped built-in
// support for the longest, kept as the default rather than every agent so
// that the common case is still one keystroke.
var preTicked = map[string]bool{"claude": true, "codex": true}

// selectAgents asks which configured agents to act on, offering every entry
// in cfg.Targets rather than only the present ones: Link creates an agent's
// skills directory on demand, so picking one that isn't there yet still
// works, and hiding it would be the one time this table disagreed with
// itself about what counts as a configured agent.
func selectAgents(p picker, cfg target.Config) ([]target.Target, error) {
	present := make(map[string]bool, len(cfg.Targets))
	for _, t := range cfg.Present() {
		present[t.Name] = true
	}

	width := 0
	for _, t := range cfg.Targets {
		width = max(width, len(t.Name))
	}

	items := make([]prompt.Item, len(cfg.Targets))
	for i, t := range cfg.Targets {
		label := t.Name + strings.Repeat(" ", width-len(t.Name)) + "  " + t.Dir
		items[i] = prompt.Item{Label: label, Selected: preTicked[t.Name] && present[t.Name]}
	}

	chosen, err := p.Select(prompt.Options{
		Header: []string{"agents to install into:"},
		Items:  items,
		Help:   "↑/↓ move · space toggle · a all · enter confirm · q cancel",
	})
	if err != nil {
		return nil, err
	}

	out := make([]target.Target, 0, len(chosen))
	for _, i := range chosen {
		out = append(out, cfg.Targets[i])
	}
	return out, nil
}
