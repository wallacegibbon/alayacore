//go:build windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// SetDetachFlags sets OS-specific process attributes for Windows.
// On Windows we create the process without a visible console window.
func SetDetachFlags(cmd *exec.Cmd) {
	// CREATE_NO_WINDOW (0x08000000) — the process runs without creating
	// a visible console window.  This prevents a command prompt window
	// from flashing on screen each time a command is executed.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}

// OpenDevNull returns a file handle to the null device (NUL on Windows).
func OpenDevNull() (*os.File, error) {
	return os.Open("NUL")
}
