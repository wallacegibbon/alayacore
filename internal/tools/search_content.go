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

type SearchContentInput struct {
	Pattern    string `json:"pattern" jsonschema:"required,description=Regex pattern to search for"`
	Path       string `json:"path" jsonschema:"description=File or directory to search (default: cwd)"`
	FileType   string `json:"file_type" jsonschema:"description=File type filter (e.g. go, python, rust)"`
	Glob       string `json:"glob" jsonschema:"description=Glob pattern (e.g. *.go, *.{ts,tsx})"`
	IgnoreCase bool   `json:"ignore_case" jsonschema:"description=Enable case-insensitive search"`
	MaxLines   int    `json:"max_lines" jsonschema:"description=Max matching lines (default 100)"`
}

const defaultSearchContentMaxLines = 100

func RGAvailable() bool {
	_, err := exec.LookPath("rg")
	return err == nil
}

func NewSearchContentTool() llm.Tool {
	return llm.NewTool(
		"search_content",
		`Search file contents using ripgrep. Supports regex, file type filters, glob patterns, and case-insensitive search. Use this instead of reading files to locate code, definitions, and patterns.`,
	).
		WithSchema(llm.MustGenerateSchema(SearchContentInput{})).
		WithExecute(llm.TypedExecute(executeSearchContent)).
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

func handleSearchContentResult(execErr error, stdout, stderr *bytes.Buffer, maxLines int) ([]llm.ContentPart, error) {
	if execErr != nil {
		// rg exits with code 1 when no matches found — that's not an error for us
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 && stderr.Len() == 0 {
				return []llm.ContentPart{llm.TextPart{Text: "No matches found"}}, nil
			}
		}
		// Real error (bad regex, permission denied, etc.)
		errMsg := execErr.Error()
		if stderr.Len() > 0 {
			errMsg = stderr.String()
		}
		return nil, errors.New(errMsg)
	}

	// Use default if maxLines not specified
	if maxLines <= 0 {
		maxLines = defaultSearchContentMaxLines
	}

	output := stdout.String()
	if output == "" {
		return []llm.ContentPart{llm.TextPart{Text: "No matches found"}}, nil
	}

	// Count total lines in output
	totalLines := countLines(output)

	// If output exceeds maxLines, save full results to file and return metadata
	if totalLines > maxLines {
		return handleLargeSearchResult(output, totalLines)
	}

	return []llm.ContentPart{llm.TextPart{Text: output}}, nil
}

func handleLargeSearchResult(output string, totalLines int) ([]llm.ContentPart, error) {
	filePath, err := saveToTmpFile(output, "search-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to save large search results: %w", err)
	}

	return []llm.ContentPart{llm.TextPart{Text: fmt.Sprintf(
		"Search found %d matching lines. Results saved to: %s\nUse read_file to access specific matches.",
		totalLines, filePath,
	)}}, nil
}

func executeSearchContent(ctx context.Context, args SearchContentInput) ([]llm.ContentPart, error) {
	if args.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("ripgrep (rg) is not available on this system")
	}

	rgArgs := buildSearchContentArgs(args)

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	//nolint:gosec // G204: rg path is validated, args are from user input
	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("canceled")
		}
		return handleSearchContentResult(err, &stdout, &stderr, args.MaxLines)
	}
	return handleSearchContentResult(nil, &stdout, &stderr, args.MaxLines)
}
