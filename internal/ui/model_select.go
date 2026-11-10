package ui

import (
	"image"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"github.com/pixelbender/claudit/internal/theme"
)

// selection is the text the left button drags over, kept within the text of the pane where the
// press was: the chat or a pane of the IDE.
type selection struct {
	box      image.Rectangle // the pane's text on screen; empty for none
	from, to image.Point     // where the press was, where the mouse is; in the chat, a row is a line of the whole conversation
	chat     bool            // in the chat: it scrolls with the conversation, past what the screen shows
	mouse    image.Point     // where the mouse is on screen
	drag     bool            // the mouse moved with the button down: there is something to copy
	text     string          // what it covers, as the last frame drew it; the chat's is read from its lines
	released time.Time       // when the button let go and what it covers was copied
}

// chatView is the chat as the last frame drew it.
type chatView struct {
	box     image.Rectangle // its text on screen: past the "›", short of the times; empty for no chat
	entries []ChatEntry     // what it drew, tw cells wide, kept in cache
	tw      int
	cache   *chatCache
	lines   []string // every line of the conversation, styled, from the first: drawn when asked
	drawn   bool
}

// chatText is every line of the conversation as the last frame drew the chat, styled, from the
// first. A frame draws only the lines it shows: the rest are drawn once a selection asks.
func (m *Model) chatText() []string {
	v := &m.chatView
	if !v.drawn && !v.box.Empty() {
		v.lines, _, _ = drawChatLines(v.cache, v.entries, v.tw, 0)
		v.drawn = true
	}
	return v.lines
}

// selScrollFrame is how often a drag held past the top or bottom of the chat scrolls it, and
// selScrollMax the most lines a frame scrolls, however far past the mouse is.
const (
	selScrollFrame = 50 * time.Millisecond
	selScrollMax   = 10
)

// chatTextCol is the column of a chat line where its text a drag selects starts: past the "› ".
const chatTextCol = 2

// selScrollMsg scrolls the chat a frame's worth under a drag held past its edge.
type selScrollMsg struct{}

// How a copied selection goes out: a flash as the button lets go, lit a moment, so that the copy
// shows, then fading; the status line's note of the copy stays a while longer, then fades too.
const (
	selFlash  = 100 * time.Millisecond
	selHold   = 300 * time.Millisecond
	selFade   = 200 * time.Millisecond
	noteHold  = time.Second
	noteFade  = 300 * time.Millisecond
	drawNoted = "copied to clipboard"
)

// copiedColor is the light blue of a copy: the status line's note of it, the flash of what it took.
var copiedColor = theme.Mix(theme.Cyan, theme.Bright, 0.35)

// selFlashBg lights a selection as it is copied: in the light blue of the note that says so.
var selFlashBg = theme.Mix(copiedColor, theme.Bg, 0.45)

// out is how far the light of s has gone at now: 0 while it is dragged and for selHold after the
// release, then up to 1 over selFade.
func (s selection) out(now time.Time) float64 {
	if s.released.IsZero() {
		return 0
	}
	return min(max(float64(now.Sub(s.released)-selHold)/float64(selFade), 0), 1)
}

// lit is the colour s lights its cells in at now: the flash for selFlash after the release.
func (s selection) lit(now time.Time) color.Color {
	if !s.released.IsZero() && now.Sub(s.released) < selFlash {
		return selFlashBg
	}
	return theme.SelBg
}

// noted is how bright the status line's note of the copy of s is at now: 1 from the release, down
// to 0 over noteFade after noteHold; 0 for no copy.
func (s selection) noted(now time.Time) float64 {
	if s.released.IsZero() {
		return 0
	}
	return 1 - min(max(float64(now.Sub(s.released)-noteHold)/float64(noteFade), 0), 1)
}

// selectAt starts a selection at a press on (x, y), in the text of the topmost zone there.
func (m *Model) selectAt(x, y int) selection {
	p := image.Pt(x, y)
	for i := len(m.zones) - 1; i >= 0; i-- {
		z := m.zones[i]
		if !p.In(image.Rect(z.x, z.y, z.x+z.w, z.y+z.h)) {
			continue
		}
		if z.text == nil {
			return selection{}
		}
		box := z.text(x-z.x, y-z.y).Add(image.Pt(z.x, z.y))
		if !p.In(box) {
			return selection{}
		}
		s := selection{box: box, from: p, to: p, chat: z.chat, mouse: p}
		if z.chat {
			s.from = m.chatPoint(x, y)
			s.to = s.from
		}
		return s
	}
	return selection{}
}

// chatTop is the line of the conversation on the chat's top row as it is scrolled now; < 0 when
// the chat is short of its rows.
func (m *Model) chatTop() int {
	return len(m.chatText()) - min(m.ChatScroll, m.chatMax) - m.chatView.box.Dy()
}

// chatPoint is where the conversation is under (x, y) on the screen as the chat is scrolled now:
// a column of its text, a line of it, < 0 on the blank rows over a short chat. Above or below the
// chat it is on its top or bottom row.
func (m *Model) chatPoint(x, y int) image.Point {
	b := m.chatView.box
	line := m.chatTop() + min(max(y, b.Min.Y), b.Max.Y-1) - b.Min.Y
	return image.Pt(min(max(x, b.Min.X), b.Max.X-1), min(line, len(m.chatText())-1))
}

// dragTo moves the selection's end to (x, y), within its pane's text.
func (m *Model) dragTo(x, y int) {
	if m.sel.box.Empty() {
		return
	}
	m.sel.mouse = image.Pt(x, y)
	b := m.sel.box
	to := image.Pt(min(max(x, b.Min.X), b.Max.X-1), min(max(y, b.Min.Y), b.Max.Y-1))
	if m.sel.chat {
		if len(m.chatText()) == 0 { // the chat is gone from the screen: the drag waits for it
			return
		}
		to = m.chatPoint(x, y)
	}
	if to != m.sel.from {
		m.sel.drag = true
	}
	m.sel.to = to
}

// selEdge is how many rows past the top (< 0) or the bottom (> 0) of the chat the mouse holds a
// drag that began in it; 0 within, and with no chat on screen or something over it: a popup, the
// help, the completions. With
// the chat at the top of the screen, its top row is past.
func (m *Model) selEdge() int {
	y, b := m.sel.mouse.Y, m.chatView.box
	if !m.mouseDown || !m.sel.chat || b.Empty() || m.Popup != nil || m.ShowHelp || len(m.Suggest) > 0 {
		return 0
	}
	switch {
	case y < b.Min.Y:
		return y - b.Min.Y
	case y <= 0:
		return -1
	case y >= b.Max.Y:
		return y - b.Max.Y + 1
	}
	return 0
}

// holdSelScroll starts scrolling the chat under a drag held past its edge, unless it scrolls already.
func (m *Model) holdSelScroll() tea.Cmd {
	if m.selTick || m.selEdge() == 0 {
		return nil
	}
	m.selTick = true
	return tea.Tick(selScrollFrame, func(time.Time) tea.Msg { return selScrollMsg{} })
}

// selScroll scrolls the chat under a drag held past its edge, a line a frame and more the farther
// past; the selection's end stays under the mouse. Let go, back inside or at the top, the scroll
// stops; at the bottom it goes on, as the conversation grows there.
func (m *Model) selScroll() tea.Cmd {
	m.selTick = false
	d := m.selEdge()
	if d == 0 || m.tooSmall() || d < 0 && m.ChatScroll >= m.chatMax {
		return nil
	}
	m.scrollChat(-min(max(d, -selScrollMax), selScrollMax))
	return m.holdSelScroll()
}

// selectChat selects the whole conversation and copies it, as a drag over all of it would.
func (m *Model) selectChat() tea.Cmd {
	b, n := m.chatView.box, len(m.chatText()) // no conversation: copySelection finds nothing to copy
	m.sel = selection{box: b, chat: true, drag: true, from: image.Pt(b.Min.X, 0), to: image.Pt(b.Max.X-1, n-1)}
	return m.copySelection()
}

// selText is what the selection covers: in the chat, the text of every line it spans, on screen
// or not, past the arrows and short of the times; elsewhere what the last frame drew. Under a
// popup or the help nothing shows it: it covers nothing.
func (m *Model) selText() string {
	s := m.sel
	if !s.chat || !s.drag || m.Popup != nil || m.ShowHelp { // a click is no selection: the frame drew none
		return s.text
	}
	from, to := s.ends()
	lines := m.chatText()
	var rows []string
	for y := max(from.Y, 0); y <= min(to.Y, len(lines)-1); y++ { // from the blank rows over a short chat, its first line whole
		x0, x1 := s.span(y)
		x0, x1 = x0-s.box.Min.X+chatTextCol, x1-s.box.Min.X+chatTextCol // the line starts 2 cells into the row
		rows = append(rows, strings.TrimRight(selCut(lines[y], x0, x1), " "))
	}
	return strings.Join(rows, "\n")
}

// selCut is the text of styled line l in columns [x0, x1): each character whose first column is
// in them, a wide one whole, as drawSelection takes it from the screen.
func selCut(l string, x0, x1 int) string {
	var b strings.Builder
	g := uniseg.NewGraphemes(ansi.Strip(l))
	for x := 0; x < x1 && g.Next(); x += ansi.StringWidth(g.Str()) { // as the screen measures it
		if x >= x0 {
			b.WriteString(g.Str())
		}
	}
	return b.String()
}

// ends are the selection's ends in reading order.
func (s selection) ends() (from, to image.Point) {
	from, to = s.from, s.to
	if to.Y < from.Y || to.Y == from.Y && to.X < from.X {
		from, to = to, from
	}
	return from, to
}

// span is the columns of row y that s covers: the whole of its box between the ends.
func (s selection) span(y int) (x0, x1 int) {
	from, to := s.ends()
	x0, x1 = s.box.Min.X, s.box.Max.X
	if y == from.Y {
		x0 = from.X
	}
	if y == to.Y {
		x1 = to.X + 1
	}
	return x0, x1
}

// copySelection ends the selection at the release of the button and copies what it covers; its
// light goes out after it.
func (m *Model) copySelection() tea.Cmd {
	if !m.sel.released.IsZero() { // copied already: another button let go
		return nil
	}
	text := m.selText()
	if strings.TrimSpace(text) == "" { // blank rows light nothing: nothing to copy
		m.sel = selection{}
		return nil
	}
	m.sel.released = m.clock()
	return copyText(text)
}

// writeClipboard puts text on the system clipboard; a test catches it there instead.
var writeClipboard = writeClipboardText

// copyText uses the system clipboard, falling back to the terminal's OSC 52 support.
func copyText(text string) tea.Cmd {
	return func() tea.Msg {
		if writeClipboard(text) == nil {
			return nil
		}
		return tea.SetClipboard(text)()
	}
}
