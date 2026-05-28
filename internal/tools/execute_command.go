package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/alayacore/alayacore/internal/llm"
	"github.com/alayacore/alayacore/internal/tools/shell"
)

// defaultCommandTimeout is the maximum time a command may run before being
// terminated.  This prevents interactive programs (vim, nano, etc.) from
// hanging indefinitely when run without a TTY — they start successfully but
// never exit because they're waiting for terminal input that never arrives.
const defaultCommandTimeout = 2 * time.Minute

// maxCommandOutput is the threshold for saving command output to a temp file.
// Outputs larger than this return only the file path and metadata.
const maxCommandOutput = 64 * 1024 // 64KB

// executeCommandInput represents the input for the execute_command tool
type executeCommandInput struct {
	Command string `json:"command" jsonschema:"required,description=Command to execute"`
}

// NewExecuteCommandTool creates a tool for executing commands in the
// detected shell (bash/zsh/sh on Unix, PowerShell/cmd on Windows).
func NewExecuteCommandTool() llm.Tool {
	return llm.NewTool(
		"execute_command",
		shell.Detect().PromptFragment,
	).
		WithSchema(llm.MustGenerateSchema(executeCommandInput{})).
		WithExecute(llm.TypedExecute(executeCommand)).
		Build()
}

func executeCommand(ctx context.Context, args executeCommandInput) (llm.ToolResultOutput, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // fallback to current directory
	}

	detectedShell := shell.Detect()

	// Build a timeout context that derives from the parent context.
	// When the parent is canceled (user :cancel), the timeout context is
	// also canceled. When the timeout expires before the parent is
	// canceled, the timeout context reports DeadlineExceeded.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer timeoutCancel()

	// Build the command using the detected shell's invocation style.
	// We first construct a base command to get the shell-specific flags
	// (e.g. bash -c, pwsh -NoLogo -NonInteractive -Command, cmd /C),
	// then rebuild with CommandContext for automatic cancellation/timeout.
	//nolint:gosec // G204: Command from user input is intentional for execute_command tool
	baseCmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	//nolint:gosec // G204: Command from user input is intentional for execute_command tool
	cmd := exec.CommandContext(timeoutCtx, baseCmd.Path, baseCmd.Args[1:]...)
	cmd.Dir = cwd

	// Close stdin so commands that read stdin see EOF immediately.
	devNull, err := shell.OpenDevNull()
	if err != nil {
		return llm.NewToolResultOutputFailed("failed to open null device: " + err.Error()), nil
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set OS-specific detach flags (setsid on Unix, Job Object on Windows).
	shell.SetDetachFlags(cmd)

	// Custom cancellation: send SIGINT to the process group (Unix) or
	// terminate the Job Object (Windows) instead of the default SIGKILL.
	// This gives the process a chance to clean up (flush buffers, kill
	// subprocesses, etc.).
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return shell.SignalProcessGroup(cmd.Process)
	}
	// WaitDelay only matters on Unix: after Cancel sends SIGINT (graceful),
	// Go waits this long before sending SIGKILL (forced).  On Windows,
	// SignalProcessGroup goes straight to TerminateJobObject / taskkill /F
	// (forced kill, no grace period), so the process is already dead when
	// WaitDelay starts — it's effectively a no-op.
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		return llm.NewToolResultOutputFailed("failed to start command: " + err.Error()), nil
	}

	// Assign the child process to a Job Object (Windows) so that the
	// entire process tree can be killed on cancellation or timeout.
	// On Unix this is a no-op.
	job := shell.AssignJob(cmd.Process)
	if job != nil {
		defer func() {
			shell.ClearJob()
			_ = job.Close()
		}()
	}

	// Wait for command to complete, handling cancellation and timeout
	// automatically via the context. No manual goroutine needed.
	execErr := cmd.Wait()

	// Extract the exit code.  When the process exits in response to SIGINT
	// (e.g. sleep → exit 130) before WaitDelay expires, execErr is
	// *exec.ExitError with the correct code.  When the framework kills the
	// process after WaitDelay (SIGKILL), execErr may be a context error;
	// in that case we fall back to ProcessState directly.
	exitCode := shell.ExitCodeFromError(execErr)
	if exitCode == -1 && cmd.ProcessState != nil {
		exitCode = shell.ExitCodeFromProcessState(cmd.ProcessState)
	}

	// Check parent context first to detect user cancellation regardless of
	// what error cmd.Wait() returned.  When the process exits quickly in
	// response to SIGINT (e.g. sleep → exit 130), cmd.Wait() returns an
	// *exec.ExitError rather than context.Canceled, so we must check the
	// context directly.
	if ctx.Err() != nil {
		// Parent context was canceled (user sent :cancel)
		return handleCommandCancellation(&stdout, &stderr, exitCode), nil
	}
	if errors.Is(execErr, context.DeadlineExceeded) || timeoutCtx.Err() != nil {
		// Timeout expired — the process was killed by the framework
		return handleCommandTimeout(&stdout, &stderr, exitCode), nil
	}
	if execErr != nil {
		// Non-zero exit or other error
		return handleCommandCompletion(execErr, &stdout, &stderr), nil
	}

	return handleCommandCompletion(nil, &stdout, &stderr), nil
}

func handleCommandCancellation(stdout, stderr *bytes.Buffer, exitCode int) llm.ToolResultOutput {
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, fmt.Errorf("canceled"))
	}

	if output != "" {
		return llm.NewToolResultOutputFailed(fmt.Sprintf("Canceled (exit %d)\n%s", exitCode, output))
	}
	return llm.NewToolResultOutputFailed(fmt.Sprintf("Canceled (exit %d)", exitCode))
}

func handleCommandTimeout(stdout, stderr *bytes.Buffer, exitCode int) llm.ToolResultOutput {
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, fmt.Errorf("timed out"))
	}

	if output != "" {
		return llm.NewToolResultOutputFailed(fmt.Sprintf("Timed out (exit %d)\n%s", exitCode, output))
	}
	return llm.NewToolResultOutputFailed(fmt.Sprintf("Timed out (exit %d)", exitCode))
}

func handleCommandCompletion(execErr error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	exitCode := shell.ExitCodeFromError(execErr)

	output := formatCommandOutput(stdout, stderr, exitCode)

	// For large outputs, save to file and return path + metadata
	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, execErr)
	}

	// Small output: return as-is
	if execErr != nil {
		if exitCode != 0 {
			return llm.NewToolResultOutputFailed(fmt.Sprintf("Exit Code: %d\n%s", exitCode, output))
		}
		if _, ok := execErr.(*exec.ExitError); ok {
			return llm.NewToolResultOutputFailed(output)
		}
		return llm.NewToolResultOutputFailed(execErr.Error())
	}

	return llm.NewToolResultOutputText(output)
}

// handleLargeCommandOutput saves full output to a temp file and returns
// a message with the file path. This avoids re-running commands with side effects.
func handleLargeCommandOutput(output string, exitCode int, execErr error) llm.ToolResultOutput {
	// Save full output to temp file
	filePath, err := saveToTmpFile(output, "cmd-*.txt")
	if err != nil {
		return llm.NewToolResultOutputFailed(fmt.Sprintf("failed to save large output to temp file: %v", err))
	}

	// Count total lines
	totalLines := countLines(output)
	totalKB := float64(len(output)) / 1024

	var msg string
	if execErr != nil && exitCode > 0 {
		msg = fmt.Sprintf("Exit Code: %d\n", exitCode)
	}

	msg += fmt.Sprintf(
		"Output (%d lines, %.1fKB) saved to: %s\nUse read_file to access specific sections.",
		totalLines, totalKB, filePath,
	)

	if execErr != nil {
		return llm.NewToolResultOutputFailed(msg)
	}
	return llm.NewToolResultOutputText(msg)
}

// countLines counts the number of lines in a string.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func formatCommandOutput(stdout, stderr *bytes.Buffer, exitCode int) string {
	if exitCode == 0 && stderr.Len() == 0 {
		return stdout.String()
	}

	var output string
	if stdout.Len() > 0 {
		output = "STDOUT:\n" + stdout.String() + "\n"
	}
	if stderr.Len() > 0 {
		output += "STDERR:\n" + stderr.String()
	}
	return output
}
