package ui

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/pixelbender/claudit/internal/history"
	"github.com/pixelbender/claudit/internal/protocol"
)

// A long paste and a picture stay out of the prompt and the chat, as in Claude Code: a
// placeholder stands for each, and message puts the texts back when the prompt goes to claude.
const (
	pasteChars = 800 // a paste longer than this many characters…
	pasteLines = 2   // …or with more line breaks becomes [Pasted text #n +m lines]
)

// Claude Code scales pictures down to imageSide a side, and to imageBytes: 5 MB in base64,
// what the API takes.
const (
	imageSide  = 2000
	imageBytes = 3932160
)

var (
	placeholderRe   = regexp.MustCompile(`\[(?:Pasted text #\d+(?: \+\d+ lines)?|Image #\d+)\]`)
	placeholderTail = regexp.MustCompile(placeholderRe.String() + `$`)
	imagePathRe     = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)$`)
)

// attachment is what a placeholder stands for: pasted text, or a picture.
type attachment struct {
	text  string
	image *protocol.Image
	lost  bool // an earlier session's, which the history did not keep: its text is the placeholder itself
}

// imagesMsg brings pictures read off the UI's way, for the prompt.
type imagesMsg struct {
	images []protocol.Image
	err    error
}

// attach keeps a and returns the placeholder that stands for it. Numbers run through the session,
// so prompts recalled from the history or queued keep what they had.
func (m *Model) attach(a attachment) string {
	n := len(m.attached) + 1
	lines := strings.Count(a.text, "\n")
	p := fmt.Sprintf("[Pasted text #%d +%d lines]", n, lines)
	switch {
	case a.image != nil:
		p = fmt.Sprintf("[Image #%d]", n)
	case lines == 0:
		p = fmt.Sprintf("[Pasted text #%d]", n)
	}
	m.attached[p] = a
	return p
}

// message is the UserMessage for a prompt: pasted texts back in place of their placeholders, the
// pictures attached, theirs left in the text for the model to tell them apart; the ! commands run
// since the last prompt in front.
func (m *Model) message(text string) protocol.UserMessage {
	var msg protocol.UserMessage
	msg.Text = placeholderRe.ReplaceAllStringFunc(text, func(p string) string {
		a, ok := m.attached[p]
		switch {
		case !ok: // typed, or from a resumed conversation
			return p
		case a.image != nil:
			msg.Images = append(msg.Images, *a.image)
			return p
		}
		return a.text
	})
	msg.Text = m.withBangs(msg.Text)
	return msg
}

// pastedTexts is text with the pasted texts back in place of their placeholders, for a ! command
// to run what was pasted; a picture's placeholder stays.
func (m *Model) pastedTexts(text string) string {
	return placeholderRe.ReplaceAllStringFunc(text, func(p string) string {
		if a, ok := m.attached[p]; ok && a.image == nil {
			return a.text
		}
		return p
	})
}

// pasted puts pictures read for the prompt at its cursor.
func (m *Model) pasted(msg imagesMsg) {
	if msg.err != nil {
		m.local("Error: " + msg.err.Error())
	}
	m.Input.all = false // a picture goes with the text, never in place of it
	for _, im := range msg.images {
		m.Input.insert(m.attach(attachment{image: &im}) + " ")
	}
}

// imagePaths are the pictures a paste names, when it names nothing else: a terminal pastes the
// paths of the files dropped on it, quoted or with their blanks escaped.
func imagePaths(s string) []string {
	words := shellWords(s)
	for _, w := range words {
		if !imagePathRe.MatchString(w) {
			return nil
		}
		if st, err := os.Stat(w); err != nil || !st.Mode().IsRegular() {
			return nil
		}
	}
	return words
}

// shellWords splits s on blanks as a shell does: not on escaped or quoted ones. An unclosed quote
// leaves no words.
func shellWords(s string) []string {
	return splitPastePaths(s, runtime.GOOS == "windows")
}

func splitPastePaths(s string, windows bool) []string {
	var words []string
	var w []rune
	var quote rune
	esc := false
	windowsPath := false
	for i, r := range s + " " {
		if len(w) == 0 && quote == 0 && !esc && !unicode.IsSpace(r) {
			start := strings.TrimLeft(s[i:], `"'`)
			drivePath := len(start) >= 3 &&
				((start[0] >= 'A' && start[0] <= 'Z') || (start[0] >= 'a' && start[0] <= 'z')) &&
				start[1] == ':'
			windowsPath = strings.HasPrefix(start, `\\`) || drivePath && start[2] == '\\' ||
				windows && !strings.HasPrefix(start, "/") && !(drivePath && start[2] == '/')
		}
		switch {
		case esc:
			w, esc = append(w, r), false
		case r == '\\' && quote != '\'' && !windowsPath:
			esc = true
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			w = append(w, r)
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			if len(w) > 0 {
				words, w = append(words, string(w)), nil
			}
		default:
			w = append(w, r)
		}
	}
	if quote != 0 {
		return nil
	}
	return words
}

// isLongPaste reports whether a paste goes into the prompt as a placeholder.
func isLongPaste(s string) bool {
	return utf8.RuneCountInString(s) > pasteChars || strings.Count(s, "\n") > pasteLines
}

// loadImages reads pictures for the prompt.
func loadImages(paths []string) tea.Cmd {
	return func() tea.Msg {
		var msg imagesMsg
		for _, p := range paths {
			im, err := loadImage(p)
			if err != nil {
				return imagesMsg{err: err}
			}
			msg.images = append(msg.images, im)
		}
		return msg
	}
}

// pastedImage is the picture a paste is when a terminal pastes one as base64, bare or as a data
// URL; nil for anything else.
func pastedImage(s string) []byte {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:image/") && i > 0 {
		s = s[i+len(";base64,"):]
	}
	data, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(strings.Join(strings.Fields(s), ""), "="))
	if err != nil || !strings.HasPrefix(http.DetectContentType(data), "image/") {
		return nil
	}
	return data
}

// loadImageData reads a picture held in memory for the prompt.
func loadImageData(data []byte) tea.Msg {
	im, err := imageFromData(data)
	if err != nil {
		return imagesMsg{err: err}
	}
	return imagesMsg{images: []protocol.Image{im}}
}

// pasteClipboard reads the picture on the clipboard, as ctrl+v does in Claude Code: a terminal
// pastes only text.
func pasteClipboard() tea.Msg {
	data, err := clipboardImage()
	if err != nil {
		return imagesMsg{err: err}
	}
	return loadImageData(data)
}

// loadImage reads a picture for claude, scaling it down when it exceeds the upload limits.
func loadImage(path string) (protocol.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return protocol.Image{}, err
	}
	im, err := imageFromData(data)
	if err != nil {
		return protocol.Image{}, fmt.Errorf("%s: %w", path, err)
	}
	return im, nil
}

// UseHistory starts the history, before the first prompt, with the last prompts typed in dir,
// from Claude Code's history file, and adds the prompts sent from now on to it.
func (m *Model) UseHistory(file, dir string) {
	m.historyFile, m.historyDir = file, dir
	if entries, err := history.Load(file, dir, history.Limit); err == nil {
		m.history = entries
	}
	m.historyOld = len(m.history)
	m.histAt = len(m.history)
}

// pastedIn is the pasted texts of text's placeholders, by their number.
func (m *Model) pastedIn(text string) map[int]string {
	var out map[int]string
	for _, p := range placeholderRe.FindAllString(text, -1) {
		a, ok := m.attached[p]
		n, isText := pastedNumber(p)
		if !ok || a.image != nil || a.lost || !isText {
			continue
		}
		if out == nil {
			out = map[int]string{}
		}
		out[n] = a.text
	}
	return out
}

// recalled is a history entry for the prompt: a placeholder whose text is not the one this
// session has under it, as one from an earlier session, stands for its text anew. A placeholder of
// an earlier session whose text or picture the history did not keep gets a number of its own that
// stands for nothing, so it does not send what this session attached under its number.
func (m *Model) recalled(e history.Entry, earlier bool) string {
	return placeholderRe.ReplaceAllStringFunc(e.Text, func(p string) string {
		n, isText := pastedNumber(p)
		text, ok := e.Pasted[n]
		switch {
		case isText && ok:
			if a, have := m.attached[p]; have && a.image == nil && a.text == text {
				return p
			}
			return m.attach(attachment{text: text})
		case earlier:
			return m.lose(p)
		}
		return p
	})
}

// lose is placeholder p under a number of its own, standing for itself: what p stood for in an
// earlier session is lost, and whatever puts pasted texts in place of their placeholders leaves it
// as it is.
func (m *Model) lose(p string) string {
	i := strings.Index(p, "#") + 1
	j := i + strings.IndexFunc(p[i:], func(r rune) bool { return r < '0' || r > '9' })
	lost := p[:i] + strconv.Itoa(len(m.attached)+1) + p[j:]
	m.attached[lost] = attachment{text: lost, lost: true}
	return lost
}

// pastedNumber is the number of a "[Pasted text #N …]" placeholder.
func pastedNumber(p string) (int, bool) {
	rest, ok := strings.CutPrefix(p, "[Pasted text #")
	if !ok {
		return 0, false
	}
	end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' })
	n, err := strconv.Atoi(rest[:max(0, end)])
	return n, err == nil
}
