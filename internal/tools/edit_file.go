package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/alayacore/alayacore/internal/llm"
)

// EditFileInput represents the input for the edit_file tool
type EditFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=The path of the file to edit"`
	OldString string `json:"old_string" jsonschema:"required,description=The exact text to find and replace (must match exactly)"`
	NewString string `json:"new_string" jsonschema:"required,description=The replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() llm.Tool {
	return llm.NewTool(
		"edit_file",
		`Apply a search/replace edit to a file.

CRITICAL: Read the file first to get the exact text including whitespace.

Parameters:
- path: The file path to edit
- old_string: The exact text to find (must match exactly including all whitespace, indentation, newlines)
- new_string: The replacement text

Requirements:
- old_string must match EXACTLY (every space, tab, newline, character)
- Include 3-5 lines of context to make old_string unique
- If old_string appears multiple times, the edit fails
- To replace multiple occurrences, make separate calls with unique context

Example:
{
  "path": "test.go",
  "old_string": "func old() {\n    doSomething()\n}",
  "new_string": "func new() {\n    doSomethingElse()\n}"
}`,
	).
		WithSchema(llm.GenerateSchema(EditFileInput{})).
		WithExecute(llm.TypedExecute(executeEditFile)).
		Build()
}

func executeEditFile(_ context.Context, args EditFileInput) (llm.ToolResultOutput, error) {
	if args.Path == "" {
		return llm.NewTextErrorResponse("path is required"), nil
	}
	if args.OldString == "" {
		return llm.NewTextErrorResponse("old_string is required"), nil
	}
	// Note: new_string can be empty (for removing content)

	// Open the original file
	file, err := os.Open(args.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return llm.NewTextErrorResponse(fmt.Sprintf("file not found: %s", args.Path)), nil
		}
		return llm.NewTextErrorResponse(err.Error()), nil
	}
	defer file.Close()

	// Create a temporary file in the same directory
	tempFile, err := os.CreateTemp("", "edit_file_*.tmp")
	if err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to create temp file: %v", err)), nil
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath) // Clean up temp file on error

	// Streaming search and replace
	const chunkSize = 4096 // 4KB chunks
	oldBytes := []byte(args.OldString)
	newBytes := []byte(args.NewString)

	buffer := make([]byte, 0, len(oldBytes)*2+chunkSize)
	chunk := make([]byte, chunkSize)
	occurrences := 0

	for {
		n, err := file.Read(chunk)
		if err != nil && err.Error() != "EOF" {
			tempFile.Close()
			return llm.NewTextErrorResponse(fmt.Sprintf("failed to read file: %v", err)), nil
		}

		// Add new data to buffer
		buffer = append(buffer, chunk[:n]...)

		// Search for old_string in buffer
		for {
			idx := bytes.Index(buffer, oldBytes)
			if idx == -1 {
				break
			}

			occurrences++
			if occurrences > 1 {
				tempFile.Close()
				return llm.NewTextErrorResponse(
					fmt.Sprintf("old_string found multiple times in file. Include more surrounding context to make it unique, or use a different portion of text.")), nil
			}

			// Write everything before the match
			if _, err := tempFile.Write(buffer[:idx]); err != nil {
				tempFile.Close()
				return llm.NewTextErrorResponse(fmt.Sprintf("failed to write to temp file: %v", err)), nil
			}

			// Write the replacement
			if _, err := tempFile.Write(newBytes); err != nil {
				tempFile.Close()
				return llm.NewTextErrorResponse(fmt.Sprintf("failed to write to temp file: %v", err)), nil
			}

			// Remove processed part from buffer
			buffer = buffer[idx+len(oldBytes):]
		}

		// Keep enough data in buffer to handle matches spanning chunks
		if len(buffer) > len(oldBytes) {
			// Write excess data, keeping oldBytes length in buffer
			writeLen := len(buffer) - len(oldBytes)
			if _, err := tempFile.Write(buffer[:writeLen]); err != nil {
				tempFile.Close()
				return llm.NewTextErrorResponse(fmt.Sprintf("failed to write to temp file: %v", err)), nil
			}
			buffer = buffer[writeLen:]
		}

		if err != nil && err.Error() == "EOF" {
			break
		}
	}

	// Write any remaining buffer content
	if len(buffer) > 0 {
		if _, err := tempFile.Write(buffer); err != nil {
			tempFile.Close()
			return llm.NewTextErrorResponse(fmt.Sprintf("failed to write to temp file: %v", err)), nil
		}
	}

	// Close temp file before renaming
	if err := tempFile.Close(); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to close temp file: %v", err)), nil
	}

	// Check if we found the old_string
	if occurrences == 0 {
		return llm.NewTextErrorResponse(
			fmt.Sprintf("old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", args.OldString)), nil
	}

	// Get original file permissions
	fileInfo, err := os.Stat(args.Path)
	if err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to get file info: %v", err)), nil
	}

	// Replace original file with temp file
	if err := os.Rename(tempPath, args.Path); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to replace file: %v", err)), nil
	}

	// Restore original permissions
	if err := os.Chmod(args.Path, fileInfo.Mode()); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to restore file permissions: %v", err)), nil
	}

	return llm.NewTextResponse("File edited successfully"), nil
}
