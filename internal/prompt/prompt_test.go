package prompt

import (
	"errors"
	"strings"
	"testing"
)

// script drives a model through a sequence of keys, the way a terminal would.
func script(t *testing.T, opts Options, keys ...key) ([]int, error) {
	t.Helper()
	m := newModel(opts).fit(80, 24)
	for _, k := range keys {
		m = m.apply(k)
		if m.state != running {
			break
		}
	}
	return m.result()
}

func threeItems() Options {
	return Options{
		Header: []string{"skills in repo:"},
		Items:  []Item{{Label: "alpha"}, {Label: "beta"}, {Label: "gamma"}},
		Help:   "enter install",
	}
}

func TestSelectReturnsTheTickedRows(t *testing.T) {
	got, err := script(t, threeItems(), keySpace, keyDown, keyDown, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	want := []int{0, 2}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("selection = %v, want %v", got, want)
	}
}

func TestSelectReturnsIndicesInListOrderNotClickOrder(t *testing.T) {
	// The order the user ticks rows in is not an order to install in: the
	// receipts and the listing both follow the repository's own order.
	got, err := script(t, threeItems(), keyDown, keyDown, keySpace, keyUp, keyUp, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Errorf("selection = %v, want [0 2]", got)
	}
}

func TestToggleAllSelectsEverythingThenClearsIt(t *testing.T) {
	got, err := script(t, threeItems(), keyAll, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("selection = %v, want all three", got)
	}

	if _, err := script(t, threeItems(), keyAll, keyAll, keyEnter); !errors.Is(err, ErrCancelled) {
		t.Errorf("clearing every row and confirming = %v, want ErrCancelled", err)
	}
}

func TestToggleAllSelectsEverythingWhenOnlySomeAreTicked(t *testing.T) {
	// A partly-ticked list means "I have not finished choosing", so `a` fills
	// it in rather than clearing the work already done.
	got, err := script(t, threeItems(), keySpace, keyAll, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("selection = %v, want all three", got)
	}
}

func TestCursorWraps(t *testing.T) {
	// Up from the first row lands on the last, so a long list does not need
	// scrolling to reach the end.
	got, err := script(t, threeItems(), keyUp, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("selection = %v, want [2] from wrapping upwards", got)
	}

	got, err = script(t, threeItems(), keyDown, keyDown, keyDown, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 1 || got[0] != 0 {
		t.Errorf("selection = %v, want [0] from wrapping downwards", got)
	}
}

func TestCancelKeys(t *testing.T) {
	for _, k := range []key{keyCancel} {
		if _, err := script(t, threeItems(), keySpace, k); !errors.Is(err, ErrCancelled) {
			t.Errorf("cancel = %v, want ErrCancelled", err)
		}
	}
}

func TestConfirmingNothingIsCancelling(t *testing.T) {
	// Confirming an empty list is not a request to install nothing; it is the
	// same "never mind" that q is, and must not be reported as a success.
	if _, err := script(t, threeItems(), keyEnter); !errors.Is(err, ErrCancelled) {
		t.Errorf("empty confirm = %v, want ErrCancelled", err)
	}
}

func TestSingleModeTakesTheRowUnderTheCursor(t *testing.T) {
	opts := threeItems()
	opts.Single = true

	got, err := script(t, opts, keyDown, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("selection = %v, want [1]", got)
	}
}

func TestSingleModeIgnoresTogglingKeys(t *testing.T) {
	opts := threeItems()
	opts.Single = true

	// space and `a` cannot build a multi-selection in a mode whose whole point
	// is that --as renames exactly one skill.
	got, err := script(t, opts, keySpace, keyAll, keyDown, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("selection = %v, want [1]", got)
	}
}

func TestRenderShowsHeaderRowsAndHelp(t *testing.T) {
	m := newModel(threeItems()).fit(80, 24)
	m = m.apply(keySpace)
	got := strings.Join(m.render(), "\n")

	for _, want := range []string{"skills in repo:", "alpha", "beta", "gamma", "enter install"} {
		if !strings.Contains(got, want) {
			t.Errorf("render missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, cursorMark) {
		t.Errorf("render should mark the cursor:\n%s", got)
	}
	if !strings.Contains(got, tickedBox) || !strings.Contains(got, emptyBox) {
		t.Errorf("render should show both a ticked and an unticked box:\n%s", got)
	}
}

func TestRenderOmitsBoxesInSingleMode(t *testing.T) {
	opts := threeItems()
	opts.Single = true

	got := strings.Join(newModel(opts).fit(80, 24).render(), "\n")
	if strings.Contains(got, tickedBox) || strings.Contains(got, emptyBox) {
		t.Errorf("single mode has nothing to tick:\n%s", got)
	}
	if !strings.Contains(got, cursorMark) {
		t.Errorf("single mode still needs a cursor:\n%s", got)
	}
}

func TestRenderNeverExceedsTheTerminalHeight(t *testing.T) {
	// The block is redrawn in place by moving the cursor up over it, so one
	// taller than the screen would scroll and leave the redraw painting over
	// the wrong lines.
	var items []Item
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		items = append(items, Item{Label: n})
	}
	opts := threeItems()
	opts.Items = items

	for _, height := range []int{3, 6, 8, 24} {
		m := newModel(opts).fit(80, height)
		if got := len(m.render()); got > height {
			t.Errorf("height %d: render produced %d lines", height, got)
		}
	}
}

func TestRenderWindowsALongListAndKeepsTheCursorVisible(t *testing.T) {
	var items []Item
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		items = append(items, Item{Label: n})
	}
	opts := threeItems()
	opts.Items = items

	m := newModel(opts).fit(80, 8)
	for range 8 {
		m = m.apply(keyDown)
	}
	got := strings.Join(m.render(), "\n")

	if !strings.Contains(got, "i") {
		t.Errorf("the row under the cursor scrolled out of view:\n%s", got)
	}
	if !strings.Contains(got, "more") {
		t.Errorf("a windowed list should say how much is hidden:\n%s", got)
	}
}

func TestRenderTruncatesToWidthWithoutSplittingRunes(t *testing.T) {
	opts := threeItems()
	opts.Items = []Item{{Label: strings.Repeat("é", 200)}}

	for _, line := range newModel(opts).fit(20, 24).render() {
		if len([]rune(line)) > 20 {
			t.Errorf("line is %d runes wide, want <= 20: %q", len([]rune(line)), line)
		}
		if !utf8Valid(line) {
			t.Errorf("truncation split a rune: %q", line)
		}
	}
}

func TestDecodeKeys(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want key
		used int
	}{
		{"up arrow", "\x1b[A", keyUp, 3},
		{"down arrow", "\x1b[B", keyDown, 3},
		{"application up", "\x1bOA", keyUp, 3},
		{"k", "k", keyUp, 1},
		{"j", "j", keyDown, 1},
		{"space", " ", keySpace, 1},
		{"a", "a", keyAll, 1},
		{"enter", "\r", keyEnter, 1},
		{"newline", "\n", keyEnter, 1},
		{"q", "q", keyCancel, 1},
		{"ctrl-c", "\x03", keyCancel, 1},
		{"ctrl-d", "\x04", keyCancel, 1},
		// An escape byte on its own could still become an arrow, so nothing is
		// decided about it until more arrives — used == 0 says so.
		{"bare escape", "\x1b", keyNone, 0},
		{"half an arrow", "\x1b[", keyNone, 0},
		{"escape then a key that cannot continue it", "\x1bz", keyCancel, 1},
		{"escape twice", "\x1b\x1b", keyCancel, 1},
		{"unknown", "z", keyNone, 1},
		{"sideways arrow", "\x1b[C", keyNone, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, used := decode([]byte(tt.in))
			if got != tt.want || used != tt.used {
				t.Errorf("decode(%q) = (%v, %d), want (%v, %d)", tt.in, got, used, tt.want, tt.used)
			}
		})
	}
}

func TestDecodeConsumesAChunkOfSeveralKeys(t *testing.T) {
	// Holding a key down delivers several presses in one read.
	chunk := []byte("\x1b[B\x1b[B ")
	var got []key
	for len(chunk) > 0 {
		k, used := decode(chunk)
		got = append(got, k)
		chunk = chunk[used:]
	}
	want := []key{keyDown, keyDown, keySpace}
	if len(got) != len(want) {
		t.Fatalf("decoded %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("decoded %v, want %v", got, want)
		}
	}
}

// feedAll replays reads the way the driver does, carrying an unfinished escape
// sequence from one into the next.
func feedAll(t *testing.T, opts Options, reads ...string) ([]int, error) {
	t.Helper()
	m := newModel(opts).fit(80, 24)
	var pending []byte
	for _, r := range reads {
		m, pending = m.feed(append(pending, r...))
		if m.state != running {
			break
		}
	}
	return m.result()
}

// A local terminal writes an escape sequence whole, but a pipe or an ssh link
// can split it. Decoding each read on its own would see a lone escape byte and
// cancel the selection the user was only scrolling through.
func TestArrowKeySplitAcrossReadsIsStillAnArrowKey(t *testing.T) {
	got, err := feedAll(t, threeItems(), "\x1b", "[B", " \r")
	if err != nil {
		t.Fatalf("a split arrow did not survive reassembly: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("selection = %v, want [1]", got)
	}

	// Split between every byte, which is the worst a link can do.
	got, err = feedAll(t, threeItems(), "\x1b", "[", "B", "\x1b", "[", "B", " ", "\r")
	if err != nil {
		t.Fatalf("a byte-at-a-time arrow did not survive reassembly: %v", err)
	}
	if len(got) != 1 || got[0] != 2 {
		t.Errorf("selection = %v, want [2]", got)
	}
}

func TestEscapeCancelsOnceAnotherKeyFollows(t *testing.T) {
	// Esc alone cannot be told from the start of an arrow, so it waits. The
	// next key resolves it, and q and Ctrl-C are the keys that cancel outright.
	if _, err := feedAll(t, threeItems(), "\x1b"); !errors.Is(err, ErrCancelled) {
		t.Error("an unresolved escape should leave the selection unconfirmed")
	}
	if _, err := feedAll(t, threeItems(), "\x1b", "\x1b"); !errors.Is(err, ErrCancelled) {
		t.Errorf("Esc twice = %v, want ErrCancelled", err)
	}

	// And the thing that must not happen: the escape is not treated as a
	// cancel while an arrow could still be arriving.
	m := newModel(threeItems()).fit(80, 24)
	m, pending := m.feed([]byte("\x1b"))
	if m.state != running {
		t.Errorf("state = %v, want the model still running on a half-read arrow", m.state)
	}
	if string(pending) != "\x1b" {
		t.Errorf("pending = %q, want the escape carried into the next read", pending)
	}
}

func TestFeedConsumesAWholeChunk(t *testing.T) {
	m := newModel(threeItems()).fit(80, 24)
	m, pending := m.feed([]byte("\x1b[B "))
	if len(pending) != 0 {
		t.Errorf("pending = %q, want nothing left over", pending)
	}
	if m.cursor != 1 || !m.selected[1] {
		t.Errorf("cursor = %d, selected = %v, want the whole chunk applied", m.cursor, m.selected)
	}
}

func TestFeedStopsAtTheKeyThatEndsTheSelection(t *testing.T) {
	// Keys typed after enter belong to the shell, not to the picker.
	m := newModel(threeItems()).fit(80, 24)
	m, _ = m.feed([]byte(" \rqqq"))
	if m.state != confirmed {
		t.Errorf("state = %v, want confirmed: the q's came after the enter", m.state)
	}
}

func TestSelectWithNoItemsIsCancelled(t *testing.T) {
	if _, err := script(t, Options{}, keyEnter); !errors.Is(err, ErrCancelled) {
		t.Errorf("empty list = %v, want ErrCancelled", err)
	}
}

// groupedItems is two header rows each with two members: rows 0=cat-a header,
// 1=alpha, 2=beta, 3=cat-b header, 4=gamma, 5=delta.
func groupedItems() Options {
	return Options{
		Header: []string{"skills in repo:"},
		Items: []Item{
			{Label: "cat-a", Header: true},
			{Label: "alpha"},
			{Label: "beta"},
			{Label: "cat-b", Header: true},
			{Label: "gamma"},
			{Label: "delta"},
		},
		Help: "space toggle",
	}
}

func TestSpaceOnAHeaderTogglesItsGroup(t *testing.T) {
	got, err := script(t, groupedItems(), keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("selection = %v, want [1 2] (cat-a's members)", got)
	}
}

func TestSpaceOnAHeaderDoesNotTouchOtherGroups(t *testing.T) {
	got, err := script(t, groupedItems(), keyDown, keyDown, keyDown, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Errorf("selection = %v, want [4 5] (cat-b's members)", got)
	}
}

func TestSpaceOnAHeaderTwiceClearsItsGroup(t *testing.T) {
	if _, err := script(t, groupedItems(), keySpace, keySpace, keyEnter); !errors.Is(err, ErrCancelled) {
		t.Errorf("toggling a group on then off = %v, want ErrCancelled (nothing ticked)", err)
	}
}

func TestSpaceOnAHeaderFillsAPartlyTickedGroup(t *testing.T) {
	// One member already ticked means the group has not finished being
	// chosen, so the header fills it in rather than clearing the one tick.
	got, err := script(t, groupedItems(), keyDown, keySpace, keyUp, keySpace, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("selection = %v, want [1 2]", got)
	}
}

func TestGlobalSelectAllSkipsHeaders(t *testing.T) {
	got, err := script(t, groupedItems(), keyAll, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	want := []int{1, 2, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("selection = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selection = %v, want %v", got, want)
		}
	}

	// And it clears every member back out again, ignoring the headers'
	// always-false entries when deciding it should.
	if _, err := script(t, groupedItems(), keyAll, keyAll, keyEnter); !errors.Is(err, ErrCancelled) {
		t.Errorf("clearing every row and confirming = %v, want ErrCancelled", err)
	}
}

func TestHeaderRenderShowsAggregateState(t *testing.T) {
	m := newModel(groupedItems()).fit(80, 24)

	got := strings.Join(m.render(), "\n")
	if !strings.Contains(got, emptyBox) {
		t.Errorf("an untouched header should show the empty box:\n%s", got)
	}

	m = m.apply(keyDown).apply(keySpace) // tick alpha only, leaving beta untouched
	got = strings.Join(m.render(), "\n")
	if !strings.Contains(got, partialBox) {
		t.Errorf("a partly-ticked group's header should show the partial box:\n%s", got)
	}

	m = m.apply(keyDown).apply(keySpace) // tick beta too
	got = strings.Join(m.render(), "\n")
	if !strings.Contains(got, tickedBox) {
		t.Errorf("a fully-ticked group's header should show the ticked box:\n%s", got)
	}
}

func TestRenderIndentsMembersUnderTheirHeader(t *testing.T) {
	m := newModel(groupedItems()).fit(80, 24)
	got := m.render()

	var headerLine, memberLine string
	for _, l := range got {
		if strings.Contains(l, "cat-a") {
			headerLine = l
		}
		if strings.Contains(l, "alpha") {
			memberLine = l
		}
	}
	if headerLine == "" || memberLine == "" {
		t.Fatalf("expected both a header and a member row in:\n%s", strings.Join(got, "\n"))
	}
	if !strings.HasPrefix(memberLine, "    ") {
		t.Errorf("member row %q should be indented deeper than the header", memberLine)
	}
	if strings.HasPrefix(headerLine, "    ") {
		t.Errorf("header row %q should not be indented like a member", headerLine)
	}
}

func TestSingleModeCannotConfirmOnAHeader(t *testing.T) {
	opts := groupedItems()
	opts.Single = true

	// Enter on the header row (cursor starts at 0) is a no-op; moving onto a
	// member and confirming there is what takes the selection.
	got, err := script(t, opts, keyEnter, keyDown, keyEnter)
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("selection = %v, want [1]", got)
	}
}

// utf8Valid reports whether s survived truncation intact.
func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}
