package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
)

// PosixShellInput represents the input for the posix_shell tool
type PosixShellInput struct {
	Command string `json:"command" jsonschema:"required,description=The shell command to execute"`
}

// NewPosixShellTool creates a new posix_shell tool for executing shell commands
func NewPosixShellTool() llm.Tool {
	return llm.NewTool(
		"posix_shell",
		`Execute a shell command.

Rules:
- Use POSIX-compliant shell syntax only (no bash/zsh-specific features)
- Prefer simple, standard commands over complex pipelines
- Quote filenames with spaces or special characters
- Check command output for errors before proceeding
- Clean up temporary files when done`,
	).
		WithSchema(llm.GenerateSchema(PosixShellInput{})).
		WithExecute(llm.TypedExecute(executePosixShell)).
		Build()
}

func executePosixShell(ctx context.Context, args PosixShellInput) (llm.ToolResultOutput, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // fallback to current directory
	}

	//nolint:gosec // G204: Command from user input is intentional for shell tool
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", args.Command)
	cmd.Dir = cwd
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set process group ID so we can signal the entire process group (shell + children)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse("failed to start command: " + err.Error()), nil
	}

	// Wait for command to complete, handling cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		return handleShellCancellation(cmd, done, &stdout, &stderr)
	case execErr := <-done:
		return handleShellCompletion(execErr, &stdout, &stderr)
	}
}

func handleShellCancellation(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) (llm.ToolResultOutput, error) {
	process := cmd.Process
	if process != nil {
		terminateProcessGroup(process, done)
	}
	output := combineShellOutput(stdout, stderr)
	if output != "" {
		return llm.NewTextErrorResponse("canceled: " + output), nil
	}
	return llm.NewTextErrorResponse("canceled"), nil
}

func terminateProcessGroup(process *os.Process, done chan error) {
	// Send SIGINT (Ctrl+C) to the process group so child processes also receive it
	// Use negative PID to signal the entire process group
	pgid, pgerr := syscall.Getpgid(process.Pid)
	if pgerr == nil {
		// Signal the process group
		//nolint:errcheck // Best effort signal, errors ignored
		_ = syscall.Kill(-pgid, syscall.SIGINT)
	} else {
		// Fallback: signal just the process
		//nolint:errcheck // Best effort signal, errors ignored
		_ = process.Signal(syscall.SIGINT)
	}

	// Give the process 2 seconds to clean up
	select {
	case <-done:
		// Process exited cleanly after SIGINT
	case <-time.After(2 * time.Second):
		// Force kill if still running
		if pgerr == nil {
			//nolint:errcheck // Best effort kill, errors ignored
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			//nolint:errcheck // Best effort kill, errors ignored
			_ = process.Kill()
		}
		<-done
	}
}

func handleShellCompletion(execErr error, stdout, stderr *bytes.Buffer) (llm.ToolResultOutput, error) {
	output := combineShellOutput(stdout, stderr)

	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			return llm.NewTextErrorResponse(fmt.Sprintf("[%d] %s", exitErr.ExitCode(), output)), nil
		}
		return llm.NewTextErrorResponse(execErr.Error()), nil
	}

	return llm.NewTextResponse(output), nil
}

func combineShellOutput(stdout, stderr *bytes.Buffer) string {
	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n"
		}
		output += stderr.String()
	}
	return output
}
