package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/sessions"
)

// claude -p cannot /resume: claudit lists the saved conversations itself, restarts claude in
// the one picked and puts its prompts and replies in the chat. The disk is read in commands, off
// the UI's way.

// sessionsMsg brings the saved conversations of the project.
type sessionsMsg struct {
	query string // what followed /resume
	list  []sessions.Info
	err   error
}

// historyMsg brings what was said in the conversation picked.
type historyMsg struct {
	info    sessions.Info
	history []sessions.Message
	err     error
}

// resumable reports whether claude can be restarted in another conversation now; the chat says
// why not.
func (m *Model) resumable() bool {
	switch {
	case m.Status.Replay:
		m.local("replay: /resume is not available")
	case m.Status.State == "exited":
		m.local("Error: claude has exited: nothing to resume")
	case m.Status.Busy:
		m.local("/resume: claude is working, esc interrupts it")
	default:
		return true
	}
	return false
}

// resume runs /resume: it reads the conversations of the project, and query, when given, picks
// among them by id or title.
func (m *Model) resume(query string) tea.Cmd {
	if !m.resumable() {
		return nil
	}
	dir := sessions.Dir(m.Sim.State().Root)
	return func() tea.Msg {
		list, err := sessions.List(dir)
		return sessionsMsg{query: query, list: list, err: err}
	}
}

// listed lists the conversations to resume in a popup, or resumes the one query names.
func (m *Model) listed(msg sessionsMsg) tea.Cmd {
	if msg.err != nil {
		m.local("Error: /resume: " + msg.err.Error())
		return nil
	}
	q := strings.ToLower(msg.query)
	list := slices.DeleteFunc(msg.list, func(s sessions.Info) bool {
		return !strings.HasPrefix(s.ID, q) && !strings.Contains(strings.ToLower(s.Title), q)
	})
	other := slices.IndexFunc(list, func(s sessions.Info) bool { return s.ID != m.session })
	switch {
	case m.Popup != nil: // claude asked meanwhile: its question goes first
		m.local("/resume: answer claude first")
	case len(list) == 0 && q != "":
		m.local(fmt.Sprintf("/resume: no conversation matches %q", msg.query))
	case other < 0 && q == "":
		m.local("/resume: no other conversation in this project")
	case len(list) == 1 && q != "":
		return m.pick(list[0])
	default:
		m.Popup = &ResumePopup{Sessions: list, Current: m.session, Cursor: max(other, 0), Now: m.Now}
	}
	return nil
}

func (m *Model) resumeKey(p *ResumePopup, key string) tea.Cmd {
	switch key {
	case "up":
		p.Cursor = max(p.Cursor-1, 0)
	case "down":
		p.Cursor = min(p.Cursor+1, len(p.Sessions)-1)
	case "pgup":
		p.Cursor = max(p.Cursor-10, 0)
	case "pgdown":
		p.Cursor = min(p.Cursor+10, len(p.Sessions)-1)
	case "esc":
		m.closeResume()
	case "enter":
		m.closeResume()
		return m.pick(p.Sessions[p.Cursor])
	}
	return nil
}

// closeResume closes the list; a question claude asked meanwhile comes up.
func (m *Model) closeResume() {
	m.Popup = nil
	if len(m.queue) > 0 {
		m.Popup, m.queue = m.queue[0], m.queue[1:]
	}
}

// pick reads what was said in s; the conversation claude is in already stays as it is.
func (m *Model) pick(s sessions.Info) tea.Cmd {
	if s.ID == m.session {
		m.local("/resume: already in this conversation")
		return nil
	}
	dir := sessions.Dir(m.Sim.State().Root)
	return func() tea.Msg {
		h, err := sessions.History(dir, s.ID)
		return historyMsg{info: s, history: h, err: err}
	}
}

// entryKinds are the chat entries of what sessions.History reads.
var entryKinds = [...]EntryKind{sessions.Prompt: UserEntry, sessions.Reply: AssistantEntry, sessions.Output: CommandEntry}

// resumed restarts claude in the conversation picked, with what was said in it in the chat.
func (m *Model) resumed(msg historyMsg) {
	if msg.err != nil {
		m.local("Error: /resume: " + msg.err.Error())
		return
	}
	if !m.resumable() {
		return
	}
	m.send(protocol.Resume{SessionID: msg.info.ID})
	// The old process takes its agents and its questions along.
	m.Popup, m.queue = nil, nil
	m.exited(m.clock())
	m.Chat, m.chatLines, m.ChatScroll, m.btwSaid = nil, chatCache{}, 0, nil
	cmd := false // the last prompt was a slash command: it stays out of the chat, as live
	for _, h := range msg.history {
		if h.Role == sessions.Prompt {
			_, cmd = slash(h.Text)
		}
		if h.Role == sessions.Cut {
			if !cmd {
				m.cut()
			}
			continue
		}
		if h.Role == sessions.Prompt && cmd {
			continue
		}
		e := ChatEntry{Kind: entryKinds[h.Role], Text: h.Text}
		if !h.At.IsZero() {
			e.At = h.At.In(m.clock().Location()) // in the zone of the times said live
		}
		m.Chat = append(m.Chat, e)
	}
	m.context, m.cost, m.usageKnown, m.session = protocol.Context{}, 0, false, msg.info.ID // a new process counts anew
	m.cacheAt = time.Time{}                                                                // the cache was the old conversation's
	m.Status.State = "starting"                                                            // until the new process answers
	m.local("Resumed: " + msg.info.Title)
}
