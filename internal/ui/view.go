package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/theme"
)

var (
	styleFg     = lipgloss.NewStyle().Foreground(theme.Fg)
	styleDim    = lipgloss.NewStyle().Foreground(theme.Dim)
	styleAccent = lipgloss.NewStyle().Foreground(theme.Accent)
	styleOK     = lipgloss.NewStyle().Foreground(theme.OK)
	styleErr    = lipgloss.NewStyle().Foreground(theme.Err)
	styleLime   = lipgloss.NewStyle().Foreground(theme.Lime)
	styleStrong = lipgloss.NewStyle().Foreground(theme.Strong)
	styleCaret  = lipgloss.NewStyle().Reverse(true)
)

// Key hints for the status line, by what has the keys; the first ones go when space runs out.
var (
	drawHintsPrompt  = []string{"shift+tab mode", "/ commands", "F1 help", "esc x2 quit"}
	drawHintsBusy    = []string{"shift+tab mode", "F1 help", "esc interrupt"}
	drawHintsQueued  = []string{"shift+tab mode", "F1 help", "↑ edit queued", "esc interrupt"}
	drawHintTab      = "tab agents" // after the mode's, while the agents panel is there to go to
	drawHintsSuggest = []string{"↑/↓ pick", "tab complete", "enter run", "esc clear"}
	drawHintsHelp    = []string{"esc close"}
	drawHintsPanel   = []string{"↑/↓ select", "enter pin", "alt+0 auto", "tab back"}
	drawHintsBang    = []string{"! for shell mode"}
)

func (m *Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.ReportFocus = true // for Away
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle, v.ProgressBar = drawTitle(m.Status, m.Popup != nil)
	w, h := m.Width, m.Height
	m.zones, m.sel.text = nil, ""
	if m.tooSmall() {
		m.onDrag = nil
		v.SetContent(drawTooSmall(w, h))
		return v
	}

	entries := m.chatEntries()
	m.chatLines.paths = m.paths // the paths its entries name underline
	ideH, chatH := drawLayout(h, drawChatCount(&m.chatLines, entries, w))
	if m.inspection != nil && h >= drawIDEH {
		if m.inspection.height < 0 {
			m.inspection.height = ideH
		}
		ideH = min(m.inspection.height, h-4)
		chatH = h - ideH - 4
	}
	m.ideHeight = ideH
	st := m.ideState()
	var top string
	hints := drawHintsPrompt
	switch {
	case m.canInterrupt() && len(m.pending) > 0 && len(m.Input.Text) == 0:
		hints = drawHintsQueued
	case m.canInterrupt():
		hints = drawHintsBusy
	}
	if m.Panel == ideview.Agents {
		hints = slices.Insert(slices.Clone(hints), 1, drawHintTab)
	}
	o := ideview.Options{Panel: m.Panel, Focused: m.PanelFocus, Sel: m.PanelSel, Now: m.Now, Busy: m.Status.Busy, Wrap: m.Wrap, Scroll: m.scroll, Terms: m.termCache}
	if m.inspection != nil {
		o.Now, o.Busy = m.inspection.frame, m.inspection.busy
	}
	switch {
	case ideH == 0: // too low for the IDE: the conversation alone
		clear(m.scroll)
		clear(m.termCache)
	default:
		ideview.Prune(st, o, w, ideH)
		frame, bars := ideview.RenderWithScrollbars(st, o, w, ideH)
		top = frame
		m.zones = append(m.zones, zone{w: w, h: ideH,
			click: func(x, y int) tea.Cmd {
				path, dir := ideview.TreeAt(st, o, w, ideH, x, y)
				switch {
				case dir:
					m.Sim.Toggle(path)
					if m.inspection != nil {
						m.inspection.state.Toggle(path)
					}
				case path != "":
					m.viewFile(path)
				}
				return m.clickIDE(ideview.AgentAt(st, o, w, ideH, x, y))
			},
			wheel: func(x, y, n int) { m.scrollIDE(ideview.Wheel(st, o, w, ideH, x, y, n)) },
			text:  func(x, y int) image.Rectangle { return ideview.TextAt(st, o, w, ideH, x, y) }})
		for _, bar := range bars {
			m.zones = append(m.zones, m.scrollbarZone(bar))
		}
		if m.PanelFocus {
			hints = drawHintsPanel
		}
	}
	switch {
	case m.ShowHelp:
		hints = drawHintsHelp
	case len(m.Suggest) > 0:
		hints = drawHintsSuggest
	case bangInput(m.Input.Text) && !m.PanelFocus:
		hints = drawHintsBang
	}
	chat, chatMax, shown := renderChat(&m.chatLines, entries, w, chatH, m.ChatScroll)
	m.chatMax = chatMax
	if tw, _ := drawChatWidths(w); chatH > 0 { // the text past the "›", short of the times
		m.zones = append(m.zones, zone{y: ideH, w: w, h: chatH,
			click: func(_, y int) tea.Cmd {
				call := "" // a Bash line's: a click shows where it ran
				if i := shown[y]; i >= 0 {
					call = entries[i].Call
				}
				return m.clickChat(call, o, w, ideH)
			},
			text: func(int, int) image.Rectangle { return image.Rect(4, 0, 2+tw, chatH) }})
		m.zones = append(m.zones, m.pathZone(m.zones[len(m.zones)-1], entries, w, chatH)) // the paths in it open at a click
	}
	in, cx := renderInput(&m.Input, w-4, "Ask claude…", m.PanelFocus)
	var parts []string
	if ideH > 0 {
		parts = append(parts, top)
	}
	if chatH > 0 {
		parts = append(parts, chat)
	}
	m.Status.Noted = m.sel.noted(m.Now)
	border := theme.Border
	if bangInput(m.Input.Text) && !m.PanelFocus {
		border = theme.Pink // the shell mode, as Claude Code marks it
	}
	parts = append(parts, strings.Join(drawBox([]string{in}, w-4, border), "\n"), renderStatus(m.Status, hints, w))
	base := strings.Join(parts, "\n")

	under := []*lipgloss.Layer{lipgloss.NewLayer(base)}
	if len(m.Suggest) > 0 {
		box := renderSuggest(m.Suggest, m.SuggestSel, strings.TrimPrefix(string(m.Input.Text), "/"), w-4)
		y := max(0, ideH+chatH-lipgloss.Height(box))
		under = append(under, lipgloss.NewLayer(box).X(2).Y(y))
		m.zones = append(m.zones, m.suggestZone(2, y, lipgloss.Width(box)))
	}
	var over *lipgloss.Layer
	switch {
	case m.Popup != nil:
		m.onDrag = nil
		box, hits := drawPopup(m.Popup, min(w-4, 100), h-2)
		age := time.Duration(-1) // unknown: no drop
		if !m.popupAt.IsZero() {
			age = m.Now.Sub(m.popupAt)
		}
		x, y := (w-lipgloss.Width(box))/2, drawPopupY(h, lipgloss.Height(box), age)
		over = lipgloss.NewLayer(box).X(x).Y(y)
		m.zones = m.popupZones(m.Popup, hits, x, y) // nothing under a popup takes clicks
	case m.ShowHelp:
		box := renderHelp(w-4, h-2)
		m.onDrag = nil
		over = lipgloss.NewLayer(box).X((w - lipgloss.Width(box)) / 2).Y((h - lipgloss.Height(box)) / 2)
		m.zones = nil // a click anywhere closes it
	case !m.PanelFocus: // the keys go to the agents panel: no caret in the prompt
		v.Cursor = tea.NewCursor(2+cx, ideH+chatH+1)
	}
	selecting := m.sel.drag && m.sel.out(m.Now) < 1 && m.Popup == nil && !m.ShowHelp
	if len(under) == 1 && over == nil && !selecting {
		v.SetContent(drawBlock(base, w, h))
		return v
	}
	c := lipgloss.NewCanvas(w, h).Compose(lipgloss.NewCompositor(under...))
	if selecting {
		m.sel.text = drawSelection(c, m.sel, m.sel.lit(m.Now), m.sel.out(m.Now))
	}
	if m.Popup != nil || m.ShowHelp {
		drawDim(c)
	}
	if over != nil {
		c.Compose(lipgloss.NewCompositor(over))
	}
	// Canvas.Render trims trailing spaces; drawBlock pads the lines back to full width.
	v.SetContent(drawBlock(c.Render(), w, h))
	return v
}

// drawDimBy is how far the screen behind a popup or the help fades toward the background.
const drawDimBy = 0.6

// drawDim fades what c shows toward the background, so that a popup or the help stands out and
// the rest looks out of reach.
func drawDim(c *lipgloss.Canvas) {
	for y := range c.Height() {
		for x := range c.Width() {
			cell := c.CellAt(x, y)
			if cell == nil || cell.Content == " " && cell.Style.Fg == nil && cell.Style.Bg == nil {
				continue
			}
			cell.Style.Fg = theme.Mix(cmp.Or(cell.Style.Fg, color.Color(theme.Fg)), theme.Bg, drawDimBy)
			if cell.Style.Bg != nil {
				cell.Style.Bg = theme.Mix(cell.Style.Bg, theme.Bg, drawDimBy)
			}
		}
	}
}

// drawSelection lights the cells of c that s covers in lit, from where the drag began to where it
// is in reading order within its box, out of 1 gone out, and returns their text: a line per row,
// without trailing blanks. A row is lit as far as its text goes: its blanks after it stay dark.
func drawSelection(c *lipgloss.Canvas, s selection, lit color.Color, out float64) string {
	from, to := s.from, s.to
	if to.Y < from.Y || to.Y == from.Y && to.X < from.X {
		from, to = to, from
	}
	var rows []string
	for y := from.Y; y <= to.Y; y++ {
		x0, x1 := s.box.Min.X, s.box.Max.X
		if y == from.Y {
			x0 = from.X
		}
		if y == to.Y {
			x1 = to.X + 1
		}
		end := x0 // past the row's last character
		for x := x0; x < x1; x++ {
			if cell := c.CellAt(x, y); cell != nil && strings.TrimSpace(cell.Content) != "" {
				end = x + max(cell.Width, 1)
			}
		}
		var b strings.Builder
		for x := x0; x < end; x++ {
			cell := c.CellAt(x, y)
			if cell == nil {
				continue
			}
			bg := cell.Style.Bg
			if bg == nil {
				bg = theme.Bg
			}
			cell.Style.Bg = theme.Mix(lit, bg, out)
			if cell.Width > 0 { // not the second half of a wide character
				b.WriteString(cell.Content)
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(rows, "\n")
}

// drawTitle is the terminal's title and tab progress for the status: what claude does and how
// many agents work, a spinning bar while they do, a warning bar while a popup waits.
func drawTitle(st Status, asking bool) (string, *tea.ProgressBar) {
	title := "claudit"
	var bar *tea.ProgressBar
	switch {
	case asking:
		title += " · waiting for you"
		bar = tea.NewProgressBar(tea.ProgressBarWarning, 100)
	case st.Busy:
		title += " · " + drawOneLine(st.State)
		bar = tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
	}
	if st.Agents > 0 {
		title += " · " + strconv.Itoa(st.Agents) + " agents"
		if bar == nil {
			bar = tea.NewProgressBar(tea.ProgressBarIndeterminate, 0)
		}
	}
	return title, bar
}

// drawNotice posts text as a desktop notification (OSC 9: iTerm2, WezTerm, Ghostty), without a bell.
func drawNotice(text string) string {
	return "\x1b]9;" + ansi.Truncate(strings.ReplaceAll(drawUnescaped(text), "\n", " "), 160, "…") + "\x07"
}

// drawLayout splits the height: the IDE on top, then the chat, as tall as its chat lines up to
// drawChatShare of the screen, the input box (3 lines) and the status line. Below drawIDEH rows
// there is no IDE: the chat takes the rest.
func drawLayout(h, chat int) (ideH, chatH int) {
	if h < drawIDEH {
		return 0, h - 4
	}
	chatH = min(max(chat, 0), h*drawChatShare/100)
	return h - chatH - 4, chatH
}

// The smallest screen claudit draws, the lowest that still has the IDE, and the most of the
// screen the chat takes beside the IDE, in percent.
const (
	drawMinW, drawMinH = 40, 8
	drawIDEH           = 16
	drawChatShare      = 35
)

// tooSmall reports whether the screen is too small to draw; nothing takes keys then but ctrl+c,
// so that no popup is answered blind.
func (m *Model) tooSmall() bool { return m.Width < drawMinW || m.Height < drawMinH }

func drawTooSmall(w, h int) string {
	msg := fmt.Sprintf("terminal too small · %d×%d at least", drawMinW, drawMinH)
	lines := make([]string, h)
	if h > 0 {
		lines[h/2] = strings.Repeat(" ", max(0, (w-ansi.StringWidth(msg))/2)) + styleDim.Render(msg)
	}
	return drawBlock(strings.Join(lines, "\n"), w, h)
}

// drawFit truncates or pads one line to exactly w cells.
func drawFit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	// ansi.Truncate can leave a cluster such as the keycap "1️⃣" one cell wider than
	// ansi.StringWidth allows: cut again until the text fits.
	for n := w; ansi.StringWidth(s) > w; n-- {
		s = ansi.Truncate(s, n, "")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}

// drawBlock makes s exactly h lines of exactly w cells.
func drawBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range out {
		var l string
		if i < len(lines) {
			l = lines[i]
		}
		out[i] = drawFit(l, w)
	}
	return strings.Join(out, "\n")
}

// drawBox frames lines in a rounded border with one cell of padding on each side; the box is inner+4 cells wide.
func drawBox(lines []string, inner int, c color.Color) []string {
	b := lipgloss.NewStyle().Foreground(c)
	bar := strings.Repeat("─", max(0, inner+2))
	out := make([]string, 0, len(lines)+2)
	out = append(out, b.Render("╭"+bar+"╮"))
	for _, l := range lines {
		out = append(out, b.Render("│")+" "+drawFit(l, inner)+" "+b.Render("│"))
	}
	return append(out, b.Render("╰"+bar+"╯"))
}

// drawAgentColor is the colour of an agent's palette slot.
func drawAgentColor(slot int) color.Color { return theme.Agents[max(0, slot)%len(theme.Agents)] }

// drawUnescaped makes foreign text plain, for what is not laid out in cells (a notification, an export):
// no escape sequences, tabs as 4 spaces, no control characters but '\n'.
func drawUnescaped(s string) string {
	s = strings.ReplaceAll(ansi.Strip(s), "\t", "    ")
	return strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// drawClean makes foreign text safe to lay out: drawUnescaped without ideview.Undrawn runes.
func drawClean(s string) string {
	return strings.Map(func(r rune) rune {
		if ideview.Undrawn(r) {
			return -1
		}
		return r
	}, drawUnescaped(s))
}

// drawOneLine is drawClean with line breaks turned into spaces.
func drawOneLine(s string) string {
	return strings.ReplaceAll(drawClean(s), "\n", " ")
}

// drawWrap cleans s and word-wraps it by display width into lines of at most w cells.
func drawWrap(s string, w int) []string {
	w = max(1, w)
	lines := strings.Split(ansi.Wrap(strings.Trim(drawClean(s), "\n"), w, ""), "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, w, "") // a wide rune cannot be split when w is 1
	}
	return lines
}

// drawCut keeps at most n (≥ 1) lines and marks a cut with "…".
func drawCut(lines []string, n, w int) []string {
	if len(lines) <= n {
		return lines
	}
	lines = lines[:n]
	lines[n-1] = ansi.Truncate(lines[n-1], w-1, "") + "…"
	return lines
}
