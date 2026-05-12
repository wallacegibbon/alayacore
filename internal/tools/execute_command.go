package tools

import (
	"bytes"
	"context"
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

	// Build the command using the detected shell's invocation style.
	//nolint:gosec // G204: Command from user input is intentional for execute_command tool
	cmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	cmd.Dir = cwd

	// Close stdin so commands that read stdin see EOF immediately.
	devNull, err := shell.OpenDevNull()
	if err != nil {
		return llm.NewTextErrorResponse("failed to open null device: " + err.Error()), nil
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Set OS-specific detach flags (setsid on Unix, Job Object on Windows).
	shell.SetDetachFlags(cmd)

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse("failed to start command: " + err.Error()), nil
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

	// Apply a timeout so commands that hang (e.g. interactive programs like
	// vim that start successfully but never exit without a TTY) are eventually
	// killed.  The parent context cancellation is still honored.
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer timeoutCancel()

	// Wait for command to complete, handling cancellation and timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-timeoutCtx.Done():
		if ctx.Err() != nil {
			// Parent context was canceled (user sent :cancel)
			return handleCommandCancellation(cmd, done, &stdout, &stderr), nil
		}
		// Timeout expired — kill the hung process
		return handleCommandTimeout(cmd, done, &stdout, &stderr), nil
	case execErr := <-done:
		return handleCommandCompletion(execErr, &stdout, &stderr), nil
	}
}

func handleCommandCancellation(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	exitCode := -1
	process := cmd.Process
	if process != nil {
		exitCode = shell.TerminateProcessGroup(process, done)
	}
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, nil)
	}

	if output != "" {
		return llm.NewTextErrorResponse(fmt.Sprintf("Canceled (exit %d)\n%s", exitCode, output))
	}
	return llm.NewTextErrorResponse(fmt.Sprintf("Canceled (exit %d)", exitCode))
}

func handleCommandTimeout(cmd *exec.Cmd, done chan error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	exitCode := -1
	process := cmd.Process
	if process != nil {
		exitCode = shell.TerminateProcessGroup(process, done)
	}
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, nil)
	}

	if output != "" {
		return llm.NewTextErrorResponse(fmt.Sprintf("Timed out (exit %d)\n%s", exitCode, output))
	}
	return llm.NewTextErrorResponse(fmt.Sprintf("Timed out (exit %d)", exitCode))
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
			return llm.NewTextErrorResponse(fmt.Sprintf("Exit Code: %d\n%s", exitCode, output))
		}
		if _, ok := execErr.(*exec.ExitError); ok {
			return llm.NewTextErrorResponse(output)
		}
		return llm.NewTextErrorResponse(execErr.Error())
	}

	return llm.NewTextResponse(output)
}

// handleLargeCommandOutput saves full output to a temp file and returns
// a message with the file path. This avoids re-running commands with side effects.
func handleLargeCommandOutput(output string, exitCode int, execErr error) llm.ToolResultOutput {
	// Save full output to temp file
	filePath, err := saveToTmpFile(output, "cmd-*.txt")
	if err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to save large output to temp file: %v", err))
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
		return llm.NewTextErrorResponse(msg)
	}
	return llm.NewTextResponse(msg)
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
