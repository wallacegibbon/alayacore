package tools

import (
	"bytes"
	"context"
	"os"
	"os/exec"

	"github.com/alayacore/alayacore/internal/llm"
)

// RipgrepInput represents the input for the ripgrep tool
type RipgrepInput struct {
	Pattern  string `json:"pattern" jsonschema:"required,description=The regex pattern to search for"`
	Path     string `json:"path" jsonschema:"description=The file or directory to search in (defaults to current working directory)"`
	FileType string `json:"file_type" jsonschema:"description=Filter files by type. Common values: go, python, rust, java, js, ts, ruby, c, cpp, html, css, json, yaml, md, sh"`
	Glob     string `json:"glob" jsonschema:"description=Glob pattern to filter files (e.g., \"*.go\", \"*.{ts,tsx}\")"`
	MaxLines string `json:"max_lines" jsonschema:"description=Maximum number of matching lines to return (defaults to 200)"`
}

const defaultRipgrepMaxLines = "200"

// RGAvailable checks if the rg binary is available on the system
func RGAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// NewRipgrepTool creates a tool for searching file contents using ripgrep (rg)
func NewRipgrepTool() llm.Tool {
	return llm.NewTool(
		"ripgrep",
		`Search file contents using ripgrep (rg). This is the fastest way to find text patterns in files.

PREFER this tool over reading files chunk by chunk when you need to:
- Find where a specific string, function, variable, or pattern is defined or used
- Locate files containing certain content
- Search across a codebase

Examples:
- Find all Go files containing "func main": pattern="func main", file_type="go"
- Find TODO comments in a directory: pattern="TODO", path="./src"
- Find all imports of a package: pattern="import.*fmt"
- Find a function definition: pattern="func MyFunction"`,
	).
		WithSchema(llm.GenerateSchema(RipgrepInput{})).
		WithExecute(llm.TypedExecute(executeRipgrep)).
		Build()
}

func buildRipgrepArgs(args RipgrepInput, maxLines string) []string {
	rgArgs := []string{
		"-n",
		"--no-heading",
		"--color=never",
		"--max-count", maxLines,
		args.Pattern,
	}

	if args.Path != "" {
		rgArgs = append(rgArgs, args.Path)
	}

	if args.FileType != "" {
		rgArgs = append(rgArgs, "--type", args.FileType)
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}

	return rgArgs
}

func handleRipgrepResult(execErr error, stdout, stderr *bytes.Buffer) llm.ToolResultOutput {
	if execErr != nil {
		// rg exits with code 1 when no matches found — that's not an error for us
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 && stderr.Len() == 0 {
				return llm.NewTextResponse("No matches found")
			}
		}
		// Real error (bad regex, permission denied, etc.)
		errMsg := execErr.Error()
		if stderr.Len() > 0 {
			errMsg = stderr.String()
		}
		return llm.NewTextErrorResponse(errMsg)
	}

	output := stdout.String()
	if output == "" {
		return llm.NewTextResponse("No matches found")
	}

	return llm.NewTextResponse(output)
}

func executeRipgrep(ctx context.Context, args RipgrepInput) (llm.ToolResultOutput, error) {
	if args.Pattern == "" {
		return llm.NewTextErrorResponse("pattern is required"), nil
	}

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return llm.NewTextErrorResponse("ripgrep (rg) is not available on this system"), nil
	}

	maxLines := args.MaxLines
	if maxLines == "" {
		maxLines = defaultRipgrepMaxLines
	}

	rgArgs := buildRipgrepArgs(args, maxLines)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	//nolint:gosec // G204: rg path is validated, args are from user input which is intentional
	cmd := exec.Command(rgPath, rgArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Wait for command to complete, handling cancellation
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Parent context was canceled — kill the rg process
		killRipgrepProcess(cmd, done)
		return llm.NewTextErrorResponse("Canceled"), nil
	case execErr := <-done:
		return handleRipgrepResult(execErr, &stdout, &stderr), nil
	}
}

func killRipgrepProcess(cmd *exec.Cmd, done chan error) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill() //nolint:errcheck // best-effort kill on cancel path
		<-done                 // wait for Process to release resources
	}
}
