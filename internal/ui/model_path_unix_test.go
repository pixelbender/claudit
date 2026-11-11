//go:build !windows

package ui

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// An opener that fails says why in its own words; one that does what it is asked says nothing.
func TestRunPath(t *testing.T) {
	if err := runPath([]string{"sh", "-c", "echo 'The file does not exist.' >&2; exit 1"}); err == nil || err.Error() != "The file does not exist." {
		t.Errorf("a failed opener: %v", err)
	}
	if err := runPath([]string{"sh", "-c", "exit 3"}); err == nil || err.Error() != "exit status 3" {
		t.Errorf("a failed opener that says nothing: %v", err)
	}
	if err := runPath([]string{"true"}); err != nil {
		t.Errorf("an opener that did it: %v", err)
	}
}

// What opens is where a link leads, judged as it is there: a document's link opens the document,
// a script's shows the script; a pipe named as a text, and a document that may be run, are only
// shown.
func TestPathArgsLinks(t *testing.T) {
	root := t.TempDir()
	doc, script := chatPathMake(t, root, "doc.md"), chatPathMake(t, root, "run.sh")
	for name, to := range map[string]string{"good.md": doc, "notes.md": script} {
		if err := os.Symlink(to, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	run := chatPathMake(t, root, "run.md")
	if err := os.Chmod(run, 0o755); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(root, "pipe.txt")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"good.md":  pathOpenArgs(pathRealT(t, doc), false),
		"notes.md": pathRevealArgs(pathRealT(t, script)),
		"pipe.txt": pathRevealArgs(pathRealT(t, pipe)),
		"run.md":   pathRevealArgs(pathRealT(t, run)),
	}
	for name, want := range cases {
		if got, err := pathArgs(pathTarget{shown: name, path: filepath.Join(root, name)}, ""); err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v; want %q", name, got, err, want)
		}
	}
}
