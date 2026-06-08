package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/alayacore/alayacore/internal/llm"
)

// SearchContentInput represents the input for the search_content tool
type SearchContentInput struct {
	Pattern    string `json:"pattern" jsonschema:"required,description=Regex pattern to search for"`
	Path       string `json:"path" jsonschema:"description=File or directory to search (default: cwd)"`
	FileType   string `json:"file_type" jsonschema:"description=File type filter (e.g. go, python, rust)"`
	Glob       string `json:"glob" jsonschema:"description=Glob pattern (e.g. *.go, *.{ts,tsx})"`
	IgnoreCase bool   `json:"ignore_case" jsonschema:"description=Enable case-insensitive search"`
	MaxLines   int    `json:"max_lines" jsonschema:"description=Max matching lines (default 100)"`
}

const defaultSearchContentMaxLines = 100

// RGAvailable checks if the rg binary is available on the system
func RGAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// NewSearchContentTool creates a tool for searching file contents using ripgrep (rg)
func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns.`,
	).
		WithSchema(llm.MustGenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
		SafeToConcur().
		Build()
}

func buildSearchContentArgs(args SearchContentInput) []string {
	rgArgs := []string{
		"-n",
		"--no-heading",
		"--color=never",
		"-e", args.Pattern,
	}

	if args.Path != "" {
		rgArgs = append(rgArgs, args.Path)
	}

	if args.FileType != "" {
		rgArgs = append(rgArgs, "--type", args.FileType)
	}

	if args.IgnoreCase {
		rgArgs = append(rgArgs, "-i")
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}

	return rgArgs
}

func handleSearchContentResult(execErr error, stdout, stderr *bytes.Buffer, maxLines int) llm.ToolResultOutput {
	if execErr != nil {
		// rg exits with code 1 when no matches found — that's not an error for us
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 && stderr.Len() == 0 {
				return llm.NewToolResultOutputText("No matches found")
			}
		}
		// Real error (bad regex, permission denied, etc.)
		errMsg := execErr.Error()
		if stderr.Len() > 0 {
			errMsg = stderr.String()
		}
		return llm.NewToolResultOutputFailed(errMsg)
	}

	// Use default if maxLines not specified
	if maxLines <= 0 {
		maxLines = defaultSearchContentMaxLines
	}

	output := stdout.String()
	if output == "" {
		return llm.NewToolResultOutputText("No matches found")
	}

	// Count total lines in output
	totalLines := countLines(output)

	// If output exceeds maxLines, save full results to file and return metadata
	if totalLines > maxLines {
		return handleLargeSearchResult(output, totalLines)
	}

	return llm.NewToolResultOutputText(output)
}

// handleLargeSearchResult saves full search output to a temp file and returns
// a message with the file path. This avoids partial results being misinterpreted.
func handleLargeSearchResult(output string, totalLines int) llm.ToolResultOutput {
	// Save full output to temp file
	filePath, err := saveToTmpFile(output, "search-*.txt")
	if err != nil {
		return llm.NewToolResultOutputFailed(fmt.Sprintf("failed to save large search results to temp file: %v", err))
	}

	return llm.NewToolResultOutputText(fmt.Sprintf(
		"Search found %d matching lines. Results saved to: %s\nUse read_file to access specific matches.",
		totalLines, filePath,
	))
}

func executeSearchContent(ctx context.Context, args SearchContentInput) (llm.ToolResultOutput, error) {
	if args.Pattern == "" {
		return llm.NewToolResultOutputFailed("pattern is required"), nil
	}

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return llm.NewToolResultOutputFailed("ripgrep (rg) is not available on this system"), nil
	}

	rgArgs := buildSearchContentArgs(args)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	//nolint:gosec // G204: rg path is validated, args are from user input which is intentional
	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// If the context was canceled, report cancellation
		if ctx.Err() != nil {
			return llm.NewToolResultOutputFailed("Canceled"), nil
		}
		return handleSearchContentResult(err, &stdout, &stderr, args.MaxLines), nil
	}
	return handleSearchContentResult(nil, &stdout, &stderr, args.MaxLines), nil
}
