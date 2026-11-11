package ui

import (
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// The files and folders the chat names, where they are on disk, are underlined: a click opens
// them (model_path.go).

// pathTTL is how long what a path was found to be holds. An entry that names a path not there
// looks for it again after it, for pathRecheck from when it was first drawn: a file claude names
// before it writes it underlines a moment later.
const (
	pathTTL     = 3 * time.Second
	pathRecheck = time.Minute
)

// pathCheck finds the paths the entries of the chat name that are there on disk. Drawing only
// asks for a path; ask looks for those asked for off the UI's way, a dead mount slow as it may
// be, and answer brings what it found, for the entries that asked to draw again.
type pathCheck struct {
	root    func() string    // the project, where a relative path starts; "" for none
	now     func() time.Time // the time what was found holds from
	home    string           // where "~" leads; "" for nowhere
	seen    map[string]pathSeen
	wanted  map[string]bool      // the paths drawing asked for since the last look
	looking map[string]bool      // the paths a look goes on for
	hung    map[string]time.Time // the paths a look did not end for by its deadline, and when: not there for pathHungTTL
	stats   *sync.Map            // the paths a stat runs for, its look answered or not: none is started again meanwhile
	gen     int                  // looks answered: an entry that waited for one draws again
}

// pathsSeenMsg is what a look found: whether each path is there; the paths whose stat did not
// end by its deadline, and those it did not start, no stat free by then.
type pathsSeenMsg struct {
	found  map[string]bool
	hung   []string
	queued []string
}

var (
	// pathWindows is whether paths are read as Windows reads them; tests of other systems put it on.
	pathWindows = runtime.GOOS == "windows"
	// pathStat looks at a path; tests put in one that does not end.
	pathStat = os.Stat
	// pathDeadline is how long a look waits for a stat it started: a dead mount's never ends.
	pathDeadline = 2 * time.Second
	// pathSlots are the stats that may run at once, of every look: one that does not end holds
	// its slot, and its thread, until it does.
	pathSlots = make(chan struct{}, 8)
)

// pathHungTTL is how long a path whose stat did not end by its deadline is not there; it is
// looked for again after it, once that stat ends.
const pathHungTTL = time.Minute

// ask looks for the paths asked for, as many at once as pathSlots let, while other looks go on.
// A look answers once each stat it started ended or passed its deadline; the paths it could not
// start a stat for by then go back to be asked for.
func (p *pathCheck) ask() tea.Cmd {
	if p == nil || len(p.wanted) == 0 {
		return nil
	}
	paths := slices.Collect(maps.Keys(p.wanted))
	clear(p.wanted)
	for _, path := range paths {
		p.looking[path] = true
	}
	stat, deadline, slots, stats := pathStat, pathDeadline, pathSlots, p.stats
	return func() tea.Msg {
		type look struct {
			path  string
			there bool
		}
		looks := make(chan look, len(paths)) // a stat that ends past its deadline leaves its answer here
		running := map[string]time.Time{}    // the stats of this look that run, by when they started
		start := func(path string) {
			running[path] = time.Now()
			stats.Store(path, true)
			go func() {
				_, err := stat(path)
				stats.Delete(path)
				<-slots
				looks <- look{path, err == nil}
			}()
		}
		msg := pathsSeenMsg{found: map[string]bool{}}
		for next := 0; next < len(paths) || len(running) > 0; {
			wait := deadline // for a slot, when none of this look's stats runs
			for at := range maps.Values(running) {
				wait = min(wait, time.Until(at.Add(deadline)))
			}
			timeout := time.NewTimer(max(wait, 0))
			var free chan<- struct{} // a slot to take, while a path waits for one
			if next < len(paths) {
				free = slots
			}
			select {
			case free <- struct{}{}:
				start(paths[next])
				next++
			case l := <-looks:
				if _, ok := running[l.path]; ok {
					delete(running, l.path)
					msg.found[l.path] = l.there
				}
			case <-timeout.C:
				if len(running) == 0 { // no slot freed: the rest wait for another look
					msg.queued = paths[next:]
					next = len(paths)
				}
				for path, at := range running {
					if time.Since(at) >= deadline {
						delete(running, path)
						msg.hung = append(msg.hung, path)
					}
				}
			}
			timeout.Stop()
		}
		return msg
	}
}

// answer keeps what a look found, and forgets what was found too long ago to hold: the words of
// a reply as it streams in among them.
func (p *pathCheck) answer(msg pathsSeenMsg) {
	now := p.now()
	maps.DeleteFunc(p.seen, func(_ string, s pathSeen) bool { return now.Sub(s.at) >= pathTTL })
	for path, there := range msg.found {
		p.seen[path] = pathSeen{there: there, at: now}
		delete(p.looking, path)
	}
	maps.DeleteFunc(p.hung, func(_ string, at time.Time) bool { return now.Sub(at) >= pathHungTTL })
	for _, path := range msg.hung {
		p.hung[path] = now
		delete(p.looking, path)
	}
	for _, path := range msg.queued {
		p.wanted[path] = true
		delete(p.looking, path)
	}
	p.gen++
}

// pathSeen is what a path was found to be: there or not, when.
type pathSeen struct {
	there bool
	at    time.Time
}

func newPathCheck(root func() string) *pathCheck {
	home, _ := os.UserHomeDir()
	return &pathCheck{root: root, now: time.Now, home: home, seen: map[string]pathSeen{},
		wanted: map[string]bool{}, looking: map[string]bool{}, hung: map[string]time.Time{}, stats: &sync.Map{}}
}

// pathTarget is a path the chat names: as it shows, where it is on disk, and the line it names,
// ":12" or ":12:5" ("" for none).
type pathTarget struct {
	shown, path, line string
}

// pathLink is where a path shows in an entry's drawn lines: cells x0 to x1 of line y. A path the
// chat wraps has a link on each line it takes.
type pathLink struct {
	y, x0, x1 int
	to        pathTarget
}

// chatMarks is what the paths of an entry make of its drawn lines.
type chatMarks struct {
	raw     []string // the lines as drawEntry drew them
	links   []pathLink
	done    bool      // the paths were looked for
	root    string    // in this project
	first   time.Time // when they were first looked for
	at      time.Time // when last
	gen     int       // as the looks answered by then found them
	waiting bool      // a path was not looked for yet: the entry draws again at the next answer
	missing bool      // a word that looks like a path named nothing: looked for again after pathTTL
}

// mark underlines the paths entry e names in its drawn lines, as mk keeps them; the lines
// unchanged once marked, until the project changes, a look answers what the entry waits for, or
// a path not there is looked for again.
func (p *pathCheck) mark(e ChatEntry, mk *chatMarks, lines []string) []string {
	if p == nil {
		return lines
	}
	root, now := p.root(), p.now()
	again := mk.waiting && mk.gen != p.gen || mk.missing && now.Sub(mk.at) >= pathTTL && now.Sub(mk.first) < pathRecheck
	if mk.done && mk.root == root && !again {
		return lines
	}
	if !mk.done || mk.root != root {
		mk.first = now
	}
	mk.done, mk.root, mk.at, mk.gen, mk.waiting, mk.missing, mk.links = true, root, now, p.gen, false, false, nil
	var found []pathTarget
	seen := map[string]bool{}
	for _, text := range pathTexts(e) {
		for _, w := range pathWords(text) {
			if w == "" || seen[w] {
				continue
			}
			seen[w] = true
			t, ok := p.resolve(w, root)
			if !ok {
				continue
			}
			switch there, known := p.there(t.path); {
			case !known:
				mk.waiting = true
			case !there:
				mk.missing = true
			default:
				found = append(found, t)
			}
		}
	}
	if len(found) == 0 {
		return mk.raw
	}
	mk.links = pathFind(mk.raw, found)
	return pathUnderline(mk.raw, mk.links)
}

// pathTexts are the texts of e that may name paths. A Bash call's line has none: a click on it
// shows where the call ran.
func pathTexts(e ChatEntry) []string {
	switch {
	case e.Kind == CaretEntry || e.Bash:
		return nil
	case e.Bang != "":
		return []string{e.Bang, e.Text}
	}
	return []string{e.Text}
}

// pathSpanRe is a code span: a path in one may have blanks.
var pathSpanRe = regexp.MustCompile("`([^`]+)`")

// pathWords are the words of text that may be paths, as the chat shows them: split at blanks,
// quotes and brackets, after the last "=" ("--out=/tmp/x"), without the punctuation around them;
// and each code span with blanks in it, whole.
func pathWords(text string) []string {
	var words []string
	for _, line := range strings.Split(drawClean(text), "\n") {
		for _, m := range pathSpanRe.FindAllStringSubmatch(line, -1) {
			if span := strings.TrimSpace(m[1]); strings.Contains(span, " ") {
				words = append(words, pathTrim(span))
			}
		}
		for _, w := range strings.FieldsFunc(line, pathApart) {
			if i := strings.LastIndexByte(w, '='); i >= 0 {
				w = w[i+1:]
			}
			words = append(words, pathTrim(w))
		}
	}
	return words
}

// pathApart reports whether r sets words apart: a blank, a quote, a bracket, a backtick.
func pathApart(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("`\"'()[]{}<>«»„“”|", r)
}

// pathEnd is the punctuation a sentence puts after a path, not a part of it.
const pathEnd = ".,;:!?*"

// pathTrim drops the punctuation around a word: the "*"s of bold, a full stop after it.
func pathTrim(w string) string {
	return strings.TrimRight(strings.TrimLeft(w, "*"), pathEnd)
}

// pathLineRe is a word that names a line of a file, and a column: "chat.go:590", "chat.go:590:3".
var pathLineRe = regexp.MustCompile(`^(.+?)((?::\d+){1,2})$`)

// resolve is the path word names, root for where a relative one starts: absolute, from home
// ("~/"), on a drive of Windows ("C:\x", "C:/x"), or relative with a "/" in it. A URL, a word of
// slashes and dots alone ("/", "./"), and a word without a "/" name none; nor does a share on
// another machine ("//host/share"), which Windows would reach over the network to look at,
// handing it the user's credentials, nor a ":" past the drive, a stream of a file on Windows.
func (p *pathCheck) resolve(word, root string) (pathTarget, bool) {
	t := pathTarget{shown: word}
	name := word
	if m := pathLineRe.FindStringSubmatch(word); m != nil {
		name, t.line = m[1], m[2]
	}
	vol := filepath.VolumeName(name)
	switch {
	case strings.HasPrefix(name, "//") || strings.HasPrefix(name, `\\`) || !pathLocal(name):
		return t, false
	case vol == "" && !strings.Contains(name, "/") || strings.Contains(name, "://") || strings.Trim(name, "/.~") == "":
		return t, false
	}
	switch {
	case vol != "":
		t.path = filepath.FromSlash(name)
	case strings.HasPrefix(name, "~/"):
		if p.home == "" {
			return t, false
		}
		t.path = filepath.Join(p.home, filepath.FromSlash(name[2:]))
	case strings.HasPrefix(name, "~"): // another user's home
		return t, false
	case strings.HasPrefix(name, "/"):
		t.path = filepath.FromSlash(name)
	default:
		if root == "" {
			return t, false
		}
		t.path = filepath.Join(root, filepath.FromSlash(name))
	}
	abs, err := filepath.Abs(t.path) // an opener never takes it for an option
	if err != nil || !pathLocal(abs) {
		return t, false
	}
	t.path = abs
	return t, true
}

// pathLocal reports whether path, as Windows reads it, is on a drive of this machine: no volume,
// or a drive's letter with a slash after it. A share ("\\host\share"), a device's path, which may
// reach a share too ("\\?\", "\\.\", "\\??\", either slash), a drive without a slash, which is
// the folder the drive is in now ("C:x"), and a ":" past the drive, a stream of a file, are not.
// Elsewhere every path is.
func pathLocal(path string) bool {
	if !pathWindows {
		return true
	}
	slash := func(i int) bool { return i < len(path) && (path[i] == '\\' || path[i] == '/') }
	if slash(0) && (slash(1) || strings.HasPrefix(path[1:], "??")) {
		return false
	}
	vol := filepath.VolumeName(path)
	if vol != "" && (len(vol) != 2 || vol[1] != ':' || !slash(2)) {
		return false
	}
	return !strings.Contains(path[len(vol):], ":")
}

// there is whether path is on disk, as found within pathTTL; not known when it was not looked
// for since: drawing asks for it, unless a look goes on for it, and the look finds it. A path a
// look did not end for is not there.
func (p *pathCheck) there(path string) (there, known bool) {
	s, ok := p.seen[path]
	hung, stuck := p.hung[path]
	_, runs := p.stats.Load(path)
	switch {
	case ok && p.now().Sub(s.at) < pathTTL:
		return s.there, true
	case p.looking[path]:
	case stuck && p.now().Sub(hung) < pathHungTTL || runs:
		return false, true
	default:
		p.wanted[path] = true
	}
	return false, false
}

// pathCell is a character of an entry's drawn lines, at cell x of line y, w cells wide; "\n"
// ends a line.
type pathCell struct {
	s       string
	y, x, w int
}

// pathCells are the characters of drawn lines, styles left out.
func pathCells(lines []string) []pathCell {
	var cells []pathCell
	for y, l := range lines {
		x := 0
		var state byte
		for len(l) > 0 {
			seq, w, n, st := ansi.DecodeSequence(l, state, nil)
			state, l = st, l[max(n, 1):]
			if w > 0 {
				cells = append(cells, pathCell{s: seq, y: y, x: x, w: w})
				x += w
			}
		}
		cells = append(cells, pathCell{s: "\n", y: y, x: x})
	}
	return cells
}

// pathFind finds where the paths found show in drawn lines, each as a word of its own, across
// the lines it wraps to. A longer path goes first: a shorter one is not found inside it.
func pathFind(lines []string, found []pathTarget) []pathLink {
	cells := pathCells(lines)
	taken := make([]bool, len(cells))
	slices.SortStableFunc(found, func(a, b pathTarget) int { return len(b.shown) - len(a.shown) })
	var links []pathLink
	for _, t := range found {
		word := pathGraphemes(t.shown)
		for at := 0; at < len(cells); at++ {
			if cells[at].s != word[0] || !pathBefore(cells, at) {
				continue
			}
			end, used := pathMatch(cells, at, word)
			if end < 0 || !pathAfter(cells, end) || slices.ContainsFunc(used, func(k int) bool { return taken[k] }) {
				continue
			}
			for _, k := range used {
				taken[k] = true
				c := cells[k]
				if n := len(links); n > 0 && links[n-1].to == t && links[n-1].y == c.y && links[n-1].x1 == c.x {
					links[n-1].x1 = c.x + c.w
				} else {
					links = append(links, pathLink{y: c.y, x0: c.x, x1: c.x + c.w, to: t})
				}
			}
			at = end - 1
		}
	}
	return links
}

// pathMatch is where in cells word ends when it starts at cells[at], and the cells it takes; -1
// when it is not there. At a wrap the word goes on past the indent of the next line, and the
// blank the wrap took counts as there.
func pathMatch(cells []pathCell, at int, word []string) (end int, used []int) {
	k := at
	for j := 0; j < len(word); {
		switch {
		case k < len(cells) && cells[k].s == word[j]:
			used = append(used, k)
			k, j = k+1, j+1
		case k+1 < len(cells) && cells[k].s == "\n" && j > 0:
			for j < len(word) && word[j] == " " {
				j++
			}
			for k++; k < len(cells) && (cells[k].s == " " || cells[k].s == "│" || cells[k].s == "▏"); k++ {
			}
		default:
			return -1, nil
		}
	}
	return k, used
}

// pathBefore reports whether a word may start at cells[at]: not in the middle of another.
func pathBefore(cells []pathCell, at int) bool {
	if at == 0 {
		return true
	}
	prev := cells[at-1].s
	r, _ := utf8.DecodeRuneInString(prev)
	return prev == "\n" || prev == "=" || prev == "*" || pathApart(r)
}

// pathAfter reports whether a word may end before cells[end]: what follows it on its line, up to
// a blank, is punctuation.
func pathAfter(cells []pathCell, end int) bool {
	for k := end; k < len(cells) && cells[k].s != "\n"; k++ {
		r, _ := utf8.DecodeRuneInString(cells[k].s)
		if pathApart(r) {
			return true
		}
		if !strings.Contains(pathEnd, cells[k].s) {
			return false
		}
	}
	return true
}

func pathGraphemes(s string) []string {
	var out []string
	for g := uniseg.NewGraphemes(s); g.Next(); {
		out = append(out, g.Str())
	}
	return out
}

// pathUnderline underlines the cells of lines that links cover.
func pathUnderline(lines []string, links []pathLink) []string {
	out := slices.Clone(lines)
	for y := range out {
		var spans [][2]int
		for _, l := range links {
			if l.y == y {
				spans = append(spans, [2]int{l.x0, l.x1})
			}
		}
		if len(spans) > 0 {
			out[y] = drawUnderline(out[y], spans)
		}
	}
	return out
}

// drawUnderline underlines the cells of styled line s within spans, from x0 to x1, in the colours
// they have: a style set on the way, which may reset the rest, keeps the underline on.
func drawUnderline(s string, spans [][2]int) string {
	inside := func(x int) bool {
		return slices.ContainsFunc(spans, func(sp [2]int) bool { return x >= sp[0] && x < sp[1] })
	}
	var b strings.Builder
	var state byte
	x, on := 0, false
	for len(s) > 0 {
		seq, w, n, st := ansi.DecodeSequence(s, state, nil)
		state, s = st, s[max(n, 1):]
		if w == 0 {
			b.WriteString(seq)
			if on && ansi.HasCsiPrefix(seq) && strings.HasSuffix(seq, "m") {
				b.WriteString(ansi.SGR(ansi.AttrUnderline))
			}
			continue
		}
		switch in := inside(x); {
		case in && !on:
			b.WriteString(ansi.SGR(ansi.AttrUnderline))
		case !in && on:
			b.WriteString(ansi.SGR(ansi.AttrNoUnderline))
		}
		on = inside(x)
		b.WriteString(seq)
		x += w
	}
	if on {
		b.WriteString(ansi.SGR(ansi.AttrNoUnderline))
	}
	return b.String()
}

// chatPathAt is the path at cell (x, y) of the chat renderChat draws width×height cells, scrolled
// up by scroll lines; false for none.
func chatPathAt(cache *chatCache, entries []ChatEntry, width, height, scroll, x, y int) (pathTarget, bool) {
	tw, aw := drawChatWidths(width)
	lines, _, owners := drawChatLines(cache, entries, tw, aw)
	// The rows as renderChat lays them out: the tail of the lines, scroll lines up from the bottom.
	scroll = min(max(scroll, 0), max(0, len(lines)-height))
	end := len(lines) - scroll
	from := max(0, end-height)
	top := height - (end - from)
	if y < top || y >= height {
		return pathTarget{}, false
	}
	k := from + y - top
	i := owners[k]
	if i < 0 || i >= len(cache.marks) {
		return pathTarget{}, false
	}
	first := k // the entry's first line
	for first > 0 && owners[first-1] == i {
		first--
	}
	for _, l := range cache.marks[i].links {
		if l.y == k-first && x-2 >= l.x0 && x-2 < l.x1 { // past the chat's margin
			return l.to, true
		}
	}
	return pathTarget{}, false
}
