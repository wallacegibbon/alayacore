//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Job is a no-op type on Unix, returned as nil by AssignJob.
type Job struct{}

// Close is a no-op on Unix.
func (j *Job) Close() error { return nil }

// TerminateProcessGroup sends SIGINT to the process group, waits briefly,
// then sends SIGKILL if the group hasn't exited.
// pid must be the session leader PID (same as process group ID when
// created with Setsid).
// Returns the exit code from the killed process (128+signal on Go ≥1.12).
func TerminateProcessGroup(process *os.Process, done <-chan error) int {
	pid := process.Pid

	//nolint:errcheck // Best effort signal, errors ignored
	_ = syscall.Kill(-pid, syscall.SIGINT)

	// Give the process group 2 seconds to clean up
	var waitErr error
	select {
	case waitErr = <-done:
		// Process exited cleanly after SIGINT
	case <-time.After(2 * time.Second):
		// Force kill if still running
		//nolint:errcheck // Best effort kill, errors ignored
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		waitErr = <-done
	}

	return ExitCodeFromError(waitErr)
}

// ExitCodeFromError extracts the exit code from a cmd.Wait error.
// Returns 0 for nil, 128+signal for processes killed by a signal,
// the exit status for normal non-zero exits, or -1 for
// unrecognized errors.
func ExitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if ws, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus); ok {
			if ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return ws.ExitStatus()
		}
	}
	return -1
}
