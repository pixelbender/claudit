//go:build !darwin && !windows

package ui

import (
	"slices"
	"testing"
)

// Elsewhere xdg-open opens a folder and a file; a script is only shown in its folder.
func TestPathArgsOther(t *testing.T) {
	if got := pathOpenArgs("/a/b", true); !slices.Equal(got, []string{"xdg-open", "/a/b"}) {
		t.Errorf("a folder opens with %q", got)
	}
	if got := pathOpenArgs("/a/b.png", false); !slices.Equal(got, []string{"xdg-open", "/a/b.png"}) {
		t.Errorf("a file opens with %q", got)
	}
	if got := pathRevealArgs("/a/run.sh"); !slices.Equal(got, []string{"xdg-open", "/a"}) {
		t.Errorf("a script shows with %q", got)
	}
}
