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

const maxCommandOutput = 64 * 1024 // 64KB

type executeCommandInput struct {
	Command string `json:"command" jsonschema:"required,description=Command to execute"`
}

func NewExecuteCommandTool() llm.Tool {
	return llm.NewTool(
		"execute_command",
		shell.Detect().Description(),
	).
		WithSchema(llm.MustGenerateSchema(executeCommandInput{})).
		WithExecute(llm.TypedExecute(executeCommand)).
		Build()
}

func executeCommand(ctx context.Context, args executeCommandInput) ([]llm.ContentPart, error) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	detectedShell := shell.Detect()
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, shell.DefaultCommandTimeout)
	defer timeoutCancel()

	baseCmd := detectedShell.BuildCmd(detectedShell.ResolvedBinary(), args.Command)
	//nolint:gosec // G204: Command from user input is intentional
	cmd := exec.CommandContext(timeoutCtx, baseCmd.Path, baseCmd.Args[1:]...)
	cmd.Dir = cwd

	devNull, err := shell.OpenDevNull()
	if err != nil {
		return nil, fmt.Errorf("failed to open null device: %w", err)
	}
	defer devNull.Close()
	cmd.Stdin = devNull

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	shell.SetDetachFlags(cmd)

	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return shell.SignalProcessGroup(cmd.Process)
	}
	cmd.WaitDelay = 2 * time.Second

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start command: %w", err)
	}

	job := shell.AssignJob(cmd.Process)
	if job != nil {
		defer func() {
			shell.ClearJob()
			_ = job.Close()
		}()
	}

	execErr := cmd.Wait()

	exitCode := shell.ExitCodeFromError(execErr)
	if exitCode == -1 && cmd.ProcessState != nil {
		exitCode = shell.ExitCodeFromProcessState(cmd.ProcessState)
	}

	if ctx.Err() != nil {
		return handleCommandOutput(&stdout, &stderr, exitCode, fmt.Errorf("canceled"))
	}
	if errors.Is(execErr, context.DeadlineExceeded) || timeoutCtx.Err() != nil {
		return handleCommandOutput(&stdout, &stderr, exitCode, fmt.Errorf("timed out"))
	}

	return handleCommandOutput(&stdout, &stderr, exitCode, execErr)
}

func handleCommandOutput(stdout, stderr *bytes.Buffer, exitCode int, execErr error) ([]llm.ContentPart, error) {
	output := formatCommandOutput(stdout, stderr, exitCode)

	if len(output) > maxCommandOutput {
		return handleLargeCommandOutput(output, exitCode, execErr)
	}

	if execErr != nil {
		if output != "" {
			return []llm.ContentPart{&llm.TextPart{Text: output}}, execErr
		}
		return nil, execErr
	}

	if output == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "Command completed successfully (no output)"}}, nil
	}
	return []llm.ContentPart{&llm.TextPart{Text: output}}, nil
}

func handleLargeCommandOutput(output string, exitCode int, execErr error) ([]llm.ContentPart, error) {
	filePath, err := saveToTmpFile(output, "cmd-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large output: %w", err)
	}

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
		return []llm.ContentPart{&llm.TextPart{Text: msg}}, execErr
	}
	return []llm.ContentPart{&llm.TextPart{Text: msg}}, nil
}

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
