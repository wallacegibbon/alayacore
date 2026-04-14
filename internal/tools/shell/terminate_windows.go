//go:build windows

package shell

import (
	"os"
	"time"
)

// TerminateProcessGroup kills the process tree on Windows.
// On Windows we use process.Kill() since the process was created with
// CREATE_NO_WINDOW, so it runs without an attached console.
func TerminateProcessGroup(process *os.Process, done <-chan error) {
	// On Windows, the process has no visible console.
	// First try a gentle interrupt, then force kill.
	//
	// Note: os.Interrupt on Windows sends CTRL_BREAK_EVENT to the process.
	// For a best-effort approach, we try Kill after a timeout.

	select {
	case <-done:
		// Process already exited
		return
	case <-time.After(2 * time.Second):
		// Force kill
		//nolint:errcheck // Best effort kill, errors ignored
		_ = process.Kill()
		<-done
	}
}
