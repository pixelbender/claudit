package ui

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// pathRunner runs an opener, as New gives it to a model; tests put in one that runs nothing.
var pathRunner = runPath

// pathOpenedMsg tells how the opener of a path clicked in the chat went.
type pathOpenedMsg struct {
	shown string // the path as the chat shows it
	err   error
}

// pathZone is the chat's zone under, w×h cells, with the paths its entries name on top: a press
// on one opens it as the button lets go, unless the press starts a selection. Elsewhere, a Bash
// call's line among them, and where under takes a press itself whatever is there, under takes
// the press.
func (m *Model) pathZone(under zone, entries []ChatEntry, w, h int, takes func(x, y int) bool) zone {
	scroll, z := m.ChatScroll, under
	z.click = func(x, y int) tea.Cmd {
		t, ok := chatPathAt(&m.chatLines, entries, w, h, scroll, x, y)
		switch {
		case ok && !takes(x, y):
			m.PanelFocus = false
			m.onRelease = func() { m.opening = m.openPath(t) }
		case under.click != nil:
			return under.click(x, y)
		}
		return nil
	}
	return z
}

// openPath opens t off the UI's way, as it is on disk then: a folder in the file manager, a
// document, a picture or a source file in its app, or with CLAUDIT_OPEN; anything else is only
// shown in the file manager.
func (m *Model) openPath(t pathTarget) tea.Cmd {
	opener, run := os.Getenv("CLAUDIT_OPEN"), m.pathRun
	return func() tea.Msg {
		args, err := pathArgs(t, opener)
		if err == nil {
			err = run(args)
		}
		return pathOpenedMsg{shown: t.shown, err: err}
	}
}

// pathOpened says in the chat when an opener failed.
func (m *Model) pathOpened(msg pathOpenedMsg) {
	if msg.err != nil {
		m.local("Error: open " + msg.shown + ": " + msg.err.Error())
	}
}

// pathArgs is the command line that opens t, as it is on disk now: where its links lead, judged
// as it is there, so that what opens is what was judged. opener, CLAUDIT_OPEN's command, opens a
// file instead of its app, given the path and the line t names.
func pathArgs(t pathTarget, opener string) ([]string, error) {
	real, err := filepath.EvalSymlinks(t.path)
	var fi fs.FileInfo
	switch {
	case err != nil:
	case !pathLocal(real): // a link to a share or a device
		err = errors.New("it leads off this machine")
	default:
		fi, err = os.Lstat(real)
	}
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(real))
	switch fields := strings.Fields(opener); {
	case fi.IsDir() && (slices.Contains(pathBundles, ext) || pathPacked(real)):
		return pathRevealArgs(real), nil
	case fi.IsDir():
		return pathOpenArgs(real, true), nil
	case !pathOpens(real, fi):
		return pathRevealArgs(real), nil
	case len(fields) > 0:
		if err := pathBatchSafe(fields[0], real+t.line); err != nil {
			return nil, err
		}
		return append(fields, real+t.line), nil
	}
	return pathOpenArgs(real, false), nil
}

// pathBundles are the folders the system runs as programs, or installs, when opened.
var pathBundles = []string{".app", ".workflow", ".prefpane", ".scptd", ".action", ".bundle", ".pkg", ".mpkg",
	".saver", ".plugin", ".qlgenerator", ".kext", ".appex", ".xpc"}

// pathPacked reports whether folder dir is a program packed in a folder, whatever its name: it
// has the Contents/Info.plist of one.
func pathPacked(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, "Contents", "Info.plist"))
	return err == nil
}

// pathDocs are the files that open in their app: documents, data, source, pictures, media and
// office files without macros. A script, a page or a picture a browser runs code of, a rich text
// that may hold objects that run, and a file of any other kind, which may run or lead elsewhere,
// are only shown.
var pathDocs = []string{
	".md", ".markdown", ".txt", ".log", ".csv", ".tsv", ".json", ".jsonl", ".yaml", ".yml", ".toml", ".ini",
	".conf", ".xml", ".diff", ".patch", ".rst", ".org",
	".go", ".mod", ".sum", ".rs", ".c", ".h", ".cc", ".cpp", ".hpp", ".java", ".kt", ".swift", ".ts", ".tsx",
	".css", ".scss", ".sql", ".proto", ".graphql", ".vue", ".svelte", ".lua", ".zig", ".ex", ".exs",
	".erl", ".hs", ".ml", ".scala", ".dart", ".r", ".tex",
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".bmp", ".tiff", ".ico", ".pdf",
	".mp4", ".mov", ".m4v", ".webm", ".mkv", ".mp3", ".m4a", ".wav", ".flac", ".ogg", ".opus",
	".docx", ".xlsx", ".pptx", ".odt", ".ods", ".odp",
}

// pathOpens reports whether file path, fi as it is, opens in its app: a plain file of pathDocs
// that may not be run, not a dotfile.
func pathOpens(path string, fi fs.FileInfo) bool {
	name := filepath.Base(path)
	return fi.Mode().IsRegular() && fi.Mode()&0o111 == 0 && !strings.HasPrefix(name, ".") &&
		slices.Contains(pathDocs, strings.ToLower(filepath.Ext(name)))
}

// pathLookPath finds the program a command names; tests put in one of their own.
var pathLookPath = exec.LookPath

// pathBatchUnsafe are what cmd takes for its own in the arguments of a batch file, in quotes or
// not: a path with one of them could run a command of its own.
const pathBatchUnsafe = `&|<>^%!()"`

// pathShells are the shells of Windows: each reads its arguments as commands, and a name of
// Windows may hold what PowerShell runs (";", "$"), so none opens a path.
var pathShells = []string{"cmd", "powershell", "pwsh"}

// pathBatchSafe refuses opener when it is a shell, and arg for it when the opener is a batch
// file and arg has one of pathBatchUnsafe.
func pathBatchSafe(opener, arg string) error {
	bin, err := pathLookPath(opener)
	if err != nil { // running it says so
		return nil
	}
	base := filepath.Base(filepath.ToSlash(bin))
	name := strings.ToLower(base)
	switch ext := filepath.Ext(name); {
	case slices.Contains(pathShells, strings.TrimSuffix(name, ".exe")):
		return errors.New("CLAUDIT_OPEN runs " + base + ": a shell is not an opener")
	case (ext == ".bat" || ext == ".cmd") && strings.ContainsAny(arg, pathBatchUnsafe):
		return errors.New(base + " would run a part of this path: it has one of " + pathBatchUnsafe)
	}
	return nil
}

// runPath runs an opener without the terminal and waits for it, off the UI's way; a failure
// says what the opener wrote of it.
func runPath(args []string) error {
	if done, err := pathStart(args); done {
		return err
	}
	cmd := pathCommand(args)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	err := pathExit(args, cmd.Run())
	if errors.Is(err, exec.ErrWaitDelay) { // it ended well; an app it started holds its stderr
		return nil
	}
	var exit *exec.ExitError
	if line, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); errors.As(err, &exit) && line != "" {
		return errors.New(drawOneLine(line))
	}
	return err
}
