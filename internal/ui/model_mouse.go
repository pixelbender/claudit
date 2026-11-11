package ui

import (
	"image"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
)

// zone is a place on screen a click acts on, as the last frame drew it; click gets the cell
// clicked, relative to the zone. A zone with wheel scrolls n rows back at a turn of the wheel there;
// one with text has text a drag selects: the box of it around the cell, relative to the zone. The
// chat's selection scrolls with it.
type zone struct {
	x, y, w, h int
	click      func(x, y int) tea.Cmd
	wheel      func(x, y, n int)
	text       func(x, y int) image.Rectangle
	chat       bool
}

// wheelStep is how many chat lines a notch of the wheel scrolls.
const wheelStep = 3

// scrollChat scrolls the chat n lines up (down when n < 0), within what there is to see; the end
// of a drag in it stays under the mouse.
func (m *Model) scrollChat(n int) {
	m.ChatScroll = max(0, min(min(m.ChatScroll, m.chatMax)+n, m.chatMax)) // chatMax shrinks when the window grows
	if m.mouseDown && m.sel.chat && m.sel.released.IsZero() {
		m.dragTo(m.sel.mouse.X, m.sel.mouse.Y)
	}
}

// wheelFrame is how often a scroll in progress draws. A trackpad sends wheel events faster than a
// frame takes: drawn one by one, they back up and the screen scrolls on after the fingers stop.
const wheelFrame = 33 * time.Millisecond

// wheelMsg draws what the wheel turned since the last frame of a scroll.
type wheelMsg struct{}

// wheelBurst is what the wheel turned since the last frame of a scroll.
type wheelBurst struct {
	on   bool // a scroll is in progress: Filter adds its wheel events up here
	x, y int  // where the wheel turned last
	d    int  // notches down, up when < 0
}

// Filter is the program's message filter: the wheel events of a scroll in progress add up for its
// next frame and cost no frame of their own.
func Filter(tm tea.Model, msg tea.Msg) tea.Msg {
	m, ok := tm.(*Model)
	w, wheel := msg.(tea.MouseWheelMsg)
	if !ok || !wheel || !m.burst.on || wheelNotch(w) == 0 {
		return msg
	}
	m.burst.x, m.burst.y = w.X, w.Y
	m.burst.d += wheelNotch(w)
	return nil
}

// wheelNotch is a wheel event's notch: 1 down, -1 up, 0 sideways.
func wheelNotch(w tea.MouseWheelMsg) int {
	switch w.Button {
	case tea.MouseWheelDown:
		return 1
	case tea.MouseWheelUp:
		return -1
	}
	return 0
}

// holdWheel starts a scroll unless one is in progress: Filter holds the wheel events until its next frame.
func (m *Model) holdWheel() tea.Cmd {
	if m.burst.on {
		return nil
	}
	m.burst.on = true
	return tea.Tick(wheelFrame, func(time.Time) tea.Msg { return wheelMsg{} })
}

// flushWheel scrolls by what the wheel turned since the last frame; if it did not turn, the scroll is over.
func (m *Model) flushWheel() tea.Cmd {
	b := m.burst
	m.burst = wheelBurst{}
	if b.d == 0 {
		return nil
	}
	if !m.tooSmall() {
		m.wheel(b.x, b.y, b.d)
	}
	return m.holdWheel()
}

// wheel at (x, y) scrolls d notches down (up when d < 0) what has the keys: the open popup, the
// completions; else a pane of the IDE there, else the chat.
func (m *Model) wheel(x, y, d int) {
	m.interact()
	switch p := m.Popup.(type) {
	case *QuitPopup:
		p.Scroll += d * wheelStep
	case *PermissionPopup:
		p.Scroll += d * wheelStep
	case *BtwPopup:
		p.Scroll += d * wheelStep
	case *QuestionPopup:
		if p.Current >= 0 && p.Current < len(p.Questions) {
			q := &p.Questions[p.Current]
			q.Cursor = min(max(q.Cursor+d, 0), len(q.Options))
		}
	case *ResumePopup:
		p.Cursor = min(max(p.Cursor+d, 0), len(p.Sessions)-1)
	case *PickPopup:
		p.Cursor = min(max(p.Cursor+d, 0), len(p.Options)-1)
	case *PluginsPopup:
		p.Cursor = min(max(p.Cursor+d, 0), max(0, p.rows()-1))
	case nil:
		switch {
		case m.ShowHelp:
		case len(m.Suggest) > 0:
			m.SuggestSel = min(max(m.SuggestSel+d, 0), len(m.Suggest)-1)
		case m.mouseDown && m.sel.chat && m.sel.released.IsZero(): // a drag in the chat: it scrolls wherever the mouse is
			m.scrollChat(-d * wheelStep)
		default:
			for i := len(m.zones) - 1; i >= 0; i-- {
				if z := m.zones[i]; z.wheel != nil && x >= z.x && x < z.x+z.w && y >= z.y && y < z.y+z.h {
					z.wheel(x-z.x, y-z.y, -d*wheelStep)
					return
				}
			}
			m.scrollChat(-d * wheelStep)
		}
	}
}

// scrollIDE puts the scroll of the IDE pane key; 0 makes it follow its agent again.
func (m *Model) scrollIDE(key string, back int) {
	switch {
	case key == "":
	case back == 0:
		delete(m.scroll, key)
	default:
		m.scroll[key] = back
	}
}

func (m *Model) scrollbarZone(bar ideview.Scrollbar) zone {
	return zone{x: bar.X, y: bar.Y, w: 1, h: bar.Height, click: func(_, y int) tea.Cmd {
		m.onRelease = nil
		top, n := bar.Thumb()
		y += bar.Y
		grab := n / 2
		if y >= top && y < top+n {
			grab = y - top
		} else {
			m.scrollIDE(bar.Key, bar.Scroll(y, grab))
		}
		m.onDrag = func(_, y int) { m.scrollIDE(bar.Key, bar.Scroll(y, grab)) }
		return nil
	}}
}

// click acts on a left click at (x, y): the topmost zone there takes it. Elsewhere it closes the
// help or gives the keys back to the prompt; under a popup nothing takes it.
func (m *Model) click(x, y int) tea.Cmd {
	for i := len(m.zones) - 1; i >= 0; i-- {
		if z := m.zones[i]; z.click != nil && x >= z.x && x < z.x+z.w && y >= z.y && y < z.y+z.h {
			return z.click(x-z.x, y-z.y)
		}
	}
	switch {
	case m.ShowHelp:
		m.ShowHelp = false
	case m.Popup == nil:
		m.PanelFocus = false
	}
	return nil
}

// clickIDE acts on a press in the IDE on the agent there (a row of the agents panel, a workplace
// on the right): its release, unless the press starts a selection, shows the agent alone, or,
// alone already, every agent at work again. The keys go back to the prompt.
func (m *Model) clickIDE(key string) tea.Cmd {
	m.PanelFocus = false
	m.onRelease = nil
	if key != "" {
		m.onRelease = func() { m.pickAgent(key) }
	}
	return nil
}

// clickChat acts on a press on a line of the chat, a Bash line of call, a ! command of claude's
// ("" for another line) or a row of a block of code of a reply's (nil for none): its release,
// unless the press starts a selection, shows where main ran the call in the IDE of o, w×h cells
// (none when h is 0), puts the command in the prompt, or copies what the block holds. The keys
// go back to the prompt.
func (m *Model) clickChat(call, bang string, block *chatBlock, o ideview.Options, w, h int) tea.Cmd {
	m.PanelFocus = false
	m.onRelease = nil
	switch {
	case bang != "":
		m.onRelease = func() { m.takeBang(bang) }
	case block != nil:
		b := *block
		m.onRelease = func() { m.copyBlock(b) }
	case call != "" && h > 0:
		m.onRelease = func() { m.showCall(call, o, w, h) }
	}
	return nil
}

// showCall shows main alone, the camera pinned on it, its terminal scrolled to the lines of Bash
// call call. A call the terminal holds no line of, not typed yet or long gone, shows nothing.
func (m *Model) showCall(call string, o ideview.Options, w, h int) {
	if live := m.Sim.FindAgent("main"); live != nil && slices.ContainsFunc(live.Term.Lines, func(l ide.TermLine) bool { return l.Call == call }) {
		m.refreshInspection()
	}
	main := m.ideState().Agents[0]
	if main == nil || !slices.ContainsFunc(main.Term.Lines, func(l ide.TermLine) bool { return l.Call == call }) {
		return
	}
	m.PanelSel = "main"
	m.pinSelected()
	if m.inspection != nil {
		main.Focus = ide.Terminal
	}
	m.scrollIDE(ideview.TermScroll(m.ideState(), o, w, h, "main", call))
}

// pickAgent shows the agent with that key alone, the camera pinned on it; alone already, every
// agent at work again.
func (m *Model) pickAgent(key string) {
	if st := m.ideState(); st.Pinned && st.Camera == key {
		m.pinView("")
		return
	}
	m.PanelSel = key
	m.pinSelected()
}

// suggestZone is the rows of the completion list drawn at (x, y), w cells wide: a click puts that
// command in the prompt, as tab does.
func (m *Model) suggestZone(x, y, w int) zone {
	top, n := drawSuggestRange(len(m.Suggest), m.SuggestSel)
	return zone{x: x, y: y + 1, w: w, h: n, click: func(_, row int) tea.Cmd {
		if top+row < len(m.Suggest) {
			m.SuggestSel = top + row
			m.complete(" ")
		}
		return nil
	}}
}

// popupZones are the parts of popup p drawn at (x, y) that a click acts on.
func (m *Model) popupZones(p Popup, hits []drawHit, x, y int) []zone {
	var zs []zone
	for _, h := range hits {
		at := h.at
		zs = append(zs, zone{x: x + h.x0, y: y + h.y, w: h.x1 - h.x0, h: 1, click: func(int, int) tea.Cmd {
			if m.Popup != p {
				return nil
			}
			return m.clickPopup(p, at)
		}})
	}
	return zs
}

// clickPopup acts on a click on part at of popup p: an option is chosen (checked or unchecked when
// several may be), a button pressed, a conversation resumed.
func (m *Model) clickPopup(p Popup, at int) tea.Cmd {
	switch p := p.(type) {
	case *QuitPopup:
		if at == 1 {
			return tea.Quit
		}
		m.cancelQuit()
	case *QuestionPopup:
		q := &p.Questions[p.Current]
		q.Cursor = at
		switch {
		case at == len(q.Options): // "Other": its field takes the keys
		case q.Multi:
			q.Checked[at] = !q.Checked[at]
		default:
			m.answerQuestion(p)
		}
	case *PermissionPopup:
		m.answerPermission(p, at == 0)
	case *ResumePopup:
		p.Cursor = at
		return m.resumeKey(p, "enter")
	case *PickPopup:
		p.Cursor = at
		return m.pickKey(p, "enter")
	case *PluginsPopup: // a plugin installed turns on or off; an offer is only chosen, enter installs it
		p.Cursor = at
		if !p.Discover {
			return m.pluginsKey(p, tea.KeyPressMsg{Code: tea.KeyEnter})
		}
	}
	return nil
}
