// Package ideview draws the simulated IDE: the explorer or the agents tree on the left; on the
// right, at full height, the workplace of the camera's agent (its terminal while it runs commands,
// its browser while it searches or fetches the web, the editor otherwise), side by side with those
// of the other agents at work.
package ideview

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/theme"
)

var (
	fgStyle   = lipgloss.NewStyle().Foreground(theme.Fg)
	dimStyle  = lipgloss.NewStyle().Foreground(theme.Dim)
	okStyle   = lipgloss.NewStyle().Foreground(theme.OK)
	errStyle  = lipgloss.NewStyle().Foreground(theme.Err)
	hunkStyle = lipgloss.NewStyle().Foreground(theme.Violet) // a diff's "@@ -1,2 +1,3 @@"

	spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
)

// Render draws st into exactly width×height cells (height lines, each width cells wide), following
// the camera's agent.
func Render(st *ide.State, o Options, width, height int) string {
	text, _ := RenderWithScrollbars(st, o, width, height)
	return text
}

// RenderWithScrollbars also returns the scrollbar geometry for the frame's mouse zones.
func RenderWithScrollbars(st *ide.State, o Options, width, height int) (string, []Scrollbar) {
	if height <= 0 {
		return "", nil
	}
	cam := camera(st)
	ew := leftWidth(width)
	rw := width - ew
	var left []string
	switch v := beside(st, o, width, height); {
	case o.Panel == Agents:
		left = agentsPane(st, cam, o, ew, height)
	case v != nil && !o.Explorer:
		left = outlinePane(v, o, ew, height)
	default:
		left = explorerPane(st, cam, o, ew, height, v != nil)
	}
	lines := slices.Clone(left)
	cols := columns(st, cam, o, rw, height)
	for i, r := range tiles(len(cols), rw, height) {
		for j, l := range workplace(st, cols[i], o, r.Dx(), r.Dy()) {
			lines[r.Min.Y+j] += l
		}
	}
	bars := Scrollbars(st, o, width, height)
	drawScrollbars(lines, bars)
	return strings.Join(lines, "\n"), bars
}

// leftWidth is the width of the left column, the same for the files and the agents panels.
func leftWidth(width int) int {
	return min(max(width/5, 18), 32, width/2)
}

// AgentAt is the agent a click at cell (x, y) of Render(st, o, width, height) falls on: a row of
// the agents panel, or the workplace of an agent on the right; "" for none.
func AgentAt(st *ide.State, o Options, width, height, x, y int) string {
	ew := leftWidth(width)
	switch {
	case y < 0 || y >= height || x < 0 || x >= width:
		return ""
	case x >= ew:
		if c, _, ok := columnAt(st, o, width, height, x, y); ok {
			return c.agent.Key
		}
		return ""
	case o.Panel != Agents || y == 0 || y == height-1:
		return ""
	}
	_, ih := inner(0, height)
	tree, top := agentsView(st, camera(st), o, ih)
	if i := top + y - 1; i < len(tree) && tree[i].agent != nil {
		return tree[i].agent.Key
	}
	return ""
}

// TreeAt is the explorer row a click at cell (x, y) of Render(st, o, width, height) falls on: its
// path, "" for none, and whether it is a folder. There is none while the outline of a spec is in
// place of the explorer.
func TreeAt(st *ide.State, o Options, width, height, x, y int) (path string, dir bool) {
	if o.Panel != Files || x < 0 || x >= leftWidth(width) || y < 1 || y >= height-1 || outlined(st, o, width, height) != nil {
		return "", false
	}
	_, ih := inner(0, height)
	if r := explorerTop(st, o, ih) + y - 1; r < len(st.Tree) {
		return st.Tree[r].Path, st.Tree[r].Dir
	}
	return "", false
}

// TextAt is the text of the pane at cell (x, y) of Render(st, o, width, height): the cells inside
// its frame, less the editor's line numbers and the air of a terminal, a browser or the agents
// panel. It is empty where there is no text: on a frame, in the gutter, in an empty editor.
func TextAt(st *ide.State, o Options, width, height, x, y int) image.Rectangle {
	ew := leftWidth(width)
	x0, w := 1, ew-2 // the explorer
	if o.Panel == Agents {
		x0, w = 2, textWidth(ew-2)
	}
	y0, y1 := 1, height-1
	if c, t, ok := columnAt(st, o, width, height, x, y); ok {
		iw, ih := inner(t.Dx(), t.Dy())
		x0, w, y0, y1 = t.Min.X+2, textWidth(iw), t.Min.Y+1, t.Max.Y-1
		if c.pane == ide.Editor { // a spec shown structured has the air of a terminal
			tab := shownTab(st, c.agent)
			if tab == nil {
				return image.Rectangle{}
			}
			if structured(st, c.agent, o, iw, ih) == nil {
				_, _, tw := editorText(tab, c.agent, iw)
				x0, w = t.Min.X+1+iw-tw, tw
			}
		}
	}
	r := image.Rect(x0, y0, x0+max(w, 0), max(y1, y0))
	if !image.Pt(x, y).In(r) {
		return image.Rectangle{}
	}
	return r
}

// camera is the agent the view follows: the one keyed st.Camera, else the first agent, else an
// idle stand-in so that a state without agents still draws.
func camera(st *ide.State) *ide.Agent {
	if a := findAgent(st, st.Camera); a != nil {
		return a
	}
	if len(st.Agents) > 0 {
		return st.Agents[0]
	}
	return &ide.Agent{}
}

func findAgent(st *ide.State, key string) *ide.Agent {
	for _, a := range st.Agents {
		if a.Key == key {
			return a
		}
	}
	return nil
}

// agentColor is the colour of a's palette slot; Dim for an unknown agent.
func agentColor(a *ide.Agent) color.Color {
	if a == nil {
		return theme.Dim
	}
	n := len(theme.Agents)
	return theme.Agents[(a.Color%n+n)%n]
}

// explorerPane draws the files in the left column, w×h cells; beside a spec its title offers the
// spec's outline in their place.
func explorerPane(st *ide.State, cam *ide.Agent, o Options, w, h int, outline bool) []string {
	iw, ih := inner(w, h)
	sel, lit := treeSel(st, cam), cam.Focus == ide.Explorer
	if st.Viewed != "" { // the user's pick while it fills the right column; the scroll stays put
		sel, lit = slices.IndexFunc(st.Tree, func(r ide.TreeRow) bool { return r.Path == st.Viewed }), true
	}
	rows := make([]string, ih)
	top := explorerTop(st, o, ih)
	for i := range rows {
		r := top + i
		if r >= len(st.Tree) {
			break
		}
		row := st.Tree[r]
		base := lipgloss.NewStyle()
		switch {
		case r == sel && lit:
			base = base.Background(theme.SelBg)
		case r == sel:
			base = base.Background(theme.SelBgDim)
		}
		indent := strings.Repeat(" ", 2*max(row.Depth, 0))
		// A directory's marker is its fold state; a file has none. A changed file gets a yellow
		// asterisk after it; a changed folder, a yellow name.
		marker, nc := "  ", fileTint(row.Path)
		if row.Dir {
			marker, nc = "▸ ", theme.Fg
			if row.Expanded {
				marker = "▾ "
			}
		}
		if strings.HasPrefix(row.Name, ".") || row.Ignored {
			nc = theme.Dim // hidden: .git, .env and the like; or ignored by git
		}
		room := iw - ansi.StringWidth(indent+marker)
		mark := ""
		switch {
		case !changed(st, row):
		case row.Dir:
			nc = theme.Yellow // an agent changed a file in it
		default:
			mark = " *" // an agent changed it
		}
		name := clip(row.Name, room-len(mark), "…") // the name gives way to the mark
		mark = clip(mark, room-ansi.StringWidth(name), "")
		pad := strings.Repeat(" ", max(0, room-ansi.StringWidth(name+mark)))
		rows[i] = base.Render(indent) + base.Foreground(theme.Dim).Render(marker) +
			base.Foreground(nc).Render(name) + base.Foreground(theme.Yellow).Render(mark) + base.Render(pad)
	}
	title := " " + fgStyle.Render(clip(filepath.Base(st.Root), iw, "")) + " "
	if outline {
		title = switchTitle(iw-1, "outline", "files")
	}
	return frame(w, h, theme.Border, title, rows)
}

// changed reports whether an agent changed the file of row r, or a file under its folder.
func changed(st *ide.State, r ide.TreeRow) bool {
	if !r.Dir {
		return st.Edited[r.Path]
	}
	for p, ok := range st.Edited {
		if ok && strings.HasPrefix(filepath.ToSlash(p), filepath.ToSlash(r.Path)+"/") {
			return true
		}
	}
	return false
}

// treeSel is the explorer row cam points at; -1 for none.
// explorerTop is the first row of the tree in an explorer ih rows high: near the camera's
// selection, or where the user scrolled.
func explorerTop(st *ide.State, o Options, ih int) int {
	return scrolled(scrollTop(treeSel(st, camera(st)), len(st.Tree), ih), o.Scroll[filesPane], max(0, len(st.Tree)-ih))
}

func treeSel(st *ide.State, cam *ide.Agent) int {
	if cam.TreeSel == "" {
		return -1
	}
	return slices.IndexFunc(st.Tree, func(r ide.TreeRow) bool { return r.Path == cam.TreeSel })
}

// fileTint is the colour of a file's name in the explorer: its type's colour as a pastel,
// changed or not; an unknown type is as plain as a folder.
func fileTint(path string) color.Color {
	if t := fileColor(path); t != nil {
		return theme.Mix(t, theme.Bright, 0.4)
	}
	return theme.Fg
}

// termPrompt is every terminal's prompt: the pane's title tells whose it is.
const termPrompt = "$ "

func terminalPane(st *ide.State, cam *ide.Agent, o Options, w, h int) []string {
	fw, ih := inner(w, h)
	iw := textWidth(fw)
	t := &cam.Term
	prompt := lipgloss.NewStyle().Foreground(agentColor(cam)).Render(clip(termPrompt, iw, ""))
	back := o.Scroll[termKey(cam.Key)]
	layout := o.terminalLayout(t, iw)
	back = min(max(back, 0), max(0, layout.total-ih))
	// A row of a line, styled only once it is in the window: scrolled far back, styling every row
	// below it would cost a frame its time.
	type row struct {
		line *ide.TermLine // nil for the live prompt line
		j    int           // the row's place among its line's
		at   int           // the runes of its line before it
		text string
	}
	var rows []row
	// Skip the rows below the window before wrapping any text. Keep the live row as the last
	// placeholder; it is outside the shown range when scrolled back.
	end, skipped := len(t.Lines), 0
	for end > 0 && skipped+layout.rows[end-1] < back {
		end--
		skipped += layout.rows[end]
	}
	back -= skipped
	start, need := end, max(0, ih-1+back)
	for start > 0 && need > 0 {
		start--
		need -= layout.rows[start]
	}
	lines := t.Lines[start:end]
	for i := range lines {
		l := &lines[i]
		pw := 0
		if l.Kind == ide.Command || l.Kind == ide.More {
			pw = ansi.StringWidth(prompt)
		}
		at := 0
		for j, text := range wrap(l.Text, iw-pw, iw) {
			rows = append(rows, row{l, j, at, text})
			at += utf8.RuneCountInString(text)
		}
	}
	live := fgStyle.Render(spinner[t.Spinner%len(spinner)])
	if ran := runTime(t, o.Now); ran != "" { // claude brings the output only once the command is over
		live += " " + dimStyle.Render(ran)
	}
	if !t.Running {
		// The caret is a solid block while its agent types, a spin frame while it works otherwise,
		// else none; in its colour.
		var c string
		switch {
		case typing(cam, o):
			c = lipgloss.NewStyle().Background(agentColor(cam)).Render(" ")
		case working(cam, o):
			c = lipgloss.NewStyle().Foreground(agentColor(cam)).Render(spinFrame(o.Now))
		}
		part := ide.Command
		if n := len(t.Lines); n > 0 && (t.Lines[n-1].Kind == ide.Command || t.Lines[n-1].Kind == ide.More) {
			part = ide.More // the input goes on with the command above
		}
		live = lead(prompt, part) + commandLine(tail(t.Input, iw-ansi.StringWidth(prompt)-1), t.InputLang) + c
	}
	rows = append(rows, row{})
	back = min(back, max(0, len(rows)-ih)) // as far as the scrollback goes
	var shown []string
	for _, r := range rows[max(0, len(rows)-ih-back) : len(rows)-back] {
		text := r.text
		switch {
		case r.line == nil:
			text = live
		case r.line.Kind == ide.Command || r.line.Kind == ide.More:
			text = commandLine(text, r.line.Lang)
			if r.j == 0 {
				text = lead(prompt, r.line.Kind) + text
			}
		case r.line.Kind == ide.Comment:
			text = dimStyle.Render(text)
		case r.line.Kind == ide.ErrorOutput:
			text = errStyle.Render(text)
		case r.line.Kind == ide.Added:
			text = diffLine(text, r.j == 0, r.line.Lang, gitAdd)
		case r.line.Kind == ide.Removed:
			text = diffLine(text, r.j == 0, r.line.Lang, gitDel)
		case r.line.Kind == ide.Hunk:
			text = hunkStyle.Render(text)
		case r.line.Kind == ide.Meta && (strings.HasPrefix(r.line.Text, "diff ") || strings.HasPrefix(r.line.Text, "commit ")):
			text = headStyle.Render(text)
		case r.line.Kind == ide.Meta:
			text = dimStyle.Render(text)
		case r.line.Kind == ide.Code:
			text = codeLine(st, text, r.line)
		case r.line.Kind == ide.Match:
			text = hitLine(st, text, r.j == 0, r.line)
		case r.line.Kind == ide.Status:
			text = statusLine(st, text, r.j == 0)
		case r.line.Kind == ide.Stat:
			text = statLine(st, text, r.at, r.line.Text)
		default:
			text = outputLine(st, text, r.line.Text)
		}
		shown = append(shown, text)
	}
	// Tail-aligned: blank rows on top, newest line at the bottom.
	out := append(make([]string, ih-len(shown), ih), airy(shown)...)
	head := title(st, cam, func(int) string { return "terminal" }, fw-3)
	return frame(w, h, theme.Border, " "+head+" ", out)
}

// runShown is how long a command runs before the terminal shows for how long.
const runShown = 3 * time.Second

// runTime is how long the running command of t has run by now, as the status line counts it:
// "7s", "1m05s", "1h02m"; "" for the first runShown, or when its start is unknown.
func runTime(t *ide.Term, now time.Time) string {
	d := now.Sub(t.RunAt).Truncate(time.Second)
	switch {
	case !t.Running || t.RunAt.IsZero() || d < runShown:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", d/time.Second)
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", d/time.Minute, d%time.Minute/time.Second)
	}
	return fmt.Sprintf("%dh%02dm", d/time.Hour, d%time.Hour/time.Minute)
}

// commandLine draws text, typed at a prompt, in the colours of what it is: the shell's, or those of
// the language a here-document feeds (lang, a file langOf knows); what has no colour of its own
// white, as typed.
func commandLine(text, lang string) string {
	l := shLang
	if lang != "" {
		l = langOf(lang)
	}
	rs := []rune(text)
	ks := highlight(rs, l)
	fg := make([]color.Color, len(rs))
	for i, k := range ks {
		if k == plain {
			fg[i] = theme.Bright
		}
	}
	return paintRunes(rs, ks, fg)
}

// gitAdd and gitDel are git's colours of what is staged or added, and of what is not or removed.
var (
	gitAdd = lipgloss.NewStyle().Foreground(theme.Green)
	gitDel = lipgloss.NewStyle().Foreground(theme.Red)
)

// diffLine draws text, a row of a diff's changed line, on the terminal's own background: on its
// first row the sign in git's colour of the change (sign), then the code in the colours of lang.
func diffLine(text string, first bool, lang string, sign lipgloss.Style) string {
	if !first || text == "" {
		return paint(text, langOf(lang))
	}
	_, n := utf8.DecodeRuneInString(text)
	return sign.Render(text[:n]) + paint(text[n:], langOf(lang))
}

// statusLine draws text, a row of a line of git status --short: on its first row the status as git
// colours it, the staged column green and the unstaged one red (untracked, ignored and unmerged
// red both); then the path as plain output.
func statusLine(st *ide.State, text string, first bool) string {
	rs := []rune(text)
	if !first || len(rs) < 2 {
		return outputLine(st, text, "")
	}
	x, y := gitAdd, gitDel
	if xy := string(rs[:2]); strings.ContainsAny(xy, "?!U") || xy == "AA" || xy == "DD" {
		x = gitDel
	}
	return x.Render(string(rs[0])) + y.Render(string(rs[1])) + outputLine(st, string(rs[2:]), "")
}

// statLine draws text, the row of the diffstat line that starts at its drawn rune at, as git
// colours it: the bar's "+" green, its "-" red; the rest as plain output.
func statLine(st *ide.State, text string, at int, line string) string {
	rs := []rune(text)
	bar := min(max(utf8.RuneCountInString(drawn(strings.TrimRight(line, "+-")))-at, 0), len(rs))
	out := outputLine(st, string(rs[:bar]), "")
	for i := bar; i < len(rs); {
		j := i + 1
		for j < len(rs) && rs[j] == rs[i] {
			j++
		}
		switch s := string(rs[i:j]); rs[i] {
		case '+':
			out += gitAdd.Render(s)
		case '-':
			out += gitDel.Render(s)
		default: // the "…" of a row cut short
			out += outputLine(st, s, "")
		}
		i = j
	}
	return out
}

// lead is what goes before a command's line of kind k: the prompt on its first, as much room on
// those after it.
func lead(prompt string, k ide.LineKind) string {
	if k == ide.More {
		return strings.Repeat(" ", ansi.StringWidth(prompt))
	}
	return prompt
}

// inner is the space inside a w×h frame.
func inner(w, h int) (int, int) { return max(0, w-2), max(0, h-2) }

// textWidth is the room for the text of a pane iw cells wide inside its frame that has no gutter
// of its own (a terminal, a browser, the agents): a cell of air on either side keeps it off the
// frame. airy puts the left one before rows; the frame pads the right.
func textWidth(iw int) int { return max(0, iw-2) }

func airy(rows []string) []string {
	for i := range rows {
		rows[i] = " " + rows[i]
	}
	return rows
}

// frame draws body in a rounded border of colour bc, exactly w×h cells, with title on the top edge.
func frame(w, h int, bc color.Color, title string, body []string) []string {
	if h <= 0 {
		return nil
	}
	bs := lipgloss.NewStyle().Foreground(bc)
	b := lipgloss.RoundedBorder()
	iw, ih := inner(w, h)
	top := bs.Render(b.TopLeft + strings.Repeat(b.Top, iw) + b.TopRight)
	if iw > 0 {
		title = truncate(title, iw-1)
		top = bs.Render(b.TopLeft+b.Top) + title + bs.Render(strings.Repeat(b.Top, iw-1-ansi.StringWidth(title))+b.TopRight)
	}
	out := []string{fit(top, w)}
	for i := range ih {
		var line string
		if i < len(body) {
			line = body[i]
		}
		out = append(out, fit(bs.Render(b.Left)+fit(line, iw)+bs.Render(b.Right), w))
	}
	if h > 1 {
		out = append(out, fit(bs.Render(b.BottomLeft+strings.Repeat(b.Bottom, iw)+b.BottomRight), w))
	}
	return out
}

// fit pads or truncates s to exactly w cells.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = truncate(s, w)
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// truncate cuts s to at most w cells. ansi.Truncate alone can leave a cluster such as the keycap
// "1️⃣" one cell wider than ansi.StringWidth allows, so it cuts again until the text fits.
func truncate(s string, w int) string {
	for n := w; ansi.StringWidth(s) > w; n-- {
		s = ansi.Truncate(s, n, "")
	}
	return s
}

// scrollTop is the first visible row of n rows in a window of vis rows that keeps cur in its upper third.
func scrollTop(cur, n, vis int) int {
	return min(max(cur-vis/3, 0), max(0, n-vis))
}

// cell is one grapheme cluster as drawn.
type cell struct {
	s      string // what to print
	w      int    // width in terminal cells
	r0, r1 int    // rune range [r0, r1) in the source text
}

// Undrawn reports whether drawn text goes without r: the runes that widen the character before them
// in some terminals and not in others (emoji presentation selectors, the zero-width joiner, skin
// tones), so that the layout, the renderer and the terminal count the same cells; and the regional
// indicators and tags that make flags.
func Undrawn(r rune) bool {
	return r == 0xfe0e || r == 0xfe0f || r == 0x200d || 0x1f3fb <= r && r <= 0x1f3ff ||
		0x1f1e6 <= r && r <= 0x1f1ff || 0xe0000 <= r && r <= 0xe007f
}

// layout splits s into cells: tabs become 4 spaces; escape sequences and other control characters
// vanish, so do Undrawn runes; invalid UTF-8 shows as U+FFFD. It stops once the cells are wider
// than maxW and cover more than minRunes runes, so huge lines stay cheap, and returns how many
// bytes of s it consumed.
func layout(s string, maxW, minRunes int) ([]cell, int) {
	var cs []cell
	var state byte
	x, r, n := 0, 0, 0
	for n < len(s) && (x <= maxW || r <= minRunes) {
		g, w, size, next := ansi.DecodeSequence(s[n:], state, nil)
		state = next
		c := cell{s: g, w: w, r0: r, r1: r + utf8.RuneCountInString(g)}
		switch first, _ := utf8.DecodeRuneInString(g); {
		case first == '\t':
			c.s, c.w = "    ", 4
		case unicode.IsControl(first):
			c.s, c.w = "", 0
		case !utf8.ValidString(g) || strings.ContainsFunc(g, Undrawn):
			c.s = strings.Join(strings.FieldsFunc(strings.ToValidUTF8(g, "�"), Undrawn), "")
			c.w = ansi.StringWidth(c.s)
		}
		cs = append(cs, c)
		x, r, n = x+c.w, c.r1, n+max(size, 1)
	}
	return cs, n
}

// drawn is the text of s as its cells draw it.
func drawn(s string) string { return clip(s, math.MaxInt, "") }

// clip renders s as plain one-line text at most w cells wide; text that does not fit ends with more.
func clip(s string, w int, more string) string {
	cs, n := layout(s, w, 0)
	total := 0
	for _, c := range cs {
		total += c.w
	}
	if n == len(s) && total <= w {
		return join(cs)
	}
	room := w - ansi.StringWidth(more)
	if room < 0 {
		return ""
	}
	x, i := 0, 0
	for ; i < len(cs) && x+cs[i].w <= room; i++ {
		x += cs[i].w
	}
	return join(cs[:i]) + more
}

// maxWrap is how many rows one terminal line takes at most.
const maxWrap = 4

// wrap cuts s into plain rows as a terminal wraps a long line: the first row first cells wide,
// the others w. Past maxWrap rows the text is cut with "…".
func wrap(s string, first, w int) []string {
	// Tabs take four cells even in a narrower pane; include one cell beyond the visible rows.
	cs, _ := layout(s, max(first, 4)+(maxWrap-1)*max(w, 4), 0)
	var rows []string
	room, x, start := max(first, 1), 0, 0
	for i, c := range cs {
		if x+c.w > room && i > start {
			if len(rows) == maxWrap-1 {
				return append(rows, clip(join(cs[start:]), room, "…"))
			}
			rows = append(rows, join(cs[start:i]))
			start, x, room = i, 0, max(w, 1)
		}
		x += c.w
	}
	return append(rows, join(cs[start:]))
}

// wrappedRows counts ordinary log lines without allocating their wrapped text.
func wrappedRows(s string, first, w int) int {
	first, w = max(first, 1), max(w, 1)
	// Once the fourth row starts, the rest cannot change the count.
	for i := 0; i < min(len(s), first+(maxWrap-2)*w+1); i++ {
		if s[i] < ' ' || s[i] > '~' {
			return len(wrap(s, first, w))
		}
	}
	if len(s) <= first {
		return 1
	}
	return min(maxWrap, 2+(len(s)-first-1)/w)
}

// tail renders s as plain one-line text, dropping cells on the left until it is at most w cells wide.
func tail(s string, w int) string {
	cs, _ := layout(s, math.MaxInt, 0)
	x := 0
	for _, c := range cs {
		x += c.w
	}
	i := 0
	for ; i < len(cs) && x > w; i++ {
		x -= cs[i].w
	}
	return join(cs[i:])
}

func join(cs []cell) string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.s)
	}
	return b.String()
}
