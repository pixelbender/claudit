package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func init() {
	// No test opens a file for real: a model the test gives no runner of its own fails to open.
	pathRunner = func(args []string) error { return fmt.Errorf("a test ran the opener %q", args) }
}

// modelPathNew is a live model whose opener only notes what it would run, failing with fail.
func modelPathNew(t *testing.T, fail error) (*Model, *[][]string) {
	t.Helper()
	m, _ := modelMouseNew(t)
	ran := new([][]string)
	m.pathRun = func(args []string) error {
		*ran = append(*ran, args)
		return fail
	}
	return m, ran
}

// modelPathSettle draws m and looks for the paths its chat names, as the program would.
func modelPathSettle(m *Model) {
	m.View()
	for cmd := m.paths.ask(); cmd != nil; cmd = m.paths.ask() {
		m.Update(cmd())
		m.View()
	}
}

// modelPathClick clicks where text starts on the screen once the paths are found, and plays
// what the press and the release start.
func modelPathClick(t *testing.T, m *Model, text string) {
	t.Helper()
	modelPathSettle(m)
	x, y := modelMouseAt(t, m, text)
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	modelResumeRun(m, cmd)
	_, cmd = m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	modelResumeRun(m, cmd)
}

// A click on a folder opens it in the file manager, on a file in its app; on a script, an app or
// a file that may be run it only shows it in the file manager.
func TestModelPathOpens(t *testing.T) {
	m, ran := modelPathNew(t, nil)
	root := m.Sim.State().Root
	dir := chatPathMake(t, root, "out/")
	file := chatPathMake(t, root, "out/a.png")
	script := chatPathMake(t, root, "bin/run.sh")
	app := chatPathMake(t, root, "apps/Tool.app/")
	tool := chatPathMake(t, root, "bin/tool")
	want := [][]string{pathOpenArgs(pathRealT(t, dir), true), pathOpenArgs(pathRealT(t, file), false),
		pathRevealArgs(pathRealT(t, script)), pathRevealArgs(pathRealT(t, app))}
	words := []string{"out/,", "out/a.png", "bin/run.sh", "apps/Tool.app"}
	if runtime.GOOS != "windows" { // no file of Windows may be run by its mode
		if err := os.Chmod(tool, 0o755); err != nil {
			t.Fatal(err)
		}
		want, words = append(want, pathRevealArgs(pathRealT(t, tool))), append(words, "bin/tool")
	}
	m.local("Made out/, out/a.png, bin/run.sh, apps/Tool.app and bin/tool.")
	for _, w := range words {
		modelPathClick(t, m, w)
	}
	if !slices.EqualFunc(*ran, want, slices.Equal) {
		t.Errorf("ran %q, want %q", *ran, want)
	}
	if modelResumeSaid(m, "Error") {
		t.Errorf("an open failed: %+v", m.Chat)
	}
}

// CLAUDIT_OPEN opens a file instead of its app, given the line the chat names; a folder and a
// script still go to the file manager.
func TestModelPathOpener(t *testing.T) {
	t.Setenv("CLAUDIT_OPEN", "code  -g")
	m, ran := modelPathNew(t, nil)
	root := m.Sim.State().Root
	file := chatPathMake(t, root, "internal/ui/chat.go")
	script := chatPathMake(t, root, "make.py")
	dir := filepath.Dir(file)
	m.local("See internal/ui/chat.go:590, ./make.py and internal/ui/ there.")
	for _, w := range []string{"internal/ui/chat.go:590", "./make.py", "internal/ui/ "} {
		modelPathClick(t, m, w)
	}
	want := [][]string{{"code", "-g", pathRealT(t, file) + ":590"}, pathRevealArgs(pathRealT(t, script)), pathOpenArgs(pathRealT(t, dir), true)}
	if !slices.EqualFunc(*ran, want, slices.Equal) {
		t.Errorf("ran %q, want %q", *ran, want)
	}
}

// A drag over a path selects it and opens nothing; a click on a word not on disk opens nothing.
func TestModelPathDrag(t *testing.T) {
	m, ran := modelPathNew(t, nil)
	chatPathMake(t, m.Sim.State().Root, "docs/notes.md")
	m.local("Read docs/notes.md and docs/gone.md")
	modelPathSettle(m)
	x, y := modelMouseAt(t, m, "docs/notes.md")
	m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseMotionMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	m.View()
	_, cmd := m.Update(tea.MouseReleaseMsg{X: x + 5, Y: y, Button: tea.MouseLeft})
	if m.opening != nil || len(*ran) != 0 {
		t.Errorf("a drag opened %q", *ran)
	}
	if cmd == nil {
		t.Error("a drag over a path copies nothing")
	}
	modelPathClick(t, m, "docs/gone.md")
	if len(*ran) != 0 || modelResumeSaid(m, "Error") {
		t.Errorf("a path not there opened %q: %+v", *ran, m.Chat)
	}
}

// An opener that fails, or a path gone since it was drawn, says so in the chat.
func TestModelPathError(t *testing.T) {
	m, ran := modelPathNew(t, errors.New("no application knows it"))
	chatPathMake(t, m.Sim.State().Root, "out/a.xyz")
	gone := chatPathMake(t, m.Sim.State().Root, "out/b.txt")
	m.local("Made out/a.xyz and out/b.txt")
	modelPathClick(t, m, "out/a.xyz")
	if len(*ran) != 1 || !modelResumeSaid(m, "Error: open out/a.xyz: no application knows it") {
		t.Fatalf("ran %q, chat %+v", *ran, m.Chat)
	}
	m.inspection = nil // the chat grows again to show both lines
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	modelPathClick(t, m, "out/b.txt")
	if len(*ran) != 1 || !slices.ContainsFunc(m.Chat, func(e ChatEntry) bool {
		return strings.HasPrefix(e.Text, "Error: open out/b.txt: ") && e.Kind == CommandEntry
	}) {
		t.Errorf("ran %q for a path gone, chat %+v", *ran, m.Chat)
	}
}

// A path on a Bash call's line is no link: a click on it shows where the call ran, as before.
func TestModelPathBashLine(t *testing.T) {
	m, ran := modelPathNew(t, nil)
	chatPathMake(t, m.Sim.State().Root, "out/log.txt")
	m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry, Bash: true, Call: "b1", Text: "Read out/log.txt"})
	modelPathClick(t, m, "out/log.txt")
	if len(*ran) != 0 {
		t.Errorf("a click on a Bash line opened %q", *ran)
	}
}

// pathRealT is where path leads, its links followed.
func pathRealT(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

// Only documents, pictures, media and source files open in their app; any other file, a script
// or a page a browser runs among them, and a folder that runs as a program are only shown.
func TestPathArgsAllow(t *testing.T) {
	root := t.TempDir()
	open := []string{"notes.md", "a.PNG", "doc.pdf", "main.go", "report.docx", "clip.mov", "data.json", "out/"}
	shown := []string{"run.py", "page.html", "pic.svg", "app.js", "tool.pyw", "help.chm", "Makefile", "conf/.env", "conf/.hidden.md",
		"letter.rtf", "panel.jsx", "Pkg/Contents/Info.plist",
		"share.afploc", "macro.docm", "x.exe", "Script.scptd/", "Plug.bundle/", "Tool.app/", "fix.SH"}
	for _, f := range open {
		path := chatPathMake(t, root, f)
		want := pathOpenArgs(pathRealT(t, path), strings.HasSuffix(f, "/"))
		if got, err := pathArgs(pathTarget{shown: f, path: path}, ""); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v; want %q", f, got, err, want)
		}
	}
	for _, f := range shown {
		path := chatPathMake(t, root, f)
		if f == "Pkg/Contents/Info.plist" { // a folder a program is packed in, whatever its name
			path, f = filepath.Join(root, "Pkg"), "Pkg/"
		}
		want := pathRevealArgs(pathRealT(t, path))
		if got, err := pathArgs(pathTarget{shown: f, path: path}, "code -g"); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v; want %q", f, got, err, want)
		}
	}
}

// A batch file of CLAUDIT_OPEN is run by cmd, which takes "&", "%" and the like in a path for its
// own: such a path is not given to it, and the chat says why.
func TestModelPathOpenerBatch(t *testing.T) {
	t.Setenv("CLAUDIT_OPEN", "code")
	look := pathLookPath
	t.Cleanup(func() { pathLookPath = look })
	pathLookPath = func(name string) (string, error) { return "/opt/bin/" + name + ".cmd", nil }
	m, ran := modelPathNew(t, nil)
	root := m.Sim.State().Root
	chatPathMake(t, root, "src/a&calc&.txt")
	ok := chatPathMake(t, root, "src/ok.txt")
	m.local("See src/a&calc&.txt and src/ok.txt")
	modelPathClick(t, m, "src/a&calc&.txt")
	if len(*ran) != 0 || !modelResumeSaid(m, "Error: open src/a&calc&.txt: ") {
		t.Fatalf("ran %q, chat %+v", *ran, m.Chat)
	}
	m.inspection = nil // the chat grows again to show both lines
	modelPathClick(t, m, "src/ok.txt")
	if want := [][]string{{"code", pathRealT(t, ok)}}; !slices.EqualFunc(*ran, want, slices.Equal) {
		t.Errorf("ran %q, want %q", *ran, want)
	}
}

// cmd and PowerShell, as CLAUDIT_OPEN's command, read a path as a batch file does.
func TestPathArgsShells(t *testing.T) {
	look := pathLookPath
	t.Cleanup(func() { pathLookPath = look })
	file := chatPathMake(t, t.TempDir(), "a;calc;$x.txt")
	for _, bin := range []string{"C:/Windows/System32/cmd.exe", "C:/Windows/System32/WindowsPowerShell/v1.0/PowerShell.exe", "/usr/local/bin/pwsh", "C:/Program Files/PowerShell/7/pwsh.exe"} {
		pathLookPath = func(string) (string, error) { return bin, nil }
		if got, err := pathArgs(pathTarget{shown: "a;calc;$x.txt", path: file}, "opener /c"); err == nil || !strings.Contains(err.Error(), "a shell is not an opener") {
			t.Errorf("%s is given %q: %v", bin, got, err)
		}
	}
	pathLookPath = func(string) (string, error) { return "/opt/bin/code.cmd", nil } // cmd takes ";" and "$" for letters
	if got, err := pathArgs(pathTarget{shown: "a;calc;$x.txt", path: file}, "code"); err != nil {
		t.Errorf("a batch file is refused %q: %v", got, err)
	}
}

// A path in a ! command of claude's is no link of its own: a click on it puts the command in the
// prompt, as a click elsewhere on the command does; one in a block of code copies the block. A
// path in the prose of the same reply opens as before.
func TestModelPathInCommand(t *testing.T) {
	m, ran := modelPathNew(t, nil)
	root := m.Sim.State().Root
	for _, p := range []string{"bin/a.sh", "bin/b.sh", "out/c.txt", "out/d.txt"} {
		chatPathMake(t, root, p)
	}
	var copied []string
	defer func(w func(string) error) { writeClipboard = w }(writeClipboard)
	writeClipboard = func(s string) error { copied = append(copied, s); return nil }
	m.Chat = append(m.Chat, ChatEntry{Kind: AssistantEntry,
		Text: "Run:\n```\n! bash bin/a.sh\n```\nor `! sh bin/b.sh` here; see out/c.txt\n```\ncat out/d.txt\n```"})
	modelPathSettle(m)
	if !slices.ContainsFunc(m.chatLines.marks, func(mk chatMarks) bool { return len(mk.links) == 4 }) {
		t.Fatalf("the paths are not links: %+v", m.chatLines.marks)
	}

	modelPathClick(t, m, "bin/a.sh")
	if got := string(m.Input.Text); got != "!bash bin/a.sh" || len(*ran) != 0 {
		t.Errorf("a path in a ! block: prompt %q, ran %q", got, *ran)
	}
	m.Input = Input{}
	modelPathClick(t, m, "bin/b.sh")
	if got := string(m.Input.Text); got != "!sh bin/b.sh" || len(*ran) != 0 {
		t.Errorf("a path in an inline ! command: prompt %q, ran %q", got, *ran)
	}
	m.Input = Input{}
	modelPathClick(t, m, "out/d.txt")
	if len(*ran) != 0 || !slices.Equal(copied, []string{"cat out/d.txt"}) || len(m.Input.Text) != 0 {
		t.Errorf("a path in a block of code: ran %q, copied %q, prompt %q", *ran, copied, string(m.Input.Text))
	}
	modelPathClick(t, m, "out/c.txt")
	if len(*ran) != 1 || len(m.Input.Text) != 0 {
		t.Errorf("a path in the prose: ran %q, prompt %q", *ran, string(m.Input.Text))
	}
}
