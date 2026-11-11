package ui

import (
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/protocol"
)

// globalKey handles the keys that work whatever has the focus, when no popup is open, and
// reports whether key was one of them.
func (m *Model) globalKey(key string) bool {
	switch {
	case key == "f1":
		m.ShowHelp = true
	case key == "shift+tab":
		m.cycleMode()
	case key == "alt+z":
		m.Wrap = !m.Wrap
	case key == "ctrl+t": // as in Claude Code
		m.todo.open = !m.todo.open && len(m.todo.items) > 0
	case key == "tab":
		if m.PanelFocus {
			m.PanelFocus = false
		} else if m.Panel == ideview.Agents { // the files have nothing to pick
			m.focusPanel()
		}
	case strings.HasPrefix(key, "alt+") && isDigitKey(key[4:]):
		m.pinHotkey(int(key[4] - '0'))
	default:
		return false
	}
	return true
}

// panelKey handles a key while the agents panel has the focus; typing has gone back to the prompt.
func (m *Model) panelKey(key string) {
	switch {
	case (key == "up" || key == "down" || key == "enter") && m.Panel != ideview.Agents:
		// the explorer is on screen: no agent rows to select
	case key == "up", key == "down":
		rows := ideview.AgentRows(m.ideState())
		if len(rows) == 0 {
			return
		}
		i := slices.Index(rows, m.PanelSel)
		switch {
		case i < 0:
			i = 0
		case key == "up":
			i = max(i-1, 0)
		default:
			i = min(i+1, len(rows)-1)
		}
		m.PanelSel = rows[i]
		delete(m.scroll, "agents")
	case key == "enter":
		m.pinSelected()
	case key == "esc":
		m.PanelFocus = false
	}
}

func isDigitKey(s string) bool { return len(s) == 1 && s[0] >= '0' && s[0] <= '9' }

// focusPanel gives the keys to the agents panel, starting on the camera's agent.
func (m *Model) focusPanel() {
	m.PanelFocus = true
	st := m.ideState()
	if m.PanelSel == "" || !slices.Contains(ideview.AgentRows(st), m.PanelSel) {
		m.PanelSel = st.Camera
	}
}

// pinHotkey pins the camera on the agent with hotkey n, and selects it in the panel; 0 gives the
// camera back to the director.
func (m *Model) pinHotkey(n int) {
	if n == 0 {
		m.inspection = nil // an explicit return to auto-follow does not wait for the cooldown
		m.Sim.Pin("")
		return
	}
	for _, a := range m.ideState().Agents {
		if a.Hotkey == n {
			m.PanelSel = a.Key
			m.pinSelected()
			return
		}
	}
}

// pinSelected pins the camera on the agent selected in the panel. A workflow has no workplace of
// its own: the camera goes to its first running agent, if any.
func (m *Model) pinSelected() {
	a := m.Sim.FindAgent(m.PanelSel)
	if a == nil {
		a = m.Sim.FindAgent(m.crew.slots[m.PanelSel]) // a queued workflow slot received its agent id
		if a != nil {
			m.PanelSel = a.Key
		}
	}
	if a == nil {
		return
	}
	if a.Kind == ide.Workflow {
		i := slices.IndexFunc(m.Sim.State().Agents, func(b *ide.Agent) bool { return b.Parent == a.Key && b.Status == ide.Running })
		if i < 0 {
			return
		}
		a = m.Sim.State().Agents[i]
	}
	m.pinView(a.Key)
}

// quitWindow is how soon a second ctrl+c or esc must follow the first to quit.
const quitWindow = 4 * time.Second

// ctrlC does what ctrl+c does in Claude Code: it clears the prompt, else interrupts the turn,
// else quits — on a second press, so that a stray one costs nothing.
func (m *Model) ctrlC() tea.Cmd {
	switch {
	case m.Popup == nil && (len(m.Input.Text) > 0 || m.queueAt >= 0):
		m.clearPrompt()
	case m.Popup == nil && m.bangStop():
	case m.Popup == nil && m.canInterrupt():
		m.interrupt()
	case m.Status.State == "exited":
		return tea.Quit
	default:
		return m.quitTwice("ctrl+c")
	}
	return nil
}

// quitTwice quits on a second press of key within quitWindow. The keys do not mix: esc after
// ctrl+c is a change of mind, not a confirmation. Esc asks first if background tasks remain.
func (m *Model) quitTwice(key string) tea.Cmd {
	now := m.clock()
	if m.quitKey == key && now.Sub(m.quitAt) < quitWindow {
		if key == "esc" && len(m.tasks) > 0 {
			m.quitKey = ""
			m.Popup = &QuitPopup{Tasks: m.tasks}
			return nil
		}
		return tea.Quit
	}
	m.quitAt, m.quitKey = now, key
	return nil
}

// canInterrupt reports whether there is a turn to interrupt.
func (m *Model) canInterrupt() bool {
	return m.Status.Busy && !m.Status.Replay && m.Status.State != "exited"
}

// interrupt asks claude to stop the turn; the Result that ends it says so in the chat.
func (m *Model) interrupt() {
	m.quitKey = "" // cancelling work resets any earlier exit confirmation
	if !m.interrupting {
		m.interrupts++
		m.send(protocol.Interrupt{RequestID: "interrupt-" + strconv.Itoa(m.interrupts)})
		m.interrupting = true
	}
	m.Status.State = "interrupting"
}

// browsing reports whether the prompt shows an entry of the history.
func (m *Model) browsing() bool { return m.histAt < len(m.history) }

// recall walks the prompt history, d = -1 back and +1 forward; past the newest entry is the
// prompt as it was typed.
func (m *Model) recall(d int) {
	i := m.histAt + d
	if i < 0 || i > len(m.history) {
		return
	}
	if !m.browsing() {
		m.histDraft = m.Input
	}
	m.histAt = i
	if !m.browsing() {
		m.Input = m.histDraft
		return
	}
	text := []rune(m.recalled(m.history[i], i < m.historyOld))
	m.Input = Input{Text: text, Cursor: len(text)}
}
