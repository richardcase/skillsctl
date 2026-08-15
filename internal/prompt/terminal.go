package prompt

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ANSI sequences. The picker deliberately does not use the alternate screen:
// it draws where the command's own output goes, so what the user chose stays
// in the scrollback beside the result of choosing it.
const (
	cursorUp     = "\x1b[%dA"
	clearLine    = "\r\x1b[2K"
	hideCursor   = "\x1b[?25l"
	unhideCursor = "\x1b[?25h"
)

// Terminal is a picker backed by a real terminal.
//
// Out is where the block is drawn, and it is the stream whose size and
// terminal-ness decide the layout — which is why a caller passes stderr rather
// than stdout: `skillsctl install repo > log` should still be able to ask.
type Terminal struct {
	In  *os.File
	Out *os.File
}

// Interactive reports whether there is a user on the other end to ask.
//
// Both ends have to be a terminal: reading keystrokes needs stdin to be one,
// and redrawing a block in place needs the output to be one too, or the escape
// sequences end up in whatever the output was redirected to.
func (t Terminal) Interactive() bool {
	return t.In != nil && t.Out != nil &&
		term.IsTerminal(int(t.In.Fd())) && term.IsTerminal(int(t.Out.Fd()))
}

// Select draws the list, reads keystrokes until the user confirms or cancels,
// erases the block and returns the chosen indices. ErrCancelled means the user
// backed out.
//
// The terminal is restored on every path out, including a panic: raw mode
// leaves a shell unusable, so the restore is deferred before anything can
// fail.
func (t Terminal) Select(opts Options) ([]int, error) {
	fd := int(t.In.Fd())
	saved, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", t.In.Name(), err)
	}
	defer func() { _ = term.Restore(fd, saved) }()

	_, _ = io.WriteString(t.Out, hideCursor)
	defer func() { _, _ = io.WriteString(t.Out, unhideCursor) }()

	m := newModel(opts).fit(t.size())
	painted := paint(t.Out, 0, m.render())

	buf := make([]byte, 64)
	var pending []byte
	for {
		n, rerr := t.In.Read(buf)
		if n == 0 {
			if rerr != nil {
				// The input ended without an answer — a closed pipe, or a
				// terminal that went away. That is not a selection.
				erase(t.Out, painted)
				return nil, ErrCancelled
			}
			continue
		}
		// One read can hold several presses, from a key held down or from a
		// terminal that buffered while the last frame was drawn — and can end
		// part-way through one, which is what pending carries over.
		m, pending = m.feed(append(pending, buf[:n]...))
		if m.state != running {
			// Nothing is drawn for the last keystroke: the block is about to
			// be erased, and painting it first only flickers.
			break
		}
		// Re-read the size every frame, so a window resized mid-selection
		// re-lays out rather than scrolling the block it is about to redraw.
		m = m.fit(t.size())
		painted = paint(t.Out, painted, m.render())
	}

	erase(t.Out, painted)
	return m.result()
}

// size is the terminal's, falling back to a conventional 80x24 for a terminal
// that will not say.
func (t Terminal) size() (width, height int) {
	w, h, err := term.GetSize(int(t.Out.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// paint redraws the block in place and returns the number of lines it now
// occupies. The cursor starts and ends on the line just below the block, which
// is the reference every redraw moves up from.
func paint(w io.Writer, painted int, lines []string) int {
	var b strings.Builder
	if painted > 0 {
		fmt.Fprintf(&b, cursorUp, painted)
	}
	for _, l := range lines {
		b.WriteString(clearLine)
		b.WriteString(l)
		// \r\n, not \n: raw mode turns off the translation that would
		// otherwise return the cursor to column zero.
		b.WriteString("\r\n")
	}
	// A block that shrank leaves lines below it that are no longer ours to
	// redraw, so they are cleared once and then stepped back over.
	if painted > len(lines) {
		for range painted - len(lines) {
			b.WriteString(clearLine + "\r\n")
		}
		fmt.Fprintf(&b, cursorUp, painted-len(lines))
	}
	_, _ = io.WriteString(w, b.String())
	return len(lines)
}

// erase removes the block, leaving the cursor where it began so the command's
// own output takes its place.
func erase(w io.Writer, painted int) {
	if painted <= 0 {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, cursorUp, painted)
	for range painted {
		b.WriteString(clearLine + "\r\n")
	}
	fmt.Fprintf(&b, cursorUp, painted)
	_, _ = io.WriteString(w, b.String())
}
