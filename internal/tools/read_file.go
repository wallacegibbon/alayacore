package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

const maxFullReadSize = 100 * 1024 // 100KB limit for full file reads (~25K tokens, ~2500 lines)

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=The path of the file to read"`
	StartLine string `json:"start_line" jsonschema:"description=Optional: The starting line number (1-indexed)"`
	EndLine   string `json:"end_line" jsonschema:"description=Optional: The ending line number (1-indexed)"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		"Read the contents of a file. Supports optional line range using start_line and end_line parameters (1-indexed).",
	).
		WithSchema(llm.GenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

func executeReadFile(ctx context.Context, args ReadFileInput) (llm.ToolResultOutput, error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Parse line range parameters
	startLine, endLine, err := parseLineRange(args.StartLine, args.EndLine)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	// Full file read case
	if startLine == 0 && endLine == 0 {
		if info.Size() > maxFullReadSize {
			return llm.NewTextErrorResponse(fmt.Sprintf(
				"file is too large for full read (%d bytes, limit is %d). Use start_line and end_line to read a specific range.",
				info.Size(), maxFullReadSize,
			)), nil
		}
		var content []byte
		content, err = os.ReadFile(args.Path)
		if err != nil {
			return llm.NewTextErrorResponse(err.Error()), nil
		}
		return llm.NewTextResponse(string(content)), nil
	}

	// Line range case: stream from file to avoid loading entire file into memory
	file, err := os.Open(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	defer file.Close()

	lines, err := readLinesRange(ctx, file, startLine, endLine)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	return llm.NewTextResponse(strings.Join(lines, "\n")), nil
}

func parseLineRange(startLineStr, endLineStr string) (startLine, endLine int, err error) {
	if startLineStr == "" && endLineStr == "" {
		return 0, 0, nil
	}

	if startLineStr != "" {
		startLine, err = strconv.Atoi(startLineStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start_line: must be a number")
		}
		if startLine < 1 {
			return 0, 0, fmt.Errorf("start_line must be >= 1")
		}
	}

	if endLineStr != "" {
		endLine, err = strconv.Atoi(endLineStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end_line: must be a number")
		}
		if endLine < 1 {
			return 0, 0, fmt.Errorf("end_line must be >= 1")
		}
	}

	if startLine > 0 && endLine > 0 && startLine > endLine {
		return 0, 0, fmt.Errorf("start_line must be <= end_line")
	}

	return startLine, endLine, nil
}

func readLinesRange(ctx context.Context, file *os.File, startLine, endLine int) ([]string, error) {
	scanner := bufio.NewScanner(file)
	// Increase buffer size to handle long lines (default is 64KB)
	// We use 1MB which should be reasonable for most cases while still
	// preventing memory exhaustion from extremely long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	currentLine := 1

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if startLine > 0 && currentLine < startLine {
			currentLine++
			continue
		}

		if endLine > 0 && currentLine > endLine {
			break
		}

		lines = append(lines, scanner.Text())
		currentLine++
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}
