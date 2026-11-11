package ui

import (
	"os/exec"
	"slices"
	"testing"
)

// On Windows explorer opens a folder and shows a file selected, the path in quotes, where a comma
// is a part of it; the shell opens a file in its app. CLAUDIT_OPEN's command runs as it is.
func TestPathArgsWindows(t *testing.T) {
	if got := pathOpenArgs(`C:\a b`, true); !slices.Equal(got, []string{"explorer", `C:\a b`}) {
		t.Errorf("a folder opens with %q", got)
	}
	if got := pathOpenArgs(`C:\a&b.png`, false); !slices.Equal(got, []string{pathShellOpen, `C:\a&b.png`}) {
		t.Errorf("a file opens with %q", got)
	}
	reveal := pathRevealArgs(`C:\a,b\run.bat`)
	if got := pathCommand(reveal).SysProcAttr.CmdLine; got != `explorer /select,"C:\a,b\run.bat"` {
		t.Errorf("explorer reads %s", got)
	}
	if got := pathCommand([]string{"code", "-g", `C:\a.go:3`}).SysProcAttr.CmdLine; got != "" {
		t.Errorf("CLAUDIT_OPEN's command reads %s", got)
	}
	one, two := exec.Command("cmd", "/c", "exit 1").Run(), exec.Command("cmd", "/c", "exit 2").Run()
	if err := pathExit(reveal, one); err != nil {
		t.Errorf("explorer's exit 1 is %v", err)
	}
	if pathExit(reveal, two) == nil || pathExit([]string{"code", "x"}, one) == nil {
		t.Error("a failure is lost")
	}
}

// A drive's path is a path; a share's, a device's or a stream of a file is none.
func TestPathResolveWindows(t *testing.T) {
	p, root, _ := chatPathNew(t)
	for w, want := range map[string]string{`C:\x\a.go:12`: `C:\x\a.go`, `C:/x/a.go`: `C:\x\a.go`} {
		if to, ok := p.resolve(w, root); !ok || to.path != want {
			t.Errorf("%q names %q, %v", w, to.path, ok)
		}
	}
	for _, w := range []string{`\\?\C:\x`, `\\.\pipe\x`, `//h/s/x`, `C:\x\evil.exe::$DATA`, `\??\UNC\h\s\x`, `/??/UNC/h/s/x`,
		`\??\GLOBALROOT\x`, `C:foo`, `x:y`} {
		if to, ok := p.resolve(w, root); ok {
			t.Errorf("%q names %q", w, to.path)
		}
	}
}
