//go:build !darwin && !windows

package ui

import (
	"os/exec"
	"path/filepath"
	"syscall"
)

// pathOpenArgs open a folder in the file manager, a file in its app.
func pathOpenArgs(path string, _ bool) []string { return []string{"xdg-open", path} }

// pathRevealArgs show the folder path is in: xdg-open cannot select a file in it.
func pathRevealArgs(path string) []string { return []string{"xdg-open", filepath.Dir(path)} }

// pathStart opens what only the system opens; xdg-open is a program of its own.
func pathStart([]string) (bool, error) { return false, nil }

// pathCommand runs an opener in a session of its own, without the terminal.
func pathCommand(args []string) *exec.Cmd {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// pathExit is how an opener ended, as its exit tells.
func pathExit(_ []string, err error) error { return err }
