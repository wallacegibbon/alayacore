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
	NumLines  int    `json:"num_lines" jsonschema:"description=Number of lines to read from start_line"`
}

// NewReadFileTool creates a tool for reading files
func NewReadFileTool() llm.Tool {
	return llm.NewTool(
		"read_file",
		`Read file contents. For media files (image, video, audio, document/PDF), returns the content directly for you to see. For text files, supports optional line range using start_line and num_lines parameters (1-indexed).`,
	).
		WithSchema(llm.MustGenerateSchema(ReadFileInput{})).
		WithExecute(llm.TypedExecute(executeReadFile)).
		Build()
}

// mediaMimePrefixes maps MIME type prefixes to the corresponding ContentPart constructor.
// Each entry defines what prefix to match and a function that creates the right part.
type mediaTypeEntry struct {
	prefix  string
	newPart func(dataURI string) llm.ContentPart
}

var mediaMimePrefixes = []mediaTypeEntry{
	{prefix: "image/", newPart: func(u string) llm.ContentPart { return &llm.ImagePart{DataURI: u} }},
	{prefix: "video/", newPart: func(u string) llm.ContentPart { return &llm.VideoPart{DataURI: u} }},
	{prefix: "audio/", newPart: func(u string) llm.ContentPart { return &llm.AudioPart{DataURI: u} }},
}

// documentMimePrefixes are MIME type prefixes for document files (PDF, Office, etc.).
// These use DocumentPart instead of the generic fallback.
// Text files are excluded — they are read as plain text, not as media.
var documentMimePrefixes = []string{
	"application/pdf",
	"application/vnd.openxmlformats-officedocument",
	"application/vnd.ms-",
	"application/msword",
}

// isMediaFile checks whether the file at path is a supported media type
// (image, video, audio, document) by examining both the extension and content.
// Content sniffing is used to catch false positives where the extension-based
// MIME type is misleading (e.g., .mod → audio/x-mod for a go.mod text file).
// Only overrides extension-based detection when content explicitly says text.
// Returns the MIME type and a constructor for the appropriate ContentPart.
func isMediaFile(path string) (mimeType string, newPart func(dataURI string) llm.ContentPart, ok bool) {
	ext := strings.ToLower(filepath.Ext(path))

	// Get extension-based MIME
	var extMime string
	if ext != "" {
		extMime = mime.TypeByExtension(ext)
	}

	// Open and sniff content (first 512 bytes) for verification
	f, err := os.Open(path)
	if err != nil {
		// Can't sniff — trust extension if it says media
		if extMime != "" {
			if part, ok := matchMediaType(extMime); ok {
				return extMime, part, true
			}
		}
		return "", nil, false
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := f.Read(buf) //nolint:errcheck // partial read is fine for sniffing

	var contentMime string
	if n > 0 {
		contentMime = http.DetectContentType(buf[:n])
	}

	// If extension says media, verify with content sniffing
	if extMime != "" {
		if extPart, ok := matchMediaType(extMime); ok {
			// Content explicitly says text — extension was misleading
			// (e.g., .mod → audio/x-mod but go.mod is text)
			if contentMime != "" && strings.HasPrefix(contentMime, "text/") {
				return "", nil, false
			}
			// Content isn't text — trust extension
			return extMime, extPart, true
		}
	}

	// Extension didn't match or was overridden — use content sniffing
	if contentMime != "" {
		if part, ok := matchMediaType(contentMime); ok {
			return contentMime, part, true
		}
	}
	return "", nil, false
}

// matchMediaType checks if a MIME type matches any supported media prefix
// and returns the appropriate ContentPart constructor.
func matchMediaType(mimeType string) (func(dataURI string) llm.ContentPart, bool) {
	for _, entry := range mediaMimePrefixes {
		if strings.HasPrefix(mimeType, entry.prefix) {
			return entry.newPart, true
		}
	}
	for _, prefix := range documentMimePrefixes {
		if strings.HasPrefix(mimeType, prefix) {
			return func(u string) llm.ContentPart { return &llm.DocumentPart{DataURI: u} }, true
		}
	}
	return nil, false
}

func executeReadFile(ctx context.Context, args ReadFileInput) ([]llm.ContentPart, error) {
	info, err := os.Stat(args.Path)
	if err != nil {
		return nil, err
	}

	// Validate line parameters
	if valErr := validateLineParams(args.StartLine, args.NumLines); valErr != nil {
		return nil, valErr
	}

	// Detect media files — return content directly (only when reading full file)
	if args.StartLine == 0 && args.NumLines == 0 {
		if mimeType, newPart, ok := isMediaFile(args.Path); ok {
			return readMediaFile(args.Path, mimeType, newPart)
		}
	}

	// Full file read case
	if args.StartLine == 0 && args.NumLines == 0 {
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

	// Normalize: start_line=0 means start at line 1
	startLine := args.StartLine
	if startLine == 0 {
		startLine = 1
	}

	lines, err := readLinesRange(ctx, file, startLine, args.NumLines)
	if err != nil {
		return nil, err
	}

	return []llm.ContentPart{&llm.TextPart{Text: strings.Join(lines, "\n")}}, nil
}

// readMediaFile reads a media file and returns a ContentPart with base64-encoded data.
// Supported types: image, video, audio, document (PDF, etc.).
func readMediaFile(path, mimeType string, newPart func(dataURI string) llm.ContentPart) ([]llm.ContentPart, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64)
	sizeKB := float64(len(data)) / 1024

	return []llm.ContentPart{
		&llm.TextPart{Text: fmt.Sprintf("Read %s (%.1fKB)", filepath.Base(path), sizeKB)},
		newPart(dataURI),
	}, nil
}

// readLargeFileTruncated reads a large file and returns up to maxTextReadSize
// bytes of content with a metadata header showing total line count and size.
// Single-pass: collects lines until the byte limit, then continues counting.
func readLargeFileTruncated(path string, totalSize int64) ([]llm.ContentPart, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	var lines []string
	var bytesRead int64
	totalLines := 0
	collecting := true

	for scanner.Scan() {
		totalLines++
		if collecting {
			line := scanner.Text()
			lineBytes := int64(len(line)) + 1 // +1 for newline

			if bytesRead+lineBytes > maxTextReadSize && len(lines) > 0 {
				collecting = false
				continue
			}
			lines = append(lines, line)
			bytesRead += lineBytes
		}
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

func validateLineParams(startLine, numLines int) error {
	if startLine < 0 {
		return fmt.Errorf("start_line must be >= 0")
	}
	if numLines < 0 {
		return fmt.Errorf("num_lines must be >= 0")
	}
	// 0 means "not specified" (default int value)
	// Positive startLine values are 1-indexed line numbers
	return nil
}

func readLinesRange(ctx context.Context, file *os.File, startLine, numLines int) ([]string, error) {
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

		if currentLine < startLine {
			currentLine++
			continue
		}

		if numLines > 0 && len(lines) >= numLines {
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
