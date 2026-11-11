package ui

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/theme"
)

var drawSpinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// styleYou is the colour of the user's prompts, as Claude Code labels them; claude's replies are white.
var styleYou = lipgloss.NewStyle().Foreground(theme.Blue)

// styleBash is the colour of what a Bash call of main's says it does: the green of strings in code.
var styleBash = lipgloss.NewStyle().Foreground(theme.String)

// styleBang marks the shell mode of the prompt, a "!" first, pink as in Claude Code.
var styleBang = lipgloss.NewStyle().Foreground(theme.Pink)

// chatCache keeps the drawn lines of each chat entry, so a frame redraws only the entries that changed.
type chatCache struct {
	width   int
	entries []ChatEntry // each entry as it was when drawn
	lines   [][]string
	marks   []chatMarks // the paths in each entry's lines
	paths   *pathCheck  // finds them; nil underlines none
}

// draw returns the lines of entry i, drawing it again only when it changed.
func (c *chatCache) draw(i int, e ChatEntry, w int) []string {
	if w != c.width {
		*c = chatCache{width: w, paths: c.paths}
	}
	for len(c.entries) <= i {
		c.entries = append(c.entries, ChatEntry{Kind: -1}) // never equal to a real entry
		c.lines = append(c.lines, nil)
		c.marks = append(c.marks, chatMarks{})
	}
	if c.entries[i] != e {
		c.entries[i], c.lines[i] = e, drawEntry(e, w)
		c.marks[i] = chatMarks{raw: c.lines[i]}
	}
	c.lines[i] = c.paths.mark(e, &c.marks[i], c.lines[i])
	return c.lines[i]
}

// drawChatTime is the room at the right of the chat for the time of a message, "  15:04"; a chat
// narrower than drawChatTimeMin shows no times.
const (
	drawChatTime    = 7
	drawChatTimeMin = 30
)

// drawChatWidths are the cells of a width-wide chat row for the text of an entry and for its time.
func drawChatWidths(width int) (text, at int) {
	if width < drawChatTimeMin {
		return max(1, width-4), 0
	}
	return width - 4 - drawChatTime, drawChatTime
}

// drawChatCount is how many lines the conversation takes width cells wide.
func drawChatCount(cache *chatCache, entries []ChatEntry, width int) int {
	tw, _ := drawChatWidths(width)
	lines, _, _ := drawChatLines(cache, entries, tw, 0)
	return len(lines)
}

// drawChatLines are the lines of the conversation tw cells wide, and beside each the time its
// entry was said when it is the entry's first and aw leaves room for it, and the index of the
// entry it draws (-1 for none). A blank line sets the user's prompts apart from what claude said
// before them; claude's caret stands in its place. An answer to a popup's question sticks to the
// question instead, and the blank line goes after it.
func drawChatLines(cache *chatCache, entries []ChatEntry, tw, aw int) (lines, times []string, owners []int) {
	prev := ChatEntry{Kind: UserEntry}
	for i, e := range entries {
		drawn := cache.draw(i, e, tw)
		if len(drawn) == 0 {
			continue
		}
		if _, goal := chatGoal(e); goal {
			e.Kind = UserEntry // use prompt spacing without changing the stored entry
		}
		prompt := e.Kind == UserEntry && !e.Answer && prev.Kind != UserEntry && prev.Kind != CaretEntry
		if (prompt || prev.Answer) && len(lines) > 0 {
			lines, times, owners = append(lines, ""), append(times, ""), append(owners, -1)
		}
		prev = e
		for j, l := range drawn {
			at := ""
			if j == 0 && aw > 0 && !e.At.IsZero() {
				at = styleDim.Render(e.At.Format("15:04"))
			}
			lines, times, owners = append(lines, l), append(times, at), append(owners, i)
		}
	}
	return lines, times, owners
}

// renderChat draws the tail of the conversation, scrolled up by scroll lines, into exactly width×height cells:
// each entry with the time it was said grey at the right of its first line. maxScroll is the largest
// scroll that still shows something new; shown is the index of the entry each row draws, -1 for none.
func renderChat(cache *chatCache, entries []ChatEntry, width, height, scroll int) (out string, maxScroll int, shown []int) {
	tw, aw := drawChatWidths(width)
	lines, times, owners := drawChatLines(cache, entries, tw, aw)

	maxScroll = max(0, len(lines)-height)
	scroll = min(max(scroll, 0), maxScroll)
	end := len(lines) - scroll
	from := max(0, end-height)
	rows, shown := make([]string, height), make([]int, height)
	top := height - (end - from) // tail-aligned: short logs sit right above the input
	for i := range rows {
		var l string
		shown[i] = -1
		if i >= top {
			l, shown[i] = "  "+lines[from+i-top], owners[from+i-top]
			if at := times[from+i-top]; at != "" {
				l = "  " + drawFit(lines[from+i-top], tw) + "  " + at
			}
		}
		rows[i] = drawFit(l, width)
	}
	return strings.Join(rows, "\n"), maxScroll, shown
}

// drawEntry renders one chat entry into styled lines of at most w cells: a prompt as typed (grey
// while queued), a reply as markdown, the CLI's own reply as laid out, all after a "›". No line is
// blank: every line of the chat says something.
func drawEntry(e ChatEntry, w int) []string {
	goal, isGoal := chatGoal(e)
	if isGoal {
		e.Kind, e.Text = UserEntry, goal
	}
	switch e.Kind {
	case UserEntry:
		st, arrow := styleYou, "› "
		if isGoal {
			st = lipgloss.NewStyle().Foreground(theme.Orange)
		}
		switch {
		case e.Queued && !isGoal:
			st = styleDim
		case e.Cut:
			st, arrow = styleErr, "✗ "
		}
		lines := drawDense(drawWrap(e.Text, w-2))
		for i, l := range lines {
			prefix := "  "
			if i == 0 {
				prefix = arrow
			}
			lines[i] = st.Render(prefix + l)
		}
		return lines
	case AssistantEntry:
		if strings.TrimSpace(e.Text) == "" {
			return nil // a text block that has not streamed anything yet
		}
		var lines []string
		if e.Bash {
			lines = drawStyled(drawDense(drawWrap(e.Text, w-2)), styleBash.Render)
		} else {
			lines = drawDense(drawMarkdown(e.Text, w-2))
		}
		for i, l := range lines {
			gutter := "  "
			if i == 0 {
				gutter = styleReply.Render("› ")
			}
			lines[i] = gutter + l
		}
		return lines
	case CommandEntry:
		if e.Bang != "" {
			return drawBang(e, w)
		}
		lines := drawDense(drawCommand(e.Text, w-2))
		for i, l := range lines {
			gutter := "  "
			if i == 0 {
				gutter = styleReply.Render("› ")
			}
			lines[i] = gutter + l
		}
		return lines
	case CaretEntry:
		// Under where claude's next words go, in main's colour: it is the first agent.
		return []string{"  " + lipgloss.NewStyle().Foreground(drawAgentColor(0)).Render(e.Text)}
	}
	return nil
}

// drawBang renders a ! command as typed after a "!", then what it printed under a "⎿", as Claude
// Code shows them; "running…" until it ends.
func drawBang(e ChatEntry, w int) []string {
	lines := drawDense(drawWrap(e.Bang, w-2))
	for i, l := range lines {
		prefix := "  "
		if i == 0 {
			prefix = "! "
		}
		lines[i] = styleYou.Render(prefix + l)
	}
	out := drawDense(drawWrap(e.Text, w-4))
	st := styleFg
	if e.Text == "" {
		out, st = []string{"running…"}, styleDim
	}
	for i, l := range out {
		gutter := "    "
		if i == 0 {
			gutter = "  ⎿ "
		}
		lines = append(lines, styleDim.Render(gutter)+st.Render(l))
	}
	return lines
}

func chatGoal(e ChatEntry) (string, bool) {
	if e.Kind != CommandEntry || e.Bang != "" || !strings.Contains(e.Text, "Goal set:") {
		return "", false
	}
	text, ok := strings.CutPrefix(strings.TrimSpace(drawClean(e.Text)), "Goal set:")
	return tidy(text), ok
}

// drawDense drops the blank lines.
func drawDense(lines []string) []string {
	return slices.DeleteFunc(lines, func(l string) bool { return strings.TrimSpace(ansi.Strip(l)) == "" })
}

func drawStyled(lines []string, render func(...string) string) []string {
	for i, l := range lines {
		lines[i] = render(l)
	}
	return lines
}

// renderInput draws the prompt field, one line of exactly width cells. cursorX is the cursor column within it.
// A dimmed field does not have the keys.
func renderInput(in *Input, width int, placeholder string, dim bool) (out string, cursorX int) {
	if width <= 0 {
		return "", 0
	}
	prompt, text := styleYou.Render("› "), styleFg
	if dim {
		prompt, text = styleDim.Render("› "), styleDim
	}
	runes, cursor := in.Text, in.Cursor
	if bangInput(runes) { // the shell mode: its "!" stands in for the "›"
		prompt, runes, cursor = styleBang.Render("! "), runes[1:], max(0, cursor-1)
		if dim {
			prompt = styleDim.Render("! ")
		}
		placeholder = "a shell command, run in the project"
	}
	if len(runes) == 0 {
		return drawFit(prompt+styleDim.Render(placeholder), width), min(2, width-1)
	}
	disp, cur := drawInputRunes(runes, cursor)
	start := drawScroll(disp, cur, width-2)
	shown := ansi.Truncate(string(disp[start:]), max(0, width-2), "")
	x := 2 + ansi.StringWidth(string(disp[start:cur]))
	return drawFit(prompt+text.Render(shown), width), min(x, width-1)
}

// drawInputRunes is what a field shows of its runes, and where its cursor falls among them: '↵'
// for newlines, ' ' for other controls, no ideview.Undrawn runes.
func drawInputRunes(text []rune, cursor int) ([]rune, int) {
	out, cur := make([]rune, 0, len(text)), 0
	for i, r := range text {
		switch {
		case ideview.Undrawn(r):
			continue
		case r == '\n':
			r = '↵'
		case unicode.IsControl(r):
			r = ' '
		}
		if i < cursor {
			cur++
		}
		out = append(out, r)
	}
	return out, cur
}

// drawScroll returns the first rune to show so that the text before cur and the caret cell fit in avail cells.
func drawScroll(r []rune, cur, avail int) int {
	caret := 1
	if cur < len(r) {
		caret = max(1, ansi.StringWidth(string(r[cur])))
	}
	// Walk back rune by rune, then correct for grapheme clusters, which StringWidth measures as a whole.
	start, w := cur, 0
	for start > 0 {
		w += ansi.StringWidth(string(r[start-1]))
		if w+caret > avail {
			break
		}
		start--
	}
	for start < cur && ansi.StringWidth(string(r[start:cur]))+caret > avail {
		start++
	}
	return start
}

// renderStatus draws the status line with the key hints that fit, one line of exactly width cells:
// the model, the mode, the limits, the context, then what claude does, which changes size often,
// last, so that the rest stays put.
func renderStatus(st Status, hints []string, width int) string {
	dot := styleLime.Render("●")
	switch {
	case st.State == "exited":
		dot = styleErr.Render("●")
	case st.State == "starting":
		dot = styleAccent.Render(drawSpinner[st.Frame%len(drawSpinner)])
	case st.Busy:
		dot = styleAccent.Render("●")
	}
	// The steady part, each with the turn it goes in when the screen is too narrow for it and
	// drawStatusMoving cells of the rest: what claude does says more. The mode never goes.
	type part struct {
		s    string
		drop int
	}
	var parts []part
	if st.Model != "" {
		model := styleFg.Render(drawOneLine(st.Model))
		if st.Effort != "" {
			model += styleDim.Render(" " + drawOneLine(st.Effort))
		}
		parts = append(parts, part{model, 1})
	}
	if mode := drawMode(st.Mode); mode != "" {
		parts = append(parts, part{mode, 0})
	}
	if st.Usage != "" {
		parts = append(parts, part{drawUsage(st.Usage), 3})
	}
	if st.Context > 0 {
		parts = append(parts, part{drawContext(st.Context), 2})
	}
	if st.Replay {
		parts = append(parts, part{styleFg.Render("replay"), 4})
	}
	if st.Tasks > 0 {
		label := "tasks"
		if st.Tasks == 1 {
			label = "task"
		}
		parts = append(parts, part{lipgloss.NewStyle().Foreground(theme.Blue).Render(fmt.Sprintf("%d %s", st.Tasks, label)), 0})
	}
	var steady, moving []string
	for turn := 1; ; turn++ {
		steady = steady[:0]
		for _, p := range parts {
			steady = append(steady, p.s)
		}
		if turn > 4 || 2+ansi.StringWidth(strings.Join(steady, " · "))+3+drawStatusMoving <= width-4 {
			break
		}
		parts = slices.DeleteFunc(parts, func(p part) bool { return p.drop == turn })
	}
	spin := styleAccent.Render(drawSpinner[st.Frame%len(drawSpinner)]) + " "
	state := drawOneLine(st.State)
	if state == "idle" && (st.Tasks > 0 || st.Wakeup != "") {
		state = "waiting"
		if st.Wakeup != "" {
			state += " " + st.Wakeup
		}
	}
	switch {
	case st.Busy:
		s := spin + drawShimmer(state, st.Frame, theme.Fg)
		if st.Elapsed != "" {
			s += styleDim.Render(" " + st.Elapsed)
		}
		moving = append(moving, s)
	case state != "" && st.Alert:
		moving = append(moving, styleErr.Render(state))
	case state != "":
		moving = append(moving, styleFg.Render(state))
	}
	sep := styleDim.Render(" · ")
	join := func() string { return dot + " " + strings.Join(slices.Concat(steady, moving), sep) }

	// The hints that fit beside the steady part and drawStatusMoving cells of the rest: they do not
	// come and go as the rest changes size. The rest is cut to the room they leave. A copy just
	// made has its note there instead, light blue, fading.
	hintStyle := styleDim
	if st.Noted > 0 {
		hints, hintStyle = []string{drawNoted}, lipgloss.NewStyle().Foreground(theme.Mix(theme.Bg, copiedColor, st.Noted))
	}
	inner := width - 4
	budget := ansi.StringWidth(dot + " " + strings.Join(steady, sep))
	if len(moving) > 0 {
		budget += 3 + drawStatusMoving
	}
	for len(hints) > 0 && budget+2+ansi.StringWidth(strings.Join(hints, " · ")) > inner {
		hints = hints[1:]
	}
	room := inner
	if len(hints) > 0 {
		room -= 2 + ansi.StringWidth(strings.Join(hints, " · "))
	}
	// What claude does stays, cut only as a last resort.
	line := join()
	for len(moving) > 1 && ansi.StringWidth(line) > room {
		moving = moving[:len(moving)-1]
		line = join()
	}
	if ansi.StringWidth(line) > room {
		line = ansi.Truncate(line, max(0, room), "…")
	}
	if len(hints) > 0 {
		h := strings.Join(hints, " · ")
		line += strings.Repeat(" ", max(0, inner-ansi.StringWidth(line)-ansi.StringWidth(h))) + hintStyle.Render(h)
	}
	return drawFit("  "+line, width)
}

// drawStatusMoving is the room the status line keeps for what changes size often.
const drawStatusMoving = 30

// drawUsage draws the use of each rate-limit window in Status.Usage ("5h 9% 4h12m · 7d 58% fable
// 3% 2d8h"): the windows and the resets grey, each use coloured by drawFull; a window at its limit
// ("5h limit until 13:30") red, and extra usage paying past it yellow.
func drawUsage(usage string) string {
	parts := strings.Split(usage, " · ")
	for i, p := range parts {
		name, rest, _ := strings.Cut(p, " ")
		use, _, _ := strings.Cut(rest, " ")
		switch _, err := strconv.Atoi(strings.TrimSuffix(use, "%")); {
		case strings.HasSuffix(use, "%") && err == nil:
			var b strings.Builder
			for words := strings.Fields(p); len(words) > 0; { // "7d 58% fable 3% 2d8h": name and use in pairs, then the reset
				if len(words) == 1 {
					b.WriteString(styleDim.Render(" " + words[0]))
					break
				}
				pct, _ := strconv.Atoi(strings.TrimSuffix(words[1], "%"))
				name := words[0] + " "
				if b.Len() > 0 {
					name = " " + name
				}
				b.WriteString(styleDim.Render(name) + drawFull(pct).Render(words[1]))
				words = words[2:]
			}
			parts[i] = b.String()
		case strings.HasPrefix(rest, "limit "):
			parts[i] = styleDim.Render(name+" ") + styleErr.Render(rest)
		case p == drawExtraUsage:
			parts[i] = styleModeAuto.Render(p)
		default:
			parts[i] = styleFg.Render(p)
		}
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

// drawExtraUsage ends Status.Usage while extra usage pays past a window's limit.
const drawExtraUsage = "extra usage"

// drawContext is how full main's context is, in few cells: "ctx" grey, the percentage coloured
// by drawFull.
func drawContext(f float64) string {
	pct := int(math.Round(min(max(f, 0), 1) * 100))
	return styleDim.Render("ctx ") + drawFull(pct).Render(fmt.Sprintf("%d%%", pct))
}

// drawFull is the colour of a share used up: as the auto mode from 80%, in Claude's own orange
// from 90%.
func drawFull(pct int) lipgloss.Style {
	switch {
	case pct >= 90:
		return styleAccent
	case pct >= 80:
		return styleModeAuto
	}
	return styleFg
}

// The colours of the permission modes in Claude Code's dark theme.
var (
	styleModePlan   = lipgloss.NewStyle().Foreground(theme.Teal)   // Claude Code's planMode
	styleModeAccept = lipgloss.NewStyle().Foreground(theme.Violet) // autoAccept
	styleModeAuto   = lipgloss.NewStyle().Foreground(theme.Yellow) // warning
	styleModeBypass = lipgloss.NewStyle().Foreground(theme.Red)    // error
)

// drawMode labels a permission mode as Claude Code does; the default mode shows nothing.
func drawMode(mode string) string {
	switch mode {
	case "":
		return ""
	case "default", "manual": // claude asks before what may change something
		return styleFg.Render("⏵ manual mode")
	case "plan":
		return styleModePlan.Render("⏸ plan mode")
	case "acceptEdits":
		return styleModeAccept.Render("⏵⏵ accept edits")
	case "auto":
		return styleModeAuto.Render("⏵⏵ auto mode")
	case "bypassPermissions":
		return styleModeBypass.Render("⏵⏵ bypass permissions")
	case "dontAsk":
		return styleModeBypass.Render("⏵⏵ don't ask")
	}
	return styleAccent.Render("⏵⏵ " + drawOneLine(mode))
}
