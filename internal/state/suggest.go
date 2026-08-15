package state

import (
	"fmt"
	"sort"
	"strings"
)

// maxSuggestions bounds what a "did you mean" line offers. Three is enough to
// cover a typo and a family of related names, and short enough that the line
// stays readable inside the frames commands print it in.
const maxSuggestions = 3

// NotInstalledError reports a name no receipt claims, carrying the installed
// names closest to it.
//
// It lives here rather than in cli because the receipts are the thing that know
// which names exist, and because internal/update raises the same error and
// cannot import cli. One type is what keeps every command saying it the same
// way.
type NotInstalledError struct {
	Name        string
	Suggestions []string
}

// Error renders the message on one line. Commands print it inside frames of
// their own — pin uses "skipped %s: %v" — so a second line would break them.
func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("%q is %s", e.Name, e.Hint())
}

// Hint is the message without the name, for a caller whose own frame already
// says which skill this is about. pin prints "skipped <name>: <hint>", and
// saying the name twice in one line reads as a mistake.
func (e *NotInstalledError) Hint() string {
	if len(e.Suggestions) == 0 {
		return "not installed; run `skillsctl list` to see what is"
	}
	return fmt.Sprintf("not installed; did you mean %s?", orList(e.Suggestions))
}

// NotInstalled builds the error for a name that is not among installed. It
// takes the receipts rather than a DB because update works from a slice it was
// handed and never sees the database.
func NotInstalled(name string, installed []*Receipt) error {
	return &NotInstalledError{Name: name, Suggestions: nearMisses(name, installed)}
}

// NotInstalled builds the error for a name this DB does not hold.
func (d *DB) NotInstalled(name string) error { return NotInstalled(name, d.List()) }

// NearMisses returns the installed names closest to name, at most three of
// them, sorted.
func (d *DB) NearMisses(name string) []string { return nearMisses(name, d.List()) }

// nearMisses matches case-insensitively, by containment in both directions, so
// a guess that is too short ("brain") and one that is too long
// ("web-research-tools") both find their skill.
//
// Edit distance was the alternative: it catches a transposition and misses a
// truncation, and truncation is the mistake people make at a shell that offers
// no completion for these names.
func nearMisses(name string, installed []*Receipt) []string {
	// strings.Contains(x, "") is true for every x, so without this an empty
	// name would suggest the whole store.
	if name == "" {
		return nil
	}
	lower := strings.ToLower(name)

	var out []string
	for _, r := range installed {
		got := strings.ToLower(r.Name)
		if strings.Contains(got, lower) || strings.Contains(lower, got) {
			out = append(out, r.Name)
		}
	}

	// Sorted before the cap, so which three are offered never depends on the
	// order the caller happened to hold its receipts in.
	sort.Strings(out)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// orList joins names the way the sentence reads: "a", "a or b", "a, b or c".
func orList(names []string) string {
	switch len(names) {
	case 1:
		return names[0]
	case 2:
		return names[0] + " or " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
}
