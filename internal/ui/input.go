package ui

import (
	"slices"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// Input is a one-line text field. Cursor is a rune index into Text.
type Input struct {
	Text   []rune
	Cursor int
	killed []rune // what the last kill cut out, for ctrl+y
	all    bool   // the whole text is selected: typing replaces it, a key that deletes clears it
}

// takeAll acts on the whole text, selected, with key k: a key that deletes clears it as a kill
// would, for ctrl+y to put back, and reports true; typed text clears it the same way and goes in
// its place. Another key leaves it.
func (in *Input) takeAll(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace", "ctrl+h", "delete", "ctrl+d", "ctrl+u", "super+backspace", "ctrl+k", "ctrl+w", "alt+backspace", "alt+d":
		in.kill(0, len(in.Text))
		return true
	}
	if k.Text != "" {
		in.kill(0, len(in.Text))
	}
	return false
}

// edit applies an editing key and reports whether the key was one. The keys are those of a
// shell's line editor, and the ones macOS terminals send for cmd and option with the arrows and
// backspace.
func (in *Input) edit(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "left", "ctrl+b":
		in.Cursor = max(in.Cursor-1, 0)
	case "right", "ctrl+f":
		in.Cursor = min(in.Cursor+1, len(in.Text))
	case "home", "ctrl+a", "super+left":
		in.Cursor = 0
	case "end", "ctrl+e", "super+right":
		in.Cursor = len(in.Text)
	case "alt+left", "alt+b", "ctrl+left":
		in.Cursor = in.wordStart()
	case "alt+right", "alt+f", "ctrl+right":
		in.Cursor = in.wordEnd()
	case "backspace", "ctrl+h":
		before := string(in.Text[:in.Cursor])
		if loc := placeholderTail.FindStringIndex(before); loc != nil { // a placeholder goes whole
			n := utf8.RuneCountInString(before[loc[0]:])
			in.Text = slices.Delete(in.Text, in.Cursor-n, in.Cursor)
			in.Cursor -= n
		} else if in.Cursor > 0 {
			in.Text = slices.Delete(in.Text, in.Cursor-1, in.Cursor)
			in.Cursor--
		}
	case "delete", "ctrl+d":
		if in.Cursor < len(in.Text) {
			i, j := in.whole(in.Cursor, in.Cursor+1)
			in.Text = slices.Delete(in.Text, i, j)
			in.Cursor = i
		}
	case "ctrl+u", "super+backspace":
		in.kill(0, in.Cursor)
	case "ctrl+k":
		in.kill(in.Cursor, len(in.Text))
	case "ctrl+w": // back to a blank, as in a shell
		i := in.Cursor
		for i > 0 && unicode.IsSpace(in.Text[i-1]) {
			i--
		}
		for i > 0 && !unicode.IsSpace(in.Text[i-1]) {
			i--
		}
		in.kill(i, in.Cursor)
	case "alt+backspace":
		in.kill(in.wordStart(), in.Cursor)
	case "alt+d":
		in.kill(in.Cursor, in.wordEnd())
	case "ctrl+y":
		in.insert(string(in.killed))
	default:
		if k.Text == "" {
			return false
		}
		in.insert(k.Text)
	}
	return true
}

// kill cuts runes [i, j) out, and the placeholders they cut into, and keeps them for ctrl+y;
// cutting nothing keeps the last kill.
func (in *Input) kill(i, j int) {
	if i >= j {
		return
	}
	i, j = in.whole(i, j)
	in.killed = slices.Clone(in.Text[i:j])
	in.Text = slices.Delete(in.Text, i, j)
	in.Cursor = i
}

// whole widens runes [i, j) to the placeholders they cut into: a long paste or a picture goes whole,
// as backspace takes it, or its text would go to claude in its place.
func (in *Input) whole(i, j int) (int, int) {
	s := string(in.Text)
	for _, m := range placeholderRe.FindAllStringIndex(s, -1) {
		if a, b := utf8.RuneCountInString(s[:m[0]]), utf8.RuneCountInString(s[:m[1]]); a < j && b > i {
			i, j = min(i, a), max(j, b)
		}
	}
	return i, j
}

// isWord reports whether r belongs to a word, for the word moves: letters, digits and "_".
func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// wordStart is where alt+b goes: back over what is no word, then over the word before the cursor.
func (in *Input) wordStart() int {
	i := in.Cursor
	for i > 0 && !isWord(in.Text[i-1]) {
		i--
	}
	for i > 0 && isWord(in.Text[i-1]) {
		i--
	}
	return i
}

// wordEnd is where alt+f goes: on over what is no word, then over the word after the cursor.
func (in *Input) wordEnd() int {
	i := in.Cursor
	for i < len(in.Text) && !isWord(in.Text[i]) {
		i++
	}
	for i < len(in.Text) && isWord(in.Text[i]) {
		i++
	}
	return i
}

// insert puts s at the cursor and moves the cursor past it.
func (in *Input) insert(s string) {
	if in.all { // a paste goes in place of the whole text selected, as typing does
		in.kill(0, len(in.Text))
		in.all = false
	}
	r := []rune(s)
	in.Text = slices.Insert(in.Text, in.Cursor, r...)
	in.Cursor += len(r)
}
