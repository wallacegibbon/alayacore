//go:build windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// SetDetachFlags sets OS-specific process attributes for Windows.
// On Windows we create the process in a new console to isolate it.
func SetDetachFlags(cmd *exec.Cmd) {
	// CREATE_NEW_CONSOLE (0x00000010) — creates a new console window
	// instead of inheriting the parent's.  This prevents child processes
	// from scribbling on the user's terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000010, // CREATE_NEW_CONSOLE
	}
}

// OpenDevNull returns a file handle to the null device (NUL on Windows).
func OpenDevNull() (*os.File, error) {
	return os.Open("NUL")
}
