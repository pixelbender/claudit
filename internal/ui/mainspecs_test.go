package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// fakeFile is what the stat of a file in a mainSpecs test says.
type fakeFile struct {
	size int64
	mod  time.Time
}

func (f fakeFile) Name() string       { return "spec.md" }
func (f fakeFile) Size() int64        { return f.size }
func (f fakeFile) Mode() fs.FileMode  { return 0o644 }
func (f fakeFile) ModTime() time.Time { return f.mod }
func (f fakeFile) IsDir() bool        { return false }
func (f fakeFile) Sys() any           { return nil }

// A main spec is read once, then again only when its size or time changes; one no frame draws
// is forgotten; a missing one is read once it appears.
func TestMainSpecs(t *testing.T) {
	files := map[string]string{"/a": "one\ntwo\n"}
	mods := map[string]time.Time{"/a": modelT0}
	reads := 0
	c := newMainSpecs()
	c.stat = func(p string) (os.FileInfo, error) {
		text, ok := files[p]
		if !ok {
			return nil, fs.ErrNotExist
		}
		return fakeFile{int64(len(text)), mods[p]}, nil
	}
	c.read = func(p string) ([]byte, error) { reads++; return []byte(files[p]), nil }
	if got := c.lines("/a"); !slices.Equal(got, []string{"one", "two"}) || reads != 1 {
		t.Fatalf("%q, %d reads", got, reads)
	}
	c.lines("/a")
	c.poll(modelT0.Add(time.Second))
	if reads != 1 {
		t.Errorf("read again unchanged: %d", reads)
	}
	files["/a"] = "one\nTWO\n" // the same size: the time tells
	mods["/a"] = modelT0.Add(time.Minute)
	c.lines("/a")
	c.poll(modelT0.Add(2 * time.Second))
	if got := c.lines("/a"); !slices.Equal(got, []string{"one", "TWO"}) || reads != 2 {
		t.Errorf("after an edit: %q, %d reads", got, reads)
	}
	c.poll(modelT0.Add(2*time.Second + time.Millisecond)) // too soon: nothing
	if got := c.lines("/b"); got != nil || reads != 2 {
		t.Errorf("a missing file: %q, %d reads", got, reads)
	}
	files["/b"] = "b"
	c.poll(modelT0.Add(3 * time.Second))
	if got := c.lines("/b"); !slices.Equal(got, []string{"b"}) || reads != 3 {
		t.Errorf("a file that appeared: %q, %d reads", got, reads)
	}
	c.poll(modelT0.Add(4 * time.Second)) // both drawn since the last poll: kept
	c.poll(modelT0.Add(5 * time.Second)) // drawn by no frame since: forgotten
	if len(c.files) != 0 {
		t.Errorf("kept %d files no frame draws", len(c.files))
	}
	delete(files, "/a")
	if c.lines("/a"); reads != 3 {
		t.Errorf("read a missing file: %d", reads)
	}
}

// modelSpecMain is the main spec modelSpecText's MODIFIED requirement changes.
const modelSpecMain = `# auth Specification

## Requirements

### Requirement: Passwords
A password MUST NOT be shorter than 8 characters.
`

// A click on a word of a MODIFIED requirement's switch shows that, kept until the next; the name
// still folds; an edit of the main spec shows within a tick.
func TestModelSpecDiff(t *testing.T) {
	m, path := modelSpec(t, 120, 40)
	main := filepath.Join(filepath.Dir(path), "..", "..", "..", "..", "specs", "auth", "spec.md")
	if err := os.MkdirAll(filepath.Dir(main), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte(modelSpecMain), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := modelScreen(m); !strings.Contains(s, "▾ Passwords   was · now · diff  · 1 change") || !strings.Contains(s, "shorter than 812 characters") {
		t.Fatalf("no diff:\n%s", s)
	}
	modelMouseClick(t, m, "was ·")
	if s := modelScreen(m); m.specModes[path]["MODIFIED Requirements\nPasswords"] != "was" || !strings.Contains(s, "shorter than 8 characters") {
		t.Fatalf("was does not show the main spec:\n%s", s)
	}
	modelMouseClick(t, m, "▾ Passwords")
	if s := modelScreen(m); !strings.Contains(s, "▸ Passwords") {
		t.Fatalf("the name does not fold:\n%s", s)
	}
	if err := os.WriteFile(main, []byte(strings.Replace(modelSpecMain, "8 characters", "10 characters", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(main, modelT0, modelT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	m.Update(tickMsg(m.Now.Add(time.Second)))
	modelMouseClick(t, m, "▸ Passwords")
	if s := modelScreen(m); !strings.Contains(s, "shorter than 10 characters") {
		t.Fatalf("the main spec's edit does not show:\n%s", s)
	}
	if s := ansi.Strip(renderHelp(120, 60)); !strings.Contains(s, "was · now · diff") {
		t.Errorf("the help does not tell the switch:\n%s", s)
	}
}
