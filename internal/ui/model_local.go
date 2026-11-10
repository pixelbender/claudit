package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
)

// cliInfo is what claude tells about itself and its session.
type cliInfo struct {
	version      string
	account      string
	models       []protocol.Model
	outputStyles []string
	outputStyle  string
	mcp          []protocol.MCPServer
}

// minClaude is the oldest claude claudit is built for. By its changelog, the newest of what claudit
// leans on came by then: --permission-mode auto (2.1.86), workflow progress in headless sessions
// (2.1.198), set_model mid-turn (2.1.212), `claude plugin list --json --available` (2.1.265).
const minClaude = "2.1.265"

// updateHint is what claudit says when claude is older than minClaude.
const updateHint = "run claude update"

// setVersion keeps the version claude reports. The first one it hears of, if older than
// minClaude, is told to update; a recording's is the recording's own business.
func (m *Model) setVersion(v string) {
	if v == "" {
		return
	}
	if m.cli.version == "" && !m.Status.Replay && olderThan(v, minClaude) {
		m.local("claude " + v + " is older than " + minClaude + ", which claudit is built for · " + updateHint)
	}
	m.cli.version = v
}

// olderThan reports whether version a ("2.1.200") comes before b. A pre-release suffix is
// ignored; a version that does not parse is not older.
func olderThan(a, b string) bool {
	parts := func(v string) []int {
		v, _, _ = strings.Cut(v, "-")
		var out []int
		for p := range strings.SplitSeq(v, ".") {
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil
			}
			out = append(out, n)
		}
		return out
	}
	pa, pb := parts(a), parts(b)
	return pa != nil && pb != nil && slices.Compare(pa, pb) < 0
}

// local puts in the chat the output of a slash command claudit ran itself, or what one did that
// claude says nothing of; the command stays out, as those claude replies to do. A reply still
// streaming stays last, for its words to come in one piece: the output goes above it.
func (m *Model) local(output string) {
	at := len(m.Chat)
	if m.Status.Busy && at > 0 && m.Chat[at-1].Kind == AssistantEntry {
		at--
	}
	m.Chat = slices.Insert(m.Chat, at, ChatEntry{Kind: CommandEntry, Text: output, At: m.clock()})
}

// statusReport is the output of /status: what claude -p would show in its panel, from what it told.
func (m *Model) statusReport() string {
	rows := [][2]string{
		{"Version", "claude " + cmp.Or(m.cli.version, "?")},
		{"Session", cmp.Or(m.session, "not started yet")},
		{"Folder", m.Sim.State().Root},
		{"Account", cmp.Or(m.cli.account, "API key")},
		{"Model", cmp.Or(m.Status.Model, "not known until the first turn")},
		{"Mode", cmp.Or(m.Status.Mode, "default")},
		{"Output style", cmp.Or(m.cli.outputStyle, "default")},
	}
	var mcp []string
	for _, s := range m.cli.mcp {
		mcp = append(mcp, s.Name+" ("+s.Status+")")
	}
	if len(mcp) > 0 {
		rows = append(rows, [2]string{"MCP servers", strings.Join(mcp, ", ")})
	}
	if m.Status.Usage != "" {
		rows = append(rows, [2]string{"Usage", m.Status.Usage})
	}
	if m.Status.Context > 0 {
		rows = append(rows, [2]string{"Context", fmt.Sprintf("%.0f%% used", m.Status.Context*100)})
	}
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%-13s%s\n", r[0], r[1])
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// exportedMsg tells where /export wrote the conversation.
type exportedMsg struct {
	path string
	err  error
}

// export writes the conversation as Markdown to file (relative to the project; by default a new
// file named after the time), off the UI's way.
func (m *Model) export(file string) tea.Cmd {
	if len(m.Chat) == 0 {
		m.local("/export: nothing said yet")
		return nil
	}
	root, now := m.Sim.State().Root, m.clock()
	if file == "" {
		file = "claudit-" + now.Format("20060102-150405") + ".md"
	}
	if !filepath.IsAbs(file) {
		file = filepath.Join(root, file)
	}
	text := drawExport(m.Chat, filepath.Base(root), now.Format("2006-01-02 15:04"))
	return func() tea.Msg {
		return exportedMsg{path: file, err: os.WriteFile(file, []byte(text), 0o644)}
	}
}

// drawExport renders the conversation as Markdown: prompts as bold lines, replies as they are,
// the CLI's output in code blocks.
func drawExport(chat []ChatEntry, project, when string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# claudit · %s · %s\n\n", project, when)
	for _, e := range chat {
		at := ""
		if !e.At.IsZero() {
			at = e.At.Format("15:04") + " "
		}
		switch e.Kind {
		case UserEntry:
			fmt.Fprintf(&b, "**%s›** %s\n\n", at, strings.TrimSpace(e.Text))
		case AssistantEntry:
			if t := strings.TrimSpace(e.Text); t != "" {
				b.WriteString(t + "\n\n")
			}
		case CommandEntry:
			text := drawUnescaped(e.Text)
			if e.Bang != "" {
				text = "! " + e.Bang + "\n" + e.Text
			}
			fmt.Fprintf(&b, "```\n%s\n```\n\n", strings.TrimSpace(text))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// copyReply puts claude's last reply on the clipboard (OSC 52: the terminal must allow it).
func (m *Model) copyReply() tea.Cmd {
	for i := len(m.Chat) - 1; i >= 0; i-- {
		if e := m.Chat[i]; e.Kind == AssistantEntry && !e.Bash && strings.TrimSpace(e.Text) != "" {
			m.local("Copied claude's last reply")
			return tea.SetClipboard(strings.TrimSpace(e.Text))
		}
	}
	m.local("/copy: no reply to copy yet")
	return nil
}

// PickPopup offers what a slash command takes; enter sends the command with the one picked.
type PickPopup struct {
	Command string   // without "/"
	Options []Option // Label is what the command takes
	Current int      // the option in effect; -1 when not known
	Cursor  int
}

func (*PickPopup) isPopup() {}

// openPicker opens the list of what cmd takes, for the commands that print their usage when typed bare
// in claude -p instead of opening a picker; it reports whether it did.
func (m *Model) openPicker(cmd string) bool {
	p := &PickPopup{Command: cmd, Current: -1}
	current := m.currentModel()
	switch cmd {
	case "model":
		for _, md := range m.cli.models {
			if md.Resolved == m.Status.Model && p.Current < 0 {
				p.Current = len(p.Options)
			}
			p.Options = append(p.Options, Option{Label: md.Value, Description: strings.TrimSuffix(md.Name+" · "+md.Description, " · ")})
		}
	case "effort":
		if current != nil {
			for _, e := range current.Efforts {
				if e == m.Status.Effort {
					p.Current = len(p.Options)
				}
				p.Options = append(p.Options, Option{Label: e})
			}
			p.Options = append(p.Options, Option{Label: "auto", Description: "the model's own choice"})
		}
	case "output-style":
		for _, s := range m.cli.outputStyles {
			if s == m.cli.outputStyle {
				p.Current = len(p.Options)
			}
			p.Options = append(p.Options, Option{Label: s})
		}
	}
	if len(p.Options) == 0 || m.Popup != nil {
		return false // claude prints what it can
	}
	p.Cursor = max(p.Current, 0)
	m.Popup = p
	return true
}

// currentModel is the /model choice that runs the model of the session; nil when not known.
func (m *Model) currentModel() *protocol.Model {
	for i, md := range m.cli.models {
		if md.Resolved == m.Status.Model {
			return &m.cli.models[i]
		}
	}
	return nil
}

func (m *Model) pickKey(p *PickPopup, key string) tea.Cmd {
	switch key {
	case "up":
		p.Cursor = max(p.Cursor-1, 0)
	case "down":
		p.Cursor = min(p.Cursor+1, len(p.Options)-1)
	case "esc":
		m.closeResume()
	case "enter":
		m.closeResume()
		return m.prompt("/" + p.Command + " " + p.Options[p.Cursor].Label)
	}
	return nil
}

// BtwPopup is /btw: a question to the model beside the conversation, then its answer.
type BtwPopup struct {
	RequestID string
	Question  string
	Answer    string // "" while the model thinks
	Failed    bool   // Answer says why there is none
	Scroll    int    // first line of the answer on screen
}

func (*BtwPopup) isPopup() {}

// btw asks question beside the conversation: claude answers at once, a turn in progress goes on,
// and neither joins the conversation. The side questions answered before go along, for follow-ups.
func (m *Model) btw(question string) {
	switch {
	case strings.TrimSpace(question) == "":
		m.local("/btw <question>: ask while claude works, outside the conversation")
	case m.Status.Replay:
		m.local("replay: /btw is not available")
	case m.Status.State == "exited":
		m.local("Error: claude has exited: /btw has no one to ask")
	default:
		m.btwReqs++
		p := &BtwPopup{RequestID: fmt.Sprintf("btw-%d", m.btwReqs), Question: question}
		m.send(protocol.SideQuestion{RequestID: p.RequestID, Question: question, History: m.btwSaid})
		m.Popup = p
	}
}

// btwAnswered puts the answer to side question id in its popup, unless that was closed before, and
// keeps it for the questions to come.
func (m *Model) btwAnswered(id, text string, failed bool) {
	p, ok := m.Popup.(*BtwPopup)
	if !ok || p.RequestID != id {
		return
	}
	p.Answer, p.Failed = text, failed
	if !failed {
		m.btwSaid = append(m.btwSaid, protocol.SideQA{Question: p.Question, Response: text})
	}
}

func (m *Model) btwKey(p *BtwPopup, key string) {
	switch key {
	case "up":
		p.Scroll--
	case "down":
		p.Scroll++
	case "pgup":
		p.Scroll -= 10
	case "pgdown":
		p.Scroll += 10
	case "esc", "enter", "space":
		m.closeResume()
	}
}
