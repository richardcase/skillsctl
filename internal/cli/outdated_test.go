package cli

import (
	"strings"
	"testing"

	"github.com/richardcase/skillsctl/internal/outdated"
)

// git's stderr is several lines ("fatal: …", "Please make sure …"), and a
// table row that spans lines destroys the alignment of every row after it.
func TestOutdatedStatusKeepsAnErrorOnOneLine(t *testing.T) {
	e := outdated.Entry{
		Status: outdated.StatusError,
		Error:  "git ls-remote file:///gone: exit status 128:\nfatal: Could not read from remote repository.\n\nPlease make sure you have the correct access rights",
	}

	got := outdatedStatus(e)

	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("status spans lines:\n%s", got)
	}
	if !strings.Contains(got, "fatal: Could not read from remote repository.") {
		t.Errorf("status lost the reason: %q", got)
	}
}

func TestOutdatedErrorIsAnError(t *testing.T) {
	var err error = &OutdatedError{Reason: "1 skill has an update available"}
	if err.Error() != "1 skill has an update available" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestOutdatedStatusMarksAPin(t *testing.T) {
	got := outdatedStatus(outdated.Entry{Status: outdated.StatusOutdated, Pinned: true})

	if got != "outdated (pinned)" {
		t.Errorf("status = %q, want %q", got, "outdated (pinned)")
	}
}
