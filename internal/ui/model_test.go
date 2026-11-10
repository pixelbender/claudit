package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/protocol"
)

// modelT0 is when every test model starts; its clock stays there until modelAt moves it.
var modelT0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func modelNew(t *testing.T, replay bool) (*Model, *[]protocol.Outbound) {
	t.Helper()
	sent := new([]protocol.Outbound)
	m := New(func(o protocol.Outbound) { *sent = append(*sent, o) }, t.TempDir(), replay)
	m.Now = modelT0
	modelAt(m, 0)
	return m, sent
}

// modelAt sets the model's clock to modelT0+d and returns that time.
func modelAt(m *Model, d time.Duration) time.Time {
	at := modelT0.Add(d)
	m.clock = func() time.Time { return at }
	return at
}

// modelAgent returns the agent with that key, failing the test when there is none.
func modelAgent(t *testing.T, m *Model, key string) *ide.Agent {
	t.Helper()
	a := m.Sim.FindAgent(key)
	if a == nil {
		t.Fatalf("no agent %q", key)
	}
	return a
}

func modelPress(m *Model, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		m.Update(k)
	}
}

func modelEvents(m *Model, evs ...protocol.Event) {
	for _, ev := range evs {
		m.Update(AgentMsg{Events: []protocol.Event{ev}})
	}
}

func modelUserText(o protocol.Outbound) string {
	u, _ := o.(protocol.UserMessage)
	return u.Text
}

func modelJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// modelAsk opens an AskUserQuestion popup for the given tool input JSON.
func modelAsk(t *testing.T, m *Model, id, input string) *QuestionPopup {
	t.Helper()
	modelEvents(m, protocol.PermissionRequest{RequestID: id, ToolName: "AskUserQuestion", Input: modelJSON(t, input)})
	p, ok := m.Popup.(*QuestionPopup)
	if !ok {
		t.Fatalf("popup = %#v, want *QuestionPopup", m.Popup)
	}
	return p
}

// modelWantAllow checks that exactly one message went out: Allow for id with input plus answers.
func modelWantAllow(t *testing.T, sent []protocol.Outbound, id, input string, answers map[string]string) {
	t.Helper()
	want := modelJSON(t, input)
	want["answers"] = answers
	if len(sent) != 1 || !reflect.DeepEqual(sent[0], protocol.Allow{RequestID: id, Input: want}) {
		t.Fatalf("sent %#v\nwant Allow %#v", sent, want)
	}
}

const modelFixtureQuestion = `{"questions":[{"question":"Which function should I add to calc.py: mul or div?","header":"Function",
	"options":[{"label":"mul","description":"Multiplication: returns a * b"},{"label":"div","description":"Division: returns a / b"}],
	"multiSelect":false}]}`

func TestModelQuestionSingleSelect(t *testing.T) {
	m, sent := modelNew(t, false)
	p := modelAsk(t, m, "r1", modelFixtureQuestion)
	q := p.Questions[0]
	if q.Header != "Function" || len(q.Options) != 2 || q.Options[1] != (Option{"div", "Division: returns a / b"}) ||
		q.Multi || len(q.Checked) != 3 {
		t.Fatalf("parsed question %#v", q)
	}
	if m.Status.State != "waiting for you" {
		t.Errorf("state %q", m.Status.State)
	}
	modelPress(m, inputKeys("up up down")...)
	if p.Questions[0].Cursor != 1 {
		t.Fatalf("cursor %d, want 1", p.Questions[0].Cursor)
	}
	modelPress(m, inputKey("enter"))
	modelWantAllow(t, *sent, "r1", modelFixtureQuestion, map[string]string{"Which function should I add to calc.py: mul or div?": "div"})
	if m.Popup != nil || m.Status.State != "thinking" {
		t.Errorf("popup %#v state %q after answer", m.Popup, m.Status.State)
	}
	// The question and its answer join the chat, as claude's words and the user's.
	want := []ChatEntry{{Kind: AssistantEntry, Text: "Which function should I add to calc.py: mul or div?", At: modelT0}, {Kind: UserEntry, Text: "div", At: modelT0, Answer: true}}
	if !reflect.DeepEqual(m.Chat, want) {
		t.Errorf("chat %+v", m.Chat)
	}
}

const modelMultiQuestion = `{"questions":[{"question":"Which?","header":"Pick","multiSelect":true,
	"options":[{"label":"A","description":"a"},{"label":"B","description":"b"},{"label":"C","description":"c"}]}]}`

func TestModelQuestionMultiSelect(t *testing.T) {
	m, sent := modelNew(t, false)
	p := modelAsk(t, m, "r1", modelMultiQuestion)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 || m.Popup == nil {
		t.Fatal("enter with nothing checked must be ignored")
	}
	modelPress(m, inputKeys("space down space space down space")...)
	if got := p.Questions[0].Checked; !reflect.DeepEqual(got, []bool{true, false, true, false}) {
		t.Fatalf("checked %v", got)
	}
	modelPress(m, inputKey("enter"))
	modelWantAllow(t, *sent, "r1", modelMultiQuestion, map[string]string{"Which?": "A, C"})
}

func TestModelQuestionOtherSingle(t *testing.T) {
	m, sent := modelNew(t, false)
	p := modelAsk(t, m, "r1", modelFixtureQuestion)
	modelPress(m, inputKeys("down down down")...)
	if p.Questions[0].Cursor != 2 {
		t.Fatalf("cursor %d, want 2 (Other)", p.Questions[0].Cursor)
	}
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 {
		t.Fatal("enter on an empty Other must be ignored")
	}
	modelPress(m, inputTyped(" my own choiceX")...)
	modelPress(m, inputKeys("backspace left left right right")...)
	if got := string(p.Questions[0].Other.Text); got != " my own choice" {
		t.Fatalf("other %q", got)
	}
	modelPress(m, inputKey("enter"))
	modelWantAllow(t, *sent, "r1", modelFixtureQuestion, map[string]string{"Which function should I add to calc.py: mul or div?": "my own choice"})
}

func TestModelQuestionOtherMulti(t *testing.T) {
	m, sent := modelNew(t, false)
	p := modelAsk(t, m, "r1", modelMultiQuestion)
	q := &p.Questions[0]
	modelPress(m, inputKeys("space down down down")...)
	m.Update(tea.PasteMsg{Content: "x\ny"})
	if string(q.Other.Text) != "x y" || !q.Checked[3] {
		t.Fatalf("after paste: other %q checked %v", string(q.Other.Text), q.Checked)
	}
	modelPress(m, inputKey("ctrl+u"))
	if q.Checked[3] {
		t.Fatal("clearing Other must uncheck it")
	}
	modelPress(m, inputTyped("my own")...)
	modelPress(m, inputKey("enter"))
	modelWantAllow(t, *sent, "r1", modelMultiQuestion, map[string]string{"Which?": "A, my own"})
}

func TestModelQuestionTwoQuestions(t *testing.T) {
	const input = `{"questions":[
		{"question":"Lang?","header":"Lang","multiSelect":false,"options":[{"label":"Go","description":""},{"label":"Rust","description":""}]},
		{"question":"Extras?","header":"Extras","multiSelect":true,"options":[{"label":"tests","description":""},{"label":"docs","description":""}]}]}`
	m, sent := modelNew(t, false)
	p := modelAsk(t, m, "r7", input)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 || p.Current != 1 || m.Popup != p {
		t.Fatalf("after first answer: sent %v current %d", *sent, p.Current)
	}
	modelPress(m, inputKeys("space down space enter")...)
	modelWantAllow(t, *sent, "r7", input, map[string]string{"Lang?": "Go", "Extras?": "tests, docs"})
	if m.Popup != nil {
		t.Error("popup still open")
	}
}

func TestModelQuestionEscDenies(t *testing.T) {
	m, sent := modelNew(t, false)
	modelAsk(t, m, "r1", modelFixtureQuestion)
	modelPress(m, inputKeys("down esc")...)
	want := []protocol.Outbound{protocol.Deny{RequestID: "r1", Message: "The user dismissed the question."}}
	if !reflect.DeepEqual(*sent, want) || m.Popup != nil {
		t.Fatalf("sent %#v popup %#v", *sent, m.Popup)
	}
}

func TestModelPermissionKeys(t *testing.T) {
	const input = `{"command":"rm -rf build && make","description":"Rebuild"}`
	allow := protocol.Allow{RequestID: "p1"}
	deny := protocol.Deny{RequestID: "p1", Message: "The user denied this tool call."}
	tests := []struct {
		keys  string
		want  protocol.Outbound // nil: still open
		state string
	}{
		{"y", allow, "running"},
		{"n", deny, "thinking"},
		{"enter", allow, "running"},
		{"right enter", deny, "thinking"},
		{"left left enter", allow, "running"},
		{"tab enter", deny, "thinking"},
		{"esc", deny, "thinking"},
		{"x up down space", nil, "waiting for you"},
	}
	for _, tt := range tests {
		t.Run(tt.keys, func(t *testing.T) {
			m, sent := modelNew(t, false)
			in := modelJSON(t, input)
			modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: in})
			p, ok := m.Popup.(*PermissionPopup)
			if !ok || p.Tool != "Bash" || p.Detail != "rm -rf build && make" || !p.Allow {
				t.Fatalf("popup %#v", m.Popup)
			}
			modelPress(m, inputKeys(tt.keys)...)
			if len(m.Input.Text) != 0 {
				t.Errorf("keys leaked into the prompt: %q", string(m.Input.Text))
			}
			if m.Status.State != tt.state {
				t.Errorf("state %q, want %q", m.Status.State, tt.state)
			}
			if tt.want == nil {
				if len(*sent) != 0 || m.Popup == nil {
					t.Fatalf("sent %#v popup %#v, want still open", *sent, m.Popup)
				}
				return
			}
			want := tt.want
			if a, ok := want.(protocol.Allow); ok {
				a.Input = modelJSON(t, input)
				want = a
			}
			if !reflect.DeepEqual(*sent, []protocol.Outbound{want}) || m.Popup != nil {
				t.Fatalf("sent %#v popup %#v, want %#v", *sent, m.Popup, want)
			}
		})
	}
}

func TestModelPopupQueue(t *testing.T) {
	m, sent := modelNew(t, false)
	modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	modelEvents(m,
		// An AskUserQuestion without questions falls back to a plain permission popup.
		protocol.PermissionRequest{RequestID: "p2", ToolName: "AskUserQuestion", Input: map[string]any{}},
		protocol.PermissionRequest{RequestID: "q2", ToolName: "AskUserQuestion", Input: modelJSON(t, modelFixtureQuestion)},
	)
	if p, ok := m.Popup.(*PermissionPopup); !ok || p.RequestID != "p1" || len(m.queue) != 2 {
		t.Fatalf("popup %#v queue %d", m.Popup, len(m.queue))
	}
	modelPress(m, inputKey("y"))
	if p, ok := m.Popup.(*PermissionPopup); !ok || p.RequestID != "p2" || p.Tool != "AskUserQuestion" {
		t.Fatalf("second popup %#v", m.Popup)
	}
	if m.Status.State != "waiting for you" {
		t.Errorf("state %q while popups are queued", m.Status.State)
	}
	modelPress(m, inputKey("n"))
	if p, ok := m.Popup.(*QuestionPopup); !ok || p.RequestID != "q2" {
		t.Fatalf("third popup %#v", m.Popup)
	}
	modelPress(m, inputKey("enter"))
	if m.Popup != nil || len(m.queue) != 0 || len(*sent) != 3 {
		t.Fatalf("popup %#v queue %d sent %d", m.Popup, len(m.queue), len(*sent))
	}
	ids := []string{(*sent)[0].(protocol.Allow).RequestID, (*sent)[1].(protocol.Deny).RequestID, (*sent)[2].(protocol.Allow).RequestID}
	if !reflect.DeepEqual(ids, []string{"p1", "p2", "q2"}) {
		t.Errorf("answered %v", ids)
	}
}

func TestModelText(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Thinking{})
	if m.Status.State != "thinking" || !m.Status.Busy {
		t.Fatalf("status %+v", m.Status)
	}
	modelEvents(m,
		protocol.TextStart{}, protocol.TextDelta{Text: "Hel"}, protocol.TextDelta{Text: "lo"},
		protocol.ToolUse{ID: "t1", Name: "Bash", Input: map[string]any{"command": "ls"}},
		protocol.TextDelta{Text: "orphan"},
		protocol.TextStart{}, protocol.TextDelta{Text: "next"},
	)
	got := make([]string, len(m.Chat))
	for i, e := range m.Chat {
		got[i] = []string{"user", "assistant"}[e.Kind] + ":" + e.Text
	}
	want := []string{"assistant:Helloorphan", "assistant:next"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chat %q, want %q", got, want)
	}
	if m.Status.State != "responding" {
		t.Errorf("state %q", m.Status.State)
	}
}

func TestModelToolUseAndResult(t *testing.T) {
	m, _ := modelNew(t, false)
	root := m.Sim.State().Root
	file := filepath.Join(root, "src", "a.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modelEvents(m,
		protocol.ToolUse{ID: "t1", Name: "Bash", Input: map[string]any{"command": "echo 1\necho 2"}},
		protocol.ToolUse{ID: "t2", Name: "Read", Input: map[string]any{"file_path": file}},
	)
	if m.Status.State != "running" || !m.Status.Busy {
		t.Errorf("status %+v", m.Status)
	}
	modelEvents(m, protocol.Thinking{}, protocol.ToolUse{ID: "t3", Name: "Glob", Input: map[string]any{"pattern": "*.go"}, Parent: "t0"})
	if m.Status.State != "thinking" { // a subagent's call does not change main's
		t.Errorf("status %+v", m.Status)
	}
	modelEvents(m,
		protocol.ToolResult{ID: "t2", Output: "1\tpackage a\n2\t\n"},
		protocol.ToolResult{ID: "t1", Output: "<tool_use_error>boom\nmore</tool_use_error>", IsError: true},
		protocol.ToolResult{ID: "t3", Output: ""},
		protocol.ToolResult{ID: "unknown", Output: "x"},
	)
	// The call of an unknown subagent registers it.
	sub := modelAgent(t, m, "t0")
	if got := modelOpen(m); len(got) != 0 {
		t.Errorf("open calls %q", got)
	}
	if len(m.Chat) != 0 {
		t.Errorf("tool calls in the chat: %+v", m.Chat)
	}
	if sub.Kind != ide.Subagent || sub.Parent != "main" || sub.ToolCalls != 1 || sub.Started != modelT0 {
		t.Errorf("unknown subagent %+v", sub)
	}
	if main := modelAgent(t, m, "main"); main.ToolCalls != 2 {
		t.Errorf("main made %d calls, want 2", main.ToolCalls)
	}
}

func TestModelPermissionDetail(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "calc.py")
	tests := []struct {
		name  string
		input map[string]any
		want  string
	}{
		{"Bash", map[string]any{"command": "a\nb"}, "a\nb"},
		{"Edit", map[string]any{"file_path": file, "old_string": "x = 1\n", "new_string": "x = 2\ny = 3\n"},
			"calc.py\n-x = 1\n+x = 2\n+y = 3"},
		{"Edit", map[string]any{"file_path": file, "old_string": "1\n2\n3\n4\n5", "new_string": "z"},
			"calc.py\n-1\n-2\n-3\n-4\n-5\n+z"},
		// An anchor kept on both sides is context, not part of the change.
		{"Edit", map[string]any{"file_path": file, "old_string": "def sub(a, b):\n    return a - b\n",
			"new_string": "def sub(a, b):\n    return a - b\n\n\ndef div(a, b):\n    return a / b\n"},
			"calc.py\n def sub(a, b):\n     return a - b\n+\n+\n+def div(a, b):\n+    return a / b"},
		{"Edit", map[string]any{"file_path": file, "old_string": "a\nb\nc\n", "new_string": "a\nB\nc\n"},
			"calc.py\n a\n-b\n+B\n c"},
		{"Write", map[string]any{"file_path": file, "content": "1\n2\n3\n4\n5\n6\n7\n8\n9\n"},
			"calc.py\n1\n2\n3\n4\n5\n6\n7\n8\n9"},
		{"Read", map[string]any{"file_path": file, "offset": 10.0, "limit": 5.0}, "calc.py · lines 10–14"},
		{"Grep", map[string]any{"pattern": "def ", "path": root}, "def  in ."},
		{"ExitPlanMode", map[string]any{"plan": "1. do it"}, "1. do it"},
		{"Workflow", map[string]any{"script": "phase('Build')\nagent(\"x\")", "description": "d"}, "phase('Build')\nagent(\"x\")"},
		{"WebFetch", map[string]any{"url": "https://x.dev/?a=1&b=2"}, "https://x.dev/?a=1&b=2"},
		{"TodoWrite", map[string]any{"todos": []any{}}, "{\n  \"todos\": []\n}"},
	}
	for _, tt := range tests {
		if got := permissionDetail(tt.name, tt.input, root); got != tt.want {
			t.Errorf("permissionDetail(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
	// With the file on disk, the lines around the edit show too.
	dir := t.TempDir()
	path := filepath.Join(dir, "calc.py")
	if err := os.WriteFile(path, []byte("import math\n\ndef add(a, b):\n    return a + b\n\n\ndef sub(a, b):\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"file_path": path, "old_string": "    return a + b", "new_string": "    return b + a"}
	if got, want := permissionDetail("Edit", in, dir), "calc.py\n \n def add(a, b):\n-    return a + b\n+    return b + a\n \n "; got != want {
		t.Errorf("with context %q, want %q", got, want)
	}
}

func TestModelEnterSendsTrimmedPrompt(t *testing.T) {
	m, sent := modelNew(t, false)
	modelPress(m, inputKey("enter"))
	modelPress(m, inputTyped("   ")...)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 || len(m.Chat) != 0 {
		t.Fatalf("empty prompt was sent: %#v", *sent)
	}
	m.Input = Input{}
	m.ChatScroll = 7
	modelPress(m, inputTyped("  hello,   claude  ")...)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "Hello, claude" {
		t.Fatalf("sent %#v", *sent)
	}
	if len(m.Chat) != 1 || m.Chat[0] != (ChatEntry{Kind: UserEntry, Text: "Hello, claude", At: modelT0}) {
		t.Errorf("chat %#v", m.Chat)
	}
	if len(m.Input.Text) != 0 || m.Input.Cursor != 0 || m.ChatScroll != 0 {
		t.Errorf("input %q cursor %d scroll %d after send", string(m.Input.Text), m.Input.Cursor, m.ChatScroll)
	}
	if m.Status.State != "thinking" || !m.Status.Busy {
		t.Errorf("status %+v", m.Status)
	}
	modelPress(m, inputTyped("more")...)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || len(m.Chat) != 1 || !slices.Equal(m.pending, []string{"More"}) {
		t.Error("prompts must be accepted while busy, and wait for the turn to end")
	}
}

func TestModelTidy(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"  fix it  ", "Fix it"},
		{"fix  the   bug", "Fix the bug"},
		{"hello, claude", "Hello, claude"},
		{"what's up?", "What's up?"},
		{"fix it   \nplease\t", "Fix it\nplease"},
		{"one\n\n\n\ntwo\n \nthree", "One\n\ntwo\n\nthree"},
		{"run:\n    if  x:\n        y", "Run:\n    if x:\n        y"}, // indentation stays
		{"calc.py fails", "calc.py fails"},                            // not a plain word
		{"iOS app", "iOS app"},
		{"/model  opus", "/model opus"},
		{"[Pasted text #1 +5 lines] fix", "[Pasted text #1 +5 lines] fix"},
		{"Done", "Done"},
		{" \n ", ""},
	} {
		if got := tidy(tt.in); got != tt.want {
			t.Errorf("tidy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestModelReplayDoesNotSendPrompts(t *testing.T) {
	m, sent := modelNew(t, true)
	if !m.Status.Replay {
		t.Fatal("Status.Replay not set")
	}
	modelPress(m, inputTyped("Hi")...)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 {
		t.Fatalf("replay sent %#v", *sent)
	}
	want := []ChatEntry{{Kind: UserEntry, Text: "Hi", At: modelT0}, {Kind: CommandEntry, Text: "replay: prompts are not sent", At: modelT0}}
	if !reflect.DeepEqual(m.Chat, want) || m.Status.Busy {
		t.Errorf("chat %#v busy %v", m.Chat, m.Status.Busy)
	}
	// Popup answers still go out: the replay waits for them.
	modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	modelAt(m, popupGrace) // the user stopped typing
	modelPress(m, inputKey("y"))
	if len(*sent) != 1 {
		t.Errorf("replay must still answer popups, sent %#v", *sent)
	}
}

func TestModelKeys(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputTyped("abc")...)
	modelPress(m, inputKey("esc"))
	if len(m.Input.Text) != 0 || m.Input.Cursor != 0 {
		t.Errorf("esc left %q", string(m.Input.Text))
	}
	m.chatMax = 25
	var scrolls []int
	for _, k := range inputKeys("pgup pgup pgup pgdown pgdown pgdown pgdown") {
		m.Update(k)
		scrolls = append(scrolls, m.ChatScroll)
	}
	if want := []int{10, 20, 25, 15, 5, 0, 0}; !reflect.DeepEqual(scrolls, want) {
		t.Errorf("scrolls %v, want %v", scrolls, want)
	}
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if m.Width != 120 || m.Height != 40 {
		t.Errorf("size %dx%d", m.Width, m.Height)
	}
}

// shift+tab walks the permission modes; the agent's reports win, and an extra mode joins the cycle.
func TestModelPermissionMode(t *testing.T) {
	m, sent := modelNew(t, false)
	press := func() string {
		modelPress(m, inputKey("shift+tab"))
		return m.Status.Mode
	}
	if got := []string{press(), press(), press(), press()}; !slices.Equal(got, []string{"acceptEdits", "plan", "auto", "default"}) {
		t.Errorf("modes %v", got)
	}
	want := []protocol.Outbound{
		protocol.SetMode{RequestID: "mode-1", Mode: "acceptEdits"},
		protocol.SetMode{RequestID: "mode-2", Mode: "plan"},
		protocol.SetMode{RequestID: "mode-3", Mode: "auto"},
		protocol.SetMode{RequestID: "mode-4", Mode: "default"},
	}
	if !reflect.DeepEqual(*sent, want) {
		t.Errorf("sent %#v", *sent)
	}

	modelEvents(m, protocol.ModeChanged{Mode: "plan"})
	if m.Status.Mode != "plan" {
		t.Errorf("reported mode %q", m.Status.Mode)
	}
	// A refused switch falls back to the reported mode and says why.
	press()
	modelEvents(m, protocol.ControlError{RequestID: "mode-5", Message: "auto mode is unavailable"})
	if m.Status.Mode != "plan" {
		t.Errorf("mode after refusal %q", m.Status.Mode)
	}
	if n := len(m.Chat); n == 0 || m.Chat[n-1].Text != "Error: auto mode is unavailable" {
		t.Errorf("chat %+v", m.Chat)
	}
	modelEvents(m, protocol.Init{Model: "m", PermissionMode: "bypassPermissions"})
	if got := []string{m.Status.Mode, press(), press(), press(), press(), press()}; !slices.Equal(got,
		[]string{"bypassPermissions", "default", "acceptEdits", "plan", "auto", "bypassPermissions"}) {
		t.Errorf("modes with bypass %v", got)
	}

	// Nobody listens once claude has exited.
	modelEvents(m, protocol.Exited{})
	n := len(*sent)
	if press(); len(*sent) != n || m.Status.Mode != "bypassPermissions" {
		t.Errorf("mode switched after exit: %q", m.Status.Mode)
	}
}

// modelQuits reports whether pressing ctrl+c quits.
func modelQuits(m *Model) bool { return modelQuitsOn(m, "ctrl+c") }

func modelQuitsOn(m *Model, key string) bool {
	_, cmd := m.Update(inputKey(key))
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// ctrl+c as in Claude Code: it clears the prompt, else interrupts the turn, else quits on a
// second press within quitWindow.
func TestModelCtrlC(t *testing.T) {
	m, sent := modelNew(t, false)
	m.Input.insert("draft")
	if modelQuits(m) || len(m.Input.Text) != 0 {
		t.Fatalf("ctrl+c with a draft: input %q", string(m.Input.Text))
	}
	m.Status.Busy = true
	if modelQuits(m) || m.Status.State != "interrupting" || len(*sent) != 1 {
		t.Fatalf("ctrl+c while busy: state %q, sent %v", m.Status.State, *sent)
	}
	m.Status.Busy = false
	if modelQuits(m) {
		t.Fatal("the first ctrl+c quit")
	}
	modelAt(m, quitWindow+time.Second)
	if modelQuits(m) {
		t.Fatal("a ctrl+c long after the first quit")
	}
	modelAt(m, quitWindow+2*time.Second)
	if !modelQuits(m) {
		t.Fatal("the second ctrl+c did not quit")
	}

	m, _ = modelNew(t, false) // with a popup open: twice too, whatever its focus
	modelAsk(t, m, "r1", modelFixtureQuestion)
	modelPress(m, inputKeys("down down")...) // on the Other row, where keys are text
	if modelQuits(m) || !modelQuits(m) {
		t.Error("ctrl+c over a popup")
	}
	m, _ = modelNew(t, false) // once claude is gone, nothing is lost
	modelEvents(m, protocol.Exited{})
	if !modelQuits(m) {
		t.Error("ctrl+c after exit did not quit")
	}
}

// esc quits too, on a second press, once there is nothing left to cancel; it does not confirm a
// ctrl+c, nor a ctrl+c an esc.
func TestModelEscQuits(t *testing.T) {
	m, _ := modelNew(t, false)
	m.Input.insert("draft")
	m.Status.Busy = true
	if modelQuitsOn(m, "esc") || m.Status.State != "interrupting" || len(m.Input.Text) == 0 {
		t.Fatalf("esc while busy: state %q, input %q", m.Status.State, string(m.Input.Text))
	}
	if modelQuitsOn(m, "esc") { // still interrupting
		t.Fatal("esc quit while the turn was being interrupted")
	}
	m.Status.Busy = false
	if modelQuitsOn(m, "esc") || len(m.Input.Text) != 0 {
		t.Fatalf("esc with a draft: input %q", string(m.Input.Text))
	}
	if modelQuitsOn(m, "esc") {
		t.Fatal("the first esc quit")
	}
	if modelQuits(m) || modelQuitsOn(m, "esc") {
		t.Fatal("ctrl+c and esc mixed quit")
	}
	if !modelQuitsOn(m, "esc") {
		t.Fatal("the second esc did not quit")
	}

	m, _ = modelNew(t, false) // esc closes what is open first
	m.ShowHelp = true
	if modelQuitsOn(m, "esc") || modelQuitsOn(m, "esc") || m.ShowHelp {
		t.Fatal("esc over the help quit")
	}
}

// esc interrupts a turn, once; the error it ends with is no error to the user. The prompt of the
// turn turns red, a ✗ for its arrow, and nothing more is said.
func TestModelInterrupt(t *testing.T) {
	m, sent := modelNew(t, false)
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "go", At: modelT0}, {Kind: AssistantEntry, Text: "On it."}}
	m.Input.insert("next")
	m.Status.Busy = true
	modelPress(m, inputKey("esc"), inputKey("esc"))
	if want := []protocol.Outbound{protocol.Interrupt{RequestID: "interrupt-1"}}; !reflect.DeepEqual(*sent, want) {
		t.Fatalf("sent %v", *sent)
	}
	if string(m.Input.Text) != "next" {
		t.Errorf("esc dropped the draft: %q", string(m.Input.Text))
	}
	modelEvents(m, protocol.Result{IsError: true, Text: "[ede_diagnostic] result_type=user"})
	if len(m.Chat) != 2 || m.Chat[0] != (ChatEntry{Kind: UserEntry, Text: "go", At: modelT0, Cut: true}) || m.Status.Busy {
		t.Errorf("chat %+v busy %v", m.Chat, m.Status.Busy)
	}
	if got := drawEntry(m.Chat[0], 40); len(got) != 1 || got[0] != styleErr.Render("✗ go") {
		t.Errorf("interrupted prompt drawn %q", got)
	}
	modelPress(m, inputKey("esc")) // idle: esc clears the prompt
	if len(m.Input.Text) != 0 || len(*sent) != 1 {
		t.Errorf("idle esc: input %q sent %v", string(m.Input.Text), *sent)
	}
	m, sent = modelNew(t, true) // a replay has no turn to stop
	m.Status.Busy = true
	modelPress(m, inputKey("esc"))
	if len(*sent) != 0 {
		t.Errorf("replay sent %v", *sent)
	}
}

// up and down walk the prompts sent, back to what was typed.
func TestModelHistory(t *testing.T) {
	m, _ := modelNew(t, false)
	for _, p := range []string{"One", "/usage", "/usage", "Three"} {
		m.Input.insert(p)
		modelPress(m, inputKey("enter"))
		m.Status.Busy = false
	}
	m.Input.insert("dra")
	in := func() string { return string(m.Input.Text) }
	var got []string
	for _, k := range inputKeys("up up up up down down") {
		modelPress(m, k)
		got = append(got, in())
	}
	if want := []string{"Three", "/usage", "One", "One", "/usage", "Three"}; !reflect.DeepEqual(got, want) {
		t.Errorf("walk %q, want %q", got, want)
	}
	modelPress(m, inputKeys("up")...) // "/usage" again: its completions stay shut while browsing
	if len(m.Suggest) != 0 {
		t.Errorf("completions over a recalled prompt: %v", m.Suggest)
	}
	modelPress(m, inputKeys("down down")...)
	if in() != "dra" || m.Input.Cursor != 3 {
		t.Errorf("back to the draft: %q at %d", in(), m.Input.Cursor)
	}
	modelPress(m, inputKeys("up x up")...) // an edit ends browsing: up starts over from the newest
	if in() != "Three" {
		t.Errorf("after an edit: %q", in())
	}
}

// New lines: ctrl+j, alt+enter, shift+enter, or "\" before enter.
func TestModelNewline(t *testing.T) {
	m, sent := modelNew(t, false)
	modelPress(m, inputKeys("A ctrl+j b")...)
	modelPress(m, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	modelPress(m, inputKeys("c \\ enter d")...)
	if got := string(m.Input.Text); got != "A\nb\n\nc\nd" || len(*sent) != 0 {
		t.Errorf("input %q, sent %v", got, *sent)
	}
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "A\nb\n\nc\nd" {
		t.Errorf("sent %v", *sent)
	}
}

func TestModelTick(t *testing.T) {
	m, _ := modelNew(t, false)
	if m.Init() == nil {
		t.Fatal("Init returned no tick")
	}
	if got := m.tickInterval(); got != tickAmbient { // the spinner while claude starts
		t.Errorf("starting interval %v", got)
	}
	m.Status.State = "idle"
	if got := m.tickInterval(); got != tickIdle { // nothing moves
		t.Errorf("idle interval %v", got)
	}
	m.Status.Busy = true
	if got := m.tickInterval(); got != tickBusy {
		t.Errorf("busy interval %v", got)
	}
	t0 := time.UnixMilli(1_000_000)
	_, cmd := m.Update(tickMsg(t0))
	f0 := m.Status.Frame
	m.Update(tickMsg(t0.Add(50 * time.Millisecond)))
	f1 := m.Status.Frame
	m.Update(tickMsg(t0.Add(100 * time.Millisecond)))
	if f1 != f0 || m.Status.Frame != f0+1 {
		t.Errorf("frames %d %d %d: want one step per 100 ms", f0, f1, m.Status.Frame)
	}
	if cmd == nil {
		t.Fatal("tick did not re-arm")
	}
	if _, ok := cmd().(tickMsg); !ok {
		t.Error("re-armed command does not deliver a tick")
	}
}

func TestModelLifecycleEvents(t *testing.T) {
	m, _ := modelNew(t, false)
	if m.Status.State != "starting" {
		t.Errorf("initial state %q", m.Status.State)
	}
	cwd := t.TempDir()
	modelEvents(m, protocol.Init{SessionID: "s", Model: "claude-opus", Cwd: cwd})
	if m.Status.Model != "claude-opus" || m.Status.State != "idle" || m.Sim.State().Root != cwd {
		t.Errorf("after init: status %+v root %q", m.Status, m.Sim.State().Root)
	}

	modelEvents(m, protocol.Thinking{}, protocol.Result{Text: "done"})
	if m.Status.State != "idle" || m.Status.Busy || len(m.Chat) != 0 {
		t.Errorf("after result: status %+v chat %d", m.Status, len(m.Chat))
	}

	modelEvents(m,
		protocol.Result{IsError: true, Text: "error_max_turns"},
		protocol.Stderr{Line: "warning: x"},
		protocol.Exited{Err: errors.New("exit status 1")},
		protocol.Exited{},
	)
	// The reason claude exited stays in the chat, once, with what it wrote to stderr.
	report := "claude exited (exit status 1)\n  warning: x"
	if want := []ChatEntry{{Kind: CommandEntry, Text: "Error: " + report, At: modelT0}}; !reflect.DeepEqual(m.Chat, want) || m.ExitReport() != report {
		t.Errorf("chat %#v report %q", m.Chat, m.ExitReport())
	}
	if m.Status.State != "exited" || m.Status.Busy {
		t.Errorf("after exit: status %+v", m.Status)
	}
}

// An API call retried, and a compaction, show in the status until claude goes on.
func TestModelRetryAndCompaction(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Thinking{}, protocol.APIRetry{Attempt: 2, MaxRetries: 10, Delay: 5 * time.Second, Status: 529})
	if m.Status.State != "overloaded · retry 2/10" || !m.Status.Busy {
		t.Errorf("status %+v", m.Status)
	}
	for status, want := range map[int]string{0: "connection error · retry 1/10", 429: "rate limited · retry 1/10", 503: "API error 503 · retry 1/10"} {
		modelEvents(m, protocol.APIRetry{Attempt: 1, MaxRetries: 10, Status: status})
		if m.Status.State != want {
			t.Errorf("status %d: state %q, want %q", status, m.Status.State, want)
		}
	}
	modelEvents(m, protocol.TextStart{})
	if m.Status.State != "responding" {
		t.Errorf("after the retry: state %q", m.Status.State)
	}
	modelEvents(m, protocol.Compacting{On: true})
	if m.Status.State != "compacting" {
		t.Errorf("compaction: state %q", m.Status.State)
	}
	modelEvents(m, protocol.Compacting{On: false})
	if m.Status.State != "thinking" {
		t.Errorf("after the compaction: state %q", m.Status.State)
	}
	modelEvents(m, protocol.TextStart{}, protocol.Compacting{On: false})
	if m.Status.State != "responding" {
		t.Errorf("a compaction that did not start ended: state %q", m.Status.State)
	}
	// After the turn, a retry or a compaction (a side call's, a background agent's) starts no turn.
	modelEvents(m, protocol.Result{}, protocol.APIRetry{Attempt: 1, MaxRetries: 10, Status: 529}, protocol.Compacting{On: true})
	if m.Status.State != "idle" || m.Status.Busy {
		t.Errorf("after the turn: status %+v", m.Status)
	}
	// A list the user opened hides nothing of what claude does; only a question covers it.
	modelEvents(m, protocol.Thinking{})
	m.Popup = &ResumePopup{}
	modelEvents(m, protocol.APIRetry{Attempt: 3, MaxRetries: 10, Status: 529})
	if m.Status.State != "overloaded · retry 3/10" {
		t.Errorf("under a list: state %q", m.Status.State)
	}
}

// A compaction says so in the chat, and the context shows what the summary holds before main's
// next call tells it: /compact asks no call of its own.
func TestModelCompacted(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Init{SessionID: "one", Model: "claude-opus-5-5"}, protocol.Context{Model: "claude-opus-5-5", Tokens: 165745})
	m.windows = map[string]int{"claude-opus-5-5": 1000000} // as a turn's result tells it
	modelEvents(m, protocol.Compacted{Trigger: "manual", Before: 165745, After: 6555})
	if m.context.Tokens != 6555 {
		t.Errorf("context after the compaction: %d tokens", m.context.Tokens)
	}
	if got := m.Status.Context; got < 0.0065 || got > 0.0066 {
		t.Errorf("status context %v", got)
	}
	if n := len(m.Chat); n == 0 || m.Chat[n-1].Text != "Conversation compacted · 166k → 7k tokens" {
		t.Errorf("chat %#v", m.Chat)
	}
	// One that tells no sizes still says so, and leaves the count to the next call.
	modelEvents(m, protocol.Compacted{Trigger: "auto"})
	if m.context.Tokens != 6555 || m.Chat[len(m.Chat)-1].Text != "Conversation compacted automatically" {
		t.Errorf("without sizes: %d tokens, chat %q", m.context.Tokens, m.Chat[len(m.Chat)-1].Text)
	}
	// Asked as a turn goes on, or between its calls, it follows what was said before it.
	modelEvents(m, protocol.Thinking{}, protocol.TextStart{}, protocol.TextDelta{Text: "Reading the files first."},
		protocol.Compacted{Trigger: "auto", Before: 160000, After: 9000})
	if n := len(m.Chat); m.Chat[n-2].Text != "Reading the files first." || m.Chat[n-1].Text != "Conversation compacted automatically · 160k → 9k tokens" {
		t.Errorf("in a turn: chat %#v", m.Chat[n-2:])
	}
	// A /compact before any call of this process: the context shows on the model claude runs.
	m2, _ := modelNew(t, false)
	modelEvents(m2, protocol.Init{SessionID: "two", Model: "claude-opus-5-5"},
		protocol.Compacted{Trigger: "manual", Before: 165745, After: 6555},
		protocol.Result{ContextWindows: map[string]int{"claude-opus-5-5": 1000000}}) // the compaction's turn tells the window
	if m2.Status.Context == 0 {
		t.Errorf("before any call: status context %v, model %q", m2.Status.Context, m2.Status.Model)
	}
	if kTokens(999) != "999" || kTokens(1499) != "1k" || kTokens(1500) != "2k" {
		t.Errorf("kTokens: %q %q %q", kTokens(999), kTokens(1499), kTokens(1500))
	}
}

// The effort shows beside the model: as claude's settings tell it, at the start and after a model
// switch, as a turn tells it when it does (claude 2.1.316 does not), as /effort sets it; "auto",
// the model's own, shows none.
func TestModelEffort(t *testing.T) {
	m, sent := modelNew(t, false)
	m.known["effort"] = true
	modelEvents(m, protocol.Commands{}, protocol.Settings{Effort: "xhigh"}, protocol.Init{SessionID: "s", Model: "claude-opus-5-5"})
	if m.Status.Effort != "xhigh" {
		t.Errorf("an init that says nothing of it: effort %q", m.Status.Effort)
	}
	modelEvents(m, protocol.Init{SessionID: "s", Model: "claude-opus-5-5", Effort: "high"})
	if m.Status.Effort != "high" {
		t.Errorf("an init that tells it: effort %q", m.Status.Effort)
	}
	*sent = nil
	modelEvents(m, protocol.ModelChanged{Model: "claude-haiku-5-5"})
	if want := []protocol.Outbound{protocol.GetSettings{RequestID: "settings-0"}}; !reflect.DeepEqual(*sent, want) {
		t.Errorf("a model switch sent %v, want %v", *sent, want)
	}
	modelEvents(m, protocol.Settings{Effort: "medium"})
	if m.Status.Effort != "medium" {
		t.Errorf("the new model's: effort %q", m.Status.Effort)
	}
	chat := len(m.Chat)
	modelEvents(m, protocol.ControlError{RequestID: "settings", Message: "Unsupported control request subtype: get_settings"})
	if len(m.Chat) != chat || m.Status.Effort != "medium" {
		t.Errorf("a claude without get_settings: chat %+v, effort %q", m.Chat[chat:], m.Status.Effort)
	}
	for _, tt := range []struct{ typed, want string }{{"/effort low", "low"}, {"/effort auto", ""}, {"/effort MAX", "max"}, {"/effort nonsense", "max"}} {
		modelEvents(m, protocol.Result{})
		modelPress(m, inputTyped(tt.typed)...)
		modelPress(m, inputKey("enter"))
		if m.Status.Effort != tt.want {
			t.Errorf("%s: effort %q, want %q (sent %v)", tt.typed, m.Status.Effort, tt.want, *sent)
		}
	}
	// Once the turn of an /effort ends, claude's settings tell the level: "auto" shows as at the start.
	modelEvents(m, protocol.Result{})
	modelPress(m, inputTyped("/effort auto")...)
	modelPress(m, inputKey("enter"))
	*sent = nil
	modelEvents(m, protocol.Result{})
	if want := []protocol.Outbound{protocol.GetSettings{RequestID: "settings-effort"}}; !reflect.DeepEqual(*sent, want) {
		t.Errorf("the end of /effort's turn sent %v, want %v", *sent, want)
	}
	modelEvents(m, protocol.Settings{Effort: "medium"})
	modelPress(m, inputTyped("hi")...)
	modelPress(m, inputKey("enter"))
	*sent = nil
	modelEvents(m, protocol.Result{})
	if m.Status.Effort != "medium" || len(*sent) != 0 {
		t.Errorf("after a prompt's turn: effort %q, sent %v", m.Status.Effort, *sent)
	}
}

// An API failure goes to the status, red, not the chat, and stays there until claude goes on; an
// error retried away never shows.
func TestModelFailureInStatus(t *testing.T) {
	m, _ := modelNew(t, false)
	failure := `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	modelPress(m, inputTyped("hi")...)
	modelPress(m, inputKey("enter"))
	modelEvents(m, protocol.CLIReply{Text: failure}, protocol.Result{IsError: true, Text: failure})
	if len(m.Chat) != 1 || m.Status.State != "API error 529 · Overloaded" || !m.Status.Alert || m.Status.Busy {
		t.Errorf("chat %+v status %+v", m.Chat, m.Status)
	}
	modelPress(m, inputTyped("again")...)
	modelPress(m, inputKey("enter"))
	if m.Status.State != "thinking" || m.Status.Alert {
		t.Errorf("the next turn: status %+v", m.Status)
	}
	modelEvents(m, protocol.APIRetry{Attempt: 1, MaxRetries: 10, Status: 529}, protocol.TextStart{}, protocol.Result{})
	if m.Status.State != "idle" || m.Status.Alert {
		t.Errorf("retried away: status %+v", m.Status)
	}
	modelEvents(m, protocol.Thinking{}, protocol.Result{IsError: true, Text: "error_max_turns"})
	if m.Status.State != "error_max_turns" || !m.Status.Alert || len(m.Chat) != 3 {
		t.Errorf("another error: chat %+v status %+v", m.Chat, m.Status)
	}
	if got := failureState("API Error: Request timed out."); got != "API error · Request timed out." {
		t.Errorf("timeout: %q", got)
	}
}

// A prompt keeps how many tokens its turn took (not drawn while the count is wrong). A slash
// command's turn adds none. A /compact compacts from the start.
func TestModelPromptTokens(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputTyped("hi")...)
	modelPress(m, inputKey("enter"))
	// A process's first result counts what earlier processes of the conversation took too
	// (--resume): the turn's own count goes after the prompt.
	turn := protocol.Usage{Input: 104, Output: 7327, CacheRead: 3672567, CacheWrite: 561748}
	all := protocol.Usage{Input: 1000, Output: 90000, CacheRead: 27000000, CacheWrite: 1000000}
	modelEvents(m, protocol.Result{Usage: all, Turn: turn})
	if len(m.Chat) != 1 || m.Chat[0].Tokens != turn {
		t.Fatalf("chat %+v", m.Chat)
	}
	m.known["compact"] = true
	modelPress(m, inputTyped("/compact")...)
	modelPress(m, inputKey("enter"))
	if m.Status.State != "compacting" {
		t.Errorf("/compact: state %q", m.Status.State)
	}
	all.Output += 500
	modelEvents(m, protocol.Result{Usage: all, Turn: protocol.Usage{Output: 500}})
	if m.Chat[0].Tokens != turn {
		t.Errorf("a command's turn took the prompt's tokens: %+v", m.Chat)
	}
	// Then a prompt keeps what its turn added to the process's count, its subagents' too.
	modelPress(m, inputTyped("more")...)
	modelPress(m, inputKey("enter"))
	all.Output, all.CacheRead = all.Output+2000, all.CacheRead+400000
	modelEvents(m, protocol.Result{Usage: all, Turn: protocol.Usage{Output: 1000, CacheRead: 200000}})
	if n := len(m.Chat); m.Chat[n-1].Tokens != (protocol.Usage{Output: 2000, CacheRead: 400000}) {
		t.Errorf("the second prompt took %+v", m.Chat[n-1].Tokens)
	}
	// An answer to claude's question is no prompt: the turn's tokens go after the prompt.
	modelPress(m, inputTyped("ask")...)
	modelPress(m, inputKey("enter"))
	modelAt(m, 2*popupGrace) // the enter goes to the question, not to the prompt typed just before
	modelAsk(t, m, "r1", modelFixtureQuestion)
	modelPress(m, inputKey("enter"))
	all.Output += 300
	modelEvents(m, protocol.Result{Usage: all, Turn: protocol.Usage{Output: 300}})
	if n := len(m.Chat); m.Chat[n-3].Tokens.Output != 300 || m.Chat[n-1].Tokens != (protocol.Usage{}) {
		t.Errorf("tokens after the prompt %+v, after the answer %+v", m.Chat[n-3].Tokens, m.Chat[n-1].Tokens)
	}
}

// claude's "manual" is the default mode, which shows as such.
func TestModelManualMode(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.ModeChanged{Mode: "manual"})
	if m.Status.Mode != "default" || m.extraMode != "" || ansi.Strip(drawMode(m.Status.Mode)) != "⏵ manual mode" {
		t.Errorf("mode %q extra %q drawn %q", m.Status.Mode, m.extraMode, drawMode(m.Status.Mode))
	}
}

// A turn falling back to another model shows it in the status, and says why in the chat.
func TestModelFallback(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Init{SessionID: "s", Model: "claude-opus-5-5"}, protocol.TextStart{}, protocol.TextDelta{Text: "Let me check."},
		protocol.ModelFallback{From: "claude-opus-5-5", To: "claude-sonnet-5-5", Trigger: "overloaded", Text: "Opus is overloaded: switched to Sonnet"})
	if m.Status.Model != "claude-sonnet-5-5" {
		t.Errorf("model %q", m.Status.Model)
	}
	modelEvents(m, protocol.ModelFallback{From: "claude-opus-5-5", To: "claude-haiku-5-5", Trigger: "server_error"})
	want := []ChatEntry{
		{Kind: AssistantEntry, Text: "Let me check.", At: modelT0}, // said before the call that fell back
		{Kind: CommandEntry, Text: "Opus is overloaded: switched to Sonnet", At: modelT0},
		{Kind: CommandEntry, Text: "claude-opus-5-5 failed (server_error): this turn runs on claude-haiku-5-5", At: modelT0},
	}
	if !reflect.DeepEqual(m.Chat, want) {
		t.Errorf("chat %#v", m.Chat)
	}
	// Every call of main's names the model it runs on: the status and main's row keep up with it,
	// past the Init of a turn that names the one asked for.
	modelEvents(m, protocol.Init{SessionID: "s", Model: "claude-opus-5-5"}, protocol.Context{Model: "claude-sonnet-5-5", Tokens: 10})
	if main := modelAgent(t, m, "main"); m.Status.Model != "claude-sonnet-5-5" || main.Model != "claude-sonnet-5-5" {
		t.Errorf("after a call: status %q, main %q", m.Status.Model, main.Model)
	}
}

// A claude older than minClaude is told to update, once; newer ones, versions that do not parse
// and the claude of a recording are not.
func TestModelOldClaude(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.Commands{Version: "2.1.200"}, protocol.Init{SessionID: "s", Version: "2.1.200"})
	want := []ChatEntry{{Kind: CommandEntry, Text: "claude 2.1.200 is older than " + minClaude + ", which claudit is built for · run claude update", At: modelT0}}
	if !reflect.DeepEqual(m.Chat, want) || m.cli.version != "2.1.200" {
		t.Errorf("chat %#v version %q", m.Chat, m.cli.version)
	}
	for _, v := range []string{minClaude, "2.1.1000", "2.2.0", "3.0.0-beta.1", "dev"} {
		m, _ := modelNew(t, false)
		modelEvents(m, protocol.Commands{Version: v})
		if len(m.Chat) != 0 {
			t.Errorf("claude %s: chat %#v", v, m.Chat)
		}
	}
	m, _ = modelNew(t, true)
	modelEvents(m, protocol.Init{SessionID: "s", Version: "2.1.200"})
	if len(m.Chat) != 0 {
		t.Errorf("replay: chat %#v", m.Chat)
	}
}

// claude dying on a flag, or a flag's value, it does not know is too old: the report of its exit
// says to update it.
func TestModelExitUnknownOption(t *testing.T) {
	for _, said := range []string{
		"Error: unknown option '--include-partial-messages'",
		"Error: option '--permission-mode <mode>' argument 'auto' is invalid. Allowed choices are acceptEdits, bypassPermissions, default, plan.",
	} {
		m, _ := modelNew(t, false)
		modelEvents(m, protocol.Stderr{Line: said}, protocol.Exited{Err: errors.New("exit status 1")})
		report := "claude exited (exit status 1)\n  " + said + "\n  claudit needs claude " + minClaude + " or newer · run claude update"
		if m.ExitReport() != report {
			t.Errorf("report %q, want %q", m.ExitReport(), report)
		}
	}
}

// A tool call that never gets its result because claude died must not keep the IDE busy, and
// prompts typed afterwards must not leave the status thinking forever.
func TestModelExitEndsPendingTools(t *testing.T) {
	m, sent := modelNew(t, false)
	modelEvents(m,
		protocol.ToolUse{ID: "t1", Name: "Bash", Input: map[string]any{"command": "sleep 100"}},
		protocol.PermissionRequest{RequestID: "r1", ToolName: "Bash", Input: map[string]any{"command": "sleep 100"}},
	)
	t0 := time.UnixMilli(1_000_000)
	for i := range 200 { // type the command and press enter
		m.Update(tickMsg(t0.Add(time.Duration(i) * 16 * time.Millisecond)))
	}
	main := modelAgent(t, m, "main")
	if !main.Term.Running {
		t.Fatal("the command is not running")
	}
	modelEvents(m, protocol.Exited{Err: errors.New("signal: killed")})
	for i := range 200 {
		m.Update(tickMsg(t0.Add(time.Duration(200+i) * 16 * time.Millisecond)))
	}
	if m.Sim.Busy() || main.Term.Running || m.tickInterval() != tickIdle {
		t.Errorf("after exit: sim busy %v, running %v, tick %v", m.Sim.Busy(), main.Term.Running, m.tickInterval())
	}
	if got := modelOpen(m); len(got) != 0 {
		t.Errorf("open calls %q after exit", got)
	}
	if m.Popup != nil {
		t.Error("the permission popup outlived claude")
	}

	modelPress(m, inputTyped("hi")...)
	modelPress(m, inputKey("enter"))
	if len(*sent) != 0 || m.Status.Busy || m.Status.State != "exited" {
		t.Errorf("prompt after exit: sent %#v, status %+v", *sent, m.Status)
	}
	want := []ChatEntry{{Kind: CommandEntry, Text: "Error: claude exited (signal: killed)", At: modelT0}, {Kind: CommandEntry, Text: "Error: claude has exited: prompts are not sent", At: modelT0}}
	if !reflect.DeepEqual(m.Chat, want) {
		t.Errorf("chat after exit %+v", m.Chat) // the prompt is not in it: it was never sent
	}
}

// Parallel tool calls keep streaming in while a popup waits; the status must keep saying so.
func TestModelPopupKeepsWaitingState(t *testing.T) {
	m, _ := modelNew(t, false)
	modelEvents(m, protocol.PermissionRequest{RequestID: "r1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	for _, ev := range []protocol.Event{
		protocol.ToolUse{ID: "t2", Name: "Bash", Input: map[string]any{"command": "ls"}},
		protocol.Thinking{},
		protocol.TextStart{},
	} {
		modelEvents(m, ev)
		if m.Status.State != "waiting for you" || !m.Status.Busy {
			t.Errorf("after %T: status %+v", ev, m.Status)
		}
	}
	modelPress(m, inputKey("y"))
	if m.Status.State != "running" {
		t.Errorf("after allow: state %q", m.Status.State)
	}
}

func TestModelInitWhileBusyKeepsState(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputTyped("go")...)
	modelPress(m, inputKey("enter"))
	modelEvents(m, protocol.Init{Model: "m"})
	if m.Status.State != "thinking" || !m.Status.Busy {
		t.Errorf("status %+v", m.Status)
	}
}

// TestModelReplaysFixture feeds the real recording through the model, answering popups as the recorded user did.
func TestModelReplaysFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "demo.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	m, sent := modelNew(t, true)
	for _, line := range bytes.Split(data, []byte("\n")) {
		modelEvents(m, protocol.Parse(line)...)
		for m.Popup != nil {
			if _, ok := m.Popup.(*QuestionPopup); ok {
				modelPress(m, inputKeys("down enter")...) // "div"
			} else {
				modelPress(m, inputKey("y"))
			}
		}
	}

	if len(*sent) != 4 {
		t.Fatalf("sent %d answers, want 4: %#v", len(*sent), *sent)
	}
	ask, ok := (*sent)[0].(protocol.Allow)
	want := map[string]string{"Which function should I add to calc.py: mul or div?": "div"}
	if !ok || !reflect.DeepEqual(ask.Input["answers"], want) || ask.Input["questions"] == nil {
		t.Errorf("AskUserQuestion answer %#v", (*sent)[0])
	}
	if main := modelAgent(t, m, "main"); main.ToolCalls != 8 || len(modelOpen(m)) != 0 || len(m.Sim.State().Agents) != 1 {
		t.Errorf("main made %d calls, open %q, agents %d", main.ToolCalls, modelOpen(m), len(m.Sim.State().Agents))
	}
	for i, e := range m.Chat { // claude's words, and the user's answer after its question
		if strings.TrimSpace(e.Text) == "" || e.Kind != AssistantEntry && (i != 1 || e.Text != "div") {
			t.Errorf("chat entry %+v", e)
		}
	}
	if len(m.Chat) < 2 || m.Chat[0].Text != "Which function should I add to calc.py: mul or div?" || m.Chat[1].Kind != UserEntry {
		t.Errorf("question and answer: %+v", m.Chat)
	}
	if n := len(m.Chat); n == 0 || !strings.Contains(m.Chat[n-1].Text, "div") {
		t.Errorf("final reply missing: %+v", m.Chat)
	}
	if m.Status.Model != "claude-haiku-5-5" || m.Status.State != "idle" || m.Status.Busy || !strings.HasPrefix(m.Status.Usage, "5h 9% ") || !strings.Contains(m.Status.Usage, " · 7d 58% ") ||
		m.Status.Context != 34433.0/1000000 {
		t.Errorf("final status %+v", m.Status)
	}
}
