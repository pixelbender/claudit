package ui

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// pathShellOpen stands for the shell's own open, which no program does: ShellExecute's "open"
// opens a file in its app, no command line read on the way.
const pathShellOpen = "ShellExecute open"

// pathOpenArgs open a folder in Explorer, a file in its app.
func pathOpenArgs(path string, dir bool) []string {
	if dir {
		return []string{"explorer", path}
	}
	return []string{pathShellOpen, path}
}

// pathRevealArgs show path selected in Explorer.
func pathRevealArgs(path string) []string { return []string{"explorer", "/select," + path} }

// pathStart opens a file in its app through the shell; the rest runs as programs. The shell's
// handlers want COM on the thread that asks them: a thread of its own, held for the call, its
// COM set up and let go after; COM set up already, in another mode, does too.
func pathStart(args []string) (bool, error) {
	if len(args) != 2 || args[0] != pathShellOpen {
		return false, nil
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err {
	case nil, syscall.Errno(windows.S_FALSE): // set up now, or already in this mode: let go once
		defer windows.CoUninitialize()
	}
	file, err := windows.UTF16PtrFromString(args[1])
	if err != nil {
		return true, err
	}
	return true, windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), file, nil, nil, windows.SW_SHOWNORMAL)
}

// pathCommand runs an opener in a process group of its own. explorer reads its command line
// itself: the path goes there in quotes, where a comma is a part of it.
func pathCommand(args []string) *exec.Cmd {
	cmd := exec.Command(args[0], args[1:]...)
	attr := &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if path := args[len(args)-1]; len(args) == 2 && args[0] == "explorer" { // not CLAUDIT_OPEN's, run as it is
		if rest, ok := strings.CutPrefix(path, "/select,"); ok {
			attr.CmdLine = `explorer /select,"` + rest + `"`
		} else {
			attr.CmdLine = `explorer "` + path + `"`
		}
	}
	cmd.SysProcAttr = attr
	return cmd
}

// pathExit is how an opener ended: explorer exits with 1 when it did what it was asked.
func pathExit(args []string, err error) error {
	var exit *exec.ExitError
	if args[0] == "explorer" && errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	return err
}
