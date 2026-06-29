package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/alayacore/alayacore/internal/llm"
)

// SearchContentInput represents the input for the search_content tool.
type SearchContentInput struct {
	Pattern    string `json:"pattern" jsonschema:"required" jsonschema_desc:"Regex pattern to search for"`
	Path       string `json:"path" jsonschema_desc:"File or directory to search (default: cwd)"`
	FileType   string `json:"file_type" jsonschema_desc:"File type filter (e.g. go, python, rust)"`
	Glob       string `json:"glob" jsonschema_desc:"Glob pattern (e.g. *.go, *.{ts,tsx})"`
	IgnoreCase bool   `json:"ignore_case" jsonschema_desc:"Enable case-insensitive search"`
	MaxLines   int    `json:"max_lines" jsonschema_desc:"Max matching lines (default 100)"`
}

const defaultSearchContentMaxLines = 100

// RGAvailable checks whether ripgrep (rg) is available on this system.
// Used at startup to conditionally register the search_content tool.
// If rg is not found, the tool is omitted entirely, so executeSearchContent
// can rely on rg being present without a redundant LookPath check.
func RGAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

// NewSearchContentTool creates the search_content tool for use by the agent.
func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns.`,
	).
		WithSchema(llm.MustGenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
		Build()
}

// searchResult captures the exit status and output streams of a ripgrep search.
// Using a structured result instead of nullable error + buffer parameters
// makes the execution/formatting separation explicit and testable.
type searchResult struct {
	stdout   string
	stderr   string
	exitCode int
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

// runSearch executes ripgrep and returns a structured result.
// rg availability is guaranteed by RGAvailable() at tool registration time,
// so no exec.LookPath check is needed here.
func runSearch(ctx context.Context, args SearchContentInput) (searchResult, error) {
	if args.Pattern == "" {
		return searchResult{}, fmt.Errorf("pattern is required")
	}

	rgArgs := buildSearchContentArgs(args)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	//nolint:gosec // G204: args are from user input, rg is a trusted binary
	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	execErr := cmd.Run()

	result := searchResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
	}

	if execErr != nil {
		if ctx.Err() != nil {
			return searchResult{}, fmt.Errorf("canceled")
		}

		// rg uses exit codes to signal status: 0 = matches found,
		// 1 = no matches, 2+ = error (bad regex, permission denied, etc.).
		var exitErr *exec.ExitError
		if errors.As(execErr, &exitErr) {
			result.exitCode = exitErr.ExitCode()
			return result, nil
		}

		// Non-exit error (e.g. binary doesn't exist) — shouldn't happen
		// since RGAvailable() verified rg at startup, but handle gracefully.
		return searchResult{}, execErr
	}

	return result, nil
}

// formatSearchResult converts a structured search result into ContentParts.
// It is separated from runSearch so that each concern (execution vs. formatting)
// can be tested and reasoned about independently.
func formatSearchResult(result searchResult, maxLines int) ([]llm.ContentPart, error) {
	// rg exits with code 1 when no matches found — that's not an error for us.
	if result.exitCode == 1 && result.stderr == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	// Real error (bad regex, permission denied, etc.)
	if result.exitCode != 0 {
		errMsg := result.stderr
		if errMsg == "" {
			errMsg = fmt.Sprintf("ripgrep exited with code %d", result.exitCode)
		}
		return nil, errors.New(errMsg)
	}

	// Success path
	output := result.stdout
	if output == "" {
		return []llm.ContentPart{&llm.TextPart{Text: "No matches found"}}, nil
	}

	// Use default if maxLines not specified
	if maxLines <= 0 {
		maxLines = defaultSearchContentMaxLines
	}

	// Count total lines in output
	totalLines := countLines(output)

	// If output exceeds maxLines, save full results to file and return metadata
	if totalLines > maxLines {
		return handleLargeSearchResult(output, totalLines)
	}

	return []llm.ContentPart{&llm.TextPart{Text: output}}, nil
}

// handleLargeSearchResult saves large search output to a temp file and
// returns a summary message with the file path.
func handleLargeSearchResult(output string, totalLines int) ([]llm.ContentPart, error) {
	filePath, err := saveToTmpFile(output, "search-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large search results: %w", err)
	}

	return []llm.ContentPart{&llm.TextPart{Text: fmt.Sprintf(
		"Search found %d matching lines. Results saved to: %s\nUse read_file to access specific matches.",
		totalLines, filePath,
	)}}, nil
}

// executeSearchContent is the typed entry point for the search_content tool.
// It runs ripgrep and formats the results.
func executeSearchContent(ctx context.Context, args SearchContentInput) ([]llm.ContentPart, error) {
	result, err := runSearch(ctx, args)
	if err != nil {
		return nil, err
	}
	return formatSearchResult(result, args.MaxLines)
}
