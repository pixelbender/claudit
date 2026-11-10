package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var inputSpecialKeys = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab,
	"backspace": tea.KeyBackspace, "delete": tea.KeyDelete,
	"left": tea.KeyLeft, "right": tea.KeyRight, "up": tea.KeyUp, "down": tea.KeyDown,
	"home": tea.KeyHome, "end": tea.KeyEnd, "pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "f1": tea.KeyF1, "f2": tea.KeyF2,
}

// inputKey builds the KeyPressMsg the terminal delivers for a key name as tea spells it ("enter",
// "ctrl+w", "alt+1", "super+left", "space", "y"). Modifiers come in tea's order.
func inputKey(name string) tea.KeyPressMsg {
	var mod tea.KeyMod
	for _, m := range []struct {
		prefix string
		mod    tea.KeyMod
	}{{"ctrl+", tea.ModCtrl}, {"alt+", tea.ModAlt}, {"shift+", tea.ModShift}, {"super+", tea.ModSuper}} {
		if rest, ok := strings.CutPrefix(name, m.prefix); ok && rest != "" {
			name, mod = rest, mod|m.mod
		}
	}
	if c, ok := inputSpecialKeys[name]; ok {
		return tea.KeyPressMsg{Code: c, Mod: mod}
	}
	if name == "space" {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	if mod != 0 {
		return tea.KeyPressMsg{Code: []rune(name)[0], Mod: mod}
	}
	return tea.KeyPressMsg{Code: []rune(name)[0], Text: name}
}

// inputKeys splits a script like "a b ctrl+w" into keys; a single space is spelled "space".
func inputKeys(script string) []tea.KeyPressMsg {
	var ks []tea.KeyPressMsg
	for _, f := range strings.Fields(script) {
		ks = append(ks, inputKey(f))
	}
	return ks
}

// inputTyped returns the keys that type s rune by rune.
func inputTyped(s string) []tea.KeyPressMsg {
	var ks []tea.KeyPressMsg
	for _, r := range s {
		if r == ' ' {
			ks = append(ks, inputKey("space"))
		} else {
			ks = append(ks, inputKey(string(r)))
		}
	}
	return ks
}

func TestInputKeyNames(t *testing.T) {
	for _, name := range []string{"enter", "esc", "tab", "backspace", "delete", "left", "right", "up", "down",
		"home", "end", "pgup", "pgdown", "space", "ctrl+c", "ctrl+w", "ctrl+u", "ctrl+a", "ctrl+e", "y",
		"f2", "alt+1", "alt+0", "shift+tab", "ctrl+left", "alt+left", "super+left", "alt+backspace",
		"super+backspace", "alt+b", "super+a"} {
		if got := inputKey(name).String(); got != name {
			t.Errorf("inputKey(%q).String() = %q", name, got)
		}
	}
}

func TestInputEditing(t *testing.T) {
	tests := []struct {
		name   string
		typed  string // typed first, rune by rune
		keys   string // then these keys
		want   string
		cursor int
	}{
		{"type ascii", "hello", "", "hello", 5},
		{"type words", "hello world", "", "hello world", 11},
		{"insert in the middle", "plnet", "left left left a", "planet", 3},
		{"emoji", "a🙂b", "left backspace", "ab", 1},
		{"backspace at start", "abc", "home backspace", "abc", 0},
		{"delete", "abc", "home delete", "bc", 0},
		{"delete at end", "abc", "delete", "abc", 3},
		{"right clamps", "ab", "right right", "ab", 2},
		{"left clamps", "ab", "left left left", "ab", 0},
		{"ctrl+a ctrl+e", "ab", "ctrl+a x ctrl+e y", "xaby", 4},
		{"home end", "ab", "home x end y", "xaby", 4},
		{"ctrl+u deletes to start", "one two", "left left ctrl+u", "wo", 0},
		{"ctrl+w deletes a word", "foo bar", "ctrl+w", "foo ", 4},
		{"ctrl+w skips trailing spaces", "foo bar  ", "ctrl+w", "foo ", 4},
		{"ctrl+w mid-text", "one two six", "left left left left ctrl+w", "one  six", 4},
		{"ctrl+w at start", "abc", "home ctrl+w", "abc", 0},
		{"ctrl+b ctrl+f", "abc", "ctrl+b ctrl+b x ctrl+f y", "axbyc", 4},
		{"ctrl+d deletes forward", "abc", "home ctrl+d", "bc", 0},
		{"ctrl+h is backspace", "abc", "ctrl+h", "ab", 2},
		{"alt+b alt+f by words", "foo bar.go", "alt+b alt+b alt+f", "foo bar.go", 7},
		{"alt and ctrl arrows by words", "foo bar", "alt+left ctrl+left x ctrl+right alt+right y", "xfoo bary", 9},
		{"a word holds underscores", "x my_var", "alt+b", "x my_var", 2},
		{"cmd arrows to the ends", "ab", "super+left x super+right y", "xaby", 4},
		{"ctrl+k kills to the end", "one two", "home alt+f ctrl+k", "one", 3},
		{"cmd+backspace kills to the start", "one two", "left super+backspace", "o", 0},
		{"alt+backspace kills a word", "foo bar.go", "alt+backspace", "foo bar.", 8},
		{"alt+d kills a word forward", "foo bar", "home alt+d", " bar", 0},
		{"ctrl+y yanks the last kill", "one two", "ctrl+w ctrl+a ctrl+y space", "two one ", 4},
		{"ctrl+y yanks again", "ab cd", "ctrl+a ctrl+k ctrl+y ctrl+y", "ab cdab cd", 10},
		{"killing nothing keeps the kill", "ab", "ctrl+u ctrl+e ctrl+k ctrl+y", "ab", 2},
		{"alt+backspace takes a placeholder whole", "see [Image #1]", "alt+backspace", "see ", 4},
		{"alt+d takes a placeholder whole", "see [Image #1] now", "ctrl+a alt+f alt+d", "see now", 3},
		{"ctrl+k takes a placeholder whole", "see [Pasted text #2 +3 lines] now", "ctrl+a alt+f alt+f ctrl+k", "see ", 4},
		{"ctrl+d takes a placeholder whole", "see [Image #1] now", "ctrl+a alt+f right ctrl+d", "see  now", 4},
		{"non-editing key", "ab", "up down tab", "ab", 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in Input
			for _, k := range append(inputTyped(tt.typed), inputKeys(tt.keys)...) {
				in.edit(k)
			}
			if string(in.Text) != tt.want || in.Cursor != tt.cursor {
				t.Errorf("got %q cursor %d, want %q cursor %d", string(in.Text), in.Cursor, tt.want, tt.cursor)
			}
		})
	}
}

func TestInputEditReportsUnhandledKeys(t *testing.T) {
	var in Input
	for _, name := range []string{"enter", "esc", "up", "down", "tab", "pgup"} {
		if in.edit(inputKey(name)) {
			t.Errorf("edit(%q) = true, want false", name)
		}
	}
	if !in.edit(inputKey("x")) || !in.edit(inputKey("backspace")) {
		t.Error("editing keys must report true")
	}
}

// The line editing keys reach the prompt: nothing on the way takes them.
func TestInputLineEditingInPrompt(t *testing.T) {
	m, _ := modelNew(t, false)
	modelPress(m, inputTyped("fix the bug")...)
	modelPress(m, inputKeys("alt+b ctrl+k ctrl+a ctrl+d ctrl+e ctrl+y")...)
	if got := string(m.Input.Text); got != "ix the bug" || m.Input.Cursor != 10 {
		t.Errorf("prompt %q cursor %d", got, m.Input.Cursor)
	}
}

func TestInputPasteKeepsNewlines(t *testing.T) {
	m, sent := modelNew(t, false)
	modelPress(m, inputTyped("ab")...)
	modelPress(m, inputKey("left"))
	m.Update(tea.PasteMsg{Content: "1\r\n2\r3"})
	if got := string(m.Input.Text); got != "a1\n2\n3b" || m.Input.Cursor != 6 {
		t.Fatalf("after paste: %q cursor %d", got, m.Input.Cursor)
	}
	modelPress(m, inputKey("enter"))
	if len(*sent) != 1 || modelUserText((*sent)[0]) != "a1\n2\n3b" {
		t.Fatalf("sent %#v", *sent)
	}
}
