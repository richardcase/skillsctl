package state

import (
	"errors"
	"strings"
	"testing"
)

func dbWith(names ...string) *DB {
	d := &DB{Version: SchemaVersion, Receipts: map[string]*Receipt{}}
	for _, n := range names {
		d.Receipts[n] = &Receipt{Name: n}
	}
	return d
}

func TestNearMissesIgnoreCaseAndMatchBothWays(t *testing.T) {
	db := dbWith("brainstorming", "avoid-ai-writing", "web-research")

	cases := []struct {
		name string
		ask  string
		want []string
	}{
		{"a truncated guess", "brain", []string{"brainstorming"}},
		{"the wrong case", "Brainstorming", []string{"brainstorming"}},
		{"an over-long guess", "web-research-tools", []string{"web-research"}},
		{"a middle fragment", "ai-writing", []string{"avoid-ai-writing"}},
		{"nothing like it", "zzz", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := db.NearMisses(tc.ask)
			if len(got) != len(tc.want) {
				t.Fatalf("NearMisses(%q) = %v, want %v", tc.ask, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("NearMisses(%q)[%d] = %q, want %q", tc.ask, i, got[i], tc.want[i])
				}
			}
		})
	}
}

// strings.Contains(x, "") is true for every x, so an empty name would suggest
// the entire store if the guard were dropped.
func TestNearMissesOnAnEmptyNameSuggestNothing(t *testing.T) {
	if got := dbWith("alpha", "beta").NearMisses(""); got != nil {
		t.Errorf("NearMisses(\"\") = %v, want nothing", got)
	}
}

func TestNearMissesAreCappedAtThree(t *testing.T) {
	db := dbWith("skill-a", "skill-b", "skill-c", "skill-d", "skill-e")
	got := db.NearMisses("skill")
	if len(got) != 3 {
		t.Fatalf("NearMisses returned %d names, want 3", len(got))
	}
	// DB.List sorts, so the cap takes the first three rather than a random three.
	for i, want := range []string{"skill-a", "skill-b", "skill-c"} {
		if got[i] != want {
			t.Errorf("suggestion %d = %q, want %q", i, got[i], want)
		}
	}
}

func TestNotInstalledNamesTheSuggestions(t *testing.T) {
	err := dbWith("brainstorming", "brainstorming-visual").NotInstalled("brainstorm")

	var nie *NotInstalledError
	if !errors.As(err, &nie) {
		t.Fatalf("NotInstalled returned %T, want *NotInstalledError", err)
	}
	if nie.Name != "brainstorm" {
		t.Errorf("Name = %q, want %q", nie.Name, "brainstorm")
	}

	msg := err.Error()
	for _, want := range []string{`"brainstorm" is not installed`, "did you mean", "brainstorming", "brainstorming-visual"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
	// pin renders these inside "skipped %s: %v", so a second line would break
	// that frame.
	if strings.Contains(msg, "\n") {
		t.Errorf("message spans more than one line: %q", msg)
	}
}

func TestNotInstalledWithNoMatchesNamesList(t *testing.T) {
	msg := dbWith("alpha").NotInstalled("zzz").Error()
	if strings.Contains(msg, "did you mean") {
		t.Errorf("message suggests something it should not: %q", msg)
	}
	if !strings.Contains(msg, "skillsctl list") {
		t.Errorf("message does not name the remedy: %q", msg)
	}
}
