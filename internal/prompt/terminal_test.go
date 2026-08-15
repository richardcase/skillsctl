package prompt

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

// screen replays what paint and erase wrote, so a test can assert on the block
// the user would see rather than on the escape sequences that drew it. It
// models only what the picker uses: move up, clear line, and write.
type screen struct {
	lines  []string
	cursor int
}

func (s *screen) apply(t *testing.T, out string) {
	t.Helper()
	for len(out) > 0 {
		switch {
		case strings.HasPrefix(out, "\x1b["):
			rest := out[2:]
			i := strings.IndexAny(rest, "ABCDKmlh")
			if i < 0 {
				t.Fatalf("unterminated escape in %q", out)
			}
			verb, arg := rest[i], rest[:i]
			out = rest[i+1:]
			switch verb {
			case 'A':
				var n int
				if _, err := fmt.Sscanf(arg, "%d", &n); err != nil {
					t.Fatalf("bad cursor-up argument %q", arg)
				}
				s.cursor -= n
				if s.cursor < 0 {
					t.Fatalf("cursor moved above the top of the block, to %d", s.cursor)
				}
			case 'K':
				s.set("")
			}
		case strings.HasPrefix(out, "\r\n"):
			s.cursor++
			out = out[2:]
		case strings.HasPrefix(out, "\r"):
			out = out[1:]
		default:
			i := strings.IndexAny(out, "\x1b\r")
			if i < 0 {
				i = len(out)
			}
			s.set(s.at() + out[:i])
			out = out[i:]
		}
	}
}

func (s *screen) at() string {
	if s.cursor < len(s.lines) {
		return s.lines[s.cursor]
	}
	return ""
}

func (s *screen) set(v string) {
	for len(s.lines) <= s.cursor {
		s.lines = append(s.lines, "")
	}
	s.lines[s.cursor] = v
}

// visible is the block as it stands, trimmed of the blank tail a shrunk block
// leaves behind.
func (s *screen) visible() []string {
	end := len(s.lines)
	for end > 0 && s.lines[end-1] == "" {
		end--
	}
	return s.lines[:end]
}

func TestPaintRedrawsInPlace(t *testing.T) {
	var buf bytes.Buffer
	var s screen

	painted := paint(&buf, 0, []string{"one", "two", "three"})
	s.apply(t, buf.String())
	if painted != 3 {
		t.Errorf("painted = %d, want 3", painted)
	}
	if s.cursor != 3 {
		t.Errorf("cursor = %d, want it on the line below the block", s.cursor)
	}

	// A redraw of the same height replaces the block rather than repeating it.
	buf.Reset()
	painted = paint(&buf, painted, []string{"ONE", "TWO", "THREE"})
	s.apply(t, buf.String())
	if got := strings.Join(s.visible(), "|"); got != "ONE|TWO|THREE" {
		t.Errorf("screen = %q, want the block replaced", got)
	}
	if painted != 3 || s.cursor != 3 {
		t.Errorf("painted = %d, cursor = %d, want 3 and 3", painted, s.cursor)
	}
}

func TestPaintClearsTheLinesAShrunkBlockLeaves(t *testing.T) {
	var buf bytes.Buffer
	var s screen

	// A list that scrolls to its end loses its "N more" line, so the block
	// really does shrink mid-selection.
	painted := paint(&buf, 0, []string{"a", "b", "c", "d"})
	s.apply(t, buf.String())

	buf.Reset()
	painted = paint(&buf, painted, []string{"a", "b"})
	s.apply(t, buf.String())

	if got := strings.Join(s.visible(), "|"); got != "a|b" {
		t.Errorf("screen = %q, want the old rows cleared", got)
	}
	if painted != 2 {
		t.Errorf("painted = %d, want 2", painted)
	}
	if s.cursor != 2 {
		t.Errorf("cursor = %d, want it just below the shorter block: the next "+
			"redraw moves up from there", s.cursor)
	}
}

func TestPaintGrowsABlock(t *testing.T) {
	var buf bytes.Buffer
	var s screen

	painted := paint(&buf, 0, []string{"a"})
	s.apply(t, buf.String())

	buf.Reset()
	painted = paint(&buf, painted, []string{"a", "b", "c"})
	s.apply(t, buf.String())

	if got := strings.Join(s.visible(), "|"); got != "a|b|c" {
		t.Errorf("screen = %q, want the block grown", got)
	}
	if painted != 3 || s.cursor != 3 {
		t.Errorf("painted = %d, cursor = %d, want 3 and 3", painted, s.cursor)
	}
}

func TestEraseLeavesTheCursorWhereTheBlockBegan(t *testing.T) {
	var buf bytes.Buffer
	var s screen

	painted := paint(&buf, 0, []string{"one", "two", "three"})
	s.apply(t, buf.String())

	buf.Reset()
	erase(&buf, painted)
	s.apply(t, buf.String())

	if got := s.visible(); len(got) != 0 {
		t.Errorf("screen = %q, want the block gone so the command's own output takes its place", got)
	}
	if s.cursor != 0 {
		t.Errorf("cursor = %d, want 0", s.cursor)
	}
}

func TestEraseOfNothingWritesNothing(t *testing.T) {
	var buf bytes.Buffer
	erase(&buf, 0)
	if buf.Len() != 0 {
		t.Errorf("erase wrote %q for an unpainted block", buf.String())
	}
}

func TestPaintUsesCarriageReturnsForRawMode(t *testing.T) {
	// Raw mode turns off the newline translation that would otherwise return
	// the cursor to column zero, so every line has to do it itself.
	var buf bytes.Buffer
	paint(&buf, 0, []string{"a", "b"})
	if strings.Count(buf.String(), "\r\n") != 2 {
		t.Errorf("paint wrote %q, want \\r\\n after each line", buf.String())
	}
}

func TestInteractiveIsFalseWithoutBothEnds(t *testing.T) {
	// A pipe is not a terminal, and neither is a missing file.
	if (Terminal{}).Interactive() {
		t.Error("a Terminal with no files is not interactive")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})

	if (Terminal{In: r, Out: w}).Interactive() {
		t.Error("a pipe is not a terminal, so there is nobody to ask")
	}
}
