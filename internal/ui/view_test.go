package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/sim"
)

func drawTestModel(t *testing.T, w, h int) *Model {
	t.Helper()
	return &Model{
		Sim:       sim.New(t.TempDir()),
		Width:     w,
		Height:    h,
		Chat:      drawTestEntries,
		Input:     Input{Text: []rune("rewrite 世界 😀"), Cursor: 3},
		Status:    Status{Model: "claude-sonnet-4-5", State: "idle"},
		clock:     func() time.Time { return modelT0 },
		specs:     ideview.Specs{},
		specModes: ideview.SpecModes{},
	}
}

func TestViewSize(t *testing.T) {
	for _, sz := range [][2]int{{40, 16}, {41, 17}, {80, 24}, {120, 50}, {200, 60}, {333, 101}} {
		w, h := sz[0], sz[1]
		m := drawTestModel(t, w, h)
		v := m.View()
		if !v.AltScreen || v.WindowTitle != "claudit" {
			t.Errorf("%dx%d: AltScreen %v, WindowTitle %q", w, h, v.AltScreen, v.WindowTitle)
		}
		drawCheckSize(t, v.Content, w, h)
		if v.Cursor == nil {
			t.Fatalf("%dx%d: no cursor without a popup", w, h)
		}
		// The caret sits on the input row, after the prompt and "rew".
		lines := drawPlainLines(v.Content)
		row := lines[v.Cursor.Y]
		if !strings.HasPrefix(row, "│ › rewrite") {
			t.Errorf("%dx%d: cursor row %d is %q", w, h, v.Cursor.Y, row)
		}
		if got := ansi.Cut(row, v.Cursor.X, v.Cursor.X+1); got != "r" {
			t.Errorf("%dx%d: cursor at x=%d is on %q, want %q", w, h, v.Cursor.X, got, "r")
		}
		// Narrow, the model makes room for what claude does.
		if want := "claude-sonnet-4-5 · idle"; !strings.Contains(lines[h-1], want) && (w >= 80 || !strings.Contains(lines[h-1], "● idle")) {
			t.Errorf("%dx%d: last line is not the status: %q", w, h, lines[h-1])
		}
		if v.Cursor.Y != h-3 {
			t.Errorf("%dx%d: input row %d, want %d", w, h, v.Cursor.Y, h-3)
		}
	}
}

// The chat is as tall as what it says, up to 35% of the screen; the IDE takes the rest.
func TestViewLayout(t *testing.T) {
	tests := []struct{ h, chat, ideH, chatH int }{
		{8, 0, 0, 4}, {15, 50, 0, 11}, {16, 0, 12, 0}, {16, 3, 9, 3}, {16, 50, 7, 5},
		{24, 0, 20, 0}, {24, 50, 12, 8}, {40, 10, 26, 10}, {40, 50, 22, 14}, {100, 500, 61, 35},
	}
	for _, tt := range tests {
		if ideH, chatH := drawLayout(tt.h, tt.chat); ideH != tt.ideH || chatH != tt.chatH {
			t.Errorf("height %d, %d chat lines: ide %d chat %d, want %d %d", tt.h, tt.chat, ideH, chatH, tt.ideH, tt.chatH)
		}
	}
	for h := 16; h < 300; h++ {
		for _, chat := range []int{0, 1, h / 4, h} {
			if ideH, chatH := drawLayout(h, chat); ideH+chatH+4 != h || chatH != min(chat, h*35/100) {
				t.Fatalf("height %d, %d chat lines: ide %d chat %d", h, chat, ideH, chatH)
			}
		}
	}
}

func TestViewPopup(t *testing.T) {
	for _, sz := range [][2]int{{40, 16}, {80, 24}, {150, 50}} {
		w, h := sz[0], sz[1]
		m := drawTestModel(t, w, h)
		m.Popup = &PermissionPopup{Tool: "Bash", Detail: "pytest -q", Allow: true}
		v := m.View()
		drawCheckSize(t, v.Content, w, h)
		if v.Cursor != nil {
			t.Errorf("%dx%d: terminal cursor shown under a popup", w, h)
		}
		plain := ansi.Strip(v.Content)
		if !strings.Contains(plain, "Allow Bash?") || !strings.Contains(plain, "pytest -q") {
			t.Errorf("%dx%d: popup missing:\n%s", w, h, plain)
		}
		// The box is centred and no wider than min(w-4, 80). Its top border is the row above the
		// title; the IDE above has borders of its own.
		lines := drawPlainLines(v.Content)
		top := ""
		for r, l := range lines[1:] {
			if strings.Contains(l, "Allow Bash?") {
				top = lines[r]
				break
			}
		}
		i, j := strings.Index(top, "╭"), strings.LastIndex(top, "╮")
		if i < 0 || j < i {
			t.Fatalf("%dx%d: no popup top border above the title: %q", w, h, top)
		}
		bw := ansi.StringWidth(top[i:j]) + 1
		if x := ansi.StringWidth(top[:i]); bw > min(w-4, 100) || x != (w-bw)/2 {
			t.Errorf("%dx%d: popup box %d wide at x=%d", w, h, bw, x)
		}
		m.Popup = drawTestQuestion(true, 3, "other text")
		v = m.View()
		drawCheckSize(t, v.Content, w, h)
		if v.Cursor != nil || !strings.Contains(ansi.Strip(v.Content), "Other: other text") {
			t.Errorf("%dx%d: question popup broken", w, h)
		}
	}
}

func TestViewTooSmall(t *testing.T) {
	for _, sz := range [][2]int{{0, 0}, {1, 1}, {10, 5}, {39, 30}, {100, 7}, {39, 15}, {18, 1}} {
		w, h := sz[0], sz[1]
		m := drawTestModel(t, w, h)
		m.Popup = &PermissionPopup{Tool: "Bash"}
		v := m.View()
		if h > 0 {
			drawCheckSize(t, v.Content, w, h)
		} else if v.Content != "" {
			t.Errorf("%dx%d: content %q", w, h, v.Content)
		}
		if v.Cursor != nil {
			t.Errorf("%dx%d: cursor on the too-small screen", w, h)
		}
		if w >= 18 && h > 0 && !strings.Contains(ansi.Strip(v.Content), "terminal too small") {
			t.Errorf("%dx%d: no message:\n%s", w, h, v.Content)
		}
	}
}

func TestViewStoresChatMax(t *testing.T) {
	m := drawTestModel(t, 80, 24)
	m.Chat = nil
	for i := range 50 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprint("line ", i)})
	}
	m.View()
	// 50 entries, no blank lines, in a chat of 35% of 24 rows.
	if want := 50 - 8; m.chatMax != want {
		t.Errorf("chatMax = %d, want %d", m.chatMax, want)
	}
	m.ChatScroll = m.chatMax
	if lines := drawPlainLines(m.View().Content); !strings.Contains(strings.Join(lines, "\n"), "line 0") {
		t.Error("scrolled to the top but the first entry is not shown")
	}
}

// "1️⃣" is one cell wide for ansi.Truncate and two for ansi.StringWidth; no width may make View panic.
func TestViewKeycap(t *testing.T) {
	const label = "1️⃣ Install dependencies and configure the virtual environment 1️⃣ 2️⃣ 3️⃣ #️⃣ ✌️"
	for w := 40; w <= 120; w++ {
		m := drawTestModel(t, w, 24)
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: label}, ChatEntry{Kind: UserEntry, Text: label})
		m.Popup = &QuestionPopup{AgentName: label, Questions: []Question{{
			Text:    label,
			Options: []Option{{Label: label, Description: label}, {Label: "2️⃣"}},
			Checked: make([]bool, 3),
		}}}
		drawCheckSize(t, m.View().Content, w, 24)
	}
}

// Scrolled far up in a narrow window, then widened: the first PgDn must already move the chat.
func TestViewScrollAfterResize(t *testing.T) {
	m := drawTestModel(t, 40, 24)
	m.Chat = nil
	for i := range 20 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprint(i, strings.Repeat(" word", 30))})
	}
	m.View()
	for range 50 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	}
	m.Width = 200
	top := m.View().Content
	m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	if m.View().Content == top {
		t.Errorf("pgdown after widening did not scroll (ChatScroll %d, chatMax %d)", m.ChatScroll, m.chatMax)
	}
}

// drawTestModes sets up each way the screen can look: the IDE with the prompt focused, the agents
// panel focused, and a popup.
var drawTestModes = []struct {
	name  string
	setup func(m *Model)
}{
	{"ide", func(m *Model) {}},
	{"panel", func(m *Model) { m.PanelFocus, m.Panel, m.PanelSel = true, ideview.Agents, "main" }},
	{"popup", func(m *Model) {
		m.Popup = &PermissionPopup{AgentName: "tests", AgentColor: 1, Tool: "Bash", Detail: "go test ./...", Allow: true}
	}},
}

// drawTestAgents adds a subagent to m, on camera.
func drawTestAgents(m *Model) {
	t0 := time.UnixMilli(1_700_000_000_000)
	a := m.Sim.Agent("toolu_sub", ide.Subagent)
	a.Name = "tests"
	m.Now = t0.Add(30 * time.Second)
}

func TestViewModes(t *testing.T) {
	for _, mode := range drawTestModes {
		for _, sz := range [][2]int{{40, 16}, {57, 19}, {80, 24}, {141, 43}, {200, 60}} {
			w, h := sz[0], sz[1]
			m := drawTestModel(t, w, h)
			drawTestAgents(m)
			mode.setup(m)
			v := m.View()
			drawCheckSize(t, v.Content, w, h)
			lines := drawPlainLines(v.Content)
			if (v.Cursor != nil) != (m.Popup == nil && !m.PanelFocus) {
				t.Errorf("%s %dx%d: cursor %v", mode.name, w, h, v.Cursor)
			}
			if w >= 80 && strings.Contains(lines[h-1], "cam ") { // the agents panel tells the camera
				t.Errorf("%s %dx%d: status %q", mode.name, w, h, lines[h-1])
			}
		}
	}
}

// Hints follow the keys: the prompt or the agents panel; tab only while the panel is there to go to.
func TestViewHints(t *testing.T) {
	m, _ := modelNew(t, false)
	m.Width, m.Height = 160, 30
	modelEvents(m, protocol.ToolUse{ID: "s1", Name: "Agent", Input: map[string]any{"description": "alpha"}})
	if status := drawPlainLines(m.View().Content)[29]; !strings.HasSuffix(status, "shift+tab mode · tab agents · F1 help · esc interrupt") {
		t.Errorf("a subagent at work: status %q", status)
	}
	tests := []struct {
		mode int
		want string
	}{
		{0, "shift+tab mode · / commands · F1 help · esc x2 quit"},
		{1, "↑/↓ select · enter pin · alt+0 auto · tab back"},
	}
	for _, tt := range tests {
		m := drawTestModel(t, 160, 30)
		drawTestModes[tt.mode].setup(m)
		if status := drawPlainLines(m.View().Content)[29]; !strings.HasSuffix(status, tt.want) {
			t.Errorf("%s: status %q", drawTestModes[tt.mode].name, status)
		}
	}
}

// While the agents panel has the keys, the prompt shows no caret and looks dimmed.
func TestViewPanelFocus(t *testing.T) {
	m := drawTestModel(t, 80, 24)
	m.Input = Input{}
	prompt := func() string { return strings.Split(m.View().Content, "\n")[24-3] }
	if !strings.Contains(prompt(), styleYou.Render("› ")) {
		t.Errorf("focused prompt is not lit: %q", prompt())
	}
	m.PanelFocus = true
	if v := m.View(); v.Cursor != nil {
		t.Error("caret shown in the prompt while the panel has the keys")
	}
	if !strings.Contains(prompt(), styleDim.Render("› ")) {
		t.Errorf("prompt is not dimmed: %q", prompt())
	}
}

// Too low for the IDE, the conversation still works; too small for anything, no key answers a
// popup nobody can see.
func TestViewCompact(t *testing.T) {
	m := drawTestModel(t, 80, 12)
	content := ansi.Strip(m.View().Content)
	drawCheckSize(t, m.View().Content, 80, 12)
	if strings.Contains(content, "╭─ ") || !strings.Contains(content, "› rewrite") || !strings.Contains(content, "claude-sonnet-4-5 · idle") {
		t.Errorf("compact screen:\n%s", content)
	}
	sent := 0
	m.send = func(protocol.Outbound) { sent++ }
	m.Width, m.Height = 30, 10
	m.Popup = &PermissionPopup{Tool: "Bash", Allow: true}
	m.Update(inputKey("y"))
	if m.Popup == nil || sent != 0 {
		t.Errorf("a blind key answered the popup")
	}
}
