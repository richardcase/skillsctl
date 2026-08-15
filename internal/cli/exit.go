package cli

import "fmt"

// Exit codes. A command that did everything asked of it exits ExitOK; one that
// did nothing exits ExitError. ExitPartial is for the case in between, which a
// single failure code cannot express: --all over a repository where some names
// are already taken installs the rest, and a script has to be able to tell that
// from having installed nothing at all.
//
// ExitOutdated is not about how much work was done but about what was found:
// outdated did its whole job and the answer is that something has moved. It
// needs its own code so that a CI check can tell "you are behind" from "I could
// only check some of them".
//
// ExitUnhealthy is the same shape of answer from doctor, and separate from
// ExitOutdated for the same reason ExitOutdated is separate from ExitPartial:
// being behind and being broken call for different responses, and a check that
// collapsed them would have to read the output to tell which it got.
const (
	ExitOK        = 0
	ExitError     = 1
	ExitPartial   = 2
	ExitOutdated  = 3
	ExitUnhealthy = 4
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

// OutdatedError reports a finding rather than a failure: the command ran to
// completion and something it looked at has moved. Like PartialError it is an
// error so that it travels back through RunE and sets the exit code, and the
// root command renders it as a note.
type OutdatedError struct {
	Reason string
}

// Error implements error.
func (e *OutdatedError) Error() string { return e.Reason }

// outdatedf builds an OutdatedError from a format string.
func outdatedf(format string, args ...any) error {
	return &OutdatedError{Reason: fmt.Sprintf(format, args...)}
}

// UnhealthyError reports that doctor scanned everything it was asked to and
// found something wrong with it. Like the other two it is an error so that it
// travels back through RunE and sets the exit code, and it is rendered as a
// note: the command did its job, and what it found is the answer.
type UnhealthyError struct {
	Reason string
}

// Error implements error.
func (e *UnhealthyError) Error() string { return e.Reason }

// unhealthyf builds an UnhealthyError from a format string.
func unhealthyf(format string, args ...any) error {
	return &UnhealthyError{Reason: fmt.Sprintf(format, args...)}
}
