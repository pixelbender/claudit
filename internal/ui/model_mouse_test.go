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
	if got := drawSelection(c, s, theme.SelBg, 0, 0); got != "b\n\nd世" {
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

// modelMouseLines is a chat of n lines "line 00" on, drawn.
func modelMouseLines(t *testing.T, n int) *Model {
	t.Helper()
	m, _ := modelMouseNew(t)
	for i := range n {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprintf("line %02d", i)})
	}
	m.View()
	return m
}

// modelMouseLinesText is the text of lines "line from" to "line to", as a selection of them whole
// copies it: past the arrow of the first.
func modelMouseLinesText(from, to int) string {
	var rows []string
	for i := from; i <= to; i++ {
		rows = append(rows, fmt.Sprintf("line %02d", i))
	}
	return strings.Join(rows, "\n")
}

// A drag held below the chat scrolls it down a frame at a time, the farther below the faster, and
// the selection takes in the lines that come up; let go, the scroll stops and the copy holds every
// line dragged over, those scrolled off too.
func TestModelMouseSelectScrollsDown(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.scrollChat(30)
	m.View()
	b := m.chatView.box
	x, y := modelMouseAt(t, m, "line 20")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(tea.MouseMotionMsg{X: x + 30, Y: b.Max.Y, Button: tea.MouseLeft}); cmd == nil {
		t.Fatal("held below the chat, no scroll started")
	}
	// Another motion past the edge starts no second scroll: one frame is on the way.
	if _, cmd := m.Update(tea.MouseMotionMsg{X: x + 30, Y: b.Max.Y, Button: tea.MouseLeft}); cmd != nil {
		t.Error("a second scroll started")
	}
	for range 3 {
		m.View()
		if _, cmd := m.Update(selScrollMsg{}); cmd == nil {
			t.Fatal("the scroll stopped while held")
		}
	}
	if m.ChatScroll != 27 {
		t.Errorf("a line a frame: scrolled %d, want 27", m.ChatScroll)
	}
	m.Update(tea.MouseMotionMsg{X: x + 30, Y: b.Max.Y + 2, Button: tea.MouseLeft})
	m.View()
	m.Update(selScrollMsg{})
	if m.ChatScroll != 24 {
		t.Errorf("three rows below, three lines a frame: scrolled %d, want 24", m.ChatScroll)
	}
	m.View()
	last := 60 - 1 - m.ChatScroll // on the chat's bottom row
	if got, want := m.selText(), modelMouseLinesText(20, last); got != want {
		t.Errorf("held: %q, want %q", got, want)
	}
	m.Update(tea.MouseReleaseMsg{X: x + 30, Y: b.Max.Y + 2, Button: tea.MouseLeft})
	if _, cmd := m.Update(selScrollMsg{}); cmd != nil || m.ChatScroll != 24 {
		t.Errorf("let go: scroll goes on %v, scrolled %d", cmd != nil, m.ChatScroll)
	}
	if got, want := m.selText(), modelMouseLinesText(20, last); got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

// A drag held above the chat, over the IDE, scrolls it up; back inside, the scroll stops.
func TestModelMouseSelectScrollsUp(t *testing.T) {
	m := modelMouseLines(t, 60)
	b := m.chatView.box
	x, y := modelMouseAt(t, m, "line 55")
	m.Update(tea.MouseClickMsg{X: x + 7, Y: y, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseMotionMsg{X: 0, Y: b.Min.Y - 1, Button: tea.MouseLeft})
	for range 4 {
		m.View()
		m.Update(selScrollMsg{})
	}
	if m.ChatScroll != 4 {
		t.Errorf("scrolled %d, want 4", m.ChatScroll)
	}
	first := 60 - m.ChatScroll - b.Dy() // on the chat's top row
	if got, want := m.selText(), modelMouseLinesText(first, 54)+"\nline 55"; got != want {
		t.Errorf("%q, want %q", got, want)
	}
	m.Update(tea.MouseMotionMsg{X: x, Y: b.Min.Y + 1, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(selScrollMsg{}); cmd != nil || m.ChatScroll != 4 {
		t.Errorf("back inside: scroll goes on %v, scrolled %d", cmd != nil, m.ChatScroll)
	}
}

// The wheel turned during a drag scrolls the chat under it: the selection's end stays under the
// mouse and takes in what came up.
func TestModelMouseSelectWheel(t *testing.T) {
	m := modelMouseLines(t, 60)
	x, y := modelMouseAt(t, m, "line 50")
	m.Update(tea.MouseClickMsg{X: x + 6, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	m.View()
	if got, want := m.selText(), modelMouseLinesText(47, 50); got != want {
		t.Errorf("%q, want %q", got, want)
	}
}

// modelSelectChat presses ctrl+a on the empty prompt: it selects the whole conversation and copies it.
func modelSelectChat(m *Model) tea.Cmd {
	return m.handleKey(inputKey("ctrl+a"))
}

// With the prompt empty, ctrl+a selects the whole conversation, lit where it shows, and copies it
// whole, the lines scrolled off too; a short chat is lit on the rows of its text, not the blanks
// over it. No conversation: nothing to select.
func TestModelMouseSelectWholeChat(t *testing.T) {
	m := modelMouseLines(t, 60)
	if cmd := modelSelectChat(m); cmd == nil {
		t.Fatal("nothing copied")
	}
	if got, want := m.selText(), modelMouseLinesText(0, 59); got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
	r, g, bl, _ := selFlashBg.RGBA() // copied: it flashes
	lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
	_, y := modelMouseAt(t, m, "line 59")
	if rows := strings.Split(m.View().Content, "\n"); !strings.Contains(rows[y], lit) {
		t.Error("the last line is not lit")
	}

	m, _ = modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: "fix the bug"}, {Kind: AssistantEntry, Text: "done"}}
	m.View()
	modelSelectChat(m)
	if got := m.selText(); got != "fix the bug\ndone" {
		t.Errorf("short chat: %q", got)
	}
	v := strings.Split(m.View().Content, "\n")
	_, y = modelMouseAt(t, m, "fix the bug")
	if !strings.Contains(v[y], lit) || strings.Contains(v[y-1], lit) {
		t.Errorf("short chat lit on rows %d and %d: %v %v", y-1, y, strings.Contains(v[y-1], lit), strings.Contains(v[y], lit))
	}

	m, _ = modelMouseNew(t)
	m.View()
	if cmd := modelSelectChat(m); cmd != nil || m.sel.drag {
		t.Errorf("empty chat: copy %v, selecting %v", cmd != nil, m.sel.drag)
	}
}

// cmd+a selects the whole prompt, lit, and copies it, its pasted texts in place of their
// placeholders; typing then replaces it, a key that deletes clears it (ctrl+y puts it back), a
// paste goes in place of it, and another key acts as with nothing selected. An empty prompt has
// nothing to select.
func TestModelPromptSelectAll(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "done"}}
	if cmd := m.handleKey(inputKey("super+a")); cmd != nil || m.Input.all {
		t.Errorf("empty prompt: copy %v, selected %v", cmd != nil, m.Input.all)
	}
	m.paste(strings.Repeat("pasted line\n", 12))
	modelPress(m, inputTyped(" fix it")...)
	text := string(m.Input.Text)
	if !strings.HasPrefix(text, "[Pasted text #") {
		t.Fatalf("no placeholder: %q", text)
	}
	if got := m.pastedTexts(text); !strings.HasPrefix(got, "pasted line\npasted line") {
		t.Fatalf("pasted texts: %q", got)
	}
	var copied string
	defer func(w func(string) error) { writeClipboard = w }(writeClipboard)
	writeClipboard = func(s string) error { copied = s; return nil }
	cmd := m.handleKey(inputKey("super+a"))
	if cmd == nil || !m.Input.all {
		t.Fatalf("copy %v, selected %v", cmd != nil, m.Input.all)
	}
	if cmd(); copied != m.pastedTexts(text) {
		t.Errorf("copied %q", copied)
	}
	r, g, bl, _ := theme.SelBg.RGBA()
	lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
	v := m.View()
	_, y := modelMouseAt(t, m, "fix it")
	if rows := strings.Split(v.Content, "\n"); !strings.Contains(rows[y], lit) || !strings.Contains(ansi.Strip(v.Content), "copied to clipboard") {
		t.Errorf("prompt lit %v, noted %v", strings.Contains(rows[y], lit), strings.Contains(ansi.Strip(v.Content), "copied to clipboard"))
	}

	modelPress(m, inputTyped("new")...)
	if got := string(m.Input.Text); got != "new" || m.Input.all || m.Input.Cursor != 3 {
		t.Errorf("typed over: %q cursor %d selected %v", got, m.Input.Cursor, m.Input.all)
	}
	if strings.Contains(m.View().Content, lit) {
		t.Error("lit after typing")
	}

	for _, key := range []string{"backspace", "delete", "ctrl+u", "ctrl+k", "ctrl+w"} {
		m.handleKey(inputKey("super+a"))
		m.handleKey(inputKey(key))
		if len(m.Input.Text) != 0 || m.Input.all {
			t.Errorf("%s: %q selected %v", key, string(m.Input.Text), m.Input.all)
		}
		modelPress(m, inputKeys("ctrl+y")...)
		if got := string(m.Input.Text); got != "new" {
			t.Errorf("%s then ctrl+y: %q", key, got)
		}
	}

	m.Input = Input{Text: []rune("fresh"), Cursor: 5} // nothing killed yet
	m.handleKey(inputKey("super+a"))
	m.Update(tea.PasteMsg{Content: "pasted"})
	if got := string(m.Input.Text); got != "pasted" || m.Input.all {
		t.Errorf("paste: %q selected %v", got, m.Input.all)
	}
	m.handleKey(inputKey("ctrl+y"))
	if got := string(m.Input.Text); got != "pastedfresh" {
		t.Errorf("paste then ctrl+y: %q", got)
	}
	m.Input = Input{Text: []rune("pasted"), Cursor: 6}

	m.handleKey(inputKey("super+a"))
	m.handleKey(inputKey("left"))
	if got := string(m.Input.Text); got != "pasted" || m.Input.all || m.Input.Cursor != 5 {
		t.Errorf("left: %q cursor %d selected %v", got, m.Input.Cursor, m.Input.all)
	}
}

// With text in the prompt ctrl+a goes to its start, as in a shell, and selects nothing, neither the
// prompt nor the conversation: cmux sends it for cmd+left as well.
func TestModelPromptCtrlAGoesToStart(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "done"}}
	m.View()
	m.Input = Input{Text: []rune("fix it"), Cursor: 6}
	if cmd := m.handleKey(inputKey("ctrl+a")); cmd != nil || m.Input.all || m.sel.drag || m.Input.Cursor != 0 {
		t.Errorf("copy %v, selected %v, chat selected %v, cursor %d", cmd != nil, m.Input.all, m.sel.drag, m.Input.Cursor)
	}
	modelPress(m, inputTyped("x")...)
	if got := string(m.Input.Text); got != "xfix it" {
		t.Errorf("typed at the start: %q", got)
	}
	m.handleKey(inputKey("super+a"))
	m.handleKey(inputKey("ctrl+a"))
	if got := string(m.Input.Text); got != "xfix it" || m.Input.all || m.Input.Cursor != 0 {
		t.Errorf("cmd+a then ctrl+a: %q selected %v cursor %d", got, m.Input.all, m.Input.Cursor)
	}
}

// With the IDE gone from a low window, the chat starts at the top of the screen: a drag held on its
// top row scrolls it up.
func TestModelMouseSelectScrollsUpTopRow(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m.View()
	if b := m.chatView.box; b.Min.Y != 0 {
		t.Fatalf("the chat starts on row %d", b.Min.Y)
	}
	x, y := modelMouseAt(t, m, "line 57")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: 1, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(tea.MouseMotionMsg{X: x, Y: 0, Button: tea.MouseLeft}); cmd == nil {
		t.Fatal("on the top row, no scroll started")
	}
	m.View()
	m.Update(selScrollMsg{})
	if m.ChatScroll != 1 {
		t.Errorf("scrolled %d, want 1", m.ChatScroll)
	}
}

// Far above the chat, a frame scrolls at most selScrollMax lines; at the top, the scroll stops.
func TestModelMouseSelectScrollsUpFast(t *testing.T) {
	m := modelMouseLines(t, 30)
	b := m.chatView.box
	if b.Min.Y <= selScrollMax {
		t.Fatalf("the chat starts on row %d: no room above it", b.Min.Y)
	}
	x, y := modelMouseAt(t, m, "line 29")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: 0, Button: tea.MouseLeft})
	m.View()
	m.Update(selScrollMsg{})
	if m.ChatScroll != selScrollMax {
		t.Errorf("scrolled %d, want %d", m.ChatScroll, selScrollMax)
	}
	m.View()
	if _, cmd := m.Update(selScrollMsg{}); cmd == nil || m.ChatScroll != m.chatMax {
		t.Fatalf("to the top: scrolled %d of %d, goes on %v", m.ChatScroll, m.chatMax, cmd != nil)
	}
	m.View()
	if _, cmd := m.Update(selScrollMsg{}); cmd != nil {
		t.Error("at the top, the scroll goes on")
	}
	if got := m.selText(); got != modelMouseLinesText(0, 28)+"\nl" {
		t.Errorf("%q", got)
	}
}

// The wheel turned over the IDE during a drag in the chat scrolls the chat, not the pane.
func TestModelMouseSelectWheelOverIDE(t *testing.T) {
	m := modelMouseLines(t, 60)
	x, y := modelMouseAt(t, m, "line 59")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 5, Y: 5, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp})
	if m.ChatScroll != wheelStep || len(m.scroll) != 0 {
		t.Errorf("chat scrolled %d, panes %v", m.ChatScroll, m.scroll)
	}
}

// The window lowered during a drag: the chat may leave the screen, or nothing shows, too small;
// the drag goes on, and the release copies what it held if the chat shows it, else nothing. Back
// to its height, the chat shows it again. Narrower, the lines wrap anew: the selection goes.
func TestModelMouseSelectResized(t *testing.T) {
	for _, size := range [][2]int{{100, 20}, {100, 16}, {100, 6}, {100, 30}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := modelMouseLines(t, 60)
			x, y := modelMouseAt(t, m, "line 58")
			m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			m.Update(tea.MouseMotionMsg{X: x + 6, Y: y + 1, Button: tea.MouseLeft})
			m.View()
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			m.View()
			m.Update(tea.MouseMotionMsg{X: 99, Y: size[1] - 1, Button: tea.MouseLeft})
			m.Update(selScrollMsg{})
			m.View()
			shown := !m.chatView.box.Empty()
			m.Update(tea.MouseReleaseMsg{X: 99, Y: size[1] - 1, Button: tea.MouseLeft})
			if got, want := m.selText(), map[bool]string{true: "line 58\nline 59", false: ""}[shown]; got != want {
				t.Errorf("chat shown %v: copied %q, want %q", shown, got, want)
			}
			if size[1] == 30 && !shown {
				t.Error("the chat is not back")
			}
		})
	}
	m := modelMouseLines(t, 60)
	x, y := modelMouseAt(t, m, "line 58")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 6, Y: y + 1, Button: tea.MouseLeft})
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); cmd != nil || m.sel.drag {
		t.Errorf("narrower: copy %v, selecting %v", cmd != nil, m.sel.drag)
	}
}

// A popup over a drag held past the chat stops its scroll, and the release copies nothing: the
// popup hid it.
func TestModelMouseSelectUnderPopup(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.scrollChat(20)
	m.View()
	x, y := modelMouseAt(t, m, "line 35")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: m.chatView.box.Max.Y, Button: tea.MouseLeft})
	modelEvents(m, protocol.PermissionRequest{RequestID: "p", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	m.View()
	if _, cmd := m.Update(selScrollMsg{}); cmd != nil || m.ChatScroll != 20 {
		t.Errorf("under the popup: scroll goes on %v, scrolled %d", cmd != nil, m.ChatScroll)
	}
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); cmd != nil {
		t.Error("the release copied")
	}
}

// Over a short chat the rows are blank: a drag from one takes its first line from the start. In
// a chat with nothing in it, a drag copies nothing.
func TestModelMouseSelectShortChat(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "first line of the reply here"}, {Kind: AssistantEntry, Text: "second"}}
	_, y := modelMouseAt(t, m, "first line")
	if b := m.chatView.box; b.Min.Y >= y {
		t.Fatalf("no blank row over the chat: it starts on %d, the text on %d", b.Min.Y, y)
	}
	m.Update(tea.MouseClickMsg{X: 20, Y: m.chatView.box.Min.Y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 99, Y: y + 1, Button: tea.MouseLeft})
	m.View()
	if got := m.selText(); got != "first line of the reply here\nsecond" {
		t.Errorf("%q", got)
	}

	m, _ = modelMouseNew(t)
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	m.View()
	if m.chatView.box.Empty() {
		t.Fatal("no chat on screen")
	}
	b := m.chatView.box
	m.Update(tea.MouseClickMsg{X: b.Min.X, Y: b.Min.Y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: b.Max.X - 1, Y: b.Max.Y - 1, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: b.Max.X - 1, Y: b.Max.Y - 1, Button: tea.MouseLeft}); cmd != nil {
		t.Error("an empty chat copied")
	}
}

// The whole conversation copies a prompt that wraps a row per row, without its time.
func TestModelMouseSelectWrapped(t *testing.T) {
	m, _ := modelMouseNew(t)
	long := strings.Repeat("word ", 30)
	m.Chat = []ChatEntry{{Kind: UserEntry, Text: long, At: modelT0}}
	m.View()
	modelSelectChat(m)
	got := m.selText()
	if rows := strings.Split(got, "\n"); len(rows) < 2 || strings.Contains(got, modelT0.Format("15:04")) ||
		strings.Join(strings.Fields(got), " ") != strings.TrimSpace(long) {
		t.Errorf("%q", got)
	}
}

// A character is taken by the column it starts on, a wide one whole, as the screen lights it.
func TestSelCut(t *testing.T) {
	line := "ab世界" + styleDim.Render("cd你好") + "ef"
	for _, c := range []struct {
		x0, x1 int
		want   string
	}{{0, 2, "ab"}, {3, 5, "界"}, {2, 4, "世"}, {2, 6, "世界"}, {7, 11, "d你好"}, {12, 99, "ef"}, {50, 60, ""}} {
		if got := selCut(line, c.x0, c.x1); got != c.want {
			t.Errorf("[%d, %d): %q, want %q", c.x0, c.x1, got, c.want)
		}
	}
	// Measured as the screen measures them: a keycap takes two columns, a Thai letter with its
	// mark one.
	for _, c := range [][2]string{{"1️⃣ab", "a"}, {"กำab", "a"}} {
		if got := selCut(c[0], ansi.StringWidth(c[0])-2, ansi.StringWidth(c[0])-1); got != c[1] {
			t.Errorf("%q: %q, want %q", c[0], got, c[1])
		}
	}
}

// The chat gone from the screen during a drag and back, the selection holds what it held.
func TestModelMouseSelectChatBack(t *testing.T) {
	m := modelMouseLines(t, 60)
	x, y := modelMouseAt(t, m, "line 58")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 99, Y: y + 1, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	if m.View(); !m.chatView.box.Empty() {
		t.Fatal("the chat stays on screen")
	}
	m.Update(tea.MouseMotionMsg{X: 3, Y: 3, Button: tea.MouseLeft})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.View()
	m.Update(tea.MouseReleaseMsg{X: 3, Y: 3, Button: tea.MouseLeft})
	if got := m.selText(); got != "line 58\nline 59" {
		t.Errorf("%q", got)
	}
}

// The window lowered during a drag, the chat takes fewer rows: what it selects past them is lit
// nowhere else, the prompt under it least of all.
func TestModelMouseSelectLowered(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.scrollChat(20)
	m.View()
	b := m.chatView.box
	x, y := modelMouseAt(t, m, "line 30")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 99, Y: b.Max.Y - 1, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 26})
	v := m.View().Content
	r, g, bl, _ := theme.SelBg.RGBA()
	lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
	rows := strings.Split(v, "\n")
	for y := m.chatView.box.Max.Y; y < len(rows); y++ {
		if strings.Contains(rows[y], lit) {
			t.Errorf("row %d under the chat is lit: %q", y, ansi.Strip(rows[y]))
		}
	}
	if !strings.Contains(v, lit) {
		t.Error("nothing lit")
	}
}

// A line's blanks after its text stay out of the copy: a code block pads its lines.
func TestModelMouseSelectTrims(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "```\nx := 1\nlonger := 2\n```"}}
	m.View()
	if modelSelectChat(m) == nil || !strings.Contains(m.selText(), "longer := 2") {
		t.Fatalf("nothing selected: %q", m.selText())
	}
	for _, row := range strings.Split(m.selText(), "\n") {
		if strings.HasSuffix(row, " ") {
			t.Errorf("%q ends in blanks", row)
		}
	}
}

// The window raised with the whole conversation selected, the chat takes more rows: it is lit on
// every one of them.
func TestModelMouseSelectRaised(t *testing.T) {
	m := modelMouseLines(t, 60)
	modelSelectChat(m)
	before := m.chatView.box
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 50})
	rows := strings.Split(m.View().Content, "\n")
	b := m.chatView.box
	if b.Dy() <= before.Dy() {
		t.Fatalf("the chat took %d rows, before %d", b.Dy(), before.Dy())
	}
	r, g, bl, _ := selFlashBg.RGBA()
	lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		if !strings.Contains(rows[y], lit) {
			t.Errorf("row %d of the chat is not lit: %q", y, ansi.Strip(rows[y]))
		}
	}
}

// The chat gone from the screen during a drag, nothing is lit where it was: the prompt took its rows.
func TestModelMouseSelectChatGoneUnlit(t *testing.T) {
	m := modelMouseLines(t, 26)
	x, y := modelMouseAt(t, m, "line 16")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 99, Y: y + 2, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	v := m.View().Content
	if !m.chatView.box.Empty() {
		t.Fatal("the chat stays on screen")
	}
	r, g, bl, _ := theme.SelBg.RGBA()
	if lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8); strings.Contains(v, lit) {
		t.Error("lit with no chat on screen")
	}
}

// Over the completions the chat does not scroll under a drag.
func TestModelMouseSelectUnderCompletions(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.scrollChat(20)
	m.View()
	x, y := modelMouseAt(t, m, "line 30")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x, Y: m.chatView.box.Max.Y, Button: tea.MouseLeft})
	m.Suggest = []protocol.Command{{Name: "help"}}
	m.View()
	if _, cmd := m.Update(selScrollMsg{}); cmd != nil || m.ChatScroll != 20 {
		t.Errorf("scroll goes on %v, scrolled %d", cmd != nil, m.ChatScroll)
	}
}

// Every key that deletes clears the whole prompt selected, wherever the cursor is, and ctrl+y puts
// it back; typing clears it the same way.
func TestModelPromptSelectAllKeys(t *testing.T) {
	for _, key := range []string{"backspace", "ctrl+h", "delete", "ctrl+d", "ctrl+u", "super+backspace", "ctrl+k", "ctrl+w", "alt+backspace", "alt+d", "x"} {
		m, _ := modelMouseNew(t)
		modelPress(m, inputTyped("one two three")...)
		modelPress(m, inputKeys("left left left left left left left")...) // mid "two"
		m.handleKey(inputKey("super+a"))
		m.handleKey(inputKey(key))
		want := ""
		if key == "x" {
			want = "x"
		}
		if got := string(m.Input.Text); got != want || m.Input.all {
			t.Errorf("%s: %q selected %v", key, got, m.Input.all)
		}
		m.handleKey(inputKey("ctrl+y"))
		if got := string(m.Input.Text); got != want+"one two three" {
			t.Errorf("%s then ctrl+y: %q", key, got)
		}
	}
}

// The selected prompt is lit from its text on, the "›" out; a shell command's "!" is lit with it,
// as it is copied. Under a popup or the help nothing is lit.
func TestModelPromptSelectAllLit(t *testing.T) {
	r, g, bl, _ := theme.SelBg.RGBA()
	lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
	for _, c := range []struct{ text, first string }{{"fix it", "f"}, {"!ls -la", "!"}} {
		m, _ := modelMouseNew(t)
		modelPress(m, inputTyped(c.text)...)
		m.handleKey(inputKey("super+a"))
		_, y := modelMouseAt(t, m, c.text[1:])
		row := strings.Split(m.View().Content, "\n")[y]
		at := strings.Index(row, lit)
		if at < 0 || !strings.HasPrefix(ansi.Strip(row[at+len(lit)+1:]), c.first) { // past the "m" of the colour
			t.Errorf("%q: the lit part starts %q", c.text, ansi.Strip(row[max(at, 0):]))
		}
	}

	// Under the help or a popup the prompt shows as with nothing selected.
	m, _ := modelMouseNew(t)
	modelPress(m, inputTyped("fix it")...)
	m.handleKey(inputKey("super+a"))
	m.sel = selection{} // no note of the copy: only the prompt differs
	under := func(name string) {
		selected := m.View().Content
		m.Input.all = false
		if plain := m.View().Content; selected != plain {
			t.Errorf("lit under %s", name)
		}
		m.Input.all = true
	}
	m.ShowHelp = true
	under("the help")
	m.ShowHelp = false
	modelEvents(m, protocol.PermissionRequest{RequestID: "p", ToolName: "Bash", Input: map[string]any{"command": "ls"}})
	under("a popup")
}

// A key or a click elsewhere drops the selection: typing then goes on after the text. A picture
// pasted goes with the text, never in place of it.
func TestModelPromptSelectAllDropped(t *testing.T) {
	for _, step := range []struct {
		name string
		do   func(m *Model)
	}{
		{"f1", func(m *Model) { m.handleKey(inputKey("f1")) }},
		{"alt+z", func(m *Model) { m.handleKey(inputKey("alt+z")) }},
		{"click", func(m *Model) {
			m.Update(tea.MouseClickMsg{X: 10, Y: 2, Button: tea.MouseLeft})
			m.Update(tea.MouseReleaseMsg{X: 10, Y: 2, Button: tea.MouseLeft})
		}},
	} {
		m, _ := modelMouseNew(t)
		modelPress(m, inputTyped("keep")...)
		m.handleKey(inputKey("super+a"))
		step.do(m)
		modelPress(m, inputTyped("x")...)
		if got := string(m.Input.Text); got != "keepx" {
			t.Errorf("%s: %q", step.name, got)
		}
	}

	m, _ := modelMouseNew(t)
	modelPress(m, inputTyped("keep")...)
	m.handleKey(inputKey("super+a"))
	m.pasted(imagesMsg{images: []protocol.Image{{Media: "image/png", Data: []byte{0}}}})
	if got := string(m.Input.Text); !strings.HasPrefix(got, "keep[Image #") || m.Input.all {
		t.Errorf("picture: %q selected %v", got, m.Input.all)
	}
}

// modelCopied runs what a release starts and returns what it put on the clipboard, "" for nothing.
func modelCopied(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	var copied string
	defer func(w func(string) error) { writeClipboard = w }(writeClipboard)
	writeClipboard = func(s string) error { copied = s; return nil }
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		if batch, ok := c().(tea.BatchMsg); ok {
			for _, c := range batch {
				run(c)
			}
		}
	}
	run(cmd)
	return copied
}

// modelCodeDraft is a reply whose block of code holds a draft longer than a row of the chat 100
// cells wide, a short line after it, and a block of a ! command.
const modelCodeDraft = "Коллеги, привет!  Черновик   длинный, перенос   по словам, и строка не теряет ни   одного пробела до конца"

func modelCodeNew(t *testing.T) *Model {
	t.Helper()
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "Черновик:\n```text\n" + modelCodeDraft + "\n    вторая строка\n```\nи вход:\n```\n! make login\n```"}}
	m.View()
	return m
}

// A click on a row of a block of code copies what it holds as written, a wrapped line whole and
// without the bar, lit and noted as a copied selection is; a click elsewhere in the reply copies
// nothing, a drag copies what it covers, and a ! command's block still puts the command in the
// prompt.
func TestModelMouseClickCode(t *testing.T) {
	want := modelCodeDraft + "\n    вторая строка"
	for _, at := range []string{"вторая строка", "Коллеги", "до конца"} {
		m := modelCodeNew(t)
		x, y := modelMouseAt(t, m, at)
		m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
		if got := modelCopied(t, cmd); got != want {
			t.Errorf("a click on %q copied %q", at, got)
		}
		r, g, bl, _ := selFlashBg.RGBA()
		lit := fmt.Sprintf("48;2;%d;%d;%d", r>>8, g>>8, bl>>8)
		v := m.View().Content
		rows := strings.Split(v, "\n")
		_, top := modelMouseAt(t, m, "Коллеги")
		_, last := modelMouseAt(t, m, "вторая строка")
		_, over := modelMouseAt(t, m, "Черновик:")
		_, under := modelMouseAt(t, m, "и вход:")
		if !strings.Contains(rows[top], lit) || !strings.Contains(rows[last], lit) || strings.Contains(rows[over], lit) || strings.Contains(rows[under], lit) ||
			!strings.Contains(ansi.Strip(v), drawNoted) {
			t.Errorf("a click on %q: lit %v %v %v %v, noted %v", at, strings.Contains(rows[over], lit), strings.Contains(rows[top], lit),
				strings.Contains(rows[last], lit), strings.Contains(rows[under], lit), strings.Contains(ansi.Strip(v), drawNoted))
		}
	}

	m := modelCodeNew(t)
	x, y := modelMouseAt(t, m, "Черновик:")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); modelCopied(t, cmd) != "" {
		t.Error("a click on the prose copied")
	}

	x, y = modelMouseAt(t, m, "вторая строка")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	m.View()
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft}); modelCopied(t, cmd) != "вторая" {
		t.Errorf("a drag copied %q", m.selText())
	}

	modelMouseClick(t, m, "make login")
	if got := string(m.Input.Text); got != "!make login" {
		t.Errorf("the ! block put %q", got)
	}
	x, y = modelMouseAt(t, m, "make login")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); modelCopied(t, cmd) != "" {
		t.Error("a click on the ! block copied it")
	}
	m.Input = Input{}
	for _, x := range []int{0, 3, 98} { // beside its text, before the bar and at the right
		m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if _, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}); modelCopied(t, cmd) != "" {
			t.Errorf("a click at %d beside the ! block copied it", x)
		}
	}
}

// A drag over rows of a block of code copies them without the bar, a line that wraps joined
// whole again; from the bar's cells, a row whole; from within a row, from there; over the reply
// and the block, the reply's lines as they are, and so does ctrl+a.
func TestModelMouseSelectCode(t *testing.T) {
	m := modelCodeNew(t)
	x0, y0 := modelMouseAt(t, m, "Коллеги")
	x1, y1 := modelMouseAt(t, m, "вторая строка")
	drag := func(fx, fy, tx, ty int) string {
		m.Update(tea.MouseClickMsg{X: fx, Y: fy, Button: tea.MouseLeft})
		m.Update(tea.MouseMotionMsg{X: tx, Y: ty, Button: tea.MouseLeft})
		m.View()
		got := m.selText()
		m.Update(tea.MouseReleaseMsg{X: tx, Y: ty, Button: tea.MouseLeft})
		return got
	}
	end := x1 + len([]rune("вторая строка")) - 1
	if y1-y0 < 2 {
		t.Fatalf("the draft takes rows %d to %d: it does not wrap", y0, y1)
	}
	if got := drag(x0-2, y0, end, y1); got != modelCodeDraft+"\n    вторая строка" {
		t.Errorf("from the bar: %q", got)
	}
	if got := drag(x0+len([]rune("Коллеги, ")), y0, end, y1); got != "привет!  Черновик   длинный, перенос   по словам, и строка не теряет ни   одного пробела до конца\n    вторая строка" {
		t.Errorf("from within: %q", got)
	}
	shown := func(y int) string { // the text of row y of the block, past its bar
		row := ansi.Strip(strings.Split(m.View().Content, "\n")[y])
		return strings.TrimRight(row[strings.Index(row, "│ ")+len("│ "):], " ")
	}
	_, y := modelMouseAt(t, m, "до конца")
	if got := drag(x0-2, y, 99, y); got != shown(y) || got == "" || strings.HasPrefix(got, " ") {
		t.Errorf("a row the line wraps to, alone: %q, it shows %q", got, shown(y))
	}
	if got := drag(x0, y0, x0-2, y0+1); got != shown(y0) || strings.HasSuffix(got, " ") {
		t.Errorf("up to the bar of the row the line wraps to: %q, the first row shows %q", got, shown(y0))
	}
	_, over := modelMouseAt(t, m, "Черновик:")
	if got := drag(m.chatView.box.Min.X, over, 99, y1); got != "Черновик:\n"+modelCodeDraft+"\n    вторая строка" {
		t.Errorf("with the prose: %q", got)
	}
	modelSelectChat(m)
	if got := m.selText(); got != "Черновик:\n"+modelCodeDraft+"\n    вторая строка\nи вход:\n! make login" {
		t.Errorf("ctrl+a: %q", got)
	}
}

// modelMouseHas reports whether text is on the screen as m draws it.
func modelMouseHas(m *Model, text string) bool {
	return strings.Contains(ansi.Strip(m.View().Content), text)
}

// modelMouseLineOf is the line of the conversation that says text, -1 for none.
func modelMouseLineOf(m *Model, text string) int {
	return slices.IndexFunc(m.chatText(), func(l string) bool { return strings.Contains(ansi.Strip(l), text) })
}

// A block of code in a long conversation, scrolled to show only its end, is copied whole and lit
// on its own lines, whatever the width.
func TestModelMouseClickCodeScrolled(t *testing.T) {
	m := modelMouseLines(t, 60)
	m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: "```\nb1\nb2\nb3\nb4\nb5\nb6\n```"})
	for i := range 20 {
		m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Text: fmt.Sprintf("after %02d", i)})
	}
	for _, w := range []int{100, 60} {
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		m.ChatScroll = 0
		for i := 0; i < 80 && !(modelMouseHas(m, "b4") && !modelMouseHas(m, "b1")); i++ {
			m.scrollChat(1)
		}
		if !modelMouseHas(m, "b4") || modelMouseHas(m, "b1") {
			t.Fatalf("width %d: no scroll shows b4 without b1", w)
		}
		x, y := modelMouseAt(t, m, "b4")
		m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
		if got := modelCopied(t, cmd); got != "b1\nb2\nb3\nb4\nb5\nb6" {
			t.Errorf("width %d: copied %q", w, got)
		}
		from, to := m.sel.ends()
		if b1, b6 := modelMouseLineOf(m, "b1"), modelMouseLineOf(m, "b6"); from.Y != b1 || to.Y != b6 {
			t.Errorf("width %d: lit lines %d to %d, the block is on %d to %d", w, from.Y, to.Y, b1, b6)
		}
		m.sel = selection{}
	}
}

// A block of code still streaming copies what it holds as the button lets go, the lines that came
// in after the press too, and is lit on all of them.
func TestModelMouseClickCodeStreaming(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "```\necho 1\necho 2"}}
	x, y := modelMouseAt(t, m, "echo 1")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Chat[0].Text += "\necho 3\n```\ndone"
	m.View()
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if got := modelCopied(t, cmd); got != "echo 1\necho 2\necho 3" {
		t.Errorf("copied %q", got)
	}
	if from, to := m.sel.ends(); to.Y-from.Y != 2 {
		t.Errorf("lit lines %d to %d", from.Y, to.Y)
	}
}

// A click copies a block's tabs and blanks at a line's end as written; a drag takes what the chat
// shows, a tab as the blanks it is drawn as, and no blanks at a row's end.
func TestModelMouseClickCodeAsWritten(t *testing.T) {
	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "```make\nall:\n\tgo build ./...   \n```"}}
	x, y := modelMouseAt(t, m, "all:")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if got := modelCopied(t, cmd); got != "all:\n\tgo build ./...   " {
		t.Errorf("a click copied %q", got)
	}
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: 90, Y: y + 1, Button: tea.MouseLeft})
	m.View()
	if got := m.selText(); got != "all:\n    go build ./..." {
		t.Errorf("a drag took %q", got)
	}
}

// Between the press on a block of code and the release, the window narrowed or an entry above
// grown, the block pressed is copied and lit where it is drawn then; one become a ! command's is
// not copied.
func TestModelMouseClickCodeMoved(t *testing.T) {
	press := func(m *Model, text string) (x, y int) {
		x, y = modelMouseAt(t, m, text)
		m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		return x, y
	}
	release := func(m *Model, x, y int, want, first, last string) {
		t.Helper()
		m.View()
		_, cmd := m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
		if got := modelCopied(t, cmd); got != want {
			t.Errorf("copied %q, want %q", got, want)
		}
		if from, to := m.sel.ends(); want != "" && (from.Y != modelMouseLineOf(m, first) || to.Y != modelMouseLineOf(m, last)) {
			t.Errorf("lit lines %d to %d, the block is on %d to %d", from.Y, to.Y, modelMouseLineOf(m, first), modelMouseLineOf(m, last))
		}
	}

	m, _ := modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "```\n" + strings.TrimSpace(strings.Repeat("word ", 30)) + "\n```\nmid\n```\nB1\nB2\n```"}}
	x, y := press(m, "B1")
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 30})
	release(m, x, y, "B1\nB2", "B1", "B2")

	m, _ = modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: CommandEntry, Bang: "ls"}, {Kind: AssistantEntry, Text: "```\na1\na2\n```"}}
	x, y = press(m, "a1")
	m.Chat[0].Text = "one\ntwo\nthree\nfour"
	release(m, x, y, "a1\na2", "a1", "a2")

	m, _ = modelMouseNew(t)
	m.Chat = []ChatEntry{{Kind: AssistantEntry, Text: "```\n!"}}
	x, y = press(m, "!")
	m.Chat[0].Text += "ls -la\n```"
	release(m, x, y, "", "", "")
}
