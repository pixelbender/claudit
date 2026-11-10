package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func rowTexts(rows []inputRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, string(r.text))
	}
	return out
}

// Rows end at the edge and at newlines; the cursor right after a full row stands on the next.
func TestLayoutInput(t *testing.T) {
	for _, c := range []struct {
		text     string
		cursor   int
		avail    int
		rows     string
		row, col int
	}{
		{"abcdefgh", 3, 3, "abc|def|gh", 1, 0},
		{"abcdefgh", 8, 3, "abc|def|gh", 2, 2},
		{"abcdef", 6, 3, "abc|def|", 2, 0},
		{"ab\ncd", 2, 10, "ab|cd", 0, 2},
		{"ab\ncd", 3, 10, "ab|cd", 1, 0},
		{"ab\n", 3, 10, "ab|", 1, 0},
		{"日本語", 3, 4, "日本|語", 1, 2},
		{"a\tb", 3, 10, "a b", 0, 3},
		{"a\u200db", 1, 10, "ab", 0, 1}, // a zero-width joiner is not drawn
		{"x", 0, 0, "x", 0, 0},
		// A mark that joins the letter before it takes no cell of its own.
		{"e\u0301", 2, 10, "e\u0301", 0, 1},
		{"วันที่", 6, 10, "วันที่", 0, 3},
		{"e\u0301e\u0301e\u0301", 6, 2, "e\u0301e\u0301|e\u0301", 1, 1},
	} {
		rows, row, col := layoutInput([]rune(c.text), c.cursor, c.avail)
		if got := strings.Join(rowTexts(rows), "|"); got != c.rows || row != c.row || col != c.col {
			t.Errorf("%q at %d in %d: %q row %d col %d, want %q row %d col %d", c.text, c.cursor, c.avail, got, row, col, c.rows, c.row, c.col)
		}
	}
}

// Up and down land on the place of a column: past the marks of a letter, not between them.
func TestInputIndexAt(t *testing.T) {
	for _, c := range []struct {
		text string
		col  int
		want int
	}{
		{"e\u0301x", 1, 2},
		{"วันที่x", 3, 6},
		{"b\u200dcd", 2, 3},
		{"abc", 0, 0},
		{"abc", 9, 3},
	} {
		runes := []rune(c.text)
		if got := inputIndexAt(runes, inputRow{to: len(runes)}, false, c.col); got != c.want {
			t.Errorf("%q col %d: %d, want %d", c.text, c.col, got, c.want)
		}
	}
	if inputMaxRows(200) != 10 || inputMaxRows(20) != 5 || inputMaxRows(3) != 1 {
		t.Errorf("rows at most: %d %d %d", inputMaxRows(200), inputMaxRows(20), inputMaxRows(3))
	}
}

// The field grows to its rows, the gutter on the first; past them it scrolls to the cursor.
func TestRenderInputRows(t *testing.T) {
	strip := func(lines []string) []string {
		for i, l := range lines {
			lines[i] = strings.TrimRight(ansi.Strip(l), " ")
		}
		return lines
	}
	// The cursor stands where a one-row prompt puts it: past the gutter and the cells drawn.
	if _, x, y := renderInputRows(&Input{Text: []rune("วันที่"), Cursor: 6}, 40, "", false, 5); x != 2+3 || y != 0 {
		t.Errorf("marks: x=%d y=%d", x, y)
	}
	in := &Input{Text: []rune("abcdefghij"), Cursor: 10}
	lines, x, y := renderInputRows(in, 6, "", false, 5) // 4 cells a row past the gutter
	if got := strings.Join(strip(lines), "|"); got != "› abcd|  efgh|  ij" || x != 4 || y != 2 {
		t.Fatalf("%q x=%d y=%d", got, x, y)
	}
	lines, x, y = renderInputRows(in, 6, "", false, 2)
	if got := strings.Join(strip(lines), "|"); got != "  efgh|  ij" || x != 4 || y != 1 {
		t.Fatalf("scrolled: %q x=%d y=%d", got, x, y)
	}
	in.Cursor = 0
	if lines, _, y := renderInputRows(in, 6, "", false, 2); strings.Join(strip(lines), "|") != "› abcd|  efgh" || y != 0 {
		t.Fatalf("top: %q y=%d", strip(lines), y)
	}
	bang := &Input{Text: []rune("!git status -sb"), Cursor: 15}
	if lines, x, y := renderInputRows(bang, 10, "", false, 5); strings.Join(strip(lines), "|") != "! git stat|  us -sb" || x != 8 || y != 1 {
		t.Fatalf("shell mode: %q x=%d y=%d", strip(lines), x, y)
	}
	if lines, _, _ := renderInputRows(&Input{}, 30, "Ask claude…", false, 5); len(lines) != 1 || strip(lines)[0] != "› Ask claude…" {
		t.Fatalf("empty: %q", strip(lines))
	}
	if lines, _, _ := renderInputRows(in, 0, "", false, 5); len(lines) != 1 || lines[0] != "" {
		t.Fatalf("no room: %q", lines)
	}
	one, ox := renderInput(&Input{Text: []rune("abcdefghij"), Cursor: 10}, 6, "", false)
	if got := strings.TrimRight(ansi.Strip(one), " "); got != "› hij" || ox != 5 {
		t.Fatalf("one row scrolls sideways: %q x=%d", got, ox)
	}
}

// A long prompt takes rows from the conversation, the screen keeps its height, and the cursor
// stands where the text ends.
func TestModelInputGrows(t *testing.T) {
	m, _ := modelMouseNew(t) // 100×30
	short := len(strings.Split(m.View().Content, "\n"))
	m.Input = Input{Text: []rune(strings.Repeat("word ", 50)), Cursor: 250} // 250 cells, 94 a row
	v := m.View()
	lines := strings.Split(ansi.Strip(v.Content), "\n")
	if len(lines) != short || len(lines) != 30 {
		t.Fatalf("%d lines, %d before", len(lines), short)
	}
	var rows []int
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "│") && strings.Contains(l, "word") {
			rows = append(rows, i)
		}
	}
	if len(rows) != 3 || rows[2] != 27 || v.Cursor == nil || v.Cursor.Y != 27 {
		t.Fatalf("prompt rows %v cursor %+v\n%s", rows, v.Cursor, strings.Join(lines, "\n"))
	}
	m.Input = Input{Text: []rune(strings.Repeat("x", 5000)), Cursor: 5000}
	if n := len(strings.Split(m.View().Content, "\n")); n != 30 {
		t.Fatalf("a very long prompt: %d lines", n)
	}
}

// On a screen just tall enough for the IDE, a growing prompt keeps it: the conversation gives up
// the rows, the IDE only those the conversation has not.
func TestModelInputGrowsKeepsIDE(t *testing.T) {
	m, _ := modelNew(t, false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: drawIDEH + 2})         // a prompt of 4 rows at most
	long := Input{Text: []rune(strings.Repeat("word ", 70)), Cursor: 350} // 4 rows of 94 cells
	m.View()
	empty := m.ideHeight
	m.Input = long
	m.View()
	if m.ideHeight <= 0 || m.ideHeight != empty-3 {
		t.Fatalf("no conversation: the IDE %d rows, %d before", m.ideHeight, empty)
	}
	for range 20 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: "said"})
	}
	m.chatLines, m.Input = chatCache{}, Input{}
	m.View()
	before := m.ideHeight
	m.Input = long
	m.View()
	if m.ideHeight != before {
		t.Fatalf("the conversation keeps its rows: the IDE %d rows, %d before", m.ideHeight, before)
	}
}

// ↑ and ↓ move between the rows of a long prompt; from the first row ↑ recalls the history.
func TestModelMoveRow(t *testing.T) {
	m, _ := modelMouseNew(t) // 94 cells a row
	m.remember("earlier")
	text := strings.Repeat("a", 94) + strings.Repeat("b", 94) + "cc"
	m.Input = Input{Text: []rune(text), Cursor: 94 + 5}
	m.Update(inputKey("up"))
	if m.Input.Cursor != 5 || string(m.Input.Text) != text {
		t.Fatalf("up: cursor %d", m.Input.Cursor)
	}
	m.Update(inputKey("down"))
	m.Update(inputKey("down"))
	if m.Input.Cursor != 188+2 {
		t.Fatalf("down to the short last row: cursor %d", m.Input.Cursor)
	}
	m.Input.Cursor = 94 + 93
	m.Update(inputKey("up"))
	if m.Input.Cursor != 93 {
		t.Fatalf("up to a wrapped row's end: cursor %d", m.Input.Cursor)
	}
	m.Update(inputKey("up"))
	if string(m.Input.Text) != "earlier" {
		t.Fatalf("from the first row up recalls: %q", string(m.Input.Text))
	}
	wide := strings.Repeat("a", 93) + "日" + strings.Repeat("b", 92) + "ccccc" // 日 does not fit the first row's last cell
	m.Input = Input{Text: []rune(wide), Cursor: 93 + 1 + 91}
	m.Update(inputKey("up"))
	if m.Input.Cursor != 92 {
		t.Fatalf("up past a shorter wrapped row: cursor %d", m.Input.Cursor)
	}
	joined := strings.Repeat("a", 94) + "b\u200dc"
	m.Input = Input{Text: []rune(joined), Cursor: 94 + 3}
	m.Update(inputKey("up"))
	if m.Input.Cursor != 2 {
		t.Fatalf("over a zero-width joiner: cursor %d", m.Input.Cursor)
	}
	m.Input = Input{Text: []rune("!" + strings.Repeat("s", 100)), Cursor: 101}
	m.Update(inputKey("up"))
	if m.Input.Cursor != 1+6 {
		t.Fatalf("shell mode: cursor %d", m.Input.Cursor)
	}
	small, _ := modelNew(t, false)
	small.Update(tea.WindowSizeMsg{Width: 100, Height: 7})
	small.Input = Input{Text: []rune(text), Cursor: 100}
	if small.moveRow(-1) {
		t.Fatal("a screen too low for rows moved the cursor")
	}
}
