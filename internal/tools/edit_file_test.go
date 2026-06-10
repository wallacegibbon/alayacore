package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	tests := []struct {
		name        string
		setup       func()
		input       EditFileInput
		expectError bool
		errorMsg    string
		expected    string // expected file content after edit
	}{
		{
			name: "simple replacement",
			setup: func() {
				os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "hello",
				NewString: "goodbye",
			},
			expected: "goodbye world",
		},
		{
			name: "no changes when old and new are same",
			setup: func() {
				os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "hello",
				NewString: "hello",
			},
			expectError: true,
			errorMsg:    "identical",
		},
		{
			name: "file not found",
			input: EditFileInput{
				Path:      "/nonexistent/file.txt",
				OldString: "hello",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name: "old_string not found in file",
			setup: func() {
				os.WriteFile(testFile, []byte("hello world"), 0644)
			},
			input: EditFileInput{
				Path:      testFile,
				OldString: "goodbye",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "not found",
		},
		{
			name: "path required",
			input: EditFileInput{
				Path:      "",
				OldString: "hello",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "required",
		},
		{
			name: "old_string required",
			input: EditFileInput{
				Path:      testFile,
				OldString: "",
				NewString: "world",
			},
			expectError: true,
			errorMsg:    "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up and setup
			os.Remove(testFile)
			if tt.setup != nil {
				tt.setup()
			}

			content, err := executeEditFile(context.Background(), tt.input)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error, got success: %v", content)
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errorMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Verify file content
			fileContent, err := os.ReadFile(testFile)
			if err != nil {
				t.Fatalf("failed to read file: %v", err)
			}
			if string(fileContent) != tt.expected {
				t.Errorf("expected file content %q, got %q", tt.expected, string(fileContent))
			}
		})
	}
}
