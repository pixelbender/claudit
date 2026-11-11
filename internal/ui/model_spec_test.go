package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// modelSpecText is a spec delta long enough to scroll in a small window.
const modelSpecText = `## ADDED Requirements

### Requirement: Login
The system SHALL let a user log in.

#### Scenario: Good password
- **WHEN** a user logs in with the right password
- **THEN** the user is in

### Requirement: Logout
The system SHALL let a user log out.

#### Scenario: Logout
- **WHEN** the user logs out
- **THEN** the session ends

### Requirement: Sessions
A session SHALL last a day.

### Requirement: Tokens
A token SHALL last an hour.

### Requirement: Audit
Every login SHALL be logged.

## MODIFIED Requirements

### Requirement: Passwords
A password MUST NOT be shorter than 12 characters.

## REMOVED Requirements

### Requirement: Remember me
**Reason**: nobody used it

### Requirement: Guests
**Reason**: nobody is a guest

### Requirement: Captcha
**Reason**: nobody passed it

### Requirement: Hints
**Reason**: they gave the passwords away

### Requirement: Questions
**Reason**: everyone knew the answers
`

// modelSpec is a model w×h cells that shows a spec the user opened from the explorer.
func modelSpec(t *testing.T, w, h int) (*Model, string) {
	t.Helper()
	m, _ := modelNew(t, false)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	dir := t.TempDir()
	path := filepath.Join(dir, "openspec", "changes", "add-login", "specs", "auth", "spec.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(modelSpecText), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Sim.SetRoot(dir)
	m.viewFile(path)
	return m, path
}

// modelScreen is the screen of m as plain text; the no-break spaces that keep a tag of a spec whole
// are blanks.
func modelScreen(m *Model) string {
	return strings.ReplaceAll(ansi.Strip(m.View().Content), "\u00a0", " ")
}

// alt+s shows specs as written and back; the help tells.
func TestModelSpecRawKey(t *testing.T) {
	m, _ := modelSpec(t, 120, 40)
	if s := modelScreen(m); !strings.Contains(s, "▾ Login") || strings.Contains(s, "## ADDED") {
		t.Fatalf("not structured:\n%s", s)
	}
	modelPress(m, inputKey("alt+s"))
	if s := modelScreen(m); !m.Raw || !strings.Contains(s, "1 ## ADDED Requirements") || strings.Contains(s, "▾ Login") {
		t.Fatalf("not as written:\n%s", s)
	}
	modelPress(m, inputKey("alt+s"))
	if s := modelScreen(m); m.Raw || !strings.Contains(s, "▾ Login") {
		t.Fatalf("not structured again:\n%s", s)
	}
	if !strings.Contains(ansi.Strip(renderHelp(120, 60)), "alt+s") {
		t.Error("the help does not tell alt+s")
	}
}

// A click on a requirement opens or folds it as the button lets go; a drag from it selects text
// and folds nothing.
func TestModelSpecClick(t *testing.T) {
	m, _ := modelSpec(t, 120, 40)
	modelMouseClick(t, m, "▸ Logout")
	if s := modelScreen(m); !strings.Contains(s, "▾ Logout") || !strings.Contains(s, "▸ Logout  · 2 steps") {
		t.Fatalf("Logout did not open:\n%s", s)
	}
	modelMouseClick(t, m, "▸ Logout  · 2 steps")
	if s := modelScreen(m); !strings.Contains(s, "WHEN  the user logs out") {
		t.Fatalf("the scenario did not open:\n%s", s)
	}
	x, y := modelMouseAt(t, m, "▾ Logout")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 5, Y: y + 1, Button: tea.MouseLeft})
	m.View()
	m.Update(tea.MouseReleaseMsg{X: x + 5, Y: y + 1, Button: tea.MouseLeft})
	if s := modelScreen(m); !strings.Contains(s, "▾ Logout") || !strings.Contains(m.sel.text, "Logout") {
		t.Fatalf("a drag folded, or selected nothing: %q\n%s", m.sel.text, s)
	}
	if st := m.ideState(); st.Pinned || st.Viewed == "" {
		t.Error("a click on the spec pinned an agent or closed the file")
	}
}

// A click on a requirement of the outline opens it and scrolls the editor to it; the wheel
// scrolls the spec.
func TestModelSpecOutline(t *testing.T) {
	m, path := modelSpec(t, 100, 16)
	ew := min(max(100/5, 18), 32)
	if s := modelScreen(m); !strings.Contains(s, "outline · files") || !strings.Contains(s, "MODIFIED") {
		t.Fatalf("no outline:\n%s", s)
	}
	_, y := modelMouseAt(t, m, "▌ Passwords")
	m.Update(tea.MouseClickMsg{X: 3, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 3, Y: y, Button: tea.MouseLeft})
	rows := strings.Split(modelScreen(m), "\n")
	if !strings.Contains(rows[1], "▾ Passwords") {
		t.Fatalf("the editor did not scroll to Passwords:\n%s", strings.Join(rows, "\n"))
	}
	if m.scroll["spec:"+path] >= 0 {
		t.Errorf("spec scroll %d", m.scroll["spec:"+path])
	}
	before := m.scroll["spec:"+path]
	m.wheel(ew+10, 5, -1)
	if got := m.scroll["spec:"+path]; got <= before || got > 0 {
		t.Errorf("wheel up: %d from %d", got, before)
	}
}

// A click on the left column's title shows the files beside the spec, and they stay until the
// next click; the explorer opens files as before.
func TestModelSpecSwitch(t *testing.T) {
	m, _ := modelSpec(t, 120, 40)
	m.View()
	m.Update(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	s := modelScreen(m)
	if !m.Explorer || !strings.Contains(s, "files · outline") || !strings.Contains(s, "▸ openspec") || !strings.Contains(s, "▾ Login") {
		t.Fatalf("no files beside the spec:\n%s", s)
	}
	modelMouseClick(t, m, "▸ openspec")
	if s := modelScreen(m); !strings.Contains(s, "▾ openspec") {
		t.Fatalf("the folder did not open:\n%s", s)
	}
	m.View()
	m.Update(tea.MouseClickMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: 5, Y: 0, Button: tea.MouseLeft})
	if s := modelScreen(m); m.Explorer || !strings.Contains(s, "outline · files") && !strings.Contains(s, "requirements · files") {
		t.Fatalf("no outline again:\n%s", s)
	}
	if !strings.Contains(ansi.Strip(renderHelp(120, 60)), "click title") {
		t.Error("the help does not tell the switch")
	}
}

// Every frame with a spec on screen is exactly the window, whatever its size.
func TestModelSpecViewSize(t *testing.T) {
	for _, sz := range [][2]int{{40, 16}, {41, 17}, {60, 20}, {80, 24}, {120, 50}, {200, 60}} {
		m, _ := modelSpec(t, sz[0], sz[1])
		drawCheckSize(t, m.View().Content, sz[0], sz[1])
		modelMouseClick(t, m, "MODIFIED")
		drawCheckSize(t, m.View().Content, sz[0], sz[1])
	}
}
