package ui

import (
	"slices"
	"testing"
)

// On macOS open opens a folder in Finder and a file in its app; open -R shows it in Finder.
func TestPathArgsDarwin(t *testing.T) {
	if got := pathOpenArgs("/a/b", true); !slices.Equal(got, []string{"open", "/a/b"}) {
		t.Errorf("a folder opens with %q", got)
	}
	if got := pathOpenArgs("/a/b.png", false); !slices.Equal(got, []string{"open", "/a/b.png"}) {
		t.Errorf("a file opens with %q", got)
	}
	if got := pathRevealArgs("/a/run.sh"); !slices.Equal(got, []string{"open", "-R", "/a/run.sh"}) {
		t.Errorf("a script shows with %q", got)
	}
}
