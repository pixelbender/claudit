package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/pixelbender/claudit/internal/history"
	"github.com/pixelbender/claudit/internal/ide"
	"github.com/pixelbender/claudit/internal/ideview"
	"github.com/pixelbender/claudit/internal/protocol"
	"github.com/pixelbender/claudit/internal/theme"
)

// modelMouseNew is a live model on a 100×30 screen.
func modelMouseNew(t *testing.T) (*Model, *[]protocol.Outbound) {
	t.Helper()
	m, sent := modelNew(t, false)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, sent
}

// modelMouseAt draws m and finds text on the screen: the cell where it starts.
func modelMouseAt(t *testing.T, m *Model, text string) (x, y int) {
	t.Helper()
	for y, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if i := strings.Index(l, text); i >= 0 {
			return ansi.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("%q is not on screen:\n%s", text, ansi.Strip(m.View().Content))
	return 0, 0
}

// modelMouseClick clicks where text starts on the screen and plays what the click starts.
func modelMouseClick(t *testing.T, m *Model, text string) {
	t.Helper()
	x, y := modelMouseAt(t, m, text)
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	modelResumeRun(m, cmd)
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func modelMouseWheel(m *Model, up bool) {
	b := tea.MouseWheelDown
	if up {
		b = tea.MouseWheelUp
	}
	m.Update(tea.MouseWheelMsg{X: 50, Y: 20, Button: b})
}

// A click on a folder in the explorer folds or unfolds it.
func TestModelMouseToggleFolder(t *testing.T) {
	m, _ := modelMouseNew(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "pkg", "inner.go"), nil, 0o644)
	m.Sim.SetRoot(root)
	modelMouseClick(t, m, "▸ pkg")
	modelMouseAt(t, m, "inner.go")
	modelMouseClick(t, m, "▾ pkg")
	if strings.Contains(ansi.Strip(m.View().Content), "inner.go") {
		t.Fatal("pkg did not fold")
	}
}

// A click on a file in the explorer opens it in the editor.
func TestModelMouseOpensFile(t *testing.T) {
	m, _ := modelMouseNew(t)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello from disk"), 0o644)
	m.Sim.SetRoot(root)
	modelMouseClick(t, m, "notes.txt")
	modelMouseAt(t, m, "hello from disk")
}

func TestModelMouseScroll(t *testing.T) {
	m, _ := modelMouseNew(t)
	for i := range 40 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprint("line ", i)})
	}
	m.View()
	var scrolls []int
	for _, step := range []func(){
		func() { modelMouseWheel(m, true) },
		func() { modelMouseWheel(m, true) },
		func() { modelMouseWheel(m, false) },
		func() { modelPress(m, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}) }, // not a scroll key
	} {
		step()
		scrolls = append(scrolls, m.ChatScroll)
	}
	if want := []int{3, 6, 3, 3}; !reflect.DeepEqual(scrolls, want) {
		t.Errorf("scrolls %v, want %v", scrolls, want)
	}
	for range 50 {
		modelMouseWheel(m, true)
	}
	if m.ChatScroll != m.chatMax {
		t.Errorf("scrolled past the top: %d of %d", m.ChatScroll, m.chatMax)
	}

	// Under a popup the wheel scrolls the popup, not the chat.
	modelEvents(m, protocol.PermissionRequest{RequestID: "p", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	scroll := m.ChatScroll
	modelMouseWheel(m, false)
	if p := m.Popup.(*PermissionPopup); p.Scroll != wheelStep || m.ChatScroll != scroll {
		t.Errorf("popup scroll %d, chat %d", p.Scroll, m.ChatScroll)
	}
}

// Over the IDE the wheel scrolls the pane under it; a pane gone from the screen forgets it.
func TestModelMouseScrollIDE(t *testing.T) {
	m, _ := modelMouseNew(t)
	dir := t.TempDir()
	for i := range 40 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.Sim.SetRoot(dir)
	modelMouseAt(t, m, "f00.txt")
	m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	if _, y := modelMouseAt(t, m, "f03.txt"); y != 1 || m.ChatScroll != 0 {
		t.Errorf("f03.txt on row %d, chat scrolled %d", y, m.ChatScroll)
	}
}

// The border scrolls the files without opening one or selecting text, including a drag outside it.
func TestModelMouseScrollbar(t *testing.T) {
	m, _ := modelMouseNew(t)
	dir := t.TempDir()
	for i := range 60 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.Sim.SetRoot(dir)
	m.View()
	bottom := m.ideHeight - 2
	m.Update(tea.MouseClickMsg{X: 19, Y: bottom, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 19, Y: bottom, Button: tea.MouseLeft})
	if _, y := modelMouseAt(t, m, "f59.txt"); y != bottom {
		t.Fatalf("click did not reach the last file: row %d, want %d", y, bottom)
	}
	if len(m.ideState().Tabs) != 0 || m.sel.drag {
		t.Fatal("the scrollbar opened a file or selected text")
	}
	m.Update(tea.MouseClickMsg{X: 19, Y: bottom, Button: tea.MouseLeft})
	before := m.scroll["files"]
	m.View()
	if m.scroll["files"] != before {
		t.Fatal("grabbing the thumb moved it")
	}
	m.Update(tea.MouseMotionMsg{X: 70, Y: -10, Button: tea.MouseLeft})
	m.View()
	_, cmd := m.Update(tea.MouseReleaseMsg{X: 70, Y: -10, Button: tea.MouseLeft})
	if _, y := modelMouseAt(t, m, "f00.txt"); y != 1 || cmd != nil || m.sel.drag {
		t.Fatalf("drag to top: row %d, copy command %v, selecting %v", y, cmd != nil, m.sel.drag)
	}
	m.Update(tea.MouseMotionMsg{X: 19, Y: bottom, Button: tea.MouseLeft})
	if _, y := modelMouseAt(t, m, "f00.txt"); y != 1 {
		t.Fatal("the scrollbar still drags after release")
	}
}

func TestModelMouseScrollbarStopsWhenHidden(t *testing.T) {
	for _, hide := range []string{"help", "resize"} {
		t.Run(hide, func(t *testing.T) {
			m, _ := modelMouseNew(t)
			dir := t.TempDir()
			for i := range 60 {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			m.Sim.SetRoot(dir)
			m.View()
			m.Update(tea.MouseClickMsg{X: 19, Y: 1, Button: tea.MouseLeft})
			if hide == "help" {
				m.ShowHelp = true
			} else {
				m.Update(tea.WindowSizeMsg{Width: 80, Height: 25})
			}
			m.View()
			m.Update(tea.MouseMotionMsg{X: 19, Y: 20, Button: tea.MouseLeft})
			if m.scroll["files"] != 0 {
				t.Fatal("a hidden or resized scrollbar still captures the mouse")
			}
		})
	}
}

func TestModelMouseScrollbarKeepsAgentFocus(t *testing.T) {
	m, _ := modelMouseNew(t)
	st := m.Sim.State()
	for i := range 60 {
		st.Agents = append(st.Agents, &ide.Agent{Key: fmt.Sprintf("a%02d", i), Name: fmt.Sprintf("agent%02d", i)})
	}
	m.Panel, m.PanelFocus, m.PanelSel = ideview.Agents, true, "a40"
	rows := strings.Split(ansi.Strip(m.View().Content), "\n")
	for _, bar := range ideview.Scrollbars(st, ideview.Options{Panel: m.Panel, Focused: m.PanelFocus, Sel: m.PanelSel}, m.Width, m.ideHeight) {
		if bar.Key != "agents" {
			continue
		}
		y, _ := bar.Thumb()
		m.Update(tea.MouseClickMsg{X: 19, Y: y, Button: tea.MouseLeft})
		after := strings.Split(ansi.Strip(m.View().Content), "\n")
		if !m.PanelFocus || string([]rune(after[1])[:20]) != string([]rune(rows[1])[:20]) {
			t.Fatal("grabbing the agents scrollbar changed focus or moved its content")
		}
		m.Update(tea.MouseMotionMsg{X: 19, Y: 100, Button: tea.MouseLeft})
		m.Update(tea.MouseReleaseMsg{X: 19, Y: 100, Button: tea.MouseLeft})
		m.View()
		modelPress(m, inputKey("down"))
		modelMouseAt(t, m, "agent41")
		if m.scroll["agents"] != 0 {
			t.Fatal("keyboard navigation did not bring the selected agent back into view")
		}
		return
	}
	t.Fatal("agents scrollbar is missing")
}

// The wheel events that come while a scroll is in progress cost no frame: they add up and scroll at
// its next frame, as one. A frame with nothing to scroll ends the scroll.
func TestModelMouseWheelBurst(t *testing.T) {
	m, _ := modelMouseNew(t)
	dir := t.TempDir()
	for i := range 40 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m.Sim.SetRoot(dir)
	modelMouseAt(t, m, "f00.txt")
	down := tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown}
	if _, cmd := m.Update(down); cmd == nil {
		t.Fatal("the first notch starts no scroll")
	}
	for range 2 {
		if msg := Filter(m, down); msg != nil {
			t.Fatalf("a notch of a scroll in progress went through: %v", msg)
		}
	}
	if _, y := modelMouseAt(t, m, "f03.txt"); y != 1 {
		t.Errorf("before the frame f03.txt on row %d", y)
	}
	if _, cmd := m.Update(wheelMsg{}); cmd == nil {
		t.Error("the scroll ended while the wheel turned")
	}
	if _, y := modelMouseAt(t, m, "f09.txt"); y != 1 {
		t.Errorf("after the frame f09.txt on row %d", y)
	}
	if _, cmd := m.Update(wheelMsg{}); cmd != nil {
		t.Error("the scroll goes on with the wheel still")
	}
	if msg := Filter(m, down); msg == nil {
		t.Error("a notch after the scroll is held back")
	}
}

func TestModelMouseClickPopup(t *testing.T) {
	m, sent := modelMouseNew(t)
	modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}) // outside the popup: nothing
	if m.Popup == nil || len(*sent) != 0 {
		t.Fatalf("a click beside the popup: popup %v sent %#v", m.Popup != nil, *sent)
	}
	modelMouseClick(t, m, "[ Deny ]")
	if len(*sent) != 1 || m.Popup != nil {
		t.Fatalf("deny: popup %v sent %#v", m.Popup != nil, *sent)
	}
	if _, ok := (*sent)[0].(protocol.Deny); !ok {
		t.Errorf("deny sent %#v", (*sent)[0])
	}
	modelEvents(m, protocol.PermissionRequest{RequestID: "p2", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	modelMouseClick(t, m, "[ Allow ]")
	if a, ok := (*sent)[1].(protocol.Allow); !ok || a.RequestID != "p2" {
		t.Errorf("allow sent %#v", (*sent)[1])
	}

	*sent = nil
	modelAsk(t, m, "q1", modelFixtureQuestion)
	modelMouseClick(t, m, "Division") // the description is part of its option
	modelWantAllow(t, *sent, "q1", modelFixtureQuestion, map[string]string{"Which function should I add to calc.py: mul or div?": "div"})
}

func TestModelMouseClickPanel(t *testing.T) {
	m, sent := modelMouseNew(t)
	modelEvents(m,
		protocol.ToolUse{ID: "s1", Name: "Agent", Input: map[string]any{"description": "alpha"}},
		protocol.ToolUse{ID: "s2", Name: "Agent", Input: map[string]any{"description": "beta"}},
	)
	modelMouseClick(t, m, "stuart")
	if st := m.Sim.State(); m.PanelSel != "s2" || st.Camera != "s2" || !st.Pinned {
		t.Errorf("sel %q camera %q pinned %v", m.PanelSel, st.Camera, st.Pinned)
	}
	// Pinned, it shows alone; a click on it again, or esc, shows every agent at work again.
	if st := m.Sim.State(); !strings.Contains(ansi.Strip(m.View().Content), "stuart") {
		t.Errorf("the pinned agent is not on screen, camera %q", st.Camera)
	}
	modelMouseClick(t, m, "stuart")
	if st := m.Sim.State(); st.Pinned {
		t.Errorf("a second click left the camera pinned on %q", st.Camera)
	}
	modelMouseClick(t, m, "stuart")
	modelPress(m, inputKey("esc"))
	if st := m.Sim.State(); st.Pinned || len(*sent) != 0 {
		t.Errorf("esc: pinned %v, sent %v", st.Pinned, *sent)
	}
	// A press that starts a selection picks nothing.
	x, y := modelMouseAt(t, m, "stuart")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseReleaseMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	if st := m.Sim.State(); st.Pinned {
		t.Error("a drag over an agent pinned it")
	}
	modelPress(m, inputKey("tab"))
	modelMouseClick(t, m, "Ask claude…")
	if m.PanelFocus {
		t.Error("a click on the prompt left the keys with the panel")
	}
}

func TestModelMouseClickSuggest(t *testing.T) {
	m, _ := modelSlashNew(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	modelPress(m, inputTyped("/co")...)
	modelMouseClick(t, m, "/context")
	if got := string(m.Input.Text); got != "/context " {
		t.Errorf("prompt %q", got)
	}
}

// The help goes at a click anywhere.
func TestModelMouseClickHelp(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.ShowHelp = true
	m.View()
	m.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if m.ShowHelp {
		t.Error("the help stays")
	}
}

// A drag selects the text of the pane where it began, lit as it goes, and the release copies it:
// in the chat its arrows and times stay out, and the rows it spans join with new lines.
func TestModelMouseSelect(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "fix the bug", At: modelT0}, {Kind: AssistantEntry, Text: "done, see calc.py"}}
	x, y := modelMouseAt(t, m, "fix the bug")
	m.Update(tea.MouseClickMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 99, Y: y, Button: tea.MouseLeft}) // past the time: it stays out
	if m.View(); m.sel.text != "the bug" {
		t.Errorf("one row: %q", m.sel.text)
	}
	m.Update(tea.MouseMotionMsg{X: x + 3, Y: y + 1, Button: tea.MouseLeft})
	r, g, b, _ := theme.SelBg.RGBA()
	if v := m.View(); m.sel.text != "the bug\ndone" || !strings.Contains(v.Content, fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)) {
		t.Errorf("two rows: %q, lit %v", m.sel.text, strings.Contains(v.Content, "48;2"))
	}
	// The release copies it: it flashes, stays lit a moment, then goes out, a frame at a time; the
	// status says so in place of the hints a while longer, fading too.
	m.Status.State = "idle"
	at := modelAt(m, time.Second)
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x + 3, Y: y + 1, Button: tea.MouseLeft}) // not run: it would copy
	if cmd == nil || m.tickInterval() != tickAmbient {
		t.Errorf("release: cmd %v, tick %v", cmd != nil, m.tickInterval())
	}
	bg := func(c interface{ RGBA() (r, g, b, a uint32) }) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	tr, tg, tb, _ := copiedColor.RGBA()
	note := fmt.Sprintf("38;2;%d;%d;%dmcopied to clipboard", tr>>8, tg>>8, tb>>8)
	for _, c := range []struct {
		after                   time.Duration
		flash, lit, half, noted bool
	}{
		{selFlash / 2, true, false, false, true}, {selHold / 2, false, true, false, true},
		{selHold + selFade/2, false, false, true, true}, {selHold + selFade, false, false, false, true},
		{noteHold + noteFade, false, false, false, false},
	} {
		m.Update(tickMsg(at.Add(c.after)))
		v := m.View().Content
		got := []bool{strings.Contains(v, bg(selFlashBg)), strings.Contains(v, bg(theme.SelBg)), strings.Contains(v, bg(theme.Mix(theme.SelBg, theme.Bg, 0.5))), strings.Contains(v, note)}
		if want := []bool{c.flash, c.lit, c.half, c.noted}; !slices.Equal(got, want) {
			t.Errorf("%v after the release: flash, lit, half out, noted %v, want %v", c.after, got, want)
		}
		if gone := !strings.Contains(ansi.Strip(v), "copied to clipboard"); gone != (c.after >= noteHold+noteFade) {
			t.Errorf("%v after the release: the note gone %v", c.after, gone)
		}
	}
	if m.tickInterval() != tickIdle {
		t.Errorf("out, the tick stays %v", m.tickInterval())
	}
	// Another button let go copies nothing more: what the box covers now is not what was dragged.
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseRight}); cmd != nil {
		t.Error("a right release copies again")
	}
	// A drag over blank rows lights nothing and copies nothing.
	m.sel = selection{drag: true, text: "\n"}
	if cmd := m.copySelection(); cmd != nil || m.sel != (selection{}) {
		t.Errorf("blank rows: cmd %v, selection %+v", cmd != nil, m.sel)
	}

	// A click without a drag copies nothing.
	m.Update(tea.MouseClickMsg{X: x + 4, Y: y, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x + 4, Y: y, Button: tea.MouseLeft}); cmd != nil {
		t.Error("a click copied")
	}
}

// A selection lights each of its rows only as far as the row's text goes, a wide character whole;
// a blank row stays dark.
func TestDrawSelectionEndsWithText(t *testing.T) {
	c := lipgloss.NewCanvas(10, 3).Compose(lipgloss.NewCompositor(lipgloss.NewLayer("ab\n\nd世")))
	s := selection{box: image.Rect(0, 0, 10, 3), from: image.Pt(1, 0), to: image.Pt(9, 2), drag: true}
	if got := drawSelection(c, s, theme.SelBg, 0); got != "b\n\nd世" {
		t.Errorf("text %q", got)
	}
	for y, want := range []string{".#........", "..........", "###......."} {
		var got strings.Builder
		for x := range 10 {
			if cell := c.CellAt(x, y); cell != nil && cell.Style.Bg != nil {
				got.WriteByte('#')
			} else {
				got.WriteByte('.')
			}
		}
		if got.String() != want {
			t.Errorf("row %d lit %s, want %s", y, got.String(), want)
		}
	}
}

// modelUnqueueNew is a busy model with prompts queued.
func modelUnqueueNew(t *testing.T, prompts ...string) (*Model, *[]protocol.Outbound) {
	m, sent := modelNew(t, false)
	m.Status.Busy, m.Status.State = true, "thinking"
	for _, p := range prompts {
		modelPress(m, inputTyped(p)...)
		modelPress(m, inputKey("enter"))
	}
	return m, sent
}

// ↑ on an empty prompt takes the last queued prompt into it to edit; enter puts it back in its
// place, as edited. The others stay queued: they are joined only as they go.
func TestModelUnqueue(t *testing.T) {
	m, sent := modelUnqueueNew(t, "Fix it", "And the docs")
	modelPress(m, inputKey("up"))
	if string(m.Input.Text) != "And the docs" || len(m.chatEntries()) != 2 { // the caret and "Fix it"
		t.Fatalf("prompt %q drawn %+v", string(m.Input.Text), m.chatEntries())
	}
	modelPress(m, inputTyped(" too")...)
	modelPress(m, inputKey("enter"))
	if !slices.Equal(m.pending, []string{"Fix it", "And the docs too"}) || len(m.Input.Text) != 0 {
		t.Errorf("pending %q prompt %q", m.pending, string(m.Input.Text))
	}
	modelEvents(m, protocol.Result{})
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "Fix it\n\nAnd the docs too" {
		t.Errorf("sent %#v", *sent)
	}
	modelPress(m, inputKey("up")) // nothing queued: the history
	if string(m.Input.Text) != "And the docs too" || !m.browsing() {
		t.Errorf("history: prompt %q", string(m.Input.Text))
	}
}

// ↑ ↑ reaches the first queued prompt: the edit goes to it, the second stays as it was. ↓ walks
// back, past the newest to an empty prompt, and each keeps what it was given.
func TestModelUnqueueFirst(t *testing.T) {
	m, sent := modelUnqueueNew(t, "One", "Two")
	modelPress(m, inputKey("up"), inputKey("up"))
	modelPress(m, inputTyped(" more")...)
	modelPress(m, inputKey("enter"))
	if !slices.Equal(m.pending, []string{"One more", "Two"}) {
		t.Fatalf("pending %q", m.pending)
	}
	modelPress(m, inputKey("up"), inputKey("up"), inputKey("up")) // the oldest stays
	modelPress(m, inputKey("backspace"), inputKey("down"))
	if string(m.Input.Text) != "Two" || m.pending[0] != "One mor" {
		t.Fatalf("prompt %q pending %q", string(m.Input.Text), m.pending)
	}
	modelPress(m, inputKey("down"))
	if len(m.Input.Text) != 0 || m.queueAt != -1 || len(m.chatEntries()) != 3 { // the caret and both
		t.Fatalf("past the newest: prompt %q at %d", string(m.Input.Text), m.queueAt)
	}
	modelEvents(m, protocol.Result{})
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "One mor\n\nTwo" {
		t.Errorf("sent %q", *sent)
	}
}

// A turn that ends while a queued prompt is edited sends those before it; it and those after it
// go once it is back, in their order. An emptied one leaves the queue; ctrl+c leaves it as it was.
func TestModelUnqueueTurnEnds(t *testing.T) {
	m, sent := modelUnqueueNew(t, "A", "B", "C")
	modelPress(m, inputKey("up"), inputKey("up")) // b
	modelEvents(m, protocol.Result{})
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "A" || !slices.Equal(m.pending, []string{"B", "C"}) {
		t.Fatalf("sent %q pending %q", *sent, m.pending)
	}
	modelPress(m, inputKey("ctrl+c")) // b stays; the turn a started goes on
	if !slices.Equal(m.pending, []string{"B", "C"}) || len(*sent) != 1 {
		t.Fatalf("pending %q sent %q", m.pending, *sent)
	}
	modelPress(m, inputKey("up"), inputKey("up"))
	m.Input = Input{}
	modelEvents(m, protocol.Result{}) // the edited one is first: nothing goes
	if len(*sent) != 1 || m.Status.Busy {
		t.Fatalf("sent %q busy %v", *sent, m.Status.Busy)
	}
	modelPress(m, inputKey("enter")) // emptied: b leaves, c goes at once
	if len(*sent) != 2 || modelUserText((*sent)[1]) != "C" || len(m.pending) != 0 || !m.Status.Busy {
		t.Errorf("sent %q pending %q", *sent, m.pending)
	}
}

// With nothing said the IDE takes all the room above the prompt; the chat grows with what is said,
// up to 35% of the screen.
func TestViewChatGrows(t *testing.T) {
	m := drawTestModel(t, 80, 30)
	m.Chat = nil
	lines := drawPlainLines(m.View().Content)
	if !strings.HasPrefix(lines[25], "╰") || !strings.HasPrefix(lines[26], "╭") {
		t.Errorf("no chat: rows 25-26 %q %q", lines[25], lines[26])
	}
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "hi"}, {Kind: AssistantEntry, Text: "hello"}}
	lines = drawPlainLines(m.View().Content)
	if !strings.HasPrefix(lines[23], "╰") || lines[24] != "  › hi" || lines[25] != "  › hello" || !strings.HasPrefix(lines[26], "╭") {
		t.Errorf("two lines: rows 23-26 %q", lines[23:27])
	}
	for i := range 40 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprint("line ", i)})
	}
	lines = drawPlainLines(m.View().Content)
	if !strings.HasPrefix(lines[15], "╰") || lines[16] == "" {
		t.Errorf("a long chat takes more than 10 rows: rows 15-16 %q %q", lines[15], lines[16])
	}
}

// Behind a popup or the help the screen fades; they do not.
func TestViewDimsUnderPopup(t *testing.T) {
	sgr := func(c interface{ RGBA() (r, g, b, a uint32) }) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	m := drawTestModel(t, 100, 30)
	status := func() string {
		lines := strings.Split(m.View().Content, "\n")
		return lines[len(lines)-1]
	}
	if s := status(); !strings.Contains(s, sgr(theme.Fg)) {
		t.Fatalf("status without a popup: %q", s)
	}
	m.Popup = &PermissionPopup{Tool: "Bash", Detail: "ls", Allow: true}
	if s := status(); strings.Contains(s, sgr(theme.Fg)) || !strings.Contains(s, sgr(theme.Mix(theme.Fg, theme.Bg, drawDimBy))) {
		t.Errorf("status under a popup: %q", s)
	}
	x, y := modelMouseAt(t, m, "Allow Bash?")
	line := strings.Split(m.View().Content, "\n")[y]
	if !strings.Contains(line, sgr(theme.Accent)) || x == 0 {
		t.Errorf("the popup faded: %q", line)
	}
	m.Popup, m.ShowHelp = nil, true
	if s := status(); strings.Contains(s, sgr(theme.Fg)) || !strings.Contains(s, sgr(theme.Mix(theme.Fg, theme.Bg, drawDimBy))) {
		t.Errorf("status under the help: %q", s)
	}
	_, y = modelMouseAt(t, m, "send the prompt")
	if line := strings.Split(m.View().Content, "\n")[y]; !strings.Contains(line, sgr(theme.Accent)) {
		t.Errorf("the help faded: %q", line)
	}
}

// A click on a ! command in claude's reply puts it in the prompt, the shell mode on, to edit or
// send: a fenced block's whole text, an inline one's whole line however it wraps, the blank after
// the "!" gone as if typed. What the prompt
// said goes to the kill buffer and the history is no longer browsed. A click on a prompt, a drag,
// or a popup opened before the release puts nothing.
func TestModelMouseClickBang(t *testing.T) {
	m, sent := modelMouseNew(t)
	long := "! gh auth login --hostname git.example.com --scopes repo,read:org,workflow --web"
	m.Chat = []ChatEntry{
		{Kind: UserEntry, Text: "how do I log in? `! gh auth status`"},
		{Kind: AssistantEntry, Text: "Run this:\n```\n! gcloud auth login\n```\nor, on the other host, `" + long + "` instead."},
	}
	m.history, m.histAt = []history.Entry{{Text: "earlier"}}, 1
	m.Update(inputKey("up"))
	if !m.browsing() || string(m.Input.Text) != "earlier" {
		t.Fatalf("recalled %q", string(m.Input.Text))
	}

	modelMouseClick(t, m, "gcloud auth login")
	if got := string(m.Input.Text); got != "!gcloud auth login" || m.Input.Cursor != len(m.Input.Text) || !bangInput(m.Input.Text) || m.browsing() {
		t.Fatalf("prompt %q, cursor %d, browsing %v", got, m.Input.Cursor, m.browsing())
	}
	if !strings.Contains(m.View().Content, styleBang.Render("! ")) {
		t.Error("the prompt is not in the shell mode")
	}
	m.Update(inputKey("ctrl+y"))
	if got := string(m.Input.Text); got != "!gcloud auth loginearlier" || len(*sent) != 0 {
		t.Errorf("ctrl+y: prompt %q, sent %#v", got, *sent)
	}

	m.Input = Input{}
	modelMouseClick(t, m, "gh auth status") // the user's own prompt
	if len(m.Input.Text) != 0 {
		t.Errorf("a click on a prompt put %q", string(m.Input.Text))
	}
	x, y := modelMouseAt(t, m, "! gh auth login")
	m.Update(tea.MouseClickMsg{X: x - 1, Y: y, Button: tea.MouseLeft}) // the blank before it
	m.Update(tea.MouseReleaseMsg{X: x - 1, Y: y, Button: tea.MouseLeft})
	if len(m.Input.Text) != 0 {
		t.Errorf("a click beside the command put %q", string(m.Input.Text))
	}

	m.Update(tea.WindowSizeMsg{Width: 50, Height: 30})
	x, y = modelMouseAt(t, m, "workflow") // a row the command wraps to
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseReleaseMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	if len(m.Input.Text) != 0 {
		t.Errorf("a drag put %q", string(m.Input.Text))
	}
	modelMouseClick(t, m, "workflow")
	if got := string(m.Input.Text); got != "!"+long[2:] {
		t.Errorf("the wrapped command gave %q", got)
	}

	m.Input = Input{}
	x, y = modelMouseAt(t, m, "gcloud auth login")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	modelEvents(m, protocol.PermissionRequest{RequestID: "p1", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if m.View(); len(m.Input.Text) != 0 {
		t.Errorf("a release under a popup put %q", string(m.Input.Text))
	}
	modelMouseClick(t, m, "gcloud auth login")
	if len(m.Input.Text) != 0 || m.Popup == nil {
		t.Errorf("a click under a popup put %q", string(m.Input.Text))
	}
}

// A click on a ! command while a queued prompt is being edited and claude's turn has ended does
// not send the queue: the prompt holds the command, the queued prompt stays as it was and goes
// after a quiet period of its own.
func TestModelMouseClickBangKeepsQueue(t *testing.T) {
	m, sent := modelMouseNew(t)
	m.Status.Busy, m.Status.State = true, "thinking"
	for _, p := range []string{"Fix it", "And the docs"} {
		modelPress(m, inputTyped(p)...)
		modelPress(m, inputKey("enter"))
	}
	modelPress(m, inputKey("up"), inputKey("up")) // "Fix it" is in the prompt
	if m.queueAt != 0 || string(m.Input.Text) != "Fix it" {
		t.Fatalf("queueAt %d, prompt %q", m.queueAt, string(m.Input.Text))
	}
	modelEvents(m, protocol.TextStart{}, protocol.TextDelta{Text: "Run `! ls -la`"}, protocol.Result{}) // the turn ends
	if len(*sent) != 0 {
		t.Fatalf("the turn's end sent %#v", *sent)
	}
	modelAt(m, 10*time.Second)
	modelMouseClick(t, m, "ls -la")
	if len(*sent) != 0 || !slices.Equal(m.pending, []string{"Fix it", "And the docs"}) || string(m.Input.Text) != "!ls -la" || m.queueAt != -1 {
		t.Fatalf("sent %#v, pending %q, prompt %q, queueAt %d", *sent, m.pending, string(m.Input.Text), m.queueAt)
	}
	m.Update(tickMsg(modelT0.Add(14999 * time.Millisecond)))
	if len(*sent) != 0 {
		t.Fatalf("the queue went at once: %#v", *sent)
	}
	m.Update(tickMsg(modelT0.Add(15 * time.Second)))
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "Fix it\n\nAnd the docs" || string(m.Input.Text) != "!ls -la" {
		t.Errorf("sent %#v, prompt %q", *sent, string(m.Input.Text))
	}
}

// A click on a Bash line of the chat shows main alone, its terminal scrolled to where the call
// ran; a drag over the line, or a call the terminal holds no line of yet, shows nothing.
func TestModelMouseClickBash(t *testing.T) {
	m, _ := modelMouseNew(t)
	var out []string
	for i := range 60 {
		out = append(out, fmt.Sprintf("line %02d", i))
	}
	modelEvents(m,
		protocol.ToolUse{ID: "b1", Name: "Bash", Input: map[string]any{"command": "go test ./...", "description": "Run the tests"}},
		protocol.ToolResult{ID: "b1", Output: strings.Join(out, "\n")})
	for d := time.Duration(0); d < 20*time.Second; d += 50 * time.Millisecond {
		m.Update(tickMsg(modelT0.Add(d)))
	}
	modelEvents(m, protocol.ToolUse{ID: "b2", Name: "Bash", Input: map[string]any{"command": "ls", "description": "List the files"}})
	if n := strings.Count(ansi.Strip(m.View().Content), "Run the tests"); n != 1 {
		t.Fatalf("%q on screen %d times, want the chat's alone:\n%s", "Run the tests", n, ansi.Strip(m.View().Content))
	}

	x, y := modelMouseAt(t, m, "Run the tests")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseReleaseMsg{X: x + 3, Y: y, Button: tea.MouseLeft})
	if st := m.Sim.State(); st.Pinned {
		t.Error("a drag over a Bash line pinned the camera")
	}
	modelMouseClick(t, m, "List the files")
	if st := m.Sim.State(); st.Pinned {
		t.Error("a call not typed yet pinned the camera")
	}

	modelMouseClick(t, m, "Run the tests")
	if st := m.Sim.State(); !st.Pinned || st.Camera != "main" || m.scroll["term:main"] <= 0 {
		t.Errorf("pinned %v on %q, terminal scrolled back %d", st.Pinned, st.Camera, m.scroll["term:main"])
	}
	screen := ansi.Strip(m.View().Content)
	if !strings.Contains(screen, "# Run the tests") || !strings.Contains(screen, "go test ./...") {
		t.Errorf("the terminal does not show the call:\n%s", screen)
	}
}
