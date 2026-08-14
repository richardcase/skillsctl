package cli

import "fmt"

// Exit codes. A command that did everything asked of it exits ExitOK; one that
// did nothing exits ExitError. ExitPartial is for the case in between, which a
// single failure code cannot express: --all over a repository where some names
// are already taken installs the rest, and a script has to be able to tell that
// from having installed nothing at all.
const (
	ExitOK      = 0
	ExitError   = 1
	ExitPartial = 2
)

// PartialError reports work that was carried out, minus some part that was
// already done or could not be attempted. It is an error so that it travels
// back through RunE and sets the exit code, but it is not a failure, so the
// root command renders it as a note rather than an error.
type PartialError struct {
	Reason string
}

// Error implements error.
func (e *PartialError) Error() string { return e.Reason }

// partialf builds a PartialError from a format string.
func partialf(format string, args ...any) error {
	return &PartialError{Reason: fmt.Sprintf(format, args...)}
}
