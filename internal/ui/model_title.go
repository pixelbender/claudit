package ui

import (
	"cmp"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// The terminal's title tells claudit's windows apart: a mark for what claude does, the name of
// the conversation and the project. The name is the one /rename gave it; claude -p makes up no
// title, so without one claudit names the conversation by its prompts, without asking a model.

// The most cells the title gives the conversation's name and the project, so that a tab bar
// still shows several windows.
const (
	titleName    = 40
	titleProject = 24
)

// The marks of the title: claude works, a turn that ended while the user was away went well or
// failed, claude exited.
const (
	titleBusy   = "●"
	titleDone   = "✓"
	titleFailed = "!"
	titleExited = "✗"
)

// titleKey is a tracker's issue key, such as ABC-125036 or WT-45: two letters or more, then two
// digits or more, which is not one of the names titleNotKeys has.
var titleKey = regexp.MustCompile(`\b[A-Z]{2}[A-Z0-9]*-\d{2,}\b`)

// titleNotKeys are the prefixes of what looks like an issue key and is not: UTF-8, SHA-256,
// ISO-8601, COVID-19 and the like.
var titleNotKeys = []string{"SHA", "AES", "ISO", "UTF", "RFC", "CVE", "GPT", "COVID", "RSA", "MD", "TLS", "HTTP", "IPV"}

// titleFindKey is the first issue key in text, "" for none.
func titleFindKey(text string) string {
	for _, key := range titleKey.FindAllString(text, -1) {
		if prefix, _, _ := strings.Cut(key, "-"); !slices.Contains(titleNotKeys, prefix) {
			return key
		}
	}
	return ""
}

// titleSecret is a run of 20 characters or more of base64, hex or letters and digits: a token or
// a hash, not a word.
var titleSecret = regexp.MustCompile(`^[A-Za-z0-9+/=]{20,}$`)

// titleSecretPrefixes begin the tokens of common services; a word of titleSecretMin bytes or more
// that begins so is one.
var titleSecretPrefixes = []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "sk-", "xox", "AKIA", "eyJ"}

const titleSecretMin = 10

// titleSecretWord reports whether a word of a prompt looks like a secret: a token, a hash, or a
// key=value assignment. The title is on the screen and in the tab bar, where others see it.
func titleSecretWord(w string) bool {
	if strings.IndexByte(w, '=') > 0 {
		return true
	}
	w = strings.Trim(w, titlePunct)
	if len(w) >= titleSecretMin && slices.ContainsFunc(titleSecretPrefixes, func(p string) bool { return strings.HasPrefix(w, p) }) {
		return true
	}
	return titleSecret.MatchString(w)
}

// titlePrompts is how many of the first prompts an issue key is looked for in.
const titlePrompts = 5

// titleMinWords is how many words a prompt needs to name the conversation: "are you there?" does not.
const titleMinWords = 4

// titleScan is the name its prompts gave a chat of n entries that starts at first: the chat only
// grows or is replaced, so these say that it is the same.
type titleScan struct {
	n     int
	first *ChatEntry
	name  string
}

// sessionName is the conversation's name for the title: the one it was given, else the first
// issue key in its first prompts with words of that prompt, else the first words of its first
// prompt long enough to say what it is about; "" until there is one. It is asked at every frame:
// the chat is read again only when it has changed.
func (m *Model) sessionName() string {
	if name := cmp.Or(m.naming, m.named); name != "" {
		return name
	}
	var first *ChatEntry
	if len(m.Chat) > 0 {
		first = &m.Chat[0]
	}
	if s := &m.titled; s.n != len(m.Chat) || s.first != first {
		*s = titleScan{n: len(m.Chat), first: first, name: m.promptName()}
	}
	return m.titled.name
}

// promptName is the name the conversation's prompts give it, "" for none.
func (m *Model) promptName() string {
	n, first := 0, ""
	for _, e := range m.Chat {
		if e.Kind != UserEntry || e.Answer || e.Queued || !titlePrompt(e.Text) {
			continue
		}
		if n < titlePrompts {
			if key := titleFindKey(e.Text); key != "" {
				return titleJoin(append([]string{key}, titleWords(e.Text, key)...))
			}
		}
		if words := titleWords(e.Text, ""); first == "" && len(words) >= titleMinWords {
			first = titleJoin(words)
		}
		if n++; n >= titlePrompts && first != "" {
			break
		}
	}
	return first
}

// titlePrompt reports whether text is a prompt of the user's own: not a slash command, a !
// command, or the output of ! commands a resumed conversation keeps as a prompt.
func titlePrompt(text string) bool {
	return !strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "!") && !strings.HasPrefix(text, "<bash-")
}

// titleWords are the words of a prompt that can name the conversation: not the placeholders of
// pastes and pictures, links, lone punctuation or what looks like a secret, nor the word that
// holds key.
func titleWords(text, key string) []string {
	var words []string
	for _, w := range strings.Fields(placeholderRe.ReplaceAllString(drawOneLine(text), " ")) {
		switch {
		case strings.Contains(w, "://"), key != "" && strings.Contains(w, key):
		case strings.Trim(w, titlePunct) == "", titleSecretWord(w):
		default:
			words = append(words, w)
		}
	}
	return words
}

// titlePunct is what a name does not end with.
const titlePunct = ",.:;!?-–—()[]{}\"'«»"

// titleJoin is as many of words as fit in titleName cells; a first word too long for them is cut.
func titleJoin(words []string) string {
	s := ""
	for _, w := range words {
		next := w
		if s != "" {
			next = s + " " + w
		}
		if ansi.StringWidth(next) > titleName {
			break
		}
		s = next
	}
	if s == "" && len(words) > 0 {
		return ansi.Truncate(words[0], titleName, "…")
	}
	return strings.TrimRight(s, titlePunct)
}

// project is the name of the folder claude works in, for the title; "" while not known.
func (m *Model) project() string {
	root := m.Sim.State().Root
	if root == "" {
		return ""
	}
	if name := filepath.Base(root); name != string(filepath.Separator) { // a root has no name
		return name
	}
	return ""
}

// titleText is s cleaned for a title: one line, no escapes, control or format characters (those
// of bidirectional text, zero-width ones), single spaces, at most w cells.
func titleText(s string, w int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, drawOneLine(s))
	return ansi.Truncate(strings.Join(strings.Fields(s), " "), w, "…")
}

// claude -p runs /rename itself: it saves the name in the conversation's file, where /resume
// finds it, and says which name it took. That can differ from the one asked for: another live
// session may hold it.

// renameSent notes the prompt on its way to claude: a /rename's name shows at once, until claude
// says which it took.
func (m *Model) renameSent(text string) {
	cmd, isCmd := slash(text)
	m.renameTurn = isCmd && (cmd == "rename" || cmd == "name")
	if m.renameTurn {
		m.naming = strings.TrimSpace(strings.TrimPrefix(text, "/"+cmd))
	}
}

// renamedRe reads the name out of claude's reply to /rename (claude 2.1.321): "Session renamed
// to: <name>", maybe with why it is not the one asked for, and what failed to keep it.
var renamedRe = regexp.MustCompile(`^Session (?:renamed to|is named): (.+?)(?: \("[^"]*" is held by another live session on this machine\)| \(a newer rename landed first\))?(?:\. (?:The new name may not last|Other sessions may still show the old name)[^\n]*)?$`)

// renamed takes the name claude's reply to /rename says it took.
func (m *Model) renamed(reply string) {
	if r := renamedRe.FindStringSubmatch(strings.TrimSpace(reply)); r != nil {
		m.named, m.naming = r[1], ""
	}
}

// nameReport answers /rename without a name: what the conversation is called. Claude Code would
// have a model make a name up.
func (m *Model) nameReport() string {
	switch name := m.sessionName(); {
	case cmp.Or(m.naming, m.named) != "":
		return "This conversation is named " + drawOneLine(name) + " · /rename <name> renames it"
	case name != "":
		return "This conversation has no name; the title calls it " + drawOneLine(name) + " · /rename <name> names it"
	}
	return "This conversation has no name · /rename <name> names it"
}
