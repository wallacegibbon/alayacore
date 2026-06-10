package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alayacore/alayacore/internal/llm"
)

func TestReadFileFull(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()
	input := ReadFileInput{Path: tmpFile}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	text := extractFirstText(result)
	if text != content {
		t.Errorf("expected %q, got %q", content, text)
	}
}

func TestReadFileWithLineRange(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	var content string
	for i := 1; i <= 100; i++ {
		content += "line" + itoa(i) + "\n"
	}
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()

	tests := []struct {
		name      string
		input     ReadFileInput
		wantLines []string
		wantError bool
		errorMsg  string
	}{
		{
			name:      "read lines 5-10",
			input:     ReadFileInput{Path: tmpFile, StartLine: 5, EndLine: 10},
			wantLines: []string{"line5", "line6", "line7", "line8", "line9", "line10"},
		},
		{
			name:      "read first line",
			input:     ReadFileInput{Path: tmpFile, StartLine: 1, EndLine: 1},
			wantLines: []string{"line1"},
		},
		{
			name:      "read last line",
			input:     ReadFileInput{Path: tmpFile, StartLine: 100, EndLine: 100},
			wantLines: []string{"line100"},
		},
		{
			name:      "read from line to end",
			input:     ReadFileInput{Path: tmpFile, StartLine: 98},
			wantLines: []string{"line98", "line99", "line100"},
		},
		{
			name:      "read from start to line",
			input:     ReadFileInput{Path: tmpFile, EndLine: 3},
			wantLines: []string{"line1", "line2", "line3"},
		},
		{
			name:      "invalid negative start_line",
			input:     ReadFileInput{Path: tmpFile, StartLine: -1},
			wantError: true,
			errorMsg:  "start_line must be >= 0",
		},
		{
			name:      "invalid negative end_line",
			input:     ReadFileInput{Path: tmpFile, EndLine: -1},
			wantError: true,
			errorMsg:  "end_line must be >= 0",
		},
		{
			name:      "start > end",
			input:     ReadFileInput{Path: tmpFile, StartLine: 10, EndLine: 5},
			wantError: true,
			errorMsg:  "start_line must be <= end_line",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputJSON, err := json.Marshal(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(context.Background(), inputJSON)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got success: %v", result)
					return
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			text := extractFirstText(result)
			expected := strings.Join(tt.wantLines, "\n")
			if text != expected {
				t.Errorf("expected %q, got %q", expected, text)
			}
		})
	}
}

func TestReadFileTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.txt")
	largeContent := make([]byte, maxFullReadSize+1)
	for i := range largeContent {
		largeContent[i] = 'x'
	}
	if err := os.WriteFile(tmpFile, largeContent, 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool()
	input := ReadFileInput{Path: tmpFile}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	text := extractFirstText(result)
	if !strings.Contains(text, "[Lines 1-") {
		t.Errorf("expected truncation header, got %q", text[:100])
	}
	if !strings.Contains(text, "of ") {
		t.Errorf("expected 'of' in truncation header, got %q", text[:100])
	}
}

func TestReadFileLargeWithLineRange(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "large.txt")

	f, err := os.Create(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("first line\n")
	for i := 0; i < 100000; i++ {
		_, _ = f.WriteString("x")
	}
	_, _ = f.WriteString("\nthird line\n")
	f.Close()

	tool := NewReadFileTool()

	input := ReadFileInput{Path: tmpFile, StartLine: 1, EndLine: 1}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	text := extractFirstText(result)
	if text != "first line" {
		t.Errorf("expected 'first line', got %q", text)
	}

	input = ReadFileInput{Path: tmpFile, StartLine: 3, EndLine: 3}
	inputJSON, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	result, err = tool.Execute(context.Background(), inputJSON)
	if err != nil {
		t.Fatal(err)
	}

	text = extractFirstText(result)
	if text != "third line" {
		t.Errorf("expected 'third line', got %q", text)
	}
}

func TestReadFileNotFound(t *testing.T) {
	tool := NewReadFileTool()
	input := ReadFileInput{Path: "/nonexistent/file.txt"}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tool.Execute(context.Background(), inputJSON)
	if err == nil {
		t.Errorf("expected error for nonexistent file")
	}
}

func TestReadFileBinary(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		content []byte
	}{
		{
			name:    "text file",
			content: []byte("Hello, world!\nThis is text.\n"),
		},
		{
			name:    "binary with null bytes",
			content: []byte{0x00, 0x01, 0x02, 0x03, 'H', 'e', 'l', 'l', 'o'},
		},
		{
			name:    "PNG header",
			content: []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00},
		},
		{
			name:    "UTF-8 text with special chars",
			content: []byte("Hello 世界\nПривет мир\n🎉\n"),
		},
		{
			name:    "empty file",
			content: []byte{},
		},
		{
			name:    "code file",
			content: []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"),
		},
	}

	tool := NewReadFileTool()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile := filepath.Join(tmpDir, tt.name+".bin")
			if err := os.WriteFile(tmpFile, tt.content, 0644); err != nil {
				t.Fatal(err)
			}

			input := ReadFileInput{Path: tmpFile}
			inputJSON, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(context.Background(), inputJSON)
			if err != nil {
				t.Fatal(err)
			}

			text := extractFirstText(result)
			if len(tt.content) == 0 && text != "" {
				t.Errorf("expected empty content for empty file, got %q", text)
			}
		})
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var neg bool
	if i < 0 {
		neg = true
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func extractFirstText(parts []llm.ContentPart) string {
	for _, p := range parts {
		if tp, ok := p.(llm.TextPart); ok {
			return tp.Text
		}
	}
	return ""
}
