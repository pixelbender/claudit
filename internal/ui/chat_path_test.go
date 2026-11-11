package ui

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// chatPathNew is a path check of a project in a folder of its own, with a home of its own, at a
// clock the test moves; the files named are made in the project.
func chatPathNew(t *testing.T, files ...string) (p *pathCheck, root string, now *time.Time) {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	for _, f := range files {
		chatPathMake(t, root, f)
	}
	chatPathMake(t, home, "notes/x.md")
	at := modelT0
	p = &pathCheck{root: func() string { return root }, now: func() time.Time { return at }, home: home, seen: map[string]pathSeen{},
		wanted: map[string]bool{}, looking: map[string]bool{}, hung: map[string]time.Time{}, stats: &sync.Map{}}
	return p, root, &at
}

// chatPathMake makes file f under dir, its folders too; a name ending in "/" is a folder.
func chatPathMake(t *testing.T, dir, f string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(f))
	if strings.HasSuffix(f, "/") {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// chatPathDraw draws e w cells wide with the paths of p, once they are looked for: its lines
// and its links.
func chatPathDraw(p *pathCheck, e ChatEntry, w int) ([]string, []pathLink) {
	c := &chatCache{paths: p}
	lines := chatPathSettle(p, func() []string { return c.draw(0, e, w) })
	return lines, c.marks[0].links
}

// chatPathSettle draws, looks for the paths the drawing asks for as the program would, and
// draws again until it asks for none; the last drawing.
func chatPathSettle(p *pathCheck, draw func() []string) []string {
	lines := draw()
	for cmd := p.ask(); cmd != nil; cmd = p.ask() {
		p.answer(cmd().(pathsSeenMsg))
		lines = draw()
	}
	return lines
}

// chatPathShown are the paths links lead to, as the chat shows them, once each in order.
func chatPathShown(links []pathLink) []string {
	var out []string
	for _, l := range links {
		if !slices.Contains(out, l.to.shown) {
			out = append(out, l.to.shown)
		}
	}
	slices.Sort(out)
	return out
}

// chatPathUnderlined is the text of the cells of line that are underlined, a "|" between runs.
func chatPathUnderlined(line string) string {
	w := ansi.StringWidth(line)
	c := lipgloss.NewCanvas(w, 1).Compose(lipgloss.NewCompositor(lipgloss.NewLayer(line)))
	var b strings.Builder
	was := false
	for x := range w {
		cell := c.CellAt(x, 0)
		on := cell != nil && cell.Style.Underline != 0
		if on && !was && b.Len() > 0 {
			b.WriteString("|")
		}
		if on {
			b.WriteString(cell.Content)
		}
		was = on
	}
	return b.String()
}

// A reply names files by absolute paths, from home, relative to the project and with the line
// they mean; the punctuation after them is not a part of them, and a path not there is no link.
func TestChatPathWords(t *testing.T) {
	p, root, _ := chatPathNew(t, "internal/ui/chat.go", "out/")
	abs := filepath.ToSlash(filepath.Join(root, "out"))
	text := "See `~/notes/x.md`, internal/ui/chat.go:590: and (internal/ui/chat.go:12:3). Not ./gone/file.txt, nor and/or, nor https://example.com/out, nor ~root/x, / or ./ alone."
	if runtime.GOOS != "windows" { // a path of Windows starts with its drive
		text += "\nThe folder: " + abs + "."
	}
	_, links := chatPathDraw(p, ChatEntry{Kind: AssistantEntry, Text: text}, 200)
	want := []string{"internal/ui/chat.go:12:3", "internal/ui/chat.go:590", "~/notes/x.md"}
	if runtime.GOOS != "windows" {
		want = append(want, abs)
		slices.Sort(want)
	}
	if got := chatPathShown(links); !slices.Equal(got, want) {
		t.Fatalf("links to %q, want %q", got, want)
	}
	for _, l := range links {
		switch l.to.shown {
		case "internal/ui/chat.go:590":
			if l.to.path != filepath.Join(root, "internal", "ui", "chat.go") || l.to.line != ":590" {
				t.Errorf("%q leads to %q at %q", l.to.shown, l.to.path, l.to.line)
			}
		case "internal/ui/chat.go:12:3":
			if l.to.line != ":12:3" {
				t.Errorf("%q names line %q", l.to.shown, l.to.line)
			}
		case "~/notes/x.md":
			if l.to.path != filepath.Join(p.home, "notes", "x.md") || l.to.line != "" {
				t.Errorf("%q leads to %q at %q", l.to.shown, l.to.path, l.to.line)
			}
		}
	}
}

// A path is underlined in the colour it has, and only the path: a line without one stays as drawn.
func TestChatPathUnderline(t *testing.T) {
	p, _, _ := chatPathNew(t, "docs/a.md")
	e := ChatEntry{Kind: AssistantEntry, Text: "Wrote **docs/a.md**.\nNothing else."}
	lines, _ := chatPathDraw(p, e, 60)
	raw := drawEntry(e, 60)
	if got := chatPathUnderlined(lines[0]); got != "docs/a.md" {
		t.Errorf("underlined %q of %q", got, ansi.Strip(lines[0]))
	}
	if ansi.Strip(lines[0]) != ansi.Strip(raw[0]) || lines[1] != raw[1] {
		t.Errorf("the lines changed beyond the underline:\n%q\n%q", lines, raw)
	}
	c := lipgloss.NewCanvas(60, 1).Compose(lipgloss.NewCompositor(lipgloss.NewLayer(lines[0])))
	r := lipgloss.NewCanvas(60, 1).Compose(lipgloss.NewCompositor(lipgloss.NewLayer(raw[0])))
	x := strings.Index(ansi.Strip(lines[0]), "docs")
	if got, want := c.CellAt(x, 0).Style.Fg, r.CellAt(x, 0).Style.Fg; got != want {
		t.Errorf("the path is %v underlined, %v as drawn", got, want)
	}
}

// A code span takes a path with blanks whole; a ! command, its output and claudit's own lines
// have paths too, and a Bash call's line has none: a click on it shows where it ran.
func TestChatPathEntries(t *testing.T) {
	p, _, _ := chatPathNew(t, "My Folder/a b.txt", "out/log.txt")
	cases := []struct {
		e    ChatEntry
		want []string
	}{
		{ChatEntry{Kind: AssistantEntry, Text: "Open `My Folder/a b.txt` now"}, []string{"My Folder/a b.txt"}},
		{ChatEntry{Kind: AssistantEntry, Text: "Open My Folder/a b.txt now"}, nil}, // blanks split it outside a span
		{ChatEntry{Kind: CommandEntry, Bang: "cat out/log.txt", Text: "wrote out/log.txt"}, []string{"out/log.txt"}},
		{ChatEntry{Kind: CommandEntry, Text: "Exported to out/log.txt"}, []string{"out/log.txt"}},
		{ChatEntry{Kind: UserEntry, Text: "look at out/log.txt"}, []string{"out/log.txt"}},
		{ChatEntry{Kind: AssistantEntry, Bash: true, Call: "b1", Text: "Read out/log.txt"}, nil},
	}
	for _, c := range cases {
		lines, links := chatPathDraw(p, c.e, 60)
		if got := chatPathShown(links); !slices.Equal(got, c.want) {
			t.Errorf("%+v: links to %q, want %q", c.e, got, c.want)
		}
		if c.e.Bang != "" && len(links) != 2 {
			t.Errorf("a path in a ! command and its output: %d links", len(links))
		}
		if c.want == nil && slices.ContainsFunc(lines, func(l string) bool { return chatPathUnderlined(l) != "" }) {
			t.Errorf("%+v: underlined %q", c.e, lines)
		}
	}
}

// A path the chat wraps is one link on each line it takes: a click on any part opens it.
func TestChatPathWrapped(t *testing.T) {
	long := "deep/" + strings.Repeat("folder/", 6) + "file.txt"
	p, root, _ := chatPathNew(t, long, "spaced/a b c d e f g.txt")
	entries := []ChatEntry{
		{Kind: AssistantEntry, Text: "Saved " + long + " for you, and `spaced/a b c d e f g.txt` too."},
	}
	const w, h = 40, 12
	c := &chatCache{paths: p}
	out := strings.Join(chatPathSettle(p, func() []string {
		out, _, _ := renderChat(c, entries, w, h, 0)
		return strings.Split(out, "\n")
	}), "\n")
	rows := drawPlainLines(out)
	var parts []string
	for _, l := range strings.Split(out, "\n") {
		if u := chatPathUnderlined(l); u != "" {
			parts = append(parts, u)
		}
	}
	if joined := strings.Join(parts, ""); !strings.Contains(joined, long) || len(parts) < 3 {
		t.Fatalf("underlined %q of:\n%s", parts, strings.Join(rows, "\n"))
	}
	clicked := map[string]int{}
	for y, row := range rows {
		for x := range row {
			if to, ok := chatPathAt(c, entries, w, h, 0, x, y); ok {
				clicked[to.path]++
			}
		}
	}
	file, spaced := filepath.Join(root, filepath.FromSlash(long)), filepath.Join(root, "spaced", "a b c d e f g.txt")
	if clicked[file] != len(long) || clicked[spaced] != len("spaced/a b c d e f g.txt")-1 { // the blank the wrap took
		t.Errorf("cells that open each path: %v of:\n%s", clicked, strings.Join(rows, "\n"))
	}
	for y, row := range rows {
		if x := strings.Index(row, "Saved"); x >= 0 {
			if _, ok := chatPathAt(c, entries, w, h, 0, x, y); ok {
				t.Error("a click on a word beside a path opens it")
			}
		}
	}
}

// The paths are looked for off the drawing, and the entry is underlined once they are found. A
// path not there yet is looked for again a moment later, for a minute; what was found is
// forgotten once it is old.
func TestChatPathLater(t *testing.T) {
	p, root, now := chatPathNew(t)
	c := &chatCache{paths: p}
	e := ChatEntry{Kind: AssistantEntry, Text: "I will write out/a.png"}
	c.draw(0, e, 60)
	cmd := p.ask()
	if cmd == nil || len(p.seen) != 0 {
		t.Fatalf("drawing looked at the disk: %v, or asks for nothing", p.seen)
	}
	c.draw(0, e, 60)
	c.draw(1, ChatEntry{Kind: AssistantEntry, Text: "and out/z.png, as out/a.png"}, 60)
	other := p.ask() // while the first look goes on
	if other == nil {
		t.Fatal("a path is not looked for while another look goes on")
	}
	if z := other().(pathsSeenMsg); len(z.found) != 1 {
		t.Fatalf("the second look looks for %v, out/z.png alone", z.found)
	}
	c = &chatCache{paths: p}
	p.answer(cmd().(pathsSeenMsg))
	if c.draw(0, e, 60); len(c.marks[0].links) != 0 {
		t.Fatal("a path not there is a link")
	}
	chatPathMake(t, root, "out/a.png")
	if c.draw(0, e, 60); p.ask() != nil {
		t.Fatal("the path was looked for again at once")
	}
	*now = now.Add(pathTTL)
	if lines := chatPathSettle(p, func() []string { return c.draw(0, e, 60) }); len(c.marks[0].links) != 1 || chatPathUnderlined(lines[0]) != "out/a.png" {
		t.Fatalf("a path made later is not underlined: %q", lines)
	}

	late := ChatEntry{Kind: AssistantEntry, Text: "and out/b.png"}
	chatPathSettle(p, func() []string { return c.draw(1, late, 60) })
	*now = now.Add(pathRecheck)
	chatPathMake(t, root, "out/b.png")
	if c.draw(1, late, 60); p.ask() != nil || len(c.marks[1].links) != 0 {
		t.Error("a path is looked for past a minute")
	}
	chatPathSettle(p, func() []string { return c.draw(2, ChatEntry{Kind: AssistantEntry, Text: "see out/c.png"}, 60) })
	if len(p.seen) != 1 {
		t.Errorf("found %d paths, want out/c.png alone", len(p.seen))
	}
	for path, s := range p.seen {
		if now.Sub(s.at) >= pathTTL {
			t.Errorf("%s, found %v ago, is kept", path, now.Sub(s.at))
		}
	}
}

// A share on another machine is never looked at: on Windows that reaches the network and hands
// it the user's credentials. A stream of a file on Windows is no path either.
func TestPathResolveRejects(t *testing.T) {
	p, root, _ := chatPathNew(t)
	for _, w := range []string{"//h/s/x", `\\h\s\x`, "//see/x"} {
		if to, ok := p.resolve(w, root); ok {
			t.Errorf("%q names %q", w, to.path)
		}
	}
}

// pathWindowsT reads paths as Windows does, whatever the system, until the test ends.
func pathWindowsT(t *testing.T, on bool) {
	t.Helper()
	was := pathWindows
	t.Cleanup(func() { pathWindows = was })
	pathWindows = on
}

// As Windows reads paths, a device's path ("\\??\", "/??/") reaches a share as "\\" does, a drive
// without a slash after it is the folder the drive is in now, and a ":" past the drive is a
// stream of a file: none is a path.
func TestPathResolveWindowsRules(t *testing.T) {
	pathWindowsT(t, true)
	p, root, _ := chatPathNew(t)
	for _, w := range []string{`\\??\UNC\h\s\x`, "/??/UNC/h/s/x", "/??/GLOBALROOT/Device/Mup/h/s/x", `\??/UNC/h/s`,
		`/\h/s/x`, `\/h/s/x`, "C:foo/x", "x:y/z", "dir/evil.exe::$DATA", "dir/a:b.txt"} {
		if to, ok := p.resolve(w, root); ok {
			t.Errorf("%q names %q", w, to.path)
		}
	}
}

// Elsewhere a ":" is a letter of a name like any other.
func TestPathResolveColon(t *testing.T) {
	pathWindowsT(t, false)
	p, root, _ := chatPathNew(t)
	if to, ok := p.resolve("logs/2026-11-11T10:00.log", root); !ok || to.path != filepath.Join(root, "logs", "2026-11-11T10:00.log") {
		t.Errorf("a name with a colon names %q, %v", to.path, ok)
	}
}

// A look that does not end, a dead mount's, answers at its deadline for the rest; the path it
// waits for is not there, and is not looked for again.
func TestChatPathHung(t *testing.T) {
	p, _, now := chatPathNew(t, "out/a.png")
	hold := make(chan struct{})
	release := sync.OnceFunc(func() { close(hold) })
	stat, deadline := pathStat, pathDeadline
	t.Cleanup(func() { release(); pathStat, pathDeadline = stat, deadline })
	pathDeadline = 50 * time.Millisecond
	pathStat = func(path string) (os.FileInfo, error) {
		if strings.Contains(path, "mnt") {
			<-hold
		}
		return os.Stat(path)
	}
	c := &chatCache{paths: p}
	e := ChatEntry{Kind: AssistantEntry, Text: "See mnt/dead/x.md and out/a.png"}
	lines := chatPathSettle(p, func() []string { return c.draw(0, e, 60) })
	if got := chatPathShown(c.marks[0].links); !slices.Equal(got, []string{"out/a.png"}) || chatPathUnderlined(lines[0]) != "out/a.png" {
		t.Fatalf("links to %q", got)
	}
	// asksDead draws the entry anew and reports whether the look it asks for looks at the dead mount.
	asksDead := func() bool {
		(&chatCache{paths: p}).draw(0, e, 60)
		cmd := p.ask()
		if cmd == nil {
			return false
		}
		msg := cmd().(pathsSeenMsg)
		p.answer(msg)
		return slices.ContainsFunc(append(slices.Collect(maps.Keys(msg.found)), msg.hung...), func(path string) bool {
			return strings.Contains(path, "mnt")
		})
	}
	if *now = now.Add(pathTTL); asksDead() {
		t.Error("a path whose look did not end is looked for again")
	}
	if *now = now.Add(pathHungTTL); asksDead() {
		t.Error("a path is looked for again while its first look goes on")
	}
	release()
	for end := time.Now().Add(5 * time.Second); chatPathStatRuns(p) && time.Now().Before(end); {
		time.Sleep(time.Millisecond)
	}
	if !asksDead() || len(p.hung) != 0 {
		t.Errorf("a mount that answers again is not looked at again, or kept as hung: %v", p.hung)
	}
}

// chatPathStatRuns reports whether a stat of p still runs.
func chatPathStatRuns(p *pathCheck) bool {
	runs := false
	p.stats.Range(func(any, any) bool { runs = true; return false })
	return runs
}

// At most pathSlots stats run at once, a dead mount's among them; a path a look did not start by
// its deadline is not taken for hung, and is looked for again.
func TestChatPathPool(t *testing.T) {
	p, _, _ := chatPathNew(t)
	hold := make(chan struct{})
	var running, most atomic.Int32
	stat, deadline, slots := pathStat, pathDeadline, pathSlots
	t.Cleanup(func() { close(hold); pathStat, pathDeadline, pathSlots = stat, deadline, slots })
	pathDeadline, pathSlots = 30*time.Millisecond, make(chan struct{}, 4)
	pathStat = func(string) (os.FileInfo, error) {
		n := running.Add(1)
		for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
		}
		<-hold
		running.Add(-1)
		return nil, os.ErrNotExist
	}
	for i := range 20 {
		p.wanted[fmt.Sprintf("/dead/%02d", i)] = true
	}
	msg := p.ask()().(pathsSeenMsg)
	if most.Load() != 4 || len(msg.hung) != 4 || len(msg.queued) != 16 {
		t.Fatalf("%d stats at once, %d hung, %d not started", most.Load(), len(msg.hung), len(msg.queued))
	}
	p.answer(msg)
	if len(p.hung) != 4 || len(p.wanted) != 16 || len(p.looking) != 0 {
		t.Errorf("%d hung, %d to look for again, %d looked for", len(p.hung), len(p.wanted), len(p.looking))
	}
}

// A path whose stat did not end is not there for pathHungTTL, then is looked for again, unless
// that stat still runs.
func TestChatPathHungExpires(t *testing.T) {
	p, _, now := chatPathNew(t)
	p.hung["/dead"], p.hung["/stuck"] = *now, *now
	p.stats.Store("/stuck", true)
	if there, known := p.there("/dead"); there || !known || p.wanted["/dead"] {
		t.Errorf("a hung path is %v, known %v, asked for %v", there, known, p.wanted["/dead"])
	}
	*now = now.Add(pathHungTTL)
	if _, known := p.there("/dead"); known || !p.wanted["/dead"] {
		t.Error("a hung path is not looked for again a minute later")
	}
	if _, known := p.there("/stuck"); !known || p.wanted["/stuck"] {
		t.Error("a path is looked for again while its stat runs")
	}
}

// Each stat has its deadline from when it starts: a stat that waits for a slot, then takes less
// than the deadline, is no hung one.
func TestChatPathDeadlineEach(t *testing.T) {
	p, _, _ := chatPathNew(t)
	stat, deadline, slots := pathStat, pathDeadline, pathSlots
	t.Cleanup(func() { pathStat, pathDeadline, pathSlots = stat, deadline, slots })
	pathDeadline, pathSlots = 400*time.Millisecond, make(chan struct{}, 1)
	pathStat = func(string) (os.FileInfo, error) {
		time.Sleep(250 * time.Millisecond)
		return nil, os.ErrNotExist
	}
	p.wanted["/a"], p.wanted["/b"] = true, true
	if msg := p.ask()().(pathsSeenMsg); len(msg.found) != 2 || len(msg.hung) != 0 {
		t.Errorf("found %v, hung %q", msg.found, msg.hung)
	}
}
