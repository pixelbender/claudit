package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/protocol"
)

// modelTitle is the terminal's title m sets.
func modelTitle(m *Model) string { return m.View().WindowTitle }

// modelTitleNew is a live model in a project called MTS.
func modelTitleNew(t *testing.T) (*Model, *[]protocol.Outbound) {
	t.Helper()
	m, sent := modelNew(t, false)
	modelEvents(m, protocol.Init{SessionID: "s1", Model: "m", Cwd: filepath.Join(t.TempDir(), "MTS")})
	return m, sent
}

// The conversation is named by an issue key in its first prompts, with words of that prompt,
// else by the first words of its first prompt that says enough; greetings, commands, answers,
// links and placeholders name nothing.
func TestModelTitleName(t *testing.T) {
	prompt := func(text string) ChatEntry { return ChatEntry{Kind: UserEntry, Text: text} }
	cases := []struct {
		name string
		chat []ChatEntry
		want string
	}{
		{"none yet", nil, ""},
		{"greeting", []ChatEntry{prompt("Are you there?"), prompt("hi")}, ""},
		{"first words", []ChatEntry{prompt("Are you there?"), prompt("Why does the formatter cut the tail of a record")},
			"Why does the formatter cut the tail of a"},
		{"key first", []ChatEntry{prompt("Why does the formatter cut the tail"), prompt("It is https://tracker.example.com/issue/ABC-125036, see it"),
			prompt("DEV-1: other")}, "ABC-125036 It is see it"},
		{"key with words", []ChatEntry{prompt("ABC-125036: record tail cut [Pasted text #1 +20 lines]")}, "ABC-125036 record tail cut"},
		{"key too late", []ChatEntry{prompt("one"), prompt("two"), prompt("three"), prompt("four"),
			prompt("Five words to name it"), prompt("DEV-7 late")}, "Five words to name it"},
		{"not prompts", []ChatEntry{{Kind: UserEntry, Text: "DEV-2 answer to a question", Answer: true},
			{Kind: UserEntry, Text: "DEV-3 queued for the next turn", Queued: true},
			{Kind: CommandEntry, Text: "DEV-4 said by the CLI"}, prompt("/rename DEV-5"), prompt("!git log DEV-6 here"),
			prompt("<bash-input>ls DEV-8</bash-input>"), prompt("Fix the flaky test in sessions")}, "Fix the flaky test in sessions"},
		{"not keys", []ChatEntry{prompt("UTF-8 GPT-4 SHA-256 AES-128 ISO-8601 COVID-19 CVE-2024-1234 RFC-2119 TLS-13 RSA-2048")},
			"UTF-8 GPT-4 SHA-256 AES-128 ISO-8601"},
		{"a key among them", []ChatEntry{prompt("SHA-256 of the DEV-77 file is wrong")}, "DEV-77 SHA-256 of the file is wrong"},
		{"short keys", []ChatEntry{prompt("ABC-12 fix it")}, "ABC-12 fix it"},
		{"one digit is no key", []ChatEntry{prompt("Why does the formatter cut the tail"), prompt("see DEV-7 here")}, "Why does the formatter cut the tail"},
		{"one letter is no key", []ChatEntry{prompt("Why does the formatter cut the tail"), prompt("see A1-77 here")}, "Why does the formatter cut the tail"},
		{"secrets", []ChatEntry{prompt("Deploy with ghp_" + strings.Repeat("a", 36) + " and TOKEN=abc123 then AKIA" + strings.Repeat("B", 16) +
			" or glpat-" + strings.Repeat("x", 20) + " sk-ant-api03-abcdef xoxb-123-456 now")}, "Deploy with and then or now"},
		{"a hash", []ChatEntry{prompt("commit 0123456789abcdef0123456789abcdef01234567 broke the build")}, "commit broke the build"},
		{"secrets beside a key", []ChatEntry{prompt("DEV-12 use key=sk123 here ok")}, "DEV-12 use here ok"},
		{"not secrets", []ChatEntry{prompt("sk-learn xoxo abcdefghijklmnopqrs is it")}, "sk-learn xoxo abcdefghijklmnopqrs is it"},
		{"long word", []ChatEntry{prompt(strings.Repeat("x.", 25) + " and more words")}, strings.Repeat("x.", 19) + "x…"},
	}
	for _, c := range cases {
		m, _ := modelTitleNew(t)
		m.Chat = c.chat
		if got := m.sessionName(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The title names the conversation and the project and marks what claude does: a spinner while
// it works, "?" while it asks, "✓" for a turn that ended while the user was away, "!" for one that
// failed, until the window has the focus again or claude works again.
func TestModelTitleMarks(t *testing.T) {
	m, _ := modelTitleNew(t)
	if s := modelTitle(m); s != "claudit · MTS" {
		t.Errorf("idle, no name: %q", s)
	}
	modelResumeSubmit(m, "Fix the flaky test in sessions please")
	if s := modelTitle(m); s != "● Fix the flaky test in sessions please · MTS" {
		t.Errorf("working: %q", s)
	}
	m.Now = m.Now.Add(300 * time.Millisecond) // the mark stands still: the title is written when it changes
	m.sync()
	if s := modelTitle(m); s != "● Fix the flaky test in sessions please · MTS" {
		t.Errorf("working, a moment later: %q", s)
	}
	modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	if s := modelTitle(m); s != "? Fix the flaky test in sessions please · MTS" {
		t.Errorf("asking: %q", s)
	}
	m.Popup, m.Status.State = nil, "thinking"
	modelEvents(m, protocol.Result{})
	if s := modelTitle(m); s != "Fix the flaky test in sessions please · MTS" {
		t.Errorf("done in sight: %q", s)
	}

	m.Update(tea.BlurMsg{})
	modelResumeSubmit(m, "And the other one")
	modelEvents(m, protocol.Result{})
	if s := modelTitle(m); s != "✓ Fix the flaky test in sessions please · MTS" {
		t.Errorf("done away: %q", s)
	}
	m.Update(tea.FocusMsg{})
	if s := modelTitle(m); s != "Fix the flaky test in sessions please · MTS" {
		t.Errorf("seen: %q", s)
	}

	m.Update(tea.BlurMsg{})
	modelResumeSubmit(m, "Once more")
	modelEvents(m, protocol.Result{})
	modelResumeSubmit(m, "And again") // claude works again: it is not done
	if s := modelTitle(m); strings.HasPrefix(s, "✓") || m.unseen != "" {
		t.Errorf("new turn: %q", s)
	}
	modelEvents(m, protocol.Result{})
	if s := modelTitle(m); !strings.HasPrefix(s, "✓ ") {
		t.Errorf("done away again: %q", s)
	}
}

// A turn that failed while the user was away says so with "!", not "✓", an error that says
// nothing too; claude gone says "✗" whether the user is away or not, and a resumed conversation
// starts without it.
func TestModelTitleFailed(t *testing.T) {
	for _, text := range []string{"API Error: 500 boom", ""} {
		m, _ := modelTitleNew(t)
		m.Update(tea.BlurMsg{})
		modelResumeSubmit(m, "Fix the flaky test in sessions please")
		modelEvents(m, protocol.Result{IsError: true, Text: text})
		if s := modelTitle(m); !strings.HasPrefix(s, "! Fix the flaky") {
			t.Errorf("failed away with %q: %q", text, s)
		}
		m.Update(tea.FocusMsg{})
		if s := modelTitle(m); strings.HasPrefix(s, "!") {
			t.Errorf("seen: %q", s)
		}
	}
	m, _ := modelTitleNew(t)
	modelResumeSubmit(m, "Fix the flaky test in sessions please")
	modelEvents(m, protocol.Exited{})
	if s := modelTitle(m); s != "✗ Fix the flaky test in sessions please · MTS" {
		t.Errorf("exited in sight: %q", s)
	}
	m.Update(tea.BlurMsg{})
	if s := modelTitle(m); !strings.HasPrefix(s, "✗ ") {
		t.Errorf("exited away: %q", s)
	}
}

// A conversation named by its prompts is cut once while the chat stays as it is, and its name
// follows the chat when it changes: more prompts, /clear, another conversation of as many entries.
func TestModelTitleNameKept(t *testing.T) {
	m, _ := modelTitleNew(t)
	for i := range 200 {
		m.Chat = append(m.Chat, ChatEntry{Kind: UserEntry, Text: "Why does the formatter cut the tail of a record " + strings.Repeat("x ", i)},
			ChatEntry{Kind: AssistantEntry, Text: "Because."})
	}
	if got := m.sessionName(); got != "Why does the formatter cut the tail of a" {
		t.Fatalf("name %q", got)
	}
	if n := testing.AllocsPerRun(10, func() { m.sessionName() }); n != 0 {
		t.Errorf("a chat named already is scanned again: %v allocations", n)
	}
	m.Chat = append(m.Chat, ChatEntry{Kind: UserEntry, Text: "DEV-4242 is the key"}) // too late for a key
	if got := m.sessionName(); got != "Why does the formatter cut the tail of a" {
		t.Errorf("a late prompt: %q", got)
	}
	m.Chat = append(make([]ChatEntry, 0, 8), ChatEntry{Kind: UserEntry, Text: "Why does the formatter cut the tail"})
	if got := m.sessionName(); got != "Why does the formatter cut the tail" {
		t.Fatalf("first prompt: %q", got)
	}
	m.Chat = append(m.Chat, ChatEntry{Kind: UserEntry, Text: "DEV-4242 is the key"}) // room left: the same array
	if got := m.sessionName(); got != "DEV-4242 is the key" {
		t.Errorf("a prompt with a key: %q", got)
	}
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "DEV-4242 the tail is cut"}}
	if got := m.sessionName(); got != "DEV-4242 the tail is cut" {
		t.Errorf("another chat: %q", got)
	}
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "DEV-4243 the head is cut"}} // as long, other prompts
	if got := m.sessionName(); got != "DEV-4243 the head is cut" {
		t.Errorf("another chat of as many entries: %q", got)
	}
	modelEvents(m, protocol.Reset{})
	if got := m.sessionName(); got != "" {
		t.Errorf("cleared: %q", got)
	}
}

// A /rename cut short by claude exiting names nothing: the name asked for is not the title's.
func TestModelRenameExited(t *testing.T) {
	m, _ := modelTitleNew(t)
	modelResumeSubmit(m, "Fix the flaky test in sessions please")
	modelEvents(m, protocol.Result{})
	modelResumeSubmit(m, "/rename Mine")
	if s := m.sessionName(); s != "Mine" {
		t.Fatalf("as it goes: %q", s)
	}
	modelEvents(m, protocol.Exited{})
	if s := m.sessionName(); s != "Fix the flaky test in sessions please" || m.renameTurn {
		t.Errorf("claude exited: %q, rename turn %v", s, m.renameTurn)
	}
}

// The project shows as the folder's name; a root has none.
func TestModelTitleProject(t *testing.T) {
	m, _ := modelTitleNew(t)
	if p := m.project(); p != "MTS" {
		t.Errorf("project %q", p)
	}
	modelEvents(m, protocol.Init{SessionID: "s1", Model: "m", Cwd: "/"})
	if p, s := m.project(), modelTitle(m); p != "" || s != "claudit" {
		t.Errorf("at the root: project %q, title %q", p, s)
	}
}

// A list the user opened waits for nobody: the title asks only for claude's questions.
func TestModelTitleOwnPopup(t *testing.T) {
	m, _ := modelTitleNew(t)
	m.Popup = &ResumePopup{Current: "s1"}
	if s, bar := m.View().WindowTitle, m.View().ProgressBar; s != "claudit · MTS" || bar != nil {
		t.Errorf("own list: %q %v", s, bar)
	}
}

// claude -p runs /rename itself: the name shows as it goes and becomes the one claude says it
// took; a /rename claude turns down leaves the name as it was. Without a name, /rename says what
// the conversation is called, with no turn: Claude Code would have a model make one up.
func TestModelRename(t *testing.T) {
	m, sent := modelTitleNew(t)
	modelResumeSubmit(m, "DEV-95 cut tail")
	modelEvents(m, protocol.Result{})
	n := len(*sent)
	modelResumeSubmit(m, "/rename")
	if len(*sent) != n || !modelResumeSaid(m, "This conversation has no name; the title calls it DEV-95 cut tail") {
		t.Fatalf("/rename alone: sent %v, chat %+v", (*sent)[n:], m.Chat[len(m.Chat)-1])
	}

	modelResumeSubmit(m, "/rename My talk")
	if len(*sent) != n+1 || modelUserText((*sent)[n]) != "/rename My talk" {
		t.Fatalf("/rename goes to claude: %v", (*sent)[n:])
	}
	if s := modelTitle(m); !strings.HasSuffix(s, " My talk · MTS") {
		t.Errorf("as it goes: %q", s)
	}
	modelEvents(m, protocol.CLIReply{Text: `Session renamed to: My talk-2 ("My talk" is held by another live session on this machine)`},
		protocol.Result{})
	if s := modelTitle(m); s != "My talk-2 · MTS" {
		t.Errorf("as claude took it: %q", s)
	}
	modelResumeSubmit(m, "/name")
	if !modelResumeSaid(m, "This conversation is named My talk-2 · /rename <name> renames it") {
		t.Errorf("/name alone: %+v", m.Chat[len(m.Chat)-1])
	}

	modelResumeSubmit(m, "/rename Other")
	modelEvents(m, protocol.CLIReply{Text: "Cannot rename: This session is a teammate. Teammate names are set by the team leader."},
		protocol.Result{})
	if s := m.sessionName(); s != "My talk-2" {
		t.Errorf("turned down: %q", s)
	}

	// One typed while claude works shows once it goes to claude.
	modelResumeSubmit(m, "Go on with it")
	modelResumeSubmit(m, "/name Later")
	if s := m.sessionName(); s != "My talk-2" {
		t.Errorf("queued: %q", s)
	}
	modelEvents(m, protocol.Result{})
	if s := m.sessionName(); s != "Later" || modelUserText((*sent)[len(*sent)-1]) != "/name Later" {
		t.Errorf("sent after the turn: %q", s)
	}
	modelEvents(m, protocol.CLIReply{Text: "Session renamed to: Later. The new name may not last: the session transcript could not be updated. Run /rename Later to try again (run with --debug for the cause)"},
		protocol.Result{})
	if s := m.sessionName(); s != "Later" {
		t.Errorf("a name claude may not keep: %q", s)
	}

	modelEvents(m, protocol.Init{SessionID: "s2", Model: "m"}) // another conversation
	if s := m.sessionName(); s != "DEV-95 cut tail" {
		t.Errorf("new conversation: %q", s)
	}
}

// A resumed conversation has the name claude -p's /rename saved in its file.
func TestModelRenameResume(t *testing.T) {
	m, _ := modelResumeNew(t)
	modelResumeWrite(t, m, "named", modelT0.Add(-time.Hour),
		`{"type":"user","message":{"role":"user","content":"Look at DEV-3 please"},"timestamp":"2026-10-28T09:15:00.000Z"}`,
		`{"type":"ai-title","aiTitle":"Made up","sessionId":"named"}`,
		`{"type":"custom-title","customTitle":"Tail cut","sessionId":"named"}`)
	modelResumeSubmit(m, "/resume named")
	modelEvents(m, protocol.Init{SessionID: "named", Model: "m"})
	if s := m.sessionName(); s != "Tail cut" {
		t.Errorf("resumed: %q", s)
	}
	modelResumeSubmit(m, "/resume a1") // without a name, its prompts name it: "add div" says too little
	if s := m.sessionName(); s != "" {
		t.Errorf("resumed without a name: %q", s)
	}
}
