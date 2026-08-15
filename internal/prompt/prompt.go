// Package prompt asks the user to choose from a list on a terminal.
//
// It is split in two on purpose. Everything in this file is a pure function of
// a model and a keystroke, so the whole of the picker's behaviour is testable
// without a terminal; terminal.go holds the thin driver that puts a real
// terminal in raw mode and feeds it bytes. The package knows nothing about
// skills: a caller passes labelled rows and gets indices back.
package prompt

import (
	"errors"
	"fmt"
)

// ErrCancelled reports that the user backed out rather than choosing. Nothing
// was selected, and the caller should treat it as a decision, not a failure to
// read input.
var ErrCancelled = errors.New("cancelled")

// Item is one row the user can choose.
type Item struct {
	// Label is the whole row as it should appear, already formatted and
	// aligned by the caller. The picker only truncates it to the terminal.
	Label string
}

// Options describes one selection.
type Options struct {
	// Header is printed above the list, unchanged.
	Header []string
	// Items are the rows to choose from.
	Items []Item
	// Single asks for exactly one item: no checkboxes, and confirming takes
	// the row under the cursor.
	Single bool
	// Help is the key hints line. The caller writes it because the caller
	// knows what confirming will do.
	Help string
}

// The marks a row is drawn with. They are constants rather than literals so
// the tests can assert on them without restating the drawing.
const (
	cursorMark = "❯ "
	noCursor   = "  "
	tickedBox  = "◉ "
	emptyBox   = "◯ "
)

// visibleFloor is how many rows are worth keeping visible before the chrome
// around them is worth dropping, on a terminal too short for both.
const visibleFloor = 5

// state is how a selection ended, or that it has not.
type state int

const (
	running state = iota
	confirmed
	cancelled
)

// key is a keystroke the picker understands, decoded from the bytes a terminal
// sent.
type key int

const (
	keyNone key = iota
	keyUp
	keyDown
	keySpace
	keyAll
	keyEnter
	keyCancel
)

// model is the whole state of one selection. Every method returns a new model
// rather than mutating the receiver, so a keystroke sequence is a fold and a
// test needs nothing but the sequence.
type model struct {
	header []string
	help   string
	items  []Item
	single bool

	selected []bool
	cursor   int
	offset   int
	state    state

	// Layout, recomputed by fit whenever the terminal size is read.
	width      int
	rows       int
	showHeader bool
	showHelp   bool
	showGaps   bool
}

func newModel(opts Options) model {
	return model{
		header:   opts.Header,
		help:     opts.Help,
		items:    opts.Items,
		single:   opts.Single,
		selected: make([]bool, len(opts.Items)),
	}
}

// fit lays the block out for a terminal of this size.
//
// The block must not be taller than the screen. It is redrawn in place by
// moving the cursor back up over it, and a block that scrolled would leave the
// redraw painting over whatever the terminal moved up instead.
func (m model) fit(width, height int) model {
	m.width = max(width, 20)
	// Two lines is the smallest a picker can be: one row, and one line saying
	// how much it is not showing.
	height = max(height, 2)

	m.showHeader, m.showHelp, m.showGaps = len(m.header) > 0, m.help != "", true

	chrome := func() int {
		n := 0
		if m.showHeader {
			n += len(m.header)
			if m.showGaps {
				n++
			}
		}
		if m.showHelp {
			n++
			if m.showGaps {
				n++
			}
		}
		return n
	}

	// Chrome goes before rows do: a row the user cannot see is a skill they
	// cannot choose, while the key hints are only a reminder.
	want := min(len(m.items), visibleFloor)
	for _, drop := range []*bool{&m.showGaps, &m.showHelp, &m.showHeader} {
		if chrome()+want <= height {
			break
		}
		*drop = false
	}

	m.rows = height - chrome()
	if len(m.items) > m.rows {
		// One line goes to saying how much is hidden.
		m.rows--
	}
	m.rows = max(m.rows, 1)
	m.rows = min(m.rows, len(m.items))
	return m.scroll()
}

// apply folds one keystroke into the model.
func (m model) apply(k key) model {
	if m.state != running {
		return m
	}
	// Ending the selection is answerable whatever is in the list; moving
	// around it is not.
	switch k {
	case keyEnter:
		m.state = confirmed
		return m
	case keyCancel:
		m.state = cancelled
		return m
	}
	if len(m.items) == 0 {
		return m
	}

	switch k {
	case keyUp:
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
	case keyDown:
		m.cursor = (m.cursor + 1) % len(m.items)
	case keySpace:
		if !m.single {
			sel := m.copySelected()
			sel[m.cursor] = !sel[m.cursor]
			m.selected = sel
		}
	case keyAll:
		if !m.single {
			// A partly ticked list means the user has not finished choosing,
			// so this fills it in rather than undoing the work already done.
			fill := !m.allTicked()
			sel := m.copySelected()
			for i := range sel {
				sel[i] = fill
			}
			m.selected = sel
		}
	}
	return m.scroll()
}

// result is what the selection came to, once it has stopped running.
//
// Confirming with nothing ticked is cancelling: it is the same "never mind" as
// q, and reporting it as a successful selection of no skills would leave the
// caller installing nothing and calling it done.
func (m model) result() ([]int, error) {
	if m.state != confirmed {
		return nil, ErrCancelled
	}
	if len(m.items) == 0 {
		return nil, ErrCancelled
	}
	if m.single {
		return []int{m.cursor}, nil
	}

	// In list order, not in the order the rows were ticked: the order the user
	// clicked in is not an order to install in.
	var out []int
	for i, ok := range m.selected {
		if ok {
			out = append(out, i)
		}
	}
	if len(out) == 0 {
		return nil, ErrCancelled
	}
	return out, nil
}

// render draws the block, every line already truncated to the terminal.
func (m model) render() []string {
	var lines []string
	if m.showHeader {
		lines = append(lines, m.header...)
		if m.showGaps {
			lines = append(lines, "")
		}
	}

	end := min(m.offset+m.rows, len(m.items))
	for i := m.offset; i < end; i++ {
		lines = append(lines, "  "+m.mark(i)+m.items[i].Label)
	}
	if hidden := len(m.items) - (end - m.offset); hidden > 0 {
		lines = append(lines, fmt.Sprintf("  … %d more", hidden))
	}

	if m.showHelp {
		if m.showGaps {
			lines = append(lines, "")
		}
		lines = append(lines, "  "+m.help)
	}

	for i, l := range lines {
		lines[i] = truncate(l, m.width)
	}
	return lines
}

// mark is the cursor and checkbox a row is drawn with.
func (m model) mark(i int) string {
	cursor := noCursor
	if i == m.cursor {
		cursor = cursorMark
	}
	if m.single {
		return cursor
	}
	if m.selected[i] {
		return cursor + tickedBox
	}
	return cursor + emptyBox
}

// scroll moves the window so the cursor stays in it.
func (m model) scroll() model {
	if m.rows < 1 || len(m.items) == 0 {
		m.offset = 0
		return m
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.rows {
		m.offset = m.cursor - m.rows + 1
	}
	m.offset = min(m.offset, len(m.items)-m.rows)
	m.offset = max(m.offset, 0)
	return m
}

// copySelected keeps a keystroke from writing through to the model it was
// folded into, which is what makes a sequence of them replayable.
func (m model) copySelected() []bool {
	sel := make([]bool, len(m.selected))
	copy(sel, m.selected)
	return sel
}

func (m model) allTicked() bool {
	for _, ok := range m.selected {
		if !ok {
			return false
		}
	}
	return len(m.selected) > 0
}

// feed folds a chunk of terminal input into the model, and hands back the tail
// of it that was an escape sequence still arriving. The caller puts that in
// front of the next read.
//
// Reassembling across reads is what keeps an arrow key an arrow key. A local
// terminal writes a sequence whole, but a pipe or an ssh link can split it,
// and a lone escape byte read on its own is indistinguishable from the Esc
// key — so an arrow that arrived in two pieces would otherwise cancel the
// selection the user was scrolling through.
func (m model) feed(chunk []byte) (model, []byte) {
	for len(chunk) > 0 && m.state == running {
		k, used := decode(chunk)
		if used == 0 {
			return m, chunk
		}
		chunk = chunk[used:]
		m = m.apply(k)
	}
	return m, nil
}

// decode reads the next keystroke from a chunk of terminal input and reports
// how many bytes it consumed. Holding a key down delivers several presses in
// one read, so a caller loops until the chunk is empty.
//
// A zero length means the chunk ends part-way through an escape sequence and
// nothing can be decided about it yet. It is the reason Esc only cancels once
// another key follows it: without a timer, waiting for the rest of a possible
// arrow is the only way to avoid mistaking one for the other, and arrows are
// pressed far more often than Esc.
func decode(b []byte) (key, int) {
	if len(b) == 0 {
		return keyNone, 0
	}
	switch b[0] {
	case 0x1b:
		if len(b) < 3 && (len(b) == 1 || b[1] == '[' || b[1] == 'O') {
			return keyNone, 0 // still arriving
		}
		if b[1] == '[' || b[1] == 'O' {
			switch b[2] {
			case 'A':
				return keyUp, 3
			case 'B':
				return keyDown, 3
			}
			return keyNone, 3
		}
		return keyCancel, 1
	case '\r', '\n':
		return keyEnter, 1
	case ' ':
		return keySpace, 1
	case 'a', 'A':
		return keyAll, 1
	case 'q', 'Q', 0x03, 0x04: // q, Ctrl-C, Ctrl-D
		return keyCancel, 1
	case 'k', 'K':
		return keyUp, 1
	case 'j', 'J':
		return keyDown, 1
	}
	return keyNone, 1
}

// truncate cuts a line to width, counting runes rather than bytes so a
// multi-byte character is never split into invalid UTF-8.
func truncate(s string, width int) string {
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width < 2 {
		return string(r[:max(width, 0)])
	}
	return string(r[:width-1]) + "…"
}
