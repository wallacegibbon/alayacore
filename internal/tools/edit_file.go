package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/alayacore/alayacore/internal/llm"
)

// EditFileInput represents the input for the edit_file tool
type EditFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=File path to edit"`
	OldString string `json:"old_string" jsonschema:"required,description=Exact text to find and replace"`
	NewString string `json:"new_string" jsonschema:"required,description=Replacement text"`
}

// NewEditFileTool creates a tool for editing files using search/replace
func NewEditFileTool() llm.Tool {
	return llm.NewTool(
		"edit_file",
		`Apply a search/replace edit to a file. old_string must match EXACTLY (every space, tab, newline). Use 3-5 lines of context to make it unique. If old_string appears multiple times, the edit fails.`,
	).
		WithSchema(llm.GenerateSchema(EditFileInput{})).
		WithExecute(llm.TypedExecute(executeEditFile)).
		Build()
}

// streamEditor handles streaming search and replace on a file
type streamEditor struct {
	oldBytes    []byte
	newBytes    []byte
	buffer      []byte
	chunk       []byte
	occurrences int
}

func newStreamEditor(oldString, newString string) *streamEditor {
	const chunkSize = 4096
	oldBytes := []byte(oldString)
	return &streamEditor{
		oldBytes: oldBytes,
		newBytes: []byte(newString),
		buffer:   make([]byte, 0, len(oldBytes)*2+chunkSize),
		chunk:    make([]byte, chunkSize),
	}
}

// processChunk reads and processes a chunk, writing to tempFile
// Returns (done, error)
func (se *streamEditor) processChunk(file *os.File, tempFile *os.File) (bool, error) {
	n, err := file.Read(se.chunk)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("failed to read file: %w", err)
	}

	se.buffer = append(se.buffer, se.chunk[:n]...)

	// Search for old_string in buffer
	for {
		idx := bytes.Index(se.buffer, se.oldBytes)
		if idx == -1 {
			break
		}

		se.occurrences++
		if se.occurrences > 1 {
			return false, fmt.Errorf("old_string found multiple times in file. Include more surrounding context to make it unique, or use a different portion of text")
		}

		if _, err = tempFile.Write(se.buffer[:idx]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		if _, err = tempFile.Write(se.newBytes); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[idx+len(se.oldBytes):]
	}

	// Keep enough data in buffer to handle matches spanning chunks
	if len(se.buffer) > len(se.oldBytes) {
		writeLen := len(se.buffer) - len(se.oldBytes)
		if _, err = tempFile.Write(se.buffer[:writeLen]); err != nil {
			return false, fmt.Errorf("failed to write to temp file: %v", err)
		}
		se.buffer = se.buffer[writeLen:]
	}

	return errors.Is(err, io.EOF), nil
}

// flushRemaining writes any remaining buffer content
func (se *streamEditor) flushRemaining(tempFile *os.File) error {
	if len(se.buffer) > 0 {
		if _, err := tempFile.Write(se.buffer); err != nil {
			return fmt.Errorf("failed to write to temp file: %v", err)
		}
	}
	return nil
}

func validateEditFileInput(args EditFileInput) (string, error) {
	if args.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if args.OldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if args.OldString == args.NewString {
		return "", fmt.Errorf("old_string and new_string are identical — no changes would be made. If you intended to modify the file, make sure new_string is different from old_string")
	}
	return args.Path, nil
}

type editResult struct {
	tempPath    string
	fileInfo    os.FileInfo
	occurrences int
}

func executeEditFile(ctx context.Context, args EditFileInput) (llm.ToolResultOutput, error) {
	path, err := validateEditFileInput(args)
	if err != nil {
		return llm.NewTextErrorResponse(err.Error()), nil
	}

	result, resp := editToTemp(ctx, args, path)
	defer func() {
		if result != nil && result.tempPath != "" {
			os.Remove(result.tempPath)
		}
	}()
	if resp != nil {
		return resp, nil
	}

	return replaceFile(result.tempPath, path, result.fileInfo), nil
}

// editToTemp applies the edit to a temp file and returns the result.
// On error, the caller should clean up tempPath if it is non-empty.
func editToTemp(ctx context.Context, args EditFileInput, path string) (result *editResult, resp llm.ToolResultOutput) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, llm.NewTextErrorResponse(fmt.Sprintf("file not found: %s", path))
		}
		return nil, llm.NewTextErrorResponse(err.Error())
	}

	var tempFile *os.File
	cleanup := func() {
		file.Close()
		if tempFile != nil {
			tempFile.Close()
		}
	}

	result = &editResult{}

	// Create temp file in the same directory as the target file to avoid
	// cross-device rename errors on Windows (e.g. temp dir on C: vs target on D:).
	dir := filepath.Dir(path)
	tempFile, err = os.CreateTemp(dir, "edit_file_*.tmp")
	if err != nil {
		cleanup()
		return result, llm.NewTextErrorResponse(fmt.Sprintf("failed to create temp file: %v", err))
	}
	result.tempPath = tempFile.Name()

	// Use file.Stat() instead of os.Stat(path) to avoid a redundant syscall
	// and TOCTOU race between open and stat.
	fileInfo, err := file.Stat()
	if err != nil {
		cleanup()
		return result, llm.NewTextErrorResponse(fmt.Sprintf("failed to get file info: %v", err))
	}

	editor := newStreamEditor(args.OldString, args.NewString)

	for {
		select {
		case <-ctx.Done():
			cleanup()
			return result, llm.NewTextErrorResponse("Canceled")
		default:
		}

		done, pErr := editor.processChunk(file, tempFile)
		if pErr != nil {
			cleanup()
			return result, llm.NewTextErrorResponse(pErr.Error())
		}
		if done {
			break
		}
	}

	if err = editor.flushRemaining(tempFile); err != nil {
		cleanup()
		return result, llm.NewTextErrorResponse(err.Error())
	}

	if editor.occurrences == 0 {
		cleanup()
		return result, llm.NewTextErrorResponse(
			fmt.Sprintf("old_string not found in file. Make sure to copy the exact text including all whitespace and indentation.\n\nSearched for:\n%q", args.OldString))
	}

	result.fileInfo = fileInfo
	result.occurrences = editor.occurrences
	cleanup()
	return result, nil
}

func replaceFile(tempPath string, path string, fileInfo os.FileInfo) llm.ToolResultOutput {
	// On Windows, os.Rename fails with "Access is denied" if the target
	// file still has an open handle. All source file handles are closed
	// by the time we get here (editToTemp's cleanup closes them).
	if err := os.Rename(tempPath, path); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to replace file: %v", err))
	}

	if err := os.Chmod(path, fileInfo.Mode()); err != nil {
		return llm.NewTextErrorResponse(fmt.Sprintf("failed to restore file permissions: %v", err))
	}

	return llm.NewTextResponse("File edited successfully")
}
