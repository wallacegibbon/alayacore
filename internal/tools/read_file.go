package tools

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/alayacore/alayacore/internal/llm"
)

const maxTextReadSize = 64 * 1024 // 64KB limit for text files (~16K tokens)

// ReadFileInput represents the input for the read_file tool
type ReadFileInput struct {
	Path      string `json:"path" jsonschema:"required,description=File path to read"`
	StartLine int    `json:"start_line" jsonschema:"description=Starting line number (1-indexed)"`
	EndLine   int    `json:"end_line" jsonschema:"description=Ending line number (1-indexed)"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		`Read file contents. For image files (PNG, JPEG, etc.), returns the image directly for you to see. For text files, supports optional line range using start_line and end_line parameters (1-indexed).`,
	).
		WithSchema(llm.MustGenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

// imageMimePrefixes are MIME type prefixes that indicate an image file.
var imageMimePrefixes = []string{"image/"}

// isImageFile checks whether the file at path is an image by examining
// the extension and content. Returns the MIME type if it's an image.
func isImageFile(path string) (mimeType string, ok bool) {
	// First try extension-based detection
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		mimeType = mime.TypeByExtension(ext)
		if mimeType != "" {
			for _, prefix := range imageMimePrefixes {
				if strings.HasPrefix(mimeType, prefix) {
					return mimeType, true
				}
			}
		}
	}

	// Extension didn't match — sniff content (first 512 bytes)
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf) //nolint:errcheck // partial read is fine for sniffing
	if n == 0 {
		return "", false
	}

	mimeType = http.DetectContentType(buf[:n])
	for _, prefix := range imageMimePrefixes {
		if strings.HasPrefix(mimeType, prefix) {
			return mimeType, true
		}
	}
	return "", false
}

func executeReadFile(ctx context.Context, args ReadFileInput) ([]llm.ContentPart, error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return nil, err
	}

	// Validate line range parameters
	if valErr := validateLineRange(args.StartLine, args.EndLine); valErr != nil {
		return nil, valErr
	}

	// Detect image files — return image content directly
	if args.StartLine == 0 && args.EndLine == 0 {
		if mimeType, ok := isImageFile(args.Path); ok {
			return readImageFile(args.Path, mimeType)
		}
	}

	// Full file read case
	if args.StartLine == 0 && args.EndLine == 0 {
		if info.Size() > maxTextReadSize {
			return readLargeFileTruncated(args.Path, info.Size())
		}
		var content []byte
		content, err = os.ReadFile(args.Path)
		if err != nil {
			return nil, err
		}
		return []llm.ContentPart{&llm.TextPart{Text: string(content)}}, nil
	}

	// Line range case: stream from file to avoid loading entire file into memory
	file, err := os.Open(args.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines, err := readLinesRange(ctx, file, args.StartLine, args.EndLine)
	if err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: strings.Join(lines, "\n")}}, nil
}

// readImageFile reads an image file and returns an ImagePart with base64-encoded data.
func readImageFile(path, mimeType string) ([]llm.ContentPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURL := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	sizeKB := float64(len(data)) / 1024

	return []llm.ContentPart{
		&llm.TextPart{Text: fmt.Sprintf("Read %s (%.1fKB)", filepath.Base(path), sizeKB)},
		&llm.ImagePart{DataURL: dataURL},
	}, nil
}

// readLargeFileTruncated reads a large file up to maxTextReadSize and returns
// the content with metadata about the truncation.
func readLargeFileTruncated(path string, totalSize int64) ([]llm.ContentPart, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// First pass: count total lines
	totalLines := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		totalLines++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Reset file position for second pass
	if _, err := file.Seek(0, 0); err != nil {
		return nil, err
	}

	// Second pass: read lines up to maxTextReadSize
	var lines []string
	var bytesRead int64
	scanner = bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		lineBytes := int64(len(line)) + 1 // +1 for newline

		// Stop if adding this line would exceed limit (but always include at least one line)
		if bytesRead+lineBytes > maxTextReadSize && len(lines) > 0 {
			break
		}

		lines = append(lines, line)
		bytesRead += lineBytes
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	shownLines := len(lines)
	content := strings.Join(lines, "\n")
	header := fmt.Sprintf(
		"[Lines 1-%d of %d | %.1fKB of %.1fKB shown]\n",
		shownLines, totalLines,
		float64(bytesRead)/1024, float64(totalSize)/1024,
	)

	return []llm.ContentPart{&llm.TextPart{Text: header + "\n" + content}}, nil
}

func validateLineRange(startLine, endLine int) error {
	if startLine < 0 {
		return fmt.Errorf("start_line must be >= 0")
	}
	if endLine < 0 {
		return fmt.Errorf("end_line must be >= 0")
	}
	if startLine > 0 && endLine > 0 && startLine > endLine {
		return fmt.Errorf("start_line must be <= end_line")
	}
	// 0 means "not specified" (default int value)
	// Positive values are 1-indexed line numbers
	return nil
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
